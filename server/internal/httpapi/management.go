package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
)

type engineInstallRequest struct {
	Definition      engine.Definition `json:"definition"`
	SourceDirectory string            `json:"sourceDirectory"`
}
type engineManualRequest struct {
	Definition      engine.Definition `json:"definition"`
	ExecutablePaths map[string]string `json:"executablePaths"`
}
type profileRequest struct {
	Definition   profile.Definition `json:"definition"`
	RepositoryID string             `json:"repositoryId,omitempty"`
}
type enableRequest struct {
	Enabled bool `json:"enabled"`
}

func (a *API) listEngines(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	v, err := a.management.ListEngines(r.Context())
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"engines": v})
}
func (a *API) installEngine(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	var req engineInstallRequest
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := a.management.InstallEngine(r.Context(), req.Definition, req.SourceDirectory)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}
func (a *API) updateEngine(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	var req engineInstallRequest
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := a.management.UpdateEngine(r.Context(), r.PathValue("id"), req.Definition, req.SourceDirectory)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (a *API) registerManualEngine(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	var req engineManualRequest
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := a.management.RegisterManualEngine(r.Context(), req.Definition, req.ExecutablePaths)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}
func (a *API) removeEngine(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	resolve := r.URL.Query().Get("resolveDependencies") == "true"
	err := a.management.RemoveEngine(r.Context(), r.PathValue("id"), r.PathValue("version"), resolve)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) listProfiles(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	v, err := a.management.ListProfiles(r.Context())
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": v})
}
func (a *API) installProfile(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	var req profileRequest
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := a.management.InstallProfile(r.Context(), req.Definition, req.RepositoryID)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}
func (a *API) updateProfile(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	var req profileRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Definition.ID != r.PathValue("id") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "id_mismatch", "message": "profile id must match the URL"})
		return
	}
	v, err := a.management.UpdateProfile(r.Context(), req.Definition, req.RepositoryID)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (a *API) setProfileEnabled(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	var req enableRequest
	if !decodeBody(w, r, &req) {
		return
	}
	v, err := a.management.SetProfileEnabled(r.Context(), r.PathValue("id"), req.Enabled)
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (a *API) removeProfile(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	if err := a.management.RemoveProfile(r.Context(), r.PathValue("id")); err != nil {
		writeManagementError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) resolveProfile(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	v, err := a.management.ResolveProfile(r.PathValue("id"))
	if err != nil {
		writeManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"engines": v})
}
func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request", "message": err.Error()})
		return false
	}
	if err := d.Decode(&struct{}{}); err != io.EOF {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "invalid_request", "message": "request must contain one JSON value"})
		return false
	}
	return true
}
func writeManagementError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := "invalid_request"
	if errors.Is(err, management.ErrNotFound) {
		status = http.StatusNotFound
		code = "not_found"
	} else if errors.Is(err, management.ErrConflict) {
		status = http.StatusConflict
		code = "conflict"
	}
	writeJSON(w, status, map[string]string{"code": code, "message": err.Error()})
}
