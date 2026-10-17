package httpapi

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/jobs"
)

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
	r.Body = http.MaxBytesReader(w, r.Body, jobs.MaxInputBytes+(1<<20))
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
	upload, err := a.jobs.CreateUploadFor(r.Context(), p.ID, p.MaxFileBytes, p.Quotas.MonthlyBytes.Value, part.FileName(), part)
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
	job, duplicate, err := a.jobs.SubmitFor(r.Context(), req, p.ID, jobs.Limits{MonthlyJobs: p.Quotas.MonthlyJobs.Value})
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
