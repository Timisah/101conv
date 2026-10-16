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
	sourceDir := t.TempDir()
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
	manager2, err := management.Open(ctxDir)
	if err != nil {
		t.Fatal(err)
	}
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
