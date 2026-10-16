package auth

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestPasswordHashAndScopeChecks(t *testing.T) {
	salt := []byte("0123456789abcdef")
	first := passwordHash("example secure password", salt)
	if !reflect.DeepEqual(first, passwordHash("example secure password", salt)) {
		t.Fatal("same password and salt should verify deterministically")
	}
	if reflect.DeepEqual(first, passwordHash("different password", salt)) || reflect.DeepEqual(first, passwordHash("example secure password", []byte("fedcba9876543210"))) {
		t.Fatal("password hash must depend on both password and unique salt")
	}
	p := Principal{Role: RoleUser, Scopes: []string{ScopeRead}}
	if !ScopeAllows(p, ScopeRead) || ScopeAllows(p, ScopeSubmit) {
		t.Fatal("API key scope check returned an incorrect result")
	}
	if !ScopeAllows(AdminPrincipal(), ScopeSubmit) {
		t.Fatal("administrator should retain all scopes")
	}
}

func TestPostgresAccountAndAPIKeyLifecycle(t *testing.T) {
	dsn := os.Getenv("101CONV_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set 101CONV_TEST_DATABASE_URL to run PostgreSQL auth integration tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc, err := NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err = svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	id, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	username := "test-" + id[:12]
	user, err := svc.CreateUser(ctx, NewUser{Username: username, Password: "a strong test password", CanConvert: true, CanReadJobs: true, MaxFileBytes: 1 << 20, MonthlyJobs: 5, MonthlyBytes: 2 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`DELETE FROM conv_users WHERE id=$1`, user.ID) }()
	principal, err := svc.AuthenticatePassword(ctx, username, "a strong test password")
	if err != nil || principal.ID != user.ID {
		t.Fatalf("password auth principal=%+v err=%v", principal, err)
	}
	if _, err = svc.AuthenticatePassword(ctx, username, "wrong password"); err == nil {
		t.Fatal("wrong password was accepted")
	}
	created, err := svc.CreateAPIKey(ctx, user.ID, "automation", []string{ScopeSubmit, ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	if created.Token == "" {
		t.Fatal("new API key token was not returned once")
	}
	keyPrincipal, err := svc.AuthenticateAPIKey(ctx, created.Token)
	if err != nil || !keyPrincipal.APIKey || !ScopeAllows(keyPrincipal, ScopeRead) || ScopeAllows(keyPrincipal, ScopeCancel) {
		t.Fatalf("API key authentication/scopes=%+v err=%v", keyPrincipal, err)
	}
	keys, err := svc.ListAPIKeys(ctx, user.ID)
	if err != nil || len(keys) != 1 || keys[0].Name != "automation" {
		t.Fatalf("key listing=%+v err=%v", keys, err)
	}
	if err = svc.RevokeAPIKey(ctx, user.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AuthenticateAPIKey(ctx, created.Token); err == nil {
		t.Fatal("revoked key was accepted")
	}
	if err = svc.DeleteUser(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AuthenticatePassword(ctx, username, "a strong test password"); err == nil {
		t.Fatal("disabled user was accepted")
	}
}
