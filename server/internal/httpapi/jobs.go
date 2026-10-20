package httpapi

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/timisah/101conv/server/internal/analysis"
	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/jobs"
)

const maxBatchArchiveBytes int64 = 512 << 20

func batchOwner(p auth.Principal) string {
	if p.IsAdmin() {
		return ""
	}
	return p.ID
}
func batchScope(mode string) string {
	if mode == "compress" {
		return auth.ScopeCompress
	}
	return auth.ScopeSubmit
}
func (a *API) guestJobLimits(ctx context.Context, p auth.Principal) jobs.Limits {
	limits := jobs.Limits{MonthlyJobs: p.Quotas.MonthlyJobs.Value, Guest: p.Guest}
	if p.Guest && a.accounts != nil {
		if s, err := a.accounts.AnonymousSettings(ctx); err == nil {
			limits.MaxBatchFiles = s.MaxBatchFiles
			limits.MaxGuestActiveJobs = s.MaxSimultaneousJobs
			limits.MaxGlobalGuestJobs = s.MaxGlobalJobs
			limits.MaxGlobalGuestStorageBytes = s.MaxGlobalStorageBytes
		} else {
			limits.MaxBatchFiles = 0
			limits.MaxGuestActiveJobs = 0
			limits.MaxGlobalGuestJobs = 0
			limits.MaxGlobalGuestStorageBytes = 0
		}
	}
	return limits
}

func (a *API) consumeGuestRate(w http.ResponseWriter, r *http.Request, p auth.Principal, action string) bool {
	if !p.Guest || a.accounts == nil {
		return true
	}
	s, err := a.accounts.AnonymousSettings(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "settings_error", "could not check guest request limits")
		return false
	}
	per, global := s.SubmissionsPerMinute, s.GlobalSubmissionsPerMinute
	if action == "upload" {
		per, global = s.UploadsPerMinute, s.GlobalUploadsPerMinute
	}
	allowed, err := a.accounts.ConsumeGuestRate(r.Context(), p.ID, action, per, global)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "rate_limit_error", "could not check guest request limits")
		return false
	}
	if !allowed {
		w.Header().Set("Retry-After", "60")
		writeAPIError(w, http.StatusTooManyRequests, "guest_rate_limited", "guest request limit reached; wait a minute and try again")
		return false
	}
	return true
}

func (a *API) createBatch(w http.ResponseWriter, r *http.Request) {
	var req jobs.BatchRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Mode != "convert" && req.Mode != "compress" {
		writeAPIError(w, http.StatusBadRequest, "invalid_input", "batch mode must be convert or compress")
		return
	}
	p, ok := a.requireCapability(w, r, batchScope(req.Mode))
	if !ok || !a.jobsReady(w) {
		return
	}
	if !a.consumeGuestRate(w, r, p, "submission") {
		return
	}
	batch, duplicate, err := a.jobs.CreateBatch(r.Context(), req, p.ID, a.guestJobLimits(r.Context(), p))
	if err != nil {
		writeJobError(w, err)
		return
	}
	status := http.StatusAccepted
	if duplicate {
		status = http.StatusOK
	}
	w.Header().Set("Location", "/api/v1/batches/"+batch.ID)
	writeJSON(w, status, map[string]any{"batch": batch, "duplicate": duplicate})
}

func (a *API) listBatches(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeRead)
	if !ok || !a.jobsReady(w) {
		return
	}
	limit, offset := 25, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, e := strconv.Atoi(raw)
		if e != nil || v < 1 || v > 100 {
			writeAPIError(w, http.StatusBadRequest, "invalid_query", "limit must be between 1 and 100")
			return
		}
		limit = v
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		v, e := strconv.Atoi(raw)
		if e != nil || v < 0 || v > 100000 {
			writeAPIError(w, http.StatusBadRequest, "invalid_query", "offset must be between 0 and 100000")
			return
		}
		offset = v
	}
	batches, total, err := a.jobs.ListBatches(r.Context(), batchOwner(p), limit, offset)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"batches": batches, "total": total, "limit": limit, "offset": offset})
}

func (a *API) getBatch(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeRead)
	if !ok || !a.jobsReady(w) {
		return
	}
	batch, err := a.jobs.GetBatch(r.Context(), r.PathValue("id"), batchOwner(p), a.guestJobLimits(r.Context(), p))
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, batch)
}

