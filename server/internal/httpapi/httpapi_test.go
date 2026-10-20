package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timisah/101conv/server/internal/capability"
	"github.com/timisah/101conv/server/internal/catalog"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/repositorymanager"
)

func TestCatalogSourceIsAvailableOnFreshInstall(t *testing.T) {
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New("0.1.0-dev", catalogService))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/api/v1/catalogs")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("status = %d", response.StatusCode)
	}
}

func TestPresetRoutesReportUnavailableStorage(t *testing.T) {
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewWithPresets("0.1.0-dev", catalogService, nil, nil, nil, nil, nil, nil, ""))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/v1/presets?mode=convert")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("preset status = %d, want 503", response.StatusCode)
	}
}

func TestUpdateStatusRequiresAdministratorManagement(t *testing.T) {
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewWithPresets("0.1.0-dev", catalogService, nil, nil, nil, nil, nil, nil, ""))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/api/v1/system/update")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("update status = %d, want 503 when management is unavailable", response.StatusCode)
	}
}

func TestHealthIsLivenessAndReadinessReportsMissingDependencies(t *testing.T) {
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New("0.1.0-dev", catalogService))
	defer server.Close()

	response, err := server.Client().Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("health status = %d, want 200", response.StatusCode)
	}
	var health map[string]any
	if err = json.NewDecoder(response.Body).Decode(&health); err != nil {
		response.Body.Close()
		t.Fatal(err)
	}
	response.Body.Close()
	if health["status"] != "ok" {
		t.Fatalf("health status body = %v, want ok", health["status"])
	}

	response, err = server.Client().Get(server.URL + "/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", response.StatusCode)
	}
	var readiness map[string]any
	if err = json.NewDecoder(response.Body).Decode(&readiness); err != nil {
		t.Fatal(err)
	}
	if readiness["coreReady"] != false {
		t.Fatalf("readiness body = %#v, want coreReady false", readiness)
	}
}

func TestSameOriginRequestChecksExactOriginAndScheme(t *testing.T) {
	tests := []struct {
		name          string
		host          string
		origin        string
		referer       string
		fetchSite     string
		forwarded     string
		forwardedHost string
		trustedProxy  bool
		wantAllowed   bool
	}{
		{name: "same origin", host: "example.test:5656", origin: "http://example.test:5656", fetchSite: "same-origin", wantAllowed: true},
		{name: "exact origin despite same-site metadata", host: "example.test:5656", origin: "http://example.test:5656", fetchSite: "same-site", wantAllowed: true},
		{name: "same-origin metadata without referrer", host: "example.test:5656", fetchSite: "same-origin", wantAllowed: true},
		{name: "same-site metadata without referrer", host: "example.test:5656", fetchSite: "same-site", wantAllowed: true},
		{name: "metadata cannot override mismatched origin", host: "example.test:5656", origin: "https://public.example", fetchSite: "same-origin", wantAllowed: false},
		{name: "same-origin referer fallback", host: "example.test:5656", referer: "http://example.test:5656/convert", wantAllowed: true},
		{name: "cross-site metadata rejected", host: "example.test:5656", origin: "http://example.test:5656", fetchSite: "cross-site", wantAllowed: false},
		{name: "cross-site metadata rejected without referrer", host: "example.test:5656", fetchSite: "cross-site", wantAllowed: false},
		{name: "different host rejected", host: "example.test:5656", origin: "http://other.test:5656", wantAllowed: false},
		{name: "different port rejected", host: "example.test:5656", origin: "http://example.test:8080", wantAllowed: false},
		{name: "different scheme rejected", host: "example.test:5656", origin: "https://example.test:5656", wantAllowed: false},
		{name: "untrusted forwarded https rejected", host: "example.test:5656", origin: "https://example.test:5656", forwarded: "https", wantAllowed: false},
		{name: "trusted forwarded https origin", host: "example.test:5656", origin: "https://example.test:5656", forwarded: "https", trustedProxy: true, wantAllowed: true},
		{name: "forwarded host cannot choose origin", host: "example.test:5656", origin: "https://public.example", forwarded: "https", forwardedHost: "public.example", trustedProxy: true, wantAllowed: false},
		{name: "client cannot choose forwarded host", host: "example.test:5656", origin: "https://public.example", forwarded: "https", trustedProxy: true, wantAllowed: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://example.test:5656/api/v1/auth/guest-session", nil)
			r.Host = test.host
			r.Header.Set("Origin", test.origin)
			r.Header.Set("Referer", test.referer)
			r.Header.Set("Sec-Fetch-Site", test.fetchSite)
			r.Header.Set("X-Forwarded-Proto", test.forwarded)
			r.Header.Set("X-Forwarded-Host", test.forwardedHost)
			if test.trustedProxy {
				r.Header.Set(trustedProxyMarker, "web")
			}
			if got := sameOriginRequest(r); got != test.wantAllowed {
				t.Fatalf("sameOriginRequest() = %v, want %v", got, test.wantAllowed)
			}
		})
	}
}

