package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
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
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"overview": overview, "retention": policy, "recentCleanup": events})
}

func (a *API) cleanupPreviewToken(days int, report jobs.CleanupReport) string {
	mac := hmac.New(sha256.New, []byte(a.adminToken))
	fmt.Fprintf(mac, "abandoned-upload:%d:%d:%d:%d", days, report.Candidates, report.EstimatedBytes, time.Now().UTC().Unix()/600)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *API) previewStorageCleanup(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || !a.jobsReady(w) {
		return
	}
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || days < 1 || days > 3650 {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_retention", "days must be between 1 and 3650")
		return
	}
	report, err := a.jobs.PreviewAbandonedCleanup(r.Context(), days)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "cleanup_preview_error", "could not preview cleanup")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"report": report, "previewToken": a.cleanupPreviewToken(days, report), "expiresWithinMinutes": 10, "estimate": true})
}

func (a *API) validCleanupPreview(r *http.Request, days int, token string) bool {
	if token == "" {
		return false
	}
	report, err := a.jobs.PreviewAbandonedCleanup(r.Context(), days)
	if err != nil {
		return false
	}
	want := a.cleanupPreviewToken(days, report)
	return hmac.Equal([]byte(want), []byte(token))
}

func (a *API) updateStorageRetention(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || !a.jobsReady(w) {
		return
	}
	var req struct {
		AbandonedUploadDays *int   `json:"abandonedUploadDays"`
		PreviewToken        string `json:"previewToken"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.AbandonedUploadDays != nil {
		if *req.AbandonedUploadDays < 1 || *req.AbandonedUploadDays > 3650 {
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_retention", "days must be between 1 and 3650")
			return
		}
		if !a.validCleanupPreview(r, *req.AbandonedUploadDays, req.PreviewToken) {
			writeAPIError(w, http.StatusConflict, "preview_stale", "run a new cleanup preview before saving the retention policy")
			return
		}
	}
	if err := a.jobs.SetRetentionPolicy(r.Context(), jobs.RetentionPolicy{AbandonedUploadDays: req.AbandonedUploadDays}); err != nil {
		writeJobError(w, err)
		return
	}
	if a.accounts != nil {
		if p, ok := a.identity(r); ok {
			_ = a.accounts.RecordAudit(r.Context(), p.ID, "storage.retention_changed", "instance", "1", map[string]any{"abandonedUploadDays": req.AbandonedUploadDays})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"retention": jobs.RetentionPolicy{AbandonedUploadDays: req.AbandonedUploadDays}})
}

func (a *API) applyStorageCleanup(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || !a.jobsReady(w) {
		return
	}
	var req struct {
		Days         int    `json:"days"`
		PreviewToken string `json:"previewToken"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Days < 1 || req.Days > 3650 {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_retention", "days must be between 1 and 3650")
		return
	}
	if !a.validCleanupPreview(r, req.Days, req.PreviewToken) {
		writeAPIError(w, http.StatusConflict, "preview_stale", "run a new cleanup preview before deleting files")
		return
	}
	report, err := a.jobs.CleanupAbandoned(r.Context(), req.Days, 100)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "cleanup_interrupted", fmt.Sprintf("cleanup interrupted after %d deletions; retry later to resume", report.Deleted))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": report, "limitPerRun": 100})
}
