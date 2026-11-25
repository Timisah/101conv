package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/demo"
)

func (a *API) getDemoStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.demo == nil {
		writeJSON(w, http.StatusOK, demo.Status{Enabled: false})
		return
	}
	writeJSON(w, http.StatusOK, a.demo.Status())
}

func (a *API) recordDemoFunnel(w http.ResponseWriter, r *http.Request) {
	if a.demo == nil || !a.demo.Enabled() {
		writeAPIError(w, http.StatusNotFound, "demo_disabled", "public demo mode is not enabled on this instance")
		return
	}
	if !sameOriginRequest(r) {
		writeAPIError(w, http.StatusForbidden, "cross_origin_forbidden", "cross-origin requests are not allowed")
		return
	}
	var req struct {
		Event string `json:"event"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !demo.PublicFunnelEvents[req.Event] {
		writeAPIError(w, http.StatusBadRequest, "invalid_event", "unsupported public demo funnel event")
		return
	}
	if err := a.demo.RecordEvent(r.Context(), req.Event); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "demo_metrics_error", "could not record funnel event")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) getDemoMetrics(w http.ResponseWriter, r *http.Request) {
	p, ok := a.identity(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if !p.IsAdmin() {
		writeAPIError(w, http.StatusForbidden, "forbidden", "administrator permission is required for demo metrics")
		return
	}
	if a.demo == nil {
		writeJSON(w, http.StatusOK, demo.MetricsSummary{Enabled: false, Totals: map[string]int64{}, Daily: []demo.DailyCounts{}})
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	summary, err := a.demo.Metrics(r.Context(), days)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "demo_metrics_error", "could not load demo metrics")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, summary)
}

func (a *API) setDemoKillSwitch(w http.ResponseWriter, r *http.Request) {
	p, ok := a.identity(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if !p.IsAdmin() {
		writeAPIError(w, http.StatusForbidden, "forbidden", "administrator permission is required to control the demo kill switch")
		return
	}
	if a.demo == nil || !a.demo.Enabled() {
		writeAPIError(w, http.StatusConflict, "demo_disabled", "public demo mode is not enabled on this instance")
		return
	}
	var req struct {
		Active bool   `json:"active"`
		Reason string `json:"reason"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	status := a.demo.SetKillSwitch(req.Active, req.Reason)
	writeJSON(w, http.StatusOK, status)
}

func (a *API) enforceDemoUpload(w http.ResponseWriter, r *http.Request, p auth.Principal) (int64, bool) {
	maxBytes := p.MaxFileBytes
	if a.demo == nil || !a.demo.Enabled() || p.IsAdmin() {
		return maxBytes, true
	}
	if err := a.demo.CheckUpload(r.Context(), r.ContentLength); err != nil {
		writeDemoError(w, err)
		return 0, false
	}
	if demoMax := a.demo.MaxFileBytes(); demoMax > 0 && (maxBytes <= 0 || demoMax < maxBytes) {
		maxBytes = demoMax
	}
	return maxBytes, true
}

func (a *API) enforceDemoSubmission(w http.ResponseWriter, r *http.Request, p auth.Principal, profileID string, fileCount int) bool {
	if a.demo == nil || !a.demo.Enabled() || p.IsAdmin() {
		return true
	}
	activeJobs := 0
	if a.jobs != nil {
		if ops, err := a.jobs.Operations(r.Context()); err == nil {
			activeJobs = int(ops.JobStates["queued"] + ops.JobStates["running"])
		}
	}
	if err := a.demo.CheckSubmission(r.Context(), activeJobs, profileID, fileCount); err != nil {
		writeDemoError(w, err)
		return false
	}
	return true
}

func writeDemoError(w http.ResponseWriter, err error) {
	var rej *demo.RejectionError
	if errors.As(err, &rej) {
		if rej.RetryAfterSeconds > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(rej.RetryAfterSeconds))
		}
		writeAPIError(w, rej.HTTPStatus, rej.Code, rej.Message)
		return
	}
	writeAPIError(w, http.StatusServiceUnavailable, "demo_limit_error", err.Error())
}
