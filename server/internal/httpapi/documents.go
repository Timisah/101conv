package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/document"
)

func (a *API) listDocumentFormats(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireCapability(w, r, auth.ScopeRead); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"formats": document.SupportedFormats(),
	})
}

func (a *API) inspectDocumentSource(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeRead)
	if !ok || !a.jobsReady(w) {
		return
	}
	ownerID := ""
	if !p.IsAdmin() {
		ownerID = p.ID
	}
	upload, path, err := a.jobs.GetUploadFor(r.Context(), r.PathValue("id"), ownerID)
	if err != nil {
		writeJobError(w, err)
		return
	}
	info, err := document.InspectSource(upload.ID, path, upload.FileName, upload.Extension, upload.Size)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_document_source", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *API) listDocumentProjects(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeRead)
	if !ok || !a.jobsReady(w) {
		return
	}
	store := a.jobs.Documents()
	if store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "document project storage is unavailable")
		return
	}
	projects, err := store.List(r.Context(), p.ID, 50)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "document_project_error", "could not list document projects")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": projects})
}

func (a *API) createDocumentProject(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeSubmit)
	if !ok || !a.jobsReady(w) {
		return
	}
	store := a.jobs.Documents()
	if store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "document project storage is unavailable")
		return
	}
	var manifest document.Manifest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&manifest); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "invalid document manifest JSON")
		return
	}
	if err := manifest.Validate(); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_manifest", err.Error())
		return
	}
	if err := a.verifyManifestUploadOwnership(r.Context(), p, manifest); err != nil {
		writeJobError(w, err)
		return
	}
	var expiresAt *time.Time
	if p.Guest {
		hours := 24
		if a.accounts != nil {
			if settings, err := a.accounts.AnonymousSettings(r.Context()); err == nil && settings.UploadExpirationHours > 0 {
				hours = settings.UploadExpirationHours
			}
		}
		exp := time.Now().Add(time.Duration(hours) * time.Hour)
		expiresAt = &exp
	}
	project, err := store.Create(r.Context(), p.ID, p.Guest, manifest, expiresAt)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "document_project_error", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"project": project})
}

func (a *API) getDocumentProject(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeRead)
	if !ok || !a.jobsReady(w) {
		return
	}
	store := a.jobs.Documents()
	if store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "document project storage is unavailable")
		return
	}
	project, err := store.Get(r.Context(), r.PathValue("id"), p.ID, p.IsAdmin())
	if err != nil {
		writeDocumentProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project})
}

func (a *API) updateDocumentProject(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeSubmit)
	if !ok || !a.jobsReady(w) {
		return
	}
	store := a.jobs.Documents()
	if store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "document project storage is unavailable")
		return
	}
	var manifest document.Manifest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&manifest); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "invalid document manifest JSON")
		return
	}
	if err := manifest.Validate(); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_manifest", err.Error())
		return
	}
	if err := a.verifyManifestUploadOwnership(r.Context(), p, manifest); err != nil {
		writeJobError(w, err)
		return
	}
	project, err := store.Update(r.Context(), r.PathValue("id"), p.ID, p.IsAdmin(), manifest)
	if err != nil {
		writeDocumentProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project})
}

func (a *API) deleteDocumentProject(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeSubmit)
	if !ok || !a.jobsReady(w) {
		return
	}
	store := a.jobs.Documents()
	if store == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "unavailable", "document project storage is unavailable")
		return
	}
	if err := store.Delete(r.Context(), r.PathValue("id"), p.ID, p.IsAdmin()); err != nil {
		writeDocumentProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

type exportDocumentRequest struct {
	Manifest       document.Manifest `json:"manifest"`
	IdempotencyKey string            `json:"idempotencyKey"`
}

func (a *API) exportDocument(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeSubmit)
	if !ok || !a.jobsReady(w) {
		return
	}
	var req exportDocumentRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json", "invalid document export JSON")
		return
	}
	if strings.TrimSpace(req.IdempotencyKey) == "" {
		req.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	if err := req.Manifest.Validate(); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_manifest", err.Error())
		return
	}
	ownerID := p.ID
	if p.IsAdmin() {
		// Still attribute job to the caller's principal ID while permitting
		// ownership check inside verifyManifestUploadOwnership.
		if err := a.verifyManifestUploadOwnership(r.Context(), p, req.Manifest); err != nil {
			writeJobError(w, err)
			return
		}
	}
	jobOwner := p.ID
	if p.IsAdmin() {
		jobOwner = ""
	}
	job, replayed, err := a.jobs.SubmitDocumentBuildFor(r.Context(), jobOwner, req.Manifest, req.IdempotencyKey)
	if err != nil {
		writeJobError(w, err)
		return
	}
	_ = ownerID
	status := http.StatusAccepted
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"job": job, "replayed": replayed})
}

func (a *API) verifyManifestUploadOwnership(ctx context.Context, p auth.Principal, manifest document.Manifest) error {
	ownerID := p.ID
	if p.IsAdmin() {
		ownerID = ""
	}
	seen := make(map[string]bool)
	for _, page := range manifest.Pages {
		if seen[page.UploadID] {
			continue
		}
		seen[page.UploadID] = true
		if _, _, err := a.jobs.GetUploadFor(ctx, page.UploadID, ownerID); err != nil {
			return err
		}
	}
	return nil
}

func writeDocumentProjectError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, document.ErrProjectNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, document.ErrProjectExpired):
		writeAPIError(w, http.StatusGone, "project_expired", err.Error())
	case errors.Is(err, document.ErrForbidden):
		writeAPIError(w, http.StatusForbidden, "forbidden", err.Error())
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
	}
}
