package httpapi

import (
	"errors"
	"net/http"
	"strings"

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
	if a.jobs != nil {
		for i := range users {
			usage, e := a.jobs.MonthlyUsage(r.Context(), users[i].ID)
			if e != nil {
				writeAPIError(w, http.StatusInternalServerError, "usage_error", "could not load user quota usage")
				return
			}
			users[i].Usage = map[string]int64{"uploads": int64(usage.Uploads), "inputBytes": usage.InputBytes, "jobs": int64(usage.Conversions)}
		}
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
		Username            string           `json:"username"`
		Password            string           `json:"password"`
		RoleID              string           `json:"roleId"`
		PermissionOverrides map[string]*bool `json:"permissionOverrides"`
		Quotas              *auth.Quotas     `json:"quotas"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Quotas == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_user", "explicit quota modes are required")
		return
	}
	actor, _ := a.identity(r)
	if !actor.IsAdmin() {
		req.RoleID = "custom"
		req.PermissionOverrides = map[string]*bool{}
		maxFile := actor.MaxFileBytes
		if maxFile <= 0 {
			maxFile = 100 << 20
		}
		monthlyJobs := int64(100)
		if actor.Quotas.MonthlyJobs.Mode == "limited" && actor.Quotas.MonthlyJobs.Value != nil && *actor.Quotas.MonthlyJobs.Value < monthlyJobs {
			monthlyJobs = *actor.Quotas.MonthlyJobs.Value
		}
		monthlyBytes := int64(5 << 30)
		if actor.Quotas.MonthlyBytes.Mode == "limited" && actor.Quotas.MonthlyBytes.Value != nil && *actor.Quotas.MonthlyBytes.Value < monthlyBytes {
			monthlyBytes = *actor.Quotas.MonthlyBytes.Value
		}
		req.Quotas = &auth.Quotas{MaxFileBytes: auth.QuotaLimit{Mode: "limited", Value: &maxFile}, MonthlyJobs: auth.QuotaLimit{Mode: "limited", Value: &monthlyJobs}, MonthlyBytes: auth.QuotaLimit{Mode: "limited", Value: &monthlyBytes}}
	}
	u, err := a.accounts.CreateUser(r.Context(), auth.NewUser{Username: req.Username, Password: req.Password, RoleID: req.RoleID, PermissionOverrides: req.PermissionOverrides, Quotas: req.Quotas})
	if err != nil {
		writeAccountError(w, err)
		return
	}
	_ = a.accounts.RecordAudit(r.Context(), actor.ID, "user.created", "user", u.ID, map[string]any{"roleId": u.RoleID})
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
		Enabled             *bool            `json:"enabled"`
		Password            *string          `json:"password"`
		Role                *string          `json:"role"`
		RoleID              *string          `json:"roleId"`
		GroupIDs            *[]string        `json:"groupIds"`
		PermissionOverrides map[string]*bool `json:"permissionOverrides"`
		Quotas              *auth.Quotas     `json:"quotas"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	actor, _ := a.identity(r)
	if !actor.IsAdmin() {
		req.Role = nil
		req.RoleID = nil
		req.GroupIDs = nil
		req.PermissionOverrides = nil
		req.Quotas = nil
		req.Password = nil
		if r.PathValue("id") == actor.ID {
			req.Enabled = nil
		}
	}
	u, err := a.accounts.UpdateUserBy(r.Context(), actor.ID, r.PathValue("id"), auth.UserUpdate{Enabled: req.Enabled, Password: req.Password, Role: req.Role, RoleID: req.RoleID, GroupIDs: req.GroupIDs, PermissionOverrides: req.PermissionOverrides, Quotas: req.Quotas})
	if err != nil {
		writeAccountError(w, err)
		return
	}
	if err = a.accounts.RecordAudit(r.Context(), actor.ID, "user.updated", "user", u.ID, map[string]any{"enabled": req.Enabled != nil, "role": req.Role != nil || req.RoleID != nil, "groups": req.GroupIDs != nil, "permissions": req.PermissionOverrides != nil, "quotas": req.Quotas != nil, "passwordReset": req.Password != nil}); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "audit_error", "account updated but its audit record could not be written")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": u})
}

