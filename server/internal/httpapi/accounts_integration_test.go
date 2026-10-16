package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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
	user, err := accounts.CreateUser(ctx, auth.NewUser{Username: username, Password: "integration test password", CanConvert: true, CanReadJobs: true, MaxFileBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`DELETE FROM conv_users WHERE id=$1`, user.ID) }()
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
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
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
	if _, err = accounts.UpdateUser(ctx, user.ID, auth.UserUpdate{CanReadJobs: &denyRead}); err != nil {
		t.Fatal(err)
	}
	if got := call(http.DefaultClient, http.MethodGet, "/api/v1/jobs", key.Token, ""); got != http.StatusForbidden {
		t.Fatalf("key bypassed changed account permission, status=%d", got)
	}
	allowRead := true
	if _, err = accounts.UpdateUser(ctx, user.ID, auth.UserUpdate{CanReadJobs: &allowRead}); err != nil {
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
	if got := call(client, http.MethodGet, "/api/v1/users", "", ""); got != http.StatusForbidden {
		t.Fatalf("user account admin endpoint status=%d", got)
	}
}

func randomTestID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}
