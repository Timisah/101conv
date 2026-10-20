package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/presets"
)

type duplicatePresetRequest struct {
	Name string `json:"name"`
}

type defaultPresetRequest struct {
	Enabled *bool `json:"enabled"`
}

func (a *API) presetRead(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	if a.presets == nil || a.management == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "presets_unavailable", "preset storage is not configured")
		return auth.Principal{}, false
	}
	p, ok := a.identity(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return auth.Principal{}, false
	}
	if !p.IsAdmin() && !capabilityCatalogAllowed(p) {
		writeAPIError(w, http.StatusForbidden, "permission_denied", "conversion or compression permission is required")
		return auth.Principal{}, false
	}
	return p, true
}

func (a *API) presetMutation(w http.ResponseWriter, r *http.Request, mode string) (auth.Principal, bool) {
	if a.presets == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "presets_unavailable", "preset storage is not configured")
		return auth.Principal{}, false
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "convert" && mode != "compress" {
		writeAPIError(w, http.StatusBadRequest, "invalid_preset_mode", "preset mode must be convert or compress")
		return auth.Principal{}, false
	}
	if a.jobs != nil {
		scope := auth.ScopeSubmit
		if mode == "compress" {
			scope = auth.ScopeCompress
		}
		p, ok := a.requireCapability(w, r, scope)
		if !ok {
			return auth.Principal{}, false
		}
		if p.Guest {
			writeAPIError(w, http.StatusForbidden, "guest_endpoint_forbidden", "guest sessions cannot save personal presets")
			return auth.Principal{}, false
		}
		return p, true
	}
	p, method, ok := a.identityWithMethod(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return auth.Principal{}, false
	}
	if method == "session" && !sameOriginRequest(r) || method == "guest" && !guestSessionRequest(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
		return auth.Principal{}, false
	}
	if p.IsAdmin() {
		return p, true
	}
	if p.Guest {
		writeAPIError(w, http.StatusForbidden, "guest_endpoint_forbidden", "guest sessions cannot save personal presets")
		return auth.Principal{}, false
	}
	permission := auth.PermissionConversion
	if mode == "compress" {
		permission = auth.PermissionCompression
	}
	if p.APIKey {
		scope := auth.ScopeSubmit
		if mode == "compress" {
			scope = auth.ScopeCompress
		}
		if !auth.ScopeAllows(p, scope) || !p.Permissions[permission] {
			writeAPIError(w, http.StatusForbidden, "permission_denied", "this account or API key does not have the required permission")
			return auth.Principal{}, false
		}
	} else if !p.Permissions[permission] {
		writeAPIError(w, http.StatusForbidden, "permission_denied", "this account does not have the required permission")
		return auth.Principal{}, false
	}
	return p, true
}

func (a *API) presetAnyMutation(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := a.presetRead(w, r)
	if !ok {
		return auth.Principal{}, false
	}
	if p.Guest {
		writeAPIError(w, http.StatusForbidden, "guest_endpoint_forbidden", "guest sessions cannot modify presets")
		return auth.Principal{}, false
	}
	_, method, _ := a.identityWithMethod(r)
	if method == "session" && !sameOriginRequest(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
		return auth.Principal{}, false
	}
	if p.IsAdmin() {
		return p, true
	}
	if p.APIKey {
		allowed := auth.ScopeAllows(p, auth.ScopeSubmit) && p.Permissions[auth.PermissionConversion]
		allowed = allowed || auth.ScopeAllows(p, auth.ScopeCompress) && p.Permissions[auth.PermissionCompression]
		if !allowed {
			writeAPIError(w, http.StatusForbidden, "permission_denied", "this account or API key does not have a conversion or compression permission")
			return auth.Principal{}, false
		}
	}
	return p, true
}

func (a *API) presetExistingMutation(w http.ResponseWriter, r *http.Request, id string) (auth.Principal, bool) {
	// Authenticate and apply the broad mutation checks first so an unknown ID
	// cannot be used to probe preset storage anonymously.
	p, ok := a.presetAnyMutation(w, r)
	if !ok {
		return auth.Principal{}, false
	}
	mode, err := a.presets.Mode(r.Context(), p.ID, id)
	if err != nil {
		presetError(w, err)
		return auth.Principal{}, false
	}
	return a.presetMutation(w, r, mode)
}

func presetError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, presets.ErrInvalidInput):
		writeAPIError(w, http.StatusBadRequest, "invalid_preset", err.Error())
	case errors.Is(err, presets.ErrIncompatible):
		writeAPIError(w, http.StatusUnprocessableEntity, "incompatible_preset", err.Error())
	case errors.Is(err, presets.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "preset_not_found", err.Error())
	case errors.Is(err, presets.ErrForbidden):
		writeAPIError(w, http.StatusForbidden, "preset_forbidden", err.Error())
	default:
		writeAPIError(w, http.StatusInternalServerError, "preset_error", "the preset operation failed")
	}
}

func (a *API) listPresets(w http.ResponseWriter, r *http.Request) {
	p, ok := a.presetRead(w, r)
	if !ok {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode != "" && mode != "convert" && mode != "compress" {
		writeAPIError(w, http.StatusBadRequest, "invalid_preset_mode", "preset mode must be convert or compress")
		return
	}
	items, err := a.presets.List(r.Context(), p.ID, mode)
	if err != nil {
		presetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"presets": items})
}

func (a *API) createPreset(w http.ResponseWriter, r *http.Request) {
	var request presets.WriteRequest
	if !decodeBody(w, r, &request) {
		return
	}
	p, ok := a.presetMutation(w, r, request.Mode)
	if !ok {
		return
	}
	if request.Shared && !p.IsAdmin() {
		writeAPIError(w, http.StatusForbidden, "preset_forbidden", "only administrators can create shared presets")
		return
	}
	item, err := a.presets.Create(r.Context(), p.ID, request)
	if err != nil {
		presetError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (a *API) updatePreset(w http.ResponseWriter, r *http.Request) {
	var request presets.WriteRequest
	if !decodeBody(w, r, &request) {
		return
	}
	p, ok := a.presetMutation(w, r, request.Mode)
	if !ok {
		return
	}
	item, err := a.presets.Update(r.Context(), p.ID, r.PathValue("id"), request, p.IsAdmin())
	if err != nil {
		presetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *API) duplicatePreset(w http.ResponseWriter, r *http.Request) {
	var request duplicatePresetRequest
	if !decodeBody(w, r, &request) {
		return
	}
	// The source mode is loaded by the service so this ID-only endpoint still
	// enforces the operation-specific permission.
	p, ok := a.presetExistingMutation(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	item, err := a.presets.Duplicate(r.Context(), p.ID, r.PathValue("id"), request.Name)
	if err != nil {
		presetError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (a *API) deletePreset(w http.ResponseWriter, r *http.Request) {
	p, ok := a.presetExistingMutation(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	if err := a.presets.Delete(r.Context(), p.ID, r.PathValue("id"), p.IsAdmin()); err != nil {
		presetError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setPresetDefault(w http.ResponseWriter, r *http.Request) {
	var request defaultPresetRequest
	if !decodeBody(w, r, &request) {
		return
	}
	if request.Enabled == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_preset", "enabled is required")
		return
	}
	p, ok := a.presetExistingMutation(w, r, r.PathValue("id"))
	if !ok {
		return
	}
	item, err := a.presets.SetDefault(r.Context(), p.ID, r.PathValue("id"), p.IsAdmin(), *request.Enabled)
	if err != nil {
		presetError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
