package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
