package updatecheck

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckReportsStableUpdateAndCachesResult(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.2.0", "name": "Release 0.2.0", "html_url": "https://github.com/Timisah/101conv/releases/tag/v0.2.0", "body": "Fixes", "published_at": "2026-10-20T00:00:00Z"})
	}))
	defer server.Close()
	client := server.Client()
	checker := &Checker{currentVersion: "0.1.0", endpoint: server.URL, client: client, interval: time.Hour}
	first := checker.Check(context.Background())
	second := checker.Check(context.Background())
	if !first.UpdateAvailable || first.LatestVersion != "0.2.0" || first.Status != "update_available" {
		t.Fatalf("unexpected update status: %+v", first)
	}
	if second != first || calls != 1 {
		t.Fatalf("cached check = %+v, calls = %d", second, calls)
	}
}

func TestNewRejectsInsecureEndpoint(t *testing.T) {
	if _, err := New("0.1.0", "http://example.test/releases"); err == nil {
		t.Fatal("expected insecure update URL to be rejected")
	}
}

func TestSafeReleaseURLRejectsExecutableSchemes(t *testing.T) {
	if got := safeReleaseURL("javascript:alert(1)"); got != "" {
		t.Fatalf("unsafe release URL = %q", got)
	}
	if got := safeReleaseURL("https://github.com/Timisah/101conv/releases/tag/v0.2.0"); got == "" {
		t.Fatal("expected HTTPS release URL to be retained")
	}
}