func (a *API) downloadBatchResults(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeResult)
	if !ok || !a.jobsReady(w) {
		return
	}
	ownerID := batchOwner(p)
	batch, err := a.jobs.GetBatch(r.Context(), r.PathValue("id"), ownerID, a.guestJobLimits(r.Context(), p))
	if err != nil {
		writeJobError(w, err)
		return
	}
	type archiveItem struct {
		index int
		job   jobs.Job
		file  io.ReadCloser
	}
	items := []archiveItem{}
	var total int64
	for _, item := range batch.Items {
		if item.Job == nil || item.Job.State != jobs.Succeeded || item.Job.ResultState != "available" {
			continue
		}
		if total+item.Job.OutputSize > maxBatchArchiveBytes {
			for _, opened := range items {
				opened.file.Close()
			}
			writeAPIError(w, http.StatusRequestEntityTooLarge, "archive_too_large", "successful results exceed the 512 MiB batch archive limit; download items individually")
			return
		}
		file, job, _, openErr := a.jobs.OpenResultFor(r.Context(), item.Job.ID, ownerID)
		if openErr != nil {
			for _, opened := range items {
				opened.file.Close()
			}
			writeJobError(w, openErr)
			return
		}
		total += job.OutputSize
		items = append(items, archiveItem{item.Index, job, file})
	}
	if len(items) == 0 {
		writeAPIError(w, http.StatusConflict, "no_available_results", "this batch has no successful results available to download")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "101conv-batch-" + batch.ID + ".zip"}))
	w.WriteHeader(http.StatusOK)
	zw := zip.NewWriter(w)
	for _, item := range items {
		name := fmt.Sprintf("item-%03d-%s.%s", item.index+1, item.job.ID, item.job.TargetFormat)
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		entry, e := zw.CreateHeader(header)
		if e == nil {
			_, e = io.CopyN(entry, item.file, item.job.OutputSize)
		}
		if closeErr := item.file.Close(); e == nil {
			e = closeErr
		}
		if e != nil {
			_ = zw.Close()
			return
		}
	}
	_ = zw.Close()
}

func (a *API) resumeBatch(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeRead)
	if !ok || !a.jobsReady(w) {
		return
	}
	batch, err := a.jobs.GetBatch(r.Context(), r.PathValue("id"), batchOwner(p), a.guestJobLimits(r.Context(), p))
	if err != nil {
		writeJobError(w, err)
		return
	}
	if _, ok = a.requireCapability(w, r, batchScope(batch.Mode)); !ok {
		return
	}
	batch, err = a.jobs.ResumeBatch(r.Context(), batch.ID, batchOwner(p), a.guestJobLimits(r.Context(), p))
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, batch)
}

func (a *API) cancelBatch(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeCancel)
	if !ok || !a.jobsReady(w) {
		return
	}
	batch, err := a.jobs.CancelBatch(r.Context(), r.PathValue("id"), batchOwner(p))
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, batch)
}

func (a *API) cancelBatchItem(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeCancel)
	if !ok || !a.jobsReady(w) {
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 || index >= jobs.MaxBatchItems {
		writeAPIError(w, http.StatusBadRequest, "invalid_input", "batch item index is invalid")
		return
	}
	batch, err := a.jobs.CancelBatchItem(r.Context(), r.PathValue("id"), index, batchOwner(p))
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, batch)
}

func (a *API) retryBatch(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeRead)
	if !ok || !a.jobsReady(w) {
		return
	}
	batch, err := a.jobs.GetBatch(r.Context(), r.PathValue("id"), batchOwner(p), a.guestJobLimits(r.Context(), p))
	if err != nil {
		writeJobError(w, err)
		return
	}
	if _, ok = a.requireCapability(w, r, batchScope(batch.Mode)); !ok {
		return
	}
	var req struct {
		Items []int `json:"items"`
	}
	if r.ContentLength != 0 && !decodeBody(w, r, &req) {
		return
	}
	if !a.consumeGuestRate(w, r, p, "submission") {
		return
	}
	batch, err = a.jobs.RetryBatchItems(r.Context(), batch.ID, batchOwner(p), req.Items, a.guestJobLimits(r.Context(), p))
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, batch)
}

func (a *API) jobsReady(w http.ResponseWriter) bool {
	if a.jobs == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "jobs_unavailable", "job storage is not configured")
		return false
	}
	return true
}

