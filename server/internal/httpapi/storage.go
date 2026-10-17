package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/timisah/101conv/server/internal/jobs"
)

func (a *API) storageOverview(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || !a.jobsReady(w) {
		return
	}
	overview, err := a.jobs.StorageOverview(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "storage_error", "could not measure storage records")
		return
	}
	policy, err := a.jobs.GetRetentionPolicy(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "storage_error", "could not read retention settings")
		return
	}
	events, err := a.jobs.RecentCleanup(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "storage_error", "could not read cleanup history")
		return
	}
	scan, err := a.jobs.StorageScan(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "storage_error", "could not read filesystem scan status")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"overview": overview, "retention": policy, "recentCleanup": events, "scan": scan})
}

func (a *API) startStorageScan(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || !a.jobsReady(w) {
		return
	}
	scan, err := a.jobs.StartStorageScan(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "storage_scan_error", "could not start the filesystem inventory scan")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, map[string]any{"scan": scan})
}

func (a *API) cleanupPreviewToken(category string, days int, report jobs.CleanupReport) string {
	mac := hmac.New(sha256.New, []byte(a.adminToken))
	fmt.Fprintf(mac, "retention:%s:%d:%d:%d:%d", category, days, report.Candidates, report.EstimatedBytes, time.Now().UTC().Unix()/600)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *API) previewStorageCleanup(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || !a.jobsReady(w) {
		return
	}
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	category := r.URL.Query().Get("category")
	if category == "" {
		category = "unsubmitted"
	}
	if err != nil || ((days < 1 || days > 3650) && !(category == "successful_originals" && days == 0)) {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_retention", "days must be between 1 and 3650")
		return
	}
	report, err := a.jobs.PreviewCleanupCategory(r.Context(), category, days)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "cleanup_preview_error", "could not preview cleanup")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"category": category, "report": report, "previewToken": a.cleanupPreviewToken(category, days, report), "expiresWithinMinutes": 10, "estimate": true})
}

func (a *API) validCleanupPreview(r *http.Request, category string, days int, token string) bool {
	if token == "" {
		return false
	}
	report, err := a.jobs.PreviewCleanupCategory(r.Context(), category, days)
	if err != nil {
		return false
	}
	want := a.cleanupPreviewToken(category, days, report)
	return hmac.Equal([]byte(want), []byte(token))
}

func (a *API) updateStorageRetention(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || !a.jobsReady(w) {
		return
	}
	var req struct {
		AbandonedUploadDays json.RawMessage     `json:"abandonedUploadDays"`
		Unsubmitted         *jobs.RetentionRule `json:"unsubmittedUploads"`
		SubmittedOriginals  *jobs.RetentionRule `json:"submittedOriginals"`
		SuccessfulResults   *jobs.RetentionRule `json:"successfulResults"`
		FailedOriginals     *jobs.RetentionRule `json:"failedOriginals"`
		CanceledOriginals   *jobs.RetentionRule `json:"canceledOriginals"`
		PreviewToken        string              `json:"previewToken"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	var legacyDays *int
	if len(req.AbandonedUploadDays) > 0 && string(req.AbandonedUploadDays) != "null" {
		var value int
		if err := json.Unmarshal(req.AbandonedUploadDays, &value); err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_retention", "abandonedUploadDays must be an integer or null")
			return
		}
		legacyDays = &value
	}
	if legacyDays != nil {
		if *legacyDays < 1 || *legacyDays > 3650 {
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_retention", "days must be between 1 and 3650")
			return
		}
		if !a.validCleanupPreview(r, "unsubmitted", *legacyDays, req.PreviewToken) {
			writeAPIError(w, http.StatusConflict, "preview_stale", "run a new cleanup preview before saving the retention policy")
			return
		}
	}
	for _, item := range []struct {
		category string
		rule     *jobs.RetentionRule
	}{{"submitted_originals", req.SubmittedOriginals}, {"successful_results", req.SuccessfulResults}, {"failed_originals", req.FailedOriginals}, {"canceled_originals", req.CanceledOriginals}} {
		if item.rule != nil && item.rule.Mode == "after_days" {
			if item.rule.Days == nil || !a.validCleanupPreview(r, item.category, *item.rule.Days, req.PreviewToken) {
				writeAPIError(w, http.StatusConflict, "preview_required", "preview this retention category before saving a time-based deletion policy")
				return
			}
		}
	}
	if req.SubmittedOriginals != nil && req.SubmittedOriginals.Mode == "after_success" && !a.validCleanupPreview(r, "successful_originals", 0, req.PreviewToken) {
		writeAPIError(w, http.StatusConflict, "preview_required", "preview successful originals before enabling deletion after conversion")
		return
	}
	if req.Unsubmitted != nil && req.Unsubmitted.Mode == "after_days" && (req.Unsubmitted.Days == nil || !a.validCleanupPreview(r, "unsubmitted", *req.Unsubmitted.Days, req.PreviewToken)) {
		writeAPIError(w, http.StatusConflict, "preview_required", "preview unsubmitted uploads before saving a time-based deletion policy")
		return
	}
	policy, err := a.jobs.GetRetentionPolicy(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "storage_error", "could not read retention settings")
		return
	}
	if len(req.AbandonedUploadDays) > 0 {
		policy.AbandonedUploadDays = legacyDays
		if req.Unsubmitted == nil {
			if legacyDays == nil {
				policy.Unsubmitted = jobs.RetentionRule{Mode: "manual"}
			} else {
				policy.Unsubmitted = jobs.RetentionRule{Mode: "after_days", Days: legacyDays}
			}
		}
	}
	if req.Unsubmitted != nil {
		policy.Unsubmitted = *req.Unsubmitted
	}
	if req.SubmittedOriginals != nil {
		policy.SubmittedOriginals = *req.SubmittedOriginals
	}
	if req.SuccessfulResults != nil {
		policy.SuccessfulResults = *req.SuccessfulResults
	}
	if req.FailedOriginals != nil {
		policy.FailedOriginals = *req.FailedOriginals
	}
	if req.CanceledOriginals != nil {
		policy.CanceledOriginals = *req.CanceledOriginals
	}
	if err := a.jobs.SetRetentionPolicy(r.Context(), policy); err != nil {
		writeJobError(w, err)
		return
	}
	if a.accounts != nil {
		if p, ok := a.identity(r); ok {
			_ = a.accounts.RecordAudit(r.Context(), p.ID, "storage.retention_changed", "instance", "1", policy)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"retention": policy})
}

func (a *API) applyStorageCleanup(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || !a.jobsReady(w) {
		return
	}
	var req struct {
		Category     string `json:"category"`
		Days         int    `json:"days"`
		PreviewToken string `json:"previewToken"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if (req.Days < 1 || req.Days > 3650) && !(req.Category == "successful_originals" && req.Days == 0) {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_retention", "days must be between 1 and 3650")
		return
	}
	if req.Category == "" {
		req.Category = "unsubmitted"
	}
	if !a.validCleanupPreview(r, req.Category, req.Days, req.PreviewToken) {
		writeAPIError(w, http.StatusConflict, "preview_stale", "run a new cleanup preview before deleting files")
		return
	}
	report, err := a.jobs.CleanupCategory(r.Context(), req.Category, req.Days, 100)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "cleanup_interrupted", fmt.Sprintf("cleanup interrupted after %d deletions; retry later to resume", report.Deleted))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": report, "limitPerRun": 100})
}
