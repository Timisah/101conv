package httpapi

import (
	"errors"
	"net/http"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/capability"
)

func (a *API) capabilityCatalog(w http.ResponseWriter, r *http.Request) (capability.Catalog, auth.Principal, bool) {
	if a.management == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "management_unavailable", "converter management is not configured")
		return capability.Catalog{}, auth.Principal{}, false
	}
	p, ok := a.identity(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return capability.Catalog{}, auth.Principal{}, false
	}
	if !p.IsAdmin() && !capabilityCatalogAllowed(p) {
		writeAPIError(w, http.StatusForbidden, "permission_denied", "conversion or compression permission is required")
		return capability.Catalog{}, auth.Principal{}, false
	}
	profiles, err := a.management.ListProfiles(r.Context())
	if err != nil {
		writeManagementError(w, err)
		return capability.Catalog{}, auth.Principal{}, false
	}
	engines, err := a.management.ListEngines(r.Context())
	if err != nil {
		writeManagementError(w, err)
		return capability.Catalog{}, auth.Principal{}, false
	}
	catalog := capability.Build(profiles, engines)
	if p.Guest {
		catalog = catalog.FilterEnabled()
	}
	return catalog, p, true
}

func capabilityCatalogAllowed(p auth.Principal) bool {
	if p.APIKey {
		return auth.ScopeAllows(p, auth.ScopeSubmit) || auth.ScopeAllows(p, auth.ScopeCompress)
	}
	return p.Permissions[auth.PermissionConversion] || p.Permissions[auth.PermissionCompression]
}

func (a *API) listCapabilities(w http.ResponseWriter, r *http.Request) {
	catalog, _, ok := a.capabilityCatalog(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, catalog)
}

func (a *API) resolveCapability(w http.ResponseWriter, r *http.Request) {
	catalog, _, ok := a.capabilityCatalog(w, r)
	if !ok {
		return
	}
	request := capability.Request{Operation: r.URL.Query().Get("operation"), SourceFormat: r.URL.Query().Get("source"), TargetFormat: r.URL.Query().Get("target")}
	resolution, err := catalog.Resolve(request, r.URL.Query().Get("providerId"))
	if err != nil {
		var resolutionErr *capability.ResolutionError
		if errors.As(err, &resolutionErr) {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": map[string]string{"code": "unsupported_capability", "message": resolutionErr.Error()}, "request": resolutionErr.Request, "candidates": resolutionErr.Candidates})
			return
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_capability_request", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, resolution)
}