func (a *API) uploadFile(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeUpload)
	if !ok || !a.jobsReady(w) {
		return
	}
	if !a.consumeGuestRate(w, r, p, "upload") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, jobs.MaxInputBytes+(1<<20))
	reservationID := ""
	maxGuestStorage := int64(0)
	if p.Guest {
		settings, err := a.accounts.AnonymousSettings(r.Context())
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "settings_error", "could not check guest storage limits")
			return
		}
		maxGuestStorage = settings.MaxGlobalStorageBytes
		reservationID, err = a.jobs.ReserveGuestUpload(r.Context(), p.ID, p.MaxFileBytes, maxGuestStorage)
		if err != nil {
			writeJobError(w, err)
			return
		}
		defer a.jobs.ReleaseGuestUploadReservation(context.Background(), reservationID)
	}
	if r.ContentLength > 0 {
		free, err := a.jobs.UploadFreeBytes()
		required := uint64(r.ContentLength) + (16 << 20)
		if err == nil && free < required {
			writeAPIError(w, http.StatusInsufficientStorage, "insufficient_storage", fmt.Sprintf("uploads storage reports %d bytes free; this request needs about %d bytes including safety headroom", free, required))
			return
		}
	}
	reader, err := r.MultipartReader()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_upload", "expected a multipart upload")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_upload", "multipart form field 'file' is required")
		return
	}
	defer part.Close()
	upload, err := a.jobs.CreateUploadWithGuestReservation(r.Context(), p.ID, p.MaxFileBytes, p.Quotas.MonthlyBytes.Value, maxGuestStorage, reservationID, part.FileName(), part)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "file_too_large", "upload exceeds the configured input limit")
			return
		}
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, upload)
}

func (a *API) analyzeUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeRead)
	if !ok || !a.jobsReady(w) {
		return
	}
	if a.analysis == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "analysis_unavailable", "file inspection is not configured")
		return
	}
	ownerID := p.ID
	if p.IsAdmin() {
		ownerID = ""
	}
	upload, path, err := a.jobs.GetUploadFor(r.Context(), r.PathValue("id"), ownerID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	report, err := a.analysis.Analyze(r.Context(), analysis.File{ID: upload.ID, Name: upload.FileName, Extension: upload.Extension, Size: upload.Size, SHA256: upload.SHA256, Path: path})
	if err != nil {
		if errors.Is(err, analysis.ErrInvalidFile) {
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_file", err.Error())
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "analysis_error", "could not inspect this file")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, report)
}

// inspectUpload accepts a scan-only multipart request. Unlike POST /uploads,
// it creates no upload row and leaves no retained original; analysis owns a
// short-lived file that is removed before this response is written.
func (a *API) inspectUpload(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeRead)
	if !ok || !a.jobsReady(w) {
		return
	}
	if a.analysis == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "analysis_unavailable", "file inspection is not configured")
		return
	}
	if !a.consumeGuestRate(w, r, p, "upload") {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, jobs.MaxInputBytes+(1<<20))
	if r.ContentLength > 0 {
		free, err := a.jobs.UploadFreeBytes()
		required := uint64(r.ContentLength) + (16 << 20)
		if err == nil && free < required {
			writeAPIError(w, http.StatusInsufficientStorage, "insufficient_storage", "not enough temporary storage is available for inspection")
			return
		}
	}
	reader, err := r.MultipartReader()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_inspection", "expected a multipart inspection upload")
		return
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_inspection", "multipart form field 'file' is required")
		return
	}
	defer part.Close()
	report, err := a.analysis.AnalyzeReader(r.Context(), part.FileName(), part)
	if err != nil {
		if errors.Is(err, analysis.ErrInvalidFile) {
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_file", err.Error())
			return
		}
		writeAPIError(w, http.StatusInternalServerError, "analysis_error", "could not inspect this file")
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, report)
}

func (a *API) submitJob(w http.ResponseWriter, r *http.Request) {
	a.submitJobMode(w, r, "")
}

func (a *API) submitCompressionJob(w http.ResponseWriter, r *http.Request) {
	a.submitJobMode(w, r, "compress")
}

