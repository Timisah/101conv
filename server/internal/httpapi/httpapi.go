package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/catalog"
	"github.com/timisah/101conv/server/internal/jobs"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/repositorymanager"
)

type API struct {
	version      string
	catalog      *catalog.Service
	management   *management.Service
	adminToken   string
	repositories *repositorymanager.Manager
	jobs         *jobs.Service
	accounts     *auth.Service
	sessions     *sessionStore
	loginGuard   *loginGuard
}

func New(version string, catalogService *catalog.Service) http.Handler {
	return NewWithManagement(version, catalogService, nil, "")
}

func NewWithManagement(version string, catalogService *catalog.Service, managementService *management.Service, adminToken string) http.Handler {
	return NewWithServices(version, catalogService, managementService, nil, adminToken)
}

func NewWithServices(version string, catalogService *catalog.Service, managementService *management.Service, repositoryService *repositorymanager.Manager, adminToken string) http.Handler {
	return NewWithJobServices(version, catalogService, managementService, repositoryService, nil, adminToken)
}

func NewWithJobServices(version string, catalogService *catalog.Service, managementService *management.Service, repositoryService *repositorymanager.Manager, jobService *jobs.Service, adminToken string) http.Handler {
	return NewWithAccounts(version, catalogService, managementService, repositoryService, jobService, nil, adminToken)
}

