package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/catalog"
	"github.com/timisah/101conv/server/internal/jobs"
	"github.com/timisah/101conv/server/internal/management"
)

func TestPostgresUserSessionAndScopedAPIKeyAuthorization(t *testing.T) {
	dsn := os.Getenv("101CONV_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set 101CONV_TEST_DATABASE_URL to run PostgreSQL authorization integration tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	accounts, err := auth.NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = accounts.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	manager, err := management.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	jobService, err := jobs.NewService(db, manager, filepath.Join(t.TempDir(), "uploads"), filepath.Join(t.TempDir(), "results"))
	if err != nil {
		t.Fatal(err)
	}
	if err = jobService.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	catalogService, err := catalog.New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	id, err := randomTestID()
	if err != nil {
		t.Fatal(err)
	}
	username := "user-" + id[:12]
	maxBytes, jobsLimit, dataLimit := int64(1<<20), int64(100), int64(5<<30)
	readJobs := true
	user, err := accounts.CreateUser(ctx, auth.NewUser{Username: username, Password: "integration test password", RoleID: "converter", PermissionOverrides: map[string]*bool{auth.PermissionJobsRead: &readJobs}, Quotas: &auth.Quotas{MaxFileBytes: auth.QuotaLimit{Mode: "limited", Value: &maxBytes}, MonthlyJobs: auth.QuotaLimit{Mode: "limited", Value: &jobsLimit}, MonthlyBytes: auth.QuotaLimit{Mode: "limited", Value: &dataLimit}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`DELETE FROM conv_users WHERE id=$1`, user.ID) }()
	delegatedRole, err := accounts.SaveRole(ctx, auth.PermissionRole{ID: "manager-" + id[:12], Name: "Manager " + id[:8], Permissions: map[string]bool{auth.PermissionUsersManage: true, auth.PermissionAPIKeysManage: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(`DELETE FROM conv_users WHERE id=$1`, user.ID)
		_, _ = db.Exec(`DELETE FROM conv_roles WHERE id=$1`, delegatedRole.ID)
	}()
	if _, err = accounts.UpdateUser(ctx, user.ID, auth.UserUpdate{RoleID: &delegatedRole.ID}); err != nil {
		t.Fatal(err)
	}
	key, err := accounts.CreateAPIKey(ctx, user.ID, "read only", []string{auth.ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewWithAccounts("0.1.0-dev", catalogService, manager, nil, jobService, accounts, "global-admin"))
	defer server.Close()
	call := func(client *http.Client, method, path, token, body string) int {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if client != http.DefaultClient {
			req.Header.Set("Origin", server.URL)
			req.Header.Set("Sec-Fetch-Site", "same-origin")
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 500 {
			body, _ := io.ReadAll(resp.Body)
			t.Logf("%s %s returned %d: %s", method, path, resp.StatusCode, body)
		}
		return resp.StatusCode
	}
	preferenceUser, err := accounts.CreateUser(ctx, auth.NewUser{Username: "pref-" + id[:12], Password: "preference integration password", RoleID: "converter", Quotas: &auth.Quotas{MaxFileBytes: auth.QuotaLimit{Mode: "unlimited"}, MonthlyJobs: auth.QuotaLimit{Mode: "unlimited"}, MonthlyBytes: auth.QuotaLimit{Mode: "unlimited"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`DELETE FROM conv_users WHERE id=$1`, preferenceUser.ID) }()
	preferenceKey, err := accounts.CreateAPIKey(ctx, preferenceUser.ID, "conversion preferences", []string{auth.ScopeUpload, auth.ScopeSubmit})
	if err != nil {
		t.Fatal(err)
	}
	if got := call(http.DefaultClient, http.MethodGet, "/api/v1/profile-preferences?mode=convert", preferenceKey.Token, ""); got != http.StatusOK {
		t.Fatalf("conversion API key could not read preferences: %d", got)
	}
	if got := call(http.DefaultClient, http.MethodGet, "/api/v1/profile-preferences?mode=compress", preferenceKey.Token, ""); got != http.StatusForbidden {
		t.Fatalf("conversion-only API key read compression preferences: %d", got)
	}
	if got := call(http.DefaultClient, http.MethodPut, "/api/v1/profile-preferences/me/convert/png/jpg", preferenceKey.Token, `{"profileId":"not-installed"}`); got != http.StatusUnprocessableEntity {
		t.Fatalf("API key invalid profile preference status=%d", got)
	}
	if got := call(http.DefaultClient, http.MethodGet, "/api/v1/profile-preferences/global", preferenceKey.Token, ""); got != http.StatusForbidden {
		t.Fatalf("user API key accessed global defaults: %d", got)
	}
	if got := call(http.DefaultClient, http.MethodGet, "/api/v1/jobs", key.Token, ""); got != http.StatusOK {
		t.Fatalf("key with jobs:read status=%d", got)
	}
	if got := call(http.DefaultClient, http.MethodPost, "/api/v1/uploads", key.Token, "{}"); got != http.StatusForbidden {
		t.Fatalf("read-only key upload status=%d", got)
	}
	if got := call(http.DefaultClient, http.MethodGet, "/api/v1/engines", key.Token, ""); got != http.StatusForbidden {
		t.Fatalf("user API key engine-admin status=%d", got)
	}
	if got := call(http.DefaultClient, http.MethodGet, "/api/v1/profiles", key.Token, ""); got != http.StatusForbidden {
		t.Fatalf("read-only key profile list status=%d", got)
	}
	denyRead := false
	if _, err = accounts.UpdateUser(ctx, user.ID, auth.UserUpdate{PermissionOverrides: map[string]*bool{auth.PermissionJobsRead: &denyRead}}); err != nil {
		t.Fatal(err)
	}
	if got := call(http.DefaultClient, http.MethodGet, "/api/v1/jobs", key.Token, ""); got != http.StatusForbidden {
		t.Fatalf("key bypassed changed account permission, status=%d", got)
	}
	allowRead := true
	if _, err = accounts.UpdateUser(ctx, user.ID, auth.UserUpdate{PermissionOverrides: map[string]*bool{auth.PermissionJobsRead: &allowRead}}); err != nil {
		t.Fatal(err)
	}
	if got := call(http.DefaultClient, http.MethodGet, "/api/v1/users", "global-admin", ""); got != http.StatusOK {
		t.Fatalf("global admin user list status=%d", got)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	if got := call(client, http.MethodPost, "/api/v1/auth/session", "", `{"username":"`+username+`","password":"integration test password"}`); got != http.StatusCreated {
		t.Fatalf("user login status=%d", got)
	}
	if got := call(client, http.MethodGet, "/api/v1/me/api-keys", "", ""); got != http.StatusOK {
		t.Fatalf("own API key list status=%d", got)
	}
	if got := call(client, http.MethodGet, "/api/v1/users", "", ""); got != http.StatusOK {
		t.Fatalf("delegated manager should be allowed to list users, status=%d", got)
	}
	if got := call(client, http.MethodGet, "/api/v1/groups", "", ""); got != http.StatusForbidden {
		t.Fatalf("users.manage unexpectedly granted groups.manage, status=%d", got)
	}
	if got := call(client, http.MethodPatch, "/api/v1/users/"+user.ID, "", `{"role":"admin","permissionOverrides":{"groups.manage":true}}`); got != http.StatusOK {
		t.Fatalf("delegated self-update status=%d", got)
	}
	principal, err := accounts.Principal(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Role != auth.RoleUser || principal.Permissions[auth.PermissionGroupsManage] {
		t.Fatalf("delegated user self-granted privileges: role=%s permissions=%v", principal.Role, principal.Permissions)
	}
	limit := int64(17)
	bulkBody, err := json.Marshal(map[string]any{"userIds": []string{user.ID, "missing-" + id[:8]}, "quotas": auth.Quotas{MaxFileBytes: auth.QuotaLimit{Mode: "inherit"}, MonthlyJobs: auth.QuotaLimit{Mode: "limited", Value: &limit}, MonthlyBytes: auth.QuotaLimit{Mode: "unlimited"}}})
	if err != nil {
		t.Fatal(err)
	}
	bulkReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/users/bulk", strings.NewReader(string(bulkBody)))
	if err != nil {
		t.Fatal(err)
	}
	bulkReq.Header.Set("Authorization", "Bearer global-admin")
	bulkReq.Header.Set("Content-Type", "application/json")
	bulkResp, err := http.DefaultClient.Do(bulkReq)
	if err != nil {
		t.Fatal(err)
	}
	var bulkResult struct {
		Succeeded []string `json:"succeeded"`
		Failed    []struct {
			UserID string `json:"userId"`
		} `json:"failed"`
	}
	err = json.NewDecoder(bulkResp.Body).Decode(&bulkResult)
	_ = bulkResp.Body.Close()
	if err != nil || bulkResp.StatusCode != http.StatusMultiStatus || len(bulkResult.Succeeded) != 1 || bulkResult.Succeeded[0] != user.ID || len(bulkResult.Failed) != 1 || bulkResult.Failed[0].UserID != "missing-"+id[:8] {
		t.Fatalf("bulk update status=%d result=%+v err=%v", bulkResp.StatusCode, bulkResult, err)
	}
	principal, err = accounts.Principal(ctx, user.ID)
	if err != nil || principal.Quotas.MonthlyJobs.Value == nil || *principal.Quotas.MonthlyJobs.Value != limit {
		t.Fatalf("bulk quota change was not applied: %+v err=%v", principal.Quotas.MonthlyJobs, err)
	}
}

func randomTestID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}