func TestClientAddressDoesNotTrustUnmarkedForwardingHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "http://example.test:5656/api/v1/auth/session", nil)
	r.RemoteAddr = "192.0.2.10:4567"
	r.Header.Set("X-Real-IP", "198.51.100.20")
	if got := clientAddress(r); got != "192.0.2.10" {
		t.Fatalf("unmarked client address = %q, want immediate peer", got)
	}
	r.Header.Set(trustedProxyMarker, "web")
	if got := clientAddress(r); got != "198.51.100.20" {
		t.Fatalf("marked client address = %q, want proxy-provided address", got)
	}
}

func TestGuestSessionRequestRequiresSameOriginOrExplicitBrowserMarker(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "http://example.test:5656/api/v1/auth/guest-session", strings.NewReader(`{}`))
	r.Host = "example.test:5656"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-101conv-Request", "guest-session")
	if !guestSessionRequest(r) {
		t.Fatal("explicit guest request marker should be accepted")
	}
	r.Header.Set("Sec-Fetch-Site", "none")
	r.Header.Del("X-101conv-Request")
	if guestSessionRequest(r) {
		t.Fatal("JSON alone must not authorize a cookie-authenticated guest mutation")
	}
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	if guestSessionRequest(r) {
		t.Fatal("explicit cross-site guest request should be rejected")
	}
}