func NewWithAccounts(version string, catalogService *catalog.Service, managementService *management.Service, repositoryService *repositorymanager.Manager, jobService *jobs.Service, accountService *auth.Service, adminToken string) http.Handler {
	a := &API{version: version, catalog: catalogService, management: managementService, repositories: repositoryService, jobs: jobService, accounts: accountService, adminToken: adminToken, sessions: newSessionStore(), loginGuard: newLoginGuard()}
	if managementService != nil {
		a.sessions.setTTL(time.Duration(managementService.SessionTimeout()) * time.Minute)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", a.health)
	mux.HandleFunc("GET /readyz", a.ready)
	mux.HandleFunc("GET /api/v1/setup/status", a.setupStatus)
	mux.HandleFunc("POST /api/v1/setup/claim", a.claimInitialAdministrator)
	mux.HandleFunc("POST /api/v1/setup/complete", a.completeInitialSetup)
	mux.HandleFunc("GET /api/v1/setup/readiness", a.setupReadiness)
	mux.HandleFunc("GET /api/v1/storage", a.storageOverview)
	mux.HandleFunc("POST /api/v1/storage/scan", a.startStorageScan)
	mux.HandleFunc("GET /api/v1/operations", a.operationsOverview)
	mux.HandleFunc("GET /api/v1/storage/cleanup/preview", a.previewStorageCleanup)
	mux.HandleFunc("PUT /api/v1/storage/retention", a.updateStorageRetention)
	mux.HandleFunc("POST /api/v1/storage/cleanup", a.applyStorageCleanup)
	mux.HandleFunc("GET /api/v1/auth/session", a.getSession)
	mux.HandleFunc("POST /api/v1/auth/session", a.createSession)
	mux.HandleFunc("DELETE /api/v1/auth/session", a.deleteSession)
	mux.HandleFunc("GET /api/v1/settings/session", a.getSessionSettings)
	mux.HandleFunc("PUT /api/v1/settings/session", a.updateSessionSettings)
	mux.HandleFunc("GET /api/v1/me", a.getMe)
	mux.HandleFunc("POST /api/v1/me/password", a.changeMyPassword)
	mux.HandleFunc("GET /api/v1/me/api-keys", a.listMyAPIKeys)
	mux.HandleFunc("POST /api/v1/me/api-keys", a.createMyAPIKey)
	mux.HandleFunc("DELETE /api/v1/me/api-keys/{keyID}", a.revokeMyAPIKey)
	mux.HandleFunc("GET /api/v1/users", a.listUsers)
	mux.HandleFunc("POST /api/v1/users", a.createUser)
	mux.HandleFunc("PATCH /api/v1/users/{id}", a.updateUser)
	mux.HandleFunc("DELETE /api/v1/users/{id}", a.deleteUser)
	mux.HandleFunc("POST /api/v1/users/bulk", a.bulkUpdateUsers)
	mux.HandleFunc("GET /api/v1/groups", a.listGroups)
	mux.HandleFunc("POST /api/v1/groups", a.saveGroup)
	mux.HandleFunc("PUT /api/v1/groups/{id}", a.saveGroup)
	mux.HandleFunc("DELETE /api/v1/groups/{id}", a.deleteGroup)
	mux.HandleFunc("PUT /api/v1/groups/{id}/members", a.setGroupMembers)
	mux.HandleFunc("GET /api/v1/roles", a.listRoles)
	mux.HandleFunc("POST /api/v1/roles", a.saveRole)
	mux.HandleFunc("PUT /api/v1/roles/{id}", a.saveRole)
	mux.HandleFunc("DELETE /api/v1/roles/{id}", a.deleteRole)
	mux.HandleFunc("GET /api/v1/catalogs", a.catalogs)
	mux.HandleFunc("GET /api/v1/catalogs/101conv-official", a.officialCatalog)
	mux.HandleFunc("GET /api/v1/engines", a.listEngines)
	mux.HandleFunc("POST /api/v1/engines/install", a.installEngine)
	mux.HandleFunc("PUT /api/v1/engines", a.installEngine)
	mux.HandleFunc("PUT /api/v1/engines/{id}", a.updateEngine)
	mux.HandleFunc("POST /api/v1/engines/manual", a.registerManualEngine)
	mux.HandleFunc("DELETE /api/v1/engines/{id}/{version}", a.removeEngine)
	mux.HandleFunc("GET /api/v1/profiles", a.listProfiles)
	mux.HandleFunc("GET /api/v1/profile-preferences", a.listProfilePreferences)
	mux.HandleFunc("PUT /api/v1/profile-preferences/me/{mode}/{source}/{target}", a.setMyProfilePreference)
	mux.HandleFunc("DELETE /api/v1/profile-preferences/me/{mode}/{source}/{target}", a.deleteMyProfilePreference)
	mux.HandleFunc("GET /api/v1/profile-preferences/global", a.listGlobalProfilePreferences)
	mux.HandleFunc("PUT /api/v1/profile-preferences/global/{mode}/{source}/{target}", a.setGlobalProfilePreference)
	mux.HandleFunc("DELETE /api/v1/profile-preferences/global/{mode}/{source}/{target}", a.deleteGlobalProfilePreference)
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
	mux.HandleFunc("PATCH /api/v1/repositories/{id}/trust", a.setRepositoryTrust)
	mux.HandleFunc("POST /api/v1/repositories/{id}/refresh", a.refreshRepository)
	mux.HandleFunc("GET /api/v1/repositories/{id}/catalog", a.browseRepository)
	mux.HandleFunc("GET /api/v1/repositories/{id}/profiles/{profileID}/preview", a.previewRepositoryProfile)
	mux.HandleFunc("GET /api/v1/repositories/{id}/profiles/{profileID}/update-preview", a.previewRepositoryProfileUpdate)
	mux.HandleFunc("POST /api/v1/repositories/{id}/profiles/{profileID}/install", a.installRepositoryProfile)
	mux.HandleFunc("PUT /api/v1/repositories/{id}/profiles/{profileID}", a.updateRepositoryProfile)
	mux.HandleFunc("GET /api/v1/repositories/{id}/engines/{engineID}/{version}/preview", a.previewRepositoryEngine)
	mux.HandleFunc("POST /api/v1/repositories/{id}/engines/{engineID}/{version}/install", a.installRepositoryEngine)
	mux.HandleFunc("POST /api/v1/uploads", a.uploadFile)
	mux.HandleFunc("POST /api/v1/jobs", a.submitJob)
	mux.HandleFunc("POST /api/v1/compressions", a.submitCompressionJob)
	mux.HandleFunc("GET /api/v1/jobs", a.listJobs)
	mux.HandleFunc("GET /api/v1/jobs/{id}", a.getJob)
	mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", a.cancelJob)
	mux.HandleFunc("GET /api/v1/jobs/{id}/result", a.downloadJobResult)
	return mux
}

func (a *API) authorized(w http.ResponseWriter, r *http.Request) bool {
	permission := auth.PermissionConvertersManage
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/v1/repositories"):
		permission = auth.PermissionRepositoriesManage
	case strings.HasPrefix(r.URL.Path, "/api/v1/users"):
		permission = auth.PermissionUsersManage
	case strings.HasPrefix(r.URL.Path, "/api/v1/groups"):
		permission = auth.PermissionGroupsManage
	case strings.HasPrefix(r.URL.Path, "/api/v1/roles"):
		permission = auth.PermissionRolesManage
	case strings.HasPrefix(r.URL.Path, "/api/v1/settings"):
		permission = auth.PermissionUsersManage
	}
	return a.requirePermission(w, r, permission)
}

