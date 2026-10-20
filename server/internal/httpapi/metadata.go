package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/jobs"
)

type metadataRemovalRequest struct {
	ProfileID      string `json:"profileId,omitempty"`
	ProfileVersion string `json:"profileVersion,omitempty"`
	TargetFormat   string `json:"targetFormat,omitempty"`
	IdempotencyKey string `json:"idempotencyKey"`
}

func prepareMetadataRemoval(upload jobs.Upload, request metadataRemovalRequest) (jobs.SubmitRequest, error) {
	target := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(request.TargetFormat)), ".")
	if target == "" {
		target = upload.Extension
	}
	if target != upload.Extension {
		return jobs.SubmitRequest{}, fmt.Errorf("metadata removal must keep the uploaded format; use the normal conversion endpoint for format changes")
	}
	return jobs.SubmitRequest{
		UploadID:       upload.ID,
		ProfileID:      strings.TrimSpace(request.ProfileID),
		ProfileVersion: strings.TrimSpace(request.ProfileVersion),
		TargetFormat:   target,
		Mode:           "convert",
		Options:        map[string]string{"metadata": "-1"},
		IdempotencyKey: strings.TrimSpace(request.IdempotencyKey),
	}, nil
}

// removeMetadata creates a new conversion job with the profile's explicit
// metadata-removal option. The source upload is never modified or deleted.
func (a *API) removeMetadata(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeSubmit)
	if !ok || !a.jobsReady(w) {
		return
	}
	if !a.consumeGuestRate(w, r, p, "submission") {
		return
	}
	ownerID := p.ID
	if p.IsAdmin() {
		ownerID = ""
	}
	upload, _, err := a.jobs.GetUploadFor(r.Context(), r.PathValue("id"), ownerID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	var request metadataRemovalRequest
	if !decodeBody(w, r, &request) {
		return
	}
	submit, err := prepareMetadataRemoval(upload, request)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	job, duplicate, err := a.jobs.SubmitFor(r.Context(), submit, p.ID, a.guestJobLimits(r.Context(), p))
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
