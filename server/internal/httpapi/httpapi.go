package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/timisah/101conv/server/internal/catalog"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/repositorymanager"
)

type API struct {
	version      string
	catalog      *catalog.Service
	management   *management.Service
	adminToken   string
	repositories *repositorymanager.Manager
}

func New(version string, catalogService *catalog.Service) http.Handler {
	return NewWithManagement(version, catalogService, nil, "")
}

func NewWithManagement(version string, catalogService *catalog.Service, managementService *management.Service, adminToken string) http.Handler {
	return NewWithServices(version, catalogService, managementService, nil, adminToken)
}

func NewWithServices(version string, catalogService *catalog.Service, managementService *management.Service, repositoryService *repositorymanager.Manager, adminToken string) http.Handler {
	a := &API{version: version, catalog: catalogService, management: managementService, repositories: repositoryService, adminToken: adminToken}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /readyz", a.ready)
	mux.HandleFunc("GET /api/v1/catalogs", a.catalogs)
	mux.HandleFunc("GET /api/v1/catalogs/101conv-official", a.officialCatalog)
	mux.HandleFunc("GET /api/v1/engines", a.listEngines)
	mux.HandleFunc("POST /api/v1/engines/install", a.installEngine)
	mux.HandleFunc("PUT /api/v1/engines", a.installEngine)
	mux.HandleFunc("PUT /api/v1/engines/{id}", a.updateEngine)
	mux.HandleFunc("POST /api/v1/engines/manual", a.registerManualEngine)
	mux.HandleFunc("DELETE /api/v1/engines/{id}/{version}", a.removeEngine)
	mux.HandleFunc("GET /api/v1/profiles", a.listProfiles)
	mux.HandleFunc("POST /api/v1/profiles", a.installProfile)
	mux.HandleFunc("PUT /api/v1/profiles/{id}", a.updateProfile)
	mux.HandleFunc("PATCH /api/v1/profiles/{id}/enabled", a.setProfileEnabled)
	mux.HandleFunc("DELETE /api/v1/profiles/{id}", a.removeProfile)
	mux.HandleFunc("GET /api/v1/profiles/{id}/engines", a.resolveProfile)
	mux.HandleFunc("PUT /api/v1/profiles/{id}/engines/{engineID}", a.selectProfileEngine)
	mux.HandleFunc("GET /api/v1/repositories", a.listRepositories)
	mux.HandleFunc("POST /api/v1/repositories", a.addRepository)
	mux.HandleFunc("DELETE /api/v1/repositories/{id}", a.removeRepository)
	mux.HandleFunc("PATCH /api/v1/repositories/{id}/enabled", a.setRepositoryEnabled)
	mux.HandleFunc("POST /api/v1/repositories/{id}/refresh", a.refreshRepository)
	mux.HandleFunc("GET /api/v1/repositories/{id}/catalog", a.browseRepository)
	mux.HandleFunc("GET /api/v1/repositories/{id}/profiles/{profileID}/preview", a.previewRepositoryProfile)
	mux.HandleFunc("GET /api/v1/repositories/{id}/profiles/{profileID}/update-preview", a.previewRepositoryProfileUpdate)
	mux.HandleFunc("POST /api/v1/repositories/{id}/profiles/{profileID}/install", a.installRepositoryProfile)
	mux.HandleFunc("PUT /api/v1/repositories/{id}/profiles/{profileID}", a.updateRepositoryProfile)
	mux.HandleFunc("GET /api/v1/repositories/{id}/engines/{engineID}/{version}/preview", a.previewRepositoryEngine)
	mux.HandleFunc("POST /api/v1/repositories/{id}/engines/{engineID}/{version}/install", a.installRepositoryEngine)
	return mux
}

func (a *API) authorized(w http.ResponseWriter, r *http.Request) bool {
	if a.management == nil || a.adminToken == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "management_disabled", "management requires configured persistent storage and ENGINE_ADMIN_TOKEN")
		return false
	}
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "administrator bearer token required")
		return false
	}
	got := strings.TrimPrefix(header, "Bearer ")
	if len(got) != len(a.adminToken) || subtle.ConstantTimeCompare([]byte(got), []byte(a.adminToken)) != 1 {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "administrator bearer token required")
		return false
	}
	return true
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) ready(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "version": a.version})
}

func (a *API) catalogs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]catalog.Source{"sources": {a.catalog.Source()}})
}

func (a *API) officialCatalog(w http.ResponseWriter, _ *http.Request) {
	profiles, err := a.catalog.Available()
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"code": "catalog_incompatible", "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": a.catalog.Source(), "profiles": profiles})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
