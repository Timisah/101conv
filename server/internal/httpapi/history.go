package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/jobs"
)

// listHistory serves the unified Jobs & history stream: standalone jobs and
// batch parents, newest first, with composable filters. Ownership comes from the
// authenticated principal; only administrators may span or filter owners.
func (a *API) listHistory(w http.ResponseWriter, r *http.Request) {
	p, ok := a.requireCapability(w, r, auth.ScopeRead)
	if !ok || !a.jobsReady(w) {
		return
	}
	query := r.URL.Query()
	limit, offset := 25, 0
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeAPIError(w, http.StatusBadRequest, "invalid_query", "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	if raw := query.Get("offset"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > 100000 {
			writeAPIError(w, http.StatusBadRequest, "invalid_query", "offset must be between 0 and 100000")
			return
		}
		offset = parsed
	}
	filter := jobs.HistoryFilter{
		Mode: query.Get("mode"), Kind: query.Get("type"), Status: query.Get("status"), Query: query.Get("q"),
		ScopeOwnerID: batchOwner(p), OwnerID: query.Get("owner"), Limit: limit, Offset: offset, IncludeOwners: p.IsAdmin(),
	}
	if filter.OwnerID != "" && !p.IsAdmin() {
		writeAPIError(w, http.StatusForbidden, "permission_denied", "only administrators can filter history by owner")
		return
	}
	for name, target := range map[string]**time.Time{"from": &filter.From, "to": &filter.To} {
		if raw := query.Get(name); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_query", name+" must be an RFC 3339 timestamp")
				return
			}
			*target = &parsed
		}
	}
	entries, total, err := a.jobs.History(r.Context(), filter)
	if err != nil {
		writeJobError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "total": total, "limit": limit, "offset": offset})
}
