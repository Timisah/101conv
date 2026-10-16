package httpapi

import (
	"errors"
	"net/http"

	"github.com/timisah/101conv/server/internal/auth"
)

func (a *API) getMe(w http.ResponseWriter, r *http.Request) {
	p, ok := a.identity(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	response := map[string]any{"user": p}
	if !p.IsAdmin() && a.jobs != nil {
		usage, err := a.jobs.MonthlyUsage(r.Context(), p.ID)
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "usage_error", "could not load monthly usage")
			return
		}
		response["usage"] = usage
	}
	writeJSON(w, http.StatusOK, response)
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	if a.accounts == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "accounts_unavailable", "user storage is not configured")
		return
	}
	users, err := a.accounts.ListUsers(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "account_error", "could not list users")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	if a.accounts == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "accounts_unavailable", "user storage is not configured")
		return
	}
	var req struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		CanConvert   bool   `json:"canConvert"`
		CanReadJobs  bool   `json:"canReadJobs"`
		MaxFileBytes int64  `json:"maxFileBytes"`
		MonthlyJobs  int    `json:"monthlyJobs"`
		MonthlyBytes int64  `json:"monthlyBytes"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.MaxFileBytes == 0 {
		req.MaxFileBytes = 100 << 20
	}
	if req.MaxFileBytes > 100<<20 {
		writeAPIError(w, http.StatusBadRequest, "invalid_user", "per-file limit cannot exceed the instance limit")
		return
	}
	u, err := a.accounts.CreateUser(r.Context(), auth.NewUser{Username: req.Username, Password: req.Password, CanConvert: req.CanConvert, CanReadJobs: req.CanReadJobs, MaxFileBytes: req.MaxFileBytes, MonthlyJobs: req.MonthlyJobs, MonthlyBytes: req.MonthlyBytes})
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": u})
}

func (a *API) updateUser(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	if a.accounts == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "accounts_unavailable", "user storage is not configured")
		return
	}
	var req struct {
		Enabled      *bool   `json:"enabled"`
		CanConvert   *bool   `json:"canConvert"`
		CanReadJobs  *bool   `json:"canReadJobs"`
		MaxFileBytes *int64  `json:"maxFileBytes"`
		MonthlyJobs  *int    `json:"monthlyJobs"`
		MonthlyBytes *int64  `json:"monthlyBytes"`
		Password     *string `json:"password"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.MaxFileBytes != nil && *req.MaxFileBytes > 100<<20 {
		writeAPIError(w, http.StatusBadRequest, "invalid_user", "per-file limit cannot exceed the instance limit")
		return
	}
	u, err := a.accounts.UpdateUser(r.Context(), r.PathValue("id"), auth.UserUpdate{Enabled: req.Enabled, CanConvert: req.CanConvert, CanReadJobs: req.CanReadJobs, MaxFileBytes: req.MaxFileBytes, MonthlyJobs: req.MonthlyJobs, MonthlyBytes: req.MonthlyBytes, Password: req.Password})
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	if a.accounts == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "accounts_unavailable", "user storage is not configured")
		return
	}
	if err := a.accounts.DeleteUser(r.Context(), r.PathValue("id")); err != nil {
		writeAccountError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) listMyAPIKeys(w http.ResponseWriter, r *http.Request) {
	p, method, ok := a.identityWithMethod(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if p.IsAdmin() {
		writeJSON(w, http.StatusOK, map[string]any{"keys": []auth.APIKey{}})
		return
	}
	if !a.userSessionOnly(w, p, method) {
		return
	}
	keys, err := a.accounts.ListAPIKeys(r.Context(), p.ID)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (a *API) createMyAPIKey(w http.ResponseWriter, r *http.Request) {
	p, method, ok := a.identityWithMethod(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if !a.userSessionOnly(w, p, method) {
		return
	}
	if r.Method != http.MethodGet && !sameOriginRequest(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
		return
	}
	var req struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	key, err := a.accounts.CreateAPIKey(r.Context(), p.ID, req.Name, req.Scopes)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": key})
}

func (a *API) revokeMyAPIKey(w http.ResponseWriter, r *http.Request) {
	p, method, ok := a.identityWithMethod(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if !a.userSessionOnly(w, p, method) {
		return
	}
	if !sameOriginRequest(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
		return
	}
	if err := a.accounts.RevokeAPIKey(r.Context(), p.ID, r.PathValue("keyID")); err != nil {
		writeAccountError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) changeMyPassword(w http.ResponseWriter, r *http.Request) {
	p, method, ok := a.identityWithMethod(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if !a.userSessionOnly(w, p, method) {
		return
	}
	if !sameOriginRequest(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
		return
	}
	var req struct {
		Current string `json:"currentPassword"`
		New     string `json:"newPassword"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := a.accounts.ChangePassword(r.Context(), p.ID, req.Current, req.New); err != nil {
		writeAccountError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) userSessionOnly(w http.ResponseWriter, p auth.Principal, method string) bool {
	if a.accounts == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "accounts_unavailable", "user storage is not configured")
		return false
	}
	if p.IsAdmin() {
		writeAPIError(w, http.StatusForbidden, "forbidden", "per-user API keys are available to user accounts; use ENGINE_ADMIN_TOKEN for administrator automation")
		return false
	}
	if method != "session" || p.APIKey {
		writeAPIError(w, http.StatusForbidden, "interactive_session_required", "sign in with the account password to manage its credentials")
		return false
	}
	return true
}

func writeAccountError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrInvalidInput):
		writeAPIError(w, http.StatusBadRequest, "invalid_account_input", err.Error())
	case errors.Is(err, auth.ErrConflict):
		writeAPIError(w, http.StatusConflict, "account_conflict", "username is already in use")
	case errors.Is(err, auth.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "account or API key not found")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "current password was not accepted")
	case errors.Is(err, auth.ErrDisabled):
		writeAPIError(w, http.StatusForbidden, "account_disabled", "account is disabled")
	default:
		writeAPIError(w, http.StatusInternalServerError, "account_error", "account operation failed")
	}
}
