package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/timisah/101conv/server/internal/catalog"
	"github.com/timisah/101conv/server/internal/demo"
)

func TestDemoStatusDisabledByDefault(t *testing.T) {
	t.Setenv("DEMO_MODE", "false")
	cat, err := catalog.New("0.2.0")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	h := New("0.2.0", cat)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/demo/status", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/demo/status = %d, want 200", rec.Code)
	}
	var status demo.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Enabled {
		t.Fatalf("expected demo disabled by default, got %+v", status)
	}

	funnelReq := httptest.NewRequest(http.MethodPost, "/api/v1/demo/funnel", bytes.NewBufferString(`{"event":"demo_visit"}`))
	funnelReq.Header.Set("Origin", "http://example.com")
	funnelReq.Host = "example.com"
	funnelRec := httptest.NewRecorder()
	h.ServeHTTP(funnelRec, funnelReq)
	if funnelRec.Code != http.StatusNotFound {
		t.Fatalf("POST /api/v1/demo/funnel when disabled = %d, want 404", funnelRec.Code)
	}
}

func TestDemoEndpointsEnforceOriginAllowlistAndAdminAuth(t *testing.T) {
	t.Setenv("DEMO_MODE", "true")
	t.Setenv("DEMO_MAX_FILE_BYTES", "10485760")
	t.Setenv("DEMO_MAX_BATCH_FILES", "2")
	t.Setenv("DEMO_ALLOWED_PROFILES", "ffmpeg-image-linux-amd64,jpeg-xl-linux-amd64")

	cat, err := catalog.New("0.2.0")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	const adminToken = "test-demo-admin-token-1234567890"
	h := NewWithManagement("0.2.0", cat, nil, adminToken)

	// Public status reports bounds and allowed profiles.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/demo/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/demo/status = %d, want 200", rec.Code)
	}
	var status demo.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if !status.Enabled || status.MaxBatchFiles != 2 || len(status.AllowedProfiles) != 2 {
		t.Fatalf("unexpected status: %+v", status)
	}

	// Cross-origin funnel event is rejected.
	cross := httptest.NewRequest(http.MethodPost, "/api/v1/demo/funnel", bytes.NewBufferString(`{"event":"cta_github_click"}`))
	cross.Host = "demo.example.com"
	cross.Header.Set("Origin", "https://attacker.example")
	crossRec := httptest.NewRecorder()
	h.ServeHTTP(crossRec, cross)
	if crossRec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin funnel = %d, want 403", crossRec.Code)
	}

	// Same-origin valid funnel event succeeds.
	okReq := httptest.NewRequest(http.MethodPost, "/api/v1/demo/funnel", bytes.NewBufferString(`{"event":"cta_github_click"}`))
	okReq.Host = "demo.example.com"
	okReq.Header.Set("Origin", "http://demo.example.com")
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, okReq)
	if okRec.Code != http.StatusNoContent {
		t.Fatalf("same-origin funnel = %d, want 204 (%s)", okRec.Code, okRec.Body.String())
	}

	// Server-only event cannot be forged from browser.
	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/demo/funnel", bytes.NewBufferString(`{"event":"demo_conversion_started"}`))
	badReq.Host = "demo.example.com"
	badReq.Header.Set("Origin", "http://demo.example.com")
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("non-public funnel event = %d, want 400", badRec.Code)
	}

	// Unauthenticated metrics and kill switch are rejected.
	unauthMetrics := httptest.NewRecorder()
	h.ServeHTTP(unauthMetrics, httptest.NewRequest(http.MethodGet, "/api/v1/demo/metrics", nil))
	if unauthMetrics.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated metrics = %d, want 401", unauthMetrics.Code)
	}

	unauthKill := httptest.NewRecorder()
	h.ServeHTTP(unauthKill, httptest.NewRequest(http.MethodPut, "/api/v1/demo/kill-switch", bytes.NewBufferString(`{"active":true}`)))
	if unauthKill.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated kill switch = %d, want 401", unauthKill.Code)
	}

	// Admin can toggle kill switch and inspect aggregate funnel metrics.
	killReq := httptest.NewRequest(http.MethodPut, "/api/v1/demo/kill-switch", bytes.NewBufferString(`{"active":true,"reason":"load spike"}`))
	killReq.Header.Set("Authorization", "Bearer "+adminToken)
	killRec := httptest.NewRecorder()
	h.ServeHTTP(killRec, killReq)
	if killRec.Code != http.StatusOK {
		t.Fatalf("admin kill switch = %d, want 200 (%s)", killRec.Code, killRec.Body.String())
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/api/v1/demo/metrics?days=7", nil)
	metricsReq.Header.Set("Authorization", "Bearer "+adminToken)
	metricsRec := httptest.NewRecorder()
	h.ServeHTTP(metricsRec, metricsReq)
	if metricsRec.Code != http.StatusOK {
		t.Fatalf("admin metrics = %d, want 200 (%s)", metricsRec.Code, metricsRec.Body.String())
	}
	var summary demo.MetricsSummary
	if err := json.Unmarshal(metricsRec.Body.Bytes(), &summary); err != nil {
		t.Fatalf("decode metrics: %v", err)
	}
	if !summary.KillSwitchActive || summary.KillSwitchReason != "load spike" {
		t.Fatalf("expected active kill switch in metrics, got %+v", summary)
	}
	if summary.Totals[demo.EventCTAGitHubClick] != 1 {
		t.Fatalf("expected 1 cta_github_click, got %+v", summary.Totals)
	}
}
