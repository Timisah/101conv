package httpapi

import (
	"net/http"
	"time"

	"github.com/timisah/101conv/server/internal/updatecheck"
)

func (a *API) updateStatus(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if a.updates == nil {
		writeJSON(w, http.StatusOK, updatecheck.Status{
			CurrentVersion: a.version,
			CheckedAt:      time.Now().UTC(),
			Status:         "unavailable",
			Error:          "Release update checks are not configured.",
		})
		return
	}
	writeJSON(w, http.StatusOK, a.updates.Check(r.Context()))
}
