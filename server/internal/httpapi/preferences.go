package httpapi

import (
	"net/http"
	"strings"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/jobs"
)

type preferenceRequest struct {
	ProfileID string `json:"profileId"`
}

func preferenceScope(mode string) string {
	if strings.EqualFold(mode, "compress") {
		return auth.ScopeCompress
	}
	return auth.ScopeSubmit
}

func (a *API) listProfilePreferences(w http.ResponseWriter, r *http.Request) {
	if !a.jobsReady(w) {
		return
	}
	p, ok := a.identity(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	if mode != "" {
		if _, ok = a.requireCapability(w, r, preferenceScope(mode)); !ok {
			return
		}
	}
	if mode != "" {
		rows, err := a.jobs.ListProfilePreferences(r.Context(), p.ID, mode)
		if err != nil {
			writeJobError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"preferences": rows})
		return
	}
	modes := []string{}
	for _, candidate := range []string{"convert", "compress"} {
		allowed := p.IsAdmin()
		permission := auth.PermissionConversion
		if candidate == "compress" {
			permission = auth.PermissionCompression
		}
		if !allowed && p.Permissions[permission] {
			allowed = true
			if p.APIKey {
				scope := auth.ScopeSubmit
				if candidate == "compress" {
					scope = auth.ScopeCompress
				}
				allowed = auth.ScopeAllows(p, scope)
			}
		}
		if allowed {
			modes = append(modes, candidate)
		}
	}
	if len(modes) == 0 {
		writeAPIError(w, http.StatusForbidden, "permission_denied", "conversion or compression permission is required")
		return
	}
	all := []jobs.PreferenceMapping{}
	for _, candidate := range modes {
		rows, err := a.jobs.ListProfilePreferences(r.Context(), p.ID, candidate)
		if err != nil {
			writeJobError(w, err)
			return
		}
		all = append(all, rows...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"preferences": all})
}

func (a *API) listGlobalProfilePreferences(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || !a.jobsReady(w) {
		return
	}
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode")))
	rows, err := a.jobs.ListProfilePreferences(r.Context(), "", mode)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"preferences": rows})
}

func (a *API) setMyProfilePreference(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, preferenceScope(r.PathValue("mode")))
	if !ok || !a.jobsReady(w) {
		return
	}
	if p.Guest {
		writeAPIError(w, http.StatusForbidden, "guest_endpoint_forbidden", "guest sessions use administrator defaults and cannot save personal preferences")
		return
	}
	var req preferenceRequest
	if !decodeBody(w, r, &req) {
		return
	}
	err := a.jobs.SetProfilePreference(r.Context(), p.ID, r.PathValue("mode"), r.PathValue("source"), r.PathValue("target"), req.ProfileID, false)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

func (a *API) deleteMyProfilePreference(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, preferenceScope(r.PathValue("mode")))
	if !ok || !a.jobsReady(w) {
		return
	}
	if p.Guest {
		writeAPIError(w, http.StatusForbidden, "guest_endpoint_forbidden", "guest sessions cannot save personal preferences")
		return
	}
	if err := a.jobs.DeleteProfilePreference(r.Context(), p.ID, r.PathValue("mode"), r.PathValue("source"), r.PathValue("target"), false); err != nil {
		writeJobError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) setGlobalProfilePreference(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || !a.jobsReady(w) {
		return
	}
	var req preferenceRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := a.jobs.SetProfilePreference(r.Context(), "admin", r.PathValue("mode"), r.PathValue("source"), r.PathValue("target"), req.ProfileID, true); err != nil {
		writeJobError(w, err)
		return
	}
	if a.accounts != nil {
		if p, ok := a.identity(r); ok {
			if err := a.accounts.RecordAudit(r.Context(), p.ID, "profile.default.changed", "profile-preference", r.PathValue("mode")+":"+r.PathValue("source")+":"+r.PathValue("target"), map[string]string{"profileId": req.ProfileID}); err != nil {
				writeAPIError(w, http.StatusInternalServerError, "audit_error", "the default was saved but its audit record could not be written")
				return
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"saved": true})
}

func (a *API) deleteGlobalProfilePreference(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || !a.jobsReady(w) {
		return
	}
	if err := a.jobs.DeleteProfilePreference(r.Context(), "admin", r.PathValue("mode"), r.PathValue("source"), r.PathValue("target"), true); err != nil {
		writeJobError(w, err)
		return
	}
	if a.accounts != nil {
		if p, ok := a.identity(r); ok {
			if err := a.accounts.RecordAudit(r.Context(), p.ID, "profile.default.cleared", "profile-preference", r.PathValue("mode")+":"+r.PathValue("source")+":"+r.PathValue("target"), nil); err != nil {
				writeAPIError(w, http.StatusInternalServerError, "audit_error", "the default was cleared but its audit record could not be written")
				return
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
