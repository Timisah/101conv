package httpapi

import (
	"net/http"
	"strconv"
)

func (a *API) getAnalyticsOverview(w http.ResponseWriter, r *http.Request) {
	p, ok := a.identity(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if !p.IsAdmin() {
		writeAPIError(w, http.StatusForbidden, "forbidden", "administrator permission is required for processing analytics")
		return
	}
	if a.analytics == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "analytics_unavailable", "processing analytics require PostgreSQL")
		return
	}
	overview, err := a.analytics.Overview(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "analytics_error", "could not load processing overview")
		return
	}
	writeJSON(w, http.StatusOK, overview)
}

func (a *API) listAnalyticsUsers(w http.ResponseWriter, r *http.Request) {
	p, ok := a.identity(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if !p.IsAdmin() {
		writeAPIError(w, http.StatusForbidden, "forbidden", "administrator permission is required for processing analytics")
		return
	}
	if a.analytics == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "analytics_unavailable", "processing analytics require PostgreSQL")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	users, total, err := a.analytics.ByUser(r.Context(), limit, offset)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "analytics_error", "could not load per-user processing statistics")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users, "total": total})
}
