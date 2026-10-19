package httpapi

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/timisah/101conv/server/internal/auth"
)

func (a *API) setupStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if a.accounts == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "setup_unavailable", "PostgreSQL account storage is not configured")
		return
	}
	status, err := a.accounts.InitialSetupStatus(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "setup_unavailable", "initial setup state could not be read")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) claimInitialAdministrator(w http.ResponseWriter, r *http.Request) {
	if a.accounts == nil || a.adminToken == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "setup_unavailable", "administrator setup requires PostgreSQL and a bootstrap passphrase")
		return
	}
	if !sameOriginRequest(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
		return
	}
	finish, allowed := a.loginGuard.begin(r)
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(loginAttemptWindow.Seconds())))
		writeAPIError(w, http.StatusTooManyRequests, "login_rate_limited", "too many setup attempts; wait before trying again")
		return
	}
	success := false
	defer func() { finish(success) }()
	var req struct {
		BootstrapToken string `json:"bootstrapToken"`
		Username       string `json:"username"`
		Password       string `json:"password"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if len(a.adminToken) < 24 {
		writeAPIError(w, http.StatusServiceUnavailable, "weak_bootstrap_secret", "replace ENGINE_ADMIN_TOKEN with a random secret of at least 24 characters before claiming this instance")
		return
	}
	if len(req.BootstrapToken) != len(a.adminToken) || subtle.ConstantTimeCompare([]byte(req.BootstrapToken), []byte(a.adminToken)) != 1 {
		writeAPIError(w, http.StatusUnauthorized, "invalid_bootstrap_secret", "bootstrap passphrase was not accepted")
		return
	}
	p, err := a.accounts.CreateInitialAdmin(r.Context(), req.Username, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrConflict):
			writeAPIError(w, http.StatusConflict, "setup_claimed", "this instance already has an administrator")
		case errors.Is(err, auth.ErrInvalidInput):
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_account", "username must be 3–64 letters, digits, dots, dashes or underscores; password must be 5–256 characters")
		default:
			writeAPIError(w, http.StatusInternalServerError, "setup_failed", "could not create the administrator account")
		}
		return
	}
	token, expires, err := a.sessions.create(p)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "session_error", "administrator created; sign in with the new account to continue setup")
		return
	}
	success = true
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: token, Path: "/api/v1", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, Secure: requestIsSecure(r), SameSite: http.SameSiteStrictMode})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"authenticated": true, "username": p.Username, "role": p.Role, "canConvert": true, "canCompress": true, "canReadJobs": true, "permissions": p.Permissions})
}

func (a *API) completeInitialSetup(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) || a.accounts == nil {
		return
	}
	p, ok := a.identity(r)
	if !ok || p.ID == "admin" {
		writeAPIError(w, http.StatusForbidden, "account_required", "sign in with the new administrator account to finish setup")
		return
	}
	if _, ready := a.checkSetupStorage(); !ready {
		writeAPIError(w, http.StatusServiceUnavailable, "storage_unwritable", "application storage is not writable; check the mounted directory ownership before finishing setup")
		return
	}
	if err := a.accounts.CompleteInitialSetup(r.Context(), p.ID); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "setup_failed", "could not finish initial setup")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type storageCheck struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Writable bool   `json:"writable"`
	Issue    string `json:"issue,omitempty"`
}

func (a *API) checkSetupStorage() ([]storageCheck, bool) {
	if a.management == nil || a.jobs == nil {
		return nil, false
	}
	paths := []storageCheck{{Name: "Configuration and engines", Path: a.management.RootPath()}, {Name: "Original uploads", Path: a.jobs.UploadRoot()}, {Name: "Results", Path: a.jobs.ResultRoot()}}
	ready := true
	for i := range paths {
		path := paths[i].Path
		info, err := os.Lstat(path)
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			err = os.ErrInvalid
		}
		if err == nil {
			var file *os.File
			file, err = os.CreateTemp(path, ".write-check-")
			if err == nil {
				name := file.Name()
				if closeErr := file.Close(); closeErr != nil {
					err = closeErr
				}
				if removeErr := os.Remove(name); err == nil && removeErr != nil {
					err = removeErr
				}
			}
		}
		paths[i].Writable = err == nil
		if err != nil {
			ready = false
			paths[i].Issue = "Not writable by the Core container. Check the mount and container user permissions."
		}
	}
	return paths, ready
}

func (a *API) setupReadiness(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	checks, ready := a.checkSetupStorage()
	if checks == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "setup_unavailable", "management and job storage are required")
		return
	}
	engines, err := a.management.ListEngines(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "engine_error", "could not list installed engines")
		return
	}
	profiles, err := a.management.ListProfiles(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "profile_error", "could not list installed profiles")
		return
	}
	// Paths are container paths. They are intentionally not advertised as host
	// paths or presented as locations the browser can create.
	for i := range checks {
		checks[i].Path = filepath.Clean(checks[i].Path)
	}
	writeJSON(w, http.StatusOK, map[string]any{"storage": checks, "storageReady": ready, "installedEngines": len(engines), "installedProfiles": len(profiles), "retention": "manual", "workerWorkspaceLimitBytes": int64(1 << 30)})
}