func (a *API) submitJobMode(w http.ResponseWriter, r *http.Request, forcedMode string) {
	permissionScope := auth.ScopeSubmit
	if forcedMode == "compress" {
		permissionScope = auth.ScopeCompress
	}
	p, ok := a.requireCapability(w, r, permissionScope)
	if !ok || !a.jobsReady(w) {
		return
	}
	if !a.consumeGuestRate(w, r, p, "submission") {
		return
	}
	var req jobs.SubmitRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if forcedMode != "" {
		if req.Mode != "" && req.Mode != forcedMode {
			writeAPIError(w, http.StatusBadRequest, "invalid_input", "compression endpoint only accepts compression jobs")
			return
		}
		req.Mode = forcedMode
	} else {
		if req.Mode != "" && req.Mode != "convert" {
			writeAPIError(w, http.StatusBadRequest, "invalid_input", "use the compression endpoint for compression jobs")
			return
		}
		req.Mode = "convert"
	}
	job, duplicate, err := a.jobs.SubmitFor(r.Context(), req, p.ID, a.guestJobLimits(r.Context(), p))
	if err != nil {
		writeJobError(w, err)
		return
	}
	status := http.StatusAccepted
	if duplicate {
		status = http.StatusOK
	}
	w.Header().Set("Location", fmt.Sprintf("/api/v1/jobs/%s", job.ID))
	writeJSON(w, status, map[string]any{"job": job, "duplicate": duplicate})
}

func (a *API) listJobs(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeRead)
	if !ok || !a.jobsReady(w) {
		return
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeAPIError(w, http.StatusBadRequest, "invalid_query", "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 100000 {
			writeAPIError(w, http.StatusBadRequest, "invalid_query", "offset must be between 0 and 100000")
			return
		}
		offset = parsed
	}
	ownerID := ""
	if !p.IsAdmin() {
		ownerID = p.ID
	}
	items, total, err := a.jobs.List(r.Context(), jobs.ListFilter{Status: r.URL.Query().Get("status"), Query: r.URL.Query().Get("q"), Limit: limit, Offset: offset, OwnerID: ownerID})
	if err != nil {
		if errors.Is(err, jobs.ErrInvalidInput) {
			writeAPIError(w, http.StatusBadRequest, "invalid_query", "status must be queued, running, succeeded, failed, or cancelled")
		} else {
			writeJobError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": items, "total": total, "limit": limit, "offset": offset})
}

func (a *API) getJob(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeRead)
	if !ok || !a.jobsReady(w) {
		return
	}
	ownerID := ""
	if !p.IsAdmin() {
		ownerID = p.ID
	}
	detail, err := a.jobs.DetailFor(r.Context(), r.PathValue("id"), ownerID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (a *API) cancelJob(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeCancel)
	if !ok || !a.jobsReady(w) {
		return
	}
	ownerID := ""
	if !p.IsAdmin() {
		ownerID = p.ID
	}
	job, err := a.jobs.CancelFor(r.Context(), r.PathValue("id"), ownerID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (a *API) downloadJobResult(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeResult)
	if !ok || !a.jobsReady(w) {
		return
	}
	ownerID := ""
	if !p.IsAdmin() {
		ownerID = p.ID
	}
	file, job, contentType, err := a.jobs.OpenResultFor(r.Context(), r.PathValue("id"), ownerID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	defer file.Close()
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	prefix := "converted"
	if job.Mode == "compress" {
		prefix = "compressed"
	}
	filename := fmt.Sprintf("%s-%s.%s", prefix, job.ID, job.TargetFormat)
	contentDisposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", contentDisposition)
	w.Header().Set("Content-Length", fmt.Sprint(job.OutputSize))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func writeJobError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "job_error"
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		status = http.StatusNotFound
		code = "not_found"
	case errors.Is(err, jobs.ErrConflict):
		status = http.StatusConflict
		code = "conflict"
	case errors.Is(err, jobs.ErrUnsupported):
		status = http.StatusUnprocessableEntity
		code = "unsupported_conversion"
	case errors.Is(err, jobs.ErrInvalidInput):
		status = http.StatusBadRequest
		code = "invalid_input"
	case errors.Is(err, jobs.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
		code = "file_too_large"
	case errors.Is(err, jobs.ErrQuotaExceeded):
		status = http.StatusTooManyRequests
		code = "quota_exceeded"
	case errors.Is(err, jobs.ErrGuestCapacity):
		status = http.StatusTooManyRequests
		code = "guest_capacity_reached"
	case errors.Is(err, jobs.ErrInsufficientStorage):
		status = http.StatusInsufficientStorage
		code = "insufficient_storage"
	case errors.Is(err, jobs.ErrCanceled):
		status = http.StatusConflict
		code = "job_cancelled"
	case errors.Is(err, jobs.ErrFileExpired):
		status = http.StatusGone
		code = "file_expired"
	case errors.Is(err, jobs.ErrFileMissing):
		status = http.StatusGone
		code = "file_missing"
	}
	writeAPIError(w, status, code, err.Error())
}