func (a *API) requirePermission(w http.ResponseWriter, r *http.Request, permission string) bool {
	if a.management == nil || a.adminToken == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "management_disabled", "management requires configured persistent storage and ENGINE_ADMIN_TOKEN")
		return false
	}
	p, method, ok := a.identityWithMethod(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return false
	}
	if p.APIKey {
		writeAPIError(w, http.StatusForbidden, "forbidden", "API keys cannot administer instance settings")
		return false
	}
	if !p.IsAdmin() && !p.Permissions[permission] {
		writeAPIError(w, http.StatusForbidden, "permission_denied", "the "+permission+" permission is required")
		return false
	}
	if method == "session" && r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOriginRequest(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
		return false
	}
	return true
}

func (a *API) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if a.management == nil || a.adminToken == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "management_disabled", "management requires configured persistent storage and ENGINE_ADMIN_TOKEN")
		return false
	}
	p, method, ok := a.identityWithMethod(r)
	if ok && p.IsAdmin() {
		if method == "session" && r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOriginRequest(r) {
			writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
			return false
		}
		return true
	}
	if ok {
		writeAPIError(w, http.StatusForbidden, "forbidden", "administrator permission required")
	} else {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "administrator session required")
	}
	return false
}

func (a *API) identity(r *http.Request) (auth.Principal, bool) {
	p, _, ok := a.identityWithMethod(r)
	return p, ok
}

func (a *API) identityWithMethod(r *http.Request) (auth.Principal, string, bool) {
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		got := strings.TrimPrefix(header, "Bearer ")
		if a.adminToken != "" && len(got) == len(a.adminToken) && subtle.ConstantTimeCompare([]byte(got), []byte(a.adminToken)) == 1 {
			return auth.AdminPrincipal(), "bearer", true
		}
		if a.accounts != nil {
			p, err := a.accounts.AuthenticateAPIKey(r.Context(), got)
			if err == nil {
				return p, "bearer", true
			}
		}
		return auth.Principal{}, "", false
	}
	if a.sessions == nil {
		return auth.Principal{}, "", false
	}
	p, ok := a.sessions.principal(r)
	if !ok {
		return auth.Principal{}, "", false
	}
	if p.ID != "admin" && a.accounts != nil {
		fresh, err := a.accounts.Principal(r.Context(), p.ID)
		if err != nil {
			return auth.Principal{}, "", false
		}
		p = fresh
	}
	return p, "session", true
}

func (a *API) requireCapability(w http.ResponseWriter, r *http.Request, scope string) (auth.Principal, bool) {
	if a.jobs == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "jobs_unavailable", "job storage is not configured")
		return auth.Principal{}, false
	}
	p, method, ok := a.identityWithMethod(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return auth.Principal{}, false
	}
	if method == "session" && r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOriginRequest(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
		return auth.Principal{}, false
	}
	if p.IsAdmin() {
		return p, true
	}
	allowed := false
	permission := ""
	switch scope {
	case auth.ScopeUpload:
		permission = auth.PermissionConversion
	case auth.ScopeSubmit:
		permission = auth.PermissionConversion
	case auth.ScopeCompress:
		permission = auth.PermissionCompression
	case auth.ScopeRead, auth.ScopeResult:
		permission = auth.PermissionJobsRead
	case auth.ScopeCancel:
		permission = auth.PermissionJobsCancel
	}
	if p.APIKey {
		allowed = auth.ScopeAllows(p, scope)
		if scope == auth.ScopeUpload {
			allowed = allowed && (p.Permissions[auth.PermissionConversion] || p.Permissions[auth.PermissionCompression])
		} else {
			allowed = allowed && p.Permissions[permission]
		}
	} else if scope == auth.ScopeUpload {
		allowed = p.Permissions[auth.PermissionConversion] || p.Permissions[auth.PermissionCompression]
	} else {
		allowed = p.Permissions[permission]
	}
	if !allowed {
		writeAPIError(w, http.StatusForbidden, "permission_denied", "this account or API key does not have the required permission")
		return auth.Principal{}, false
	}
	return p, true
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