func TestManagementRESTLifecycleAndRestart(t *testing.T) {
	ctxDir := t.TempDir()
	sourceDir := filepath.Join(ctxDir, "imports")
	if err := os.MkdirAll(sourceDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "ffmpeg"), []byte("test"), 0700); err != nil {
		t.Fatal(err)
	}
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := management.Open(ctxDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	server := httptest.NewServer(NewWithManagement("0.1.0-dev", catalogService, manager, "secret"))
	defer server.Close()
	post := func(path, body string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer secret")
		req.Header.Set("Content-Type", "application/json")
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	engineJSON := `{"definition":{"schemaVersion":1,"id":"ffmpeg","version":"7.1.0","name":"FFmpeg","platform":"linux-amd64","capabilities":["video.encode"],"executables":{"ffmpeg":"ffmpeg"}},"sourceDirectory":"` + sourceDir + `"}`
	if got := post("/api/v1/engines/install", engineJSON); got != http.StatusCreated {
		t.Fatalf("install engine status=%d", got)
	}
	for _, id := range []string{"mp4-webm", "mov-webm"} {
		body := `{"definition":{"schemaVersion":1,"id":"` + id + `","version":"1.0.0","name":"` + id + `","engines":[{"engineId":"ffmpeg","versions":{"minimum":"7.0.0"},"capabilities":["video.encode"]}],"rules":[{"engineId":"ffmpeg","executable":"ffmpeg","sources":["mp4"],"targets":["webm"],"arguments":["{source}","{target}"]}]}}`
		if got := post("/api/v1/profiles", body); got != http.StatusCreated {
			t.Fatalf("install profile status=%d", got)
		}
	}
	capabilitiesRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/capabilities", nil)
	if err != nil {
		t.Fatal(err)
	}
	capabilitiesRequest.Header.Set("Authorization", "Bearer secret")
	capabilitiesResponse, err := server.Client().Do(capabilitiesRequest)
	if err != nil {
		t.Fatal(err)
	}
	var canonical capability.Catalog
	if capabilitiesResponse.StatusCode != http.StatusOK {
		capabilitiesResponse.Body.Close()
		t.Fatalf("capabilities status=%d", capabilitiesResponse.StatusCode)
	}
	if err = json.NewDecoder(capabilitiesResponse.Body).Decode(&canonical); err != nil {
		capabilitiesResponse.Body.Close()
		t.Fatal(err)
	}
	capabilitiesResponse.Body.Close()
	if len(canonical.Capabilities) != 1 || canonical.Capabilities[0].ID != "convert:mp4:webm" || len(canonical.Providers) != 2 {
		t.Fatalf("canonical catalog=%+v", canonical)
	}
	resolveRequest, err := http.NewRequest(http.MethodGet, server.URL+"/api/v1/capabilities/resolve?operation=convert&source=.MP4&target=.WEBM", nil)
	if err != nil {
		t.Fatal(err)
	}
	resolveRequest.Header.Set("Authorization", "Bearer secret")
	resolveResponse, err := server.Client().Do(resolveRequest)
	if err != nil {
		t.Fatal(err)
	}
	var resolved struct {
		Provider capability.Provider `json:"provider"`
		Strategy string              `json:"strategy"`
	}
	if resolveResponse.StatusCode != http.StatusOK {
		resolveResponse.Body.Close()
		t.Fatalf("resolve capability status=%d", resolveResponse.StatusCode)
	}
	if err = json.NewDecoder(resolveResponse.Body).Decode(&resolved); err != nil {
		resolveResponse.Body.Close()
		t.Fatal(err)
	}
	resolveResponse.Body.Close()
	if resolved.Provider.ProfileID != "mov-webm" || resolved.Strategy != "stable provider order" {
		t.Fatalf("resolved canonical provider=%+v", resolved)
	}
	if err = manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager2, err := management.Open(ctxDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager2.Close() })
	api2 := httptest.NewServer(NewWithManagement("0.1.0-dev", catalogService, manager2, "secret"))
	defer api2.Close()
	request, err := http.NewRequest(http.MethodGet, api2.URL+"/api/v1/profiles/mov-webm/engines", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := api2.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("resolve status=%d", response.StatusCode)
	}
	var result struct {
		Engines []engine.Installation `json:"engines"`
	}
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Engines) != 1 || result.Engines[0].EngineID != "ffmpeg" {
		t.Fatalf("resolved engine after restart: %+v", result)
	}
}

func TestManagementEndpointsRequireAdministratorToken(t *testing.T) {
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := management.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewWithManagement("0.1.0-dev", catalogService, manager, "secret"))
	defer server.Close()
	resp, err := server.Client().Get(server.URL + "/api/v1/engines")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated management status=%d", resp.StatusCode)
	}
}

func TestSessionTimeoutSettingIsAdminOnlyAndPersists(t *testing.T) {
	dir := t.TempDir()
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := management.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewWithManagement("0.1.0-dev", catalogService, manager, "secret"))
	client := server.Client()
	request := func(method, body string, authorized bool) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+"/api/v1/settings/session", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		if authorized {
			req.Header.Set("Authorization", "Bearer secret")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := request(http.MethodGet, "", false)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized GET status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = request(http.MethodPut, `{"sessionTimeoutMinutes":5}`, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid update status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	resp = request(http.MethodPut, `{"sessionTimeoutMinutes":4}`, true)
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid update status=%d", resp.StatusCode)
	}
	resp.Body.Close()
	server.Close()
	if err = manager.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err = management.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if got := manager.SessionTimeout(); got != 5 {
		t.Fatalf("persisted timeout=%d, want 5", got)
	}
}