func accountsAvailable(w http.ResponseWriter, a *API) bool {
	if a.accounts == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "accounts_unavailable", "user storage is not configured")
		return false
	}
	return true
}
func (a *API) listGroups(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !accountsAvailable(w, a) {
		return
	}
	groups, err := a.accounts.ListGroups(r.Context())
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": groups})
}
func (a *API) saveGroup(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !accountsAvailable(w, a) {
		return
	}
	var g auth.Group
	if !decodeBody(w, r, &g) {
		return
	}
	if r.Method == http.MethodPut {
		g.ID = r.PathValue("id")
	} else if g.ID == "" {
		g.ID = "group-" + strings.ToLower(strings.ReplaceAll(strings.TrimSpace(g.Name), " ", "-"))
	}
	actor, _ := a.identity(r)
	if !actor.IsAdmin() {
		if r.Method == http.MethodPut {
			member, err := a.accounts.IsGroupMember(r.Context(), actor.ID, g.ID)
			if err != nil {
				writeAccountError(w, err)
				return
			}
			if member {
				writeAPIError(w, http.StatusForbidden, "permission_denied", "you cannot change the policy of a group you belong to")
				return
			}
		}
		if g.RoleID != "" {
			if err := a.accounts.CanGrantRole(r.Context(), actor.ID, g.RoleID); err != nil {
				writeAPIError(w, http.StatusForbidden, "permission_denied", "cannot configure a group that grants permissions you do not have")
				return
			}
		}
		if err := a.accounts.CanGrantPolicies(r.Context(), actor.ID, g.Permissions); err != nil {
			writeAPIError(w, http.StatusForbidden, "permission_denied", "cannot grant permissions you do not have")
			return
		}
	}
	saved, err := a.accounts.SaveGroup(r.Context(), g)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	if err = a.accounts.RecordAudit(r.Context(), actor.ID, "group.saved", "group", saved.ID, saved); err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"group": saved})
}
func (a *API) deleteGroup(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !accountsAvailable(w, a) {
		return
	}
	id := r.PathValue("id")
	actor, _ := a.identity(r)
	if !actor.IsAdmin() {
		member, err := a.accounts.IsGroupMember(r.Context(), actor.ID, id)
		if err != nil {
			writeAccountError(w, err)
			return
		}
		if member {
			writeAPIError(w, http.StatusForbidden, "permission_denied", "you cannot remove a group you belong to")
			return
		}
	}
	if err := a.accounts.DeleteGroup(r.Context(), id); err != nil {
		writeAccountError(w, err)
		return
	}
	_ = a.accounts.RecordAudit(r.Context(), actor.ID, "group.deleted", "group", id, nil)
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) setGroupMembers(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !accountsAvailable(w, a) {
		return
	}
	var req struct {
		UserIDs []string `json:"userIds"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if len(req.UserIDs) > 1000 {
		writeAPIError(w, http.StatusBadRequest, "invalid_group_members", "at most 1000 users may be changed at once")
		return
	}
	actor, _ := a.identity(r)
	if !actor.IsAdmin() {
		for _, id := range req.UserIDs {
			if id == actor.ID {
				writeAPIError(w, http.StatusForbidden, "permission_denied", "you cannot change your own group membership")
				return
			}
		}
		member, err := a.accounts.IsGroupMember(r.Context(), actor.ID, r.PathValue("id"))
		if err != nil {
			writeAccountError(w, err)
			return
		}
		if member {
			writeAPIError(w, http.StatusForbidden, "permission_denied", "you cannot change membership for a group you belong to")
			return
		}
		if err := a.accounts.CanGrantGroup(r.Context(), actor.ID, r.PathValue("id")); err != nil {
			writeAPIError(w, http.StatusForbidden, "permission_denied", "cannot assign a group that grants permissions you do not have")
			return
		}
	}
	if err := a.accounts.ReplaceGroupMembers(r.Context(), r.PathValue("id"), req.UserIDs); err != nil {
		writeAccountError(w, err)
		return
	}
	actor, _ = a.identity(r)
	_ = a.accounts.RecordAudit(r.Context(), actor.ID, "group.members_replaced", "group", r.PathValue("id"), map[string]any{"count": len(req.UserIDs)})
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) listRoles(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !accountsAvailable(w, a) {
		return
	}
	roles, err := a.accounts.ListRoles(r.Context())
	if err != nil {
		writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"roles": roles, "permissions": auth.PermissionNames})
}
func (a *API) saveRole(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !accountsAvailable(w, a) {
		return
	}
	var role auth.PermissionRole
	if !decodeBody(w, r, &role) {
		return
	}
	if r.Method == http.MethodPut {
		role.ID = r.PathValue("id")
	}
	actor, _ := a.identity(r)
	if !actor.IsAdmin() {
		if r.Method == http.MethodPut {
			assigned, err := a.accounts.UsesRole(r.Context(), actor.ID, role.ID)
			if err != nil {
				writeAccountError(w, err)
				return
			}
			if assigned {
				writeAPIError(w, http.StatusForbidden, "permission_denied", "you cannot change a role assigned to your own account or group")
				return
			}
		}
		if err := a.accounts.CanGrantPolicies(r.Context(), actor.ID, role.Permissions); err != nil {
			writeAPIError(w, http.StatusForbidden, "permission_denied", "cannot grant permissions you do not have")
			return
		}
	}
	saved, err := a.accounts.SaveRole(r.Context(), role)
	if err != nil {
		writeAccountError(w, err)
		return
	}
	_ = a.accounts.RecordAudit(r.Context(), actor.ID, "role.saved", "role", saved.ID, saved)
	writeJSON(w, http.StatusOK, map[string]any{"role": saved})
}
func (a *API) deleteRole(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !accountsAvailable(w, a) {
		return
	}
	id := r.PathValue("id")
	if err := a.accounts.DeleteRole(r.Context(), id); err != nil {
		writeAccountError(w, err)
		return
	}
	actor, _ := a.identity(r)
	_ = a.accounts.RecordAudit(r.Context(), actor.ID, "role.deleted", "role", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) bulkUpdateUsers(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) || !accountsAvailable(w, a) {
		return
	}
	var req struct {
		UserIDs             []string         `json:"userIds"`
		Enabled             *bool            `json:"enabled"`
		Role                *string          `json:"role"`
		RoleID              *string          `json:"roleId"`
		GroupIDs            *[]string        `json:"groupIds"`
		GroupAction         string           `json:"groupAction"`
		GroupID             string           `json:"groupId"`
		PermissionOverrides map[string]*bool `json:"permissionOverrides"`
		Quotas              *auth.Quotas     `json:"quotas"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	ids := uniqueUserIDs(req.UserIDs)
	if len(ids) == 0 || len(ids) > 500 {
		writeAPIError(w, http.StatusBadRequest, "invalid_bulk_selection", "select between 1 and 500 users")
		return
	}
	actor, _ := a.identity(r)
	if !actor.IsAdmin() {
		writeAPIError(w, http.StatusForbidden, "permission_denied", "bulk role, group, and permission changes require administrator access")
		return
	}
	if req.GroupAction != "" && (req.GroupID == "" || (req.GroupAction != "add" && req.GroupAction != "remove")) {
		writeAPIError(w, http.StatusBadRequest, "invalid_bulk_group_action", "groupAction must be add or remove with a groupId")
		return
	}
	type failure struct {
		UserID  string `json:"userId"`
		Message string `json:"message"`
	}
	failures := []failure{}
	success := []string{}
	for _, id := range ids {
		groups := req.GroupIDs
		if req.GroupAction != "" {
			current, e := a.accounts.Principal(r.Context(), id)
			if e != nil {
				failures = append(failures, failure{id, e.Error()})
				continue
			}
			changed := append([]string(nil), current.GroupIDs...)
			found := false
			for i, gid := range changed {
				if gid == req.GroupID {
					found = true
					if req.GroupAction == "remove" {
						changed = append(changed[:i], changed[i+1:]...)
					}
					break
				}
			}
			if req.GroupAction == "add" && !found {
				changed = append(changed, req.GroupID)
			}
			groups = &changed
		}
		_, err := a.accounts.UpdateUserBy(r.Context(), actor.ID, id, auth.UserUpdate{Enabled: req.Enabled, Role: req.Role, RoleID: req.RoleID, GroupIDs: groups, PermissionOverrides: req.PermissionOverrides, Quotas: req.Quotas})
		if err != nil {
			failures = append(failures, failure{id, err.Error()})
		} else {
			success = append(success, id)
		}
	}
	_ = a.accounts.RecordAudit(r.Context(), actor.ID, "users.bulk_updated", "users", "bulk", map[string]any{"succeeded": success, "failed": failures})
	status := http.StatusOK
	if len(failures) > 0 {
		status = http.StatusMultiStatus
	}
	writeJSON(w, status, map[string]any{"succeeded": success, "failed": failures})
}
func uniqueUserIDs(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range in {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
		return
	}
	if a.accounts == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "accounts_unavailable", "user storage is not configured")
		return
	}
	actor, _ := a.identity(r)
	if err := a.accounts.DeleteUser(r.Context(), r.PathValue("id")); err != nil {
		writeAccountError(w, err)
		return
	}
	_ = a.accounts.RecordAudit(r.Context(), actor.ID, "user.disabled", "user", r.PathValue("id"), nil)
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
	if !p.Permissions[auth.PermissionAPIKeysManage] {
		writeAPIError(w, http.StatusForbidden, "permission_denied", "the api_keys.manage permission is required")
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
	if !p.Permissions[auth.PermissionAPIKeysManage] {
		writeAPIError(w, http.StatusForbidden, "permission_denied", "the api_keys.manage permission is required")
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
	_ = a.accounts.RecordAudit(r.Context(), p.ID, "api_key.created", "api_key", key.ID, map[string]any{"name": key.Name, "scopes": key.Scopes})
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
	if !p.Permissions[auth.PermissionAPIKeysManage] {
		writeAPIError(w, http.StatusForbidden, "permission_denied", "the api_keys.manage permission is required")
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
	_ = a.accounts.RecordAudit(r.Context(), p.ID, "api_key.revoked", "api_key", r.PathValue("keyID"), nil)
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