func TestBrowserSessionIsOpaqueHttpOnlyAndSameOriginForMutations(t *testing.T) {
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := management.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	server := httptest.NewServer(NewWithManagement("0.1.0-dev", catalogService, manager, "secret"))
	defer server.Close()
	client := server.Client()
	login, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/session", strings.NewReader(`{"password":"secret"}`))
	login.Header.Set("Content-Type", "application/json")
	response, err := client.Do(login)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("login status=%d", response.StatusCode)
	}
	cookie := response.Cookies()[0]
	response.Body.Close()
	if cookie.Name != adminCookie || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unsafe session cookie: %+v", cookie)
	}
	if strings.Contains(response.Header.Get("Content-Type"), "secret") {
		t.Fatal("password leaked in response")
	}
	check, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/auth/session", nil)
	check.AddCookie(cookie)
	response, err = client.Do(check)
	if err != nil {
		t.Fatal(err)
	}
	var state Session
	_ = json.NewDecoder(response.Body).Decode(&state)
	response.Body.Close()
	if !state.Authenticated {
		t.Fatal("new session was not recognized")
	}
	list, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/engines", nil)
	list.AddCookie(cookie)
	response, err = client.Do(list)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("cookie-authenticated API status=%d", response.StatusCode)
	}
	mutation, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/profiles", strings.NewReader(`{}`))
	mutation.AddCookie(cookie)
	mutation.Header.Set("Content-Type", "application/json")
	mutation.Header.Set("Origin", "https://attacker.example")
	response, err = client.Do(mutation)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin mutation status=%d", response.StatusCode)
	}
	logout, _ := http.NewRequest(http.MethodDelete, server.URL+"/api/v1/auth/session", nil)
	logout.AddCookie(cookie)
	logout.Header.Set("Origin", server.URL)
	response, err = client.Do(logout)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status=%d", response.StatusCode)
	}
}

type Session struct {
	Authenticated bool `json:"authenticated"`
}

func TestRepositoryAPIRequiresAdminAndExposesOfficialTrustState(t *testing.T) {
	root := t.TempDir()
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := management.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	repos, err := repositorymanager.Open(root, "0.1.0-dev", manager)
	if err != nil {
		t.Fatal(err)
	}
	defer repos.Close()
	server := httptest.NewServer(NewWithServices("0.1.0-dev", catalogService, manager, repos, "secret"))
	defer server.Close()
	unauth, err := server.Client().Get(server.URL + "/api/v1/repositories")
	if err != nil {
		t.Fatal(err)
	}
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated repository list status=%d", unauth.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/repositories", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("repository list status=%d", resp.StatusCode)
	}
	var body struct {
		Repositories []repositorymanager.Source `json:"repositories"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Repositories) != 1 || body.Repositories[0].ID != "101conv-official" || body.Repositories[0].Trust != repositorymanager.TrustUnavailable {
		t.Fatalf("official root absence was not exposed honestly: %+v", body.Repositories)
	}
	approve, _ := http.NewRequest(http.MethodPatch, server.URL+"/api/v1/repositories/101conv-official/trust", strings.NewReader(`{"approved":true}`))
	approve.Header.Set("Content-Type", "application/json")
	if unauth, e := server.Client().Do(approve); e != nil {
		t.Fatal(e)
	} else {
		unauth.Body.Close()
		if unauth.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unsigned trust approval without admin auth status=%d", unauth.StatusCode)
		}
	}
	approve, _ = http.NewRequest(http.MethodPatch, server.URL+"/api/v1/repositories/101conv-official/trust", strings.NewReader(`{"approved":true}`))
	approve.Header.Set("Authorization", "Bearer secret")
	approve.Header.Set("Content-Type", "application/json")
	resp, err = server.Client().Do(approve)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin unsigned trust approval status=%d", resp.StatusCode)
	}
	var approved repositorymanager.Source
	if err = json.NewDecoder(resp.Body).Decode(&approved); err != nil {
		t.Fatal(err)
	}
	if approved.Trust != repositorymanager.TrustApprovedUnsigned {
		t.Fatalf("admin approval not reflected in API: %+v", approved)
	}
}

func TestRepositoryInstallAPIRequiresPreviewPlanAndUsesStructuredErrors(t *testing.T) {
	root := t.TempDir()
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := management.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	repos, err := repositorymanager.Open(root, "0.1.0-dev", manager)
	if err != nil {
		t.Fatal(err)
	}
	defer repos.Close()
	server := httptest.NewServer(NewWithServices("0.1.0-dev", catalogService, manager, repos, "secret"))
	defer server.Close()
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/repositories/trusted/profiles/video/install", strings.NewReader(`{"planId":"missing"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("stale installation plan status=%d", resp.StatusCode)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "stale_preview" || body.Error.Message == "" {
		t.Fatalf("expected structured stale-preview error: %+v", body)
	}
}
