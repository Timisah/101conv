package auth

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"reflect"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func ptrInt64(v int64) *int64 { return &v }

func testQuotas(max, jobs, bytes QuotaLimit) *Quotas {
	return &Quotas{MaxFileBytes: max, MonthlyJobs: jobs, MonthlyBytes: bytes}
}

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
	user, err := svc.CreateUser(ctx, NewUser{Username: username, Password: "a strong test password", RoleID: "converter", Quotas: testQuotas(QuotaLimit{Mode: "limited", Value: ptrInt64(1 << 20)}, QuotaLimit{Mode: "limited", Value: ptrInt64(5)}, QuotaLimit{Mode: "limited", Value: ptrInt64(2 << 20)})})
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

func TestPostgresGroupsPermissionConflictsAndQuotaInheritance(t *testing.T) {
	dsn := os.Getenv("101CONV_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set 101CONV_TEST_DATABASE_URL to run PostgreSQL access-control integration tests")
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
	suffix, e := randomID()
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		_, _ = db.Exec(`DELETE FROM conv_users WHERE username LIKE $1`, "access-"+suffix[:8]+"%")
		_, _ = db.Exec(`DELETE FROM conv_groups WHERE id LIKE $1`, "access-"+suffix[:8]+"%")
		_, _ = db.Exec(`DELETE FROM conv_roles WHERE id LIKE $1`, "access-"+suffix[:8]+"%")
	}()
	roleID := "access-" + suffix[:8] + "-role"
	role, err := svc.SaveRole(ctx, PermissionRole{ID: roleID, Name: "Access test " + suffix[:8], Permissions: map[string]bool{PermissionConversion: true, PermissionJobsRead: true}})
	if err != nil {
		t.Fatal(err)
	}
	user, err := svc.CreateUser(ctx, NewUser{Username: "access-" + suffix[:8], Password: "access integration password", RoleID: "converter", Quotas: testQuotas(QuotaLimit{Mode: "inherit"}, QuotaLimit{Mode: "inherit"}, QuotaLimit{Mode: "inherit"})})
	if err != nil {
		t.Fatal(err)
	}
	jobs50 := int64(50)
	unlimited := QuotaLimit{Mode: "unlimited"}
	inherit := QuotaLimit{Mode: "inherit"}
	grant, err := svc.SaveGroup(ctx, Group{ID: "access-" + suffix[:8] + "-grant", Name: "Access grant " + suffix[:8], RoleID: role.ID, Permissions: map[string]bool{}, Quotas: Quotas{MaxFileBytes: inherit, MonthlyJobs: QuotaLimit{Mode: "limited", Value: &jobs50}, MonthlyBytes: unlimited}})
	if err != nil {
		t.Fatal(err)
	}
	jobs20 := int64(20)
	deny, err := svc.SaveGroup(ctx, Group{ID: "access-" + suffix[:8] + "-deny", Name: "Access deny " + suffix[:8], Permissions: map[string]bool{PermissionConversion: false}, Quotas: Quotas{MaxFileBytes: inherit, MonthlyJobs: QuotaLimit{Mode: "limited", Value: &jobs20}, MonthlyBytes: inherit}})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.SetUserGroups(ctx, user.ID, []string{grant.ID, deny.ID}); err != nil {
		t.Fatal(err)
	}
	effective, err := svc.Principal(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if effective.Permissions[PermissionConversion] {
		t.Fatal("a group denial must beat grants from the role and another group")
	}
	if effective.Permissions[PermissionJobsRead] != true {
		t.Fatal("role grant was not inherited")
	}
	if effective.Quotas.MonthlyJobs.Mode != "limited" || effective.Quotas.MonthlyJobs.Value == nil || *effective.Quotas.MonthlyJobs.Value != 20 {
		t.Fatalf("group quota conflict should choose the stricter limit, got %+v", effective.Quotas.MonthlyJobs)
	}
	if effective.Quotas.MonthlyBytes.Mode != "unlimited" {
		t.Fatalf("unlimited group quota was not inherited: %+v", effective.Quotas.MonthlyBytes)
	}
	allow := true
	effectiveUser, err := svc.UpdateUser(ctx, user.ID, UserUpdate{PermissionOverrides: map[string]*bool{PermissionConversion: &allow}, Quotas: &Quotas{MaxFileBytes: inherit, MonthlyJobs: unlimited, MonthlyBytes: inherit}})
	if err != nil {
		t.Fatal(err)
	}
	if !effectiveUser.Permissions[PermissionConversion] {
		t.Fatal("individual allow did not override conflicting group denial")
	}
	if effectiveUser.Quotas.MonthlyJobs.Mode != "unlimited" {
		t.Fatalf("individual unlimited quota did not override groups: %+v", effectiveUser.Quotas.MonthlyJobs)
	}
	// Existing quota backfill is tested by TestPostgresMigrationBackfillsExistingAccountQuotas.
	dataOnly := int64(5 << 30)
	independent, err := svc.CreateUser(ctx, NewUser{Username: "access-" + suffix[:8] + "-independent", Password: "independent quota test password", Quotas: testQuotas(QuotaLimit{Mode: "inherit"}, QuotaLimit{Mode: "unlimited"}, QuotaLimit{Mode: "limited", Value: &dataOnly})})
	if err != nil {
		t.Fatal(err)
	}
	if independent.Quotas.MonthlyJobs.Mode != "unlimited" || independent.Quotas.MonthlyBytes.Mode != "limited" || independent.Quotas.MonthlyBytes.Value == nil || *independent.Quotas.MonthlyBytes.Value != dataOnly {
		t.Fatalf("independent unlimited-job/limited-data quotas were not preserved: %+v", independent.Quotas)
	}
}

func TestPostgresLastAdministratorCannotBeRemoved(t *testing.T) {
	dsn := os.Getenv("101CONV_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set 101CONV_TEST_DATABASE_URL to run PostgreSQL administrator protection tests")
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
	suffix, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	adminQuotas := testQuotas(QuotaLimit{Mode: "limited", Value: ptrInt64(100 << 20)}, QuotaLimit{Mode: "unlimited"}, QuotaLimit{Mode: "unlimited"})
	first, err := svc.CreateUser(ctx, NewUser{Username: "admin-a-" + suffix[:8], Password: "administrator test password", Quotas: adminQuotas})
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.CreateUser(ctx, NewUser{Username: "admin-b-" + suffix[:8], Password: "administrator test password", Quotas: adminQuotas})
	if err != nil {
		_, _ = db.Exec(`DELETE FROM conv_users WHERE id=$1`, first.ID)
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(`DELETE FROM conv_users WHERE id IN ($1,$2)`, first.ID, second.ID)
	}()
	admin, regular := RoleAdmin, RoleUser
	if _, err = svc.UpdateUser(ctx, first.ID, UserUpdate{Role: &admin}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.UpdateUser(ctx, second.ID, UserUpdate{Role: &admin}); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.UpdateUser(ctx, first.ID, UserUpdate{Role: &regular}); err != nil {
		t.Fatalf("should permit removing one of two administrators: %v", err)
	}
	if _, err = svc.UpdateUser(ctx, second.ID, UserUpdate{Role: &regular}); !errors.Is(err, ErrConflict) {
		t.Fatalf("last administrator demotion should conflict, got %v", err)
	}
}

func TestPostgresMigrationBackfillsExistingAccountQuotas(t *testing.T) {
	dsn := os.Getenv("101CONV_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set 101CONV_TEST_DATABASE_URL to run PostgreSQL migration tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	suffix, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	schema := "migration_" + suffix[:12]
	if _, err = db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = db.Exec(`DROP SCHEMA ` + schema + ` CASCADE`) }()
	if _, err = db.Exec(`SET search_path TO ` + schema); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE conv_users (
	 id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_salt BYTEA NOT NULL, password_hash BYTEA NOT NULL,
	 role TEXT NOT NULL DEFAULT 'user', enabled BOOLEAN NOT NULL DEFAULT true, can_convert BOOLEAN NOT NULL DEFAULT true,
	 can_read_jobs BOOLEAN NOT NULL DEFAULT true, max_file_bytes BIGINT NOT NULL DEFAULT 104857600,
	 monthly_jobs INTEGER NOT NULL DEFAULT 0, monthly_bytes BIGINT NOT NULL DEFAULT 0,
	 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), CHECK(role IN ('user','admin')),
	 CHECK(max_file_bytes > 0), CHECK(monthly_jobs >= 0), CHECK(monthly_bytes >= 0));
	INSERT INTO conv_users(id,username,password_salt,password_hash,max_file_bytes,monthly_jobs,monthly_bytes)
	VALUES('old-account','old-user',decode('00112233445566778899aabbccddeeff','hex'),decode('00112233445566778899aabbccddeeff','hex'),7340032,27,9437184)`)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	p, err := svc.Principal(context.Background(), "old-account")
	if err != nil {
		t.Fatal(err)
	}
	if p.Quotas.MaxFileBytes.Mode != "limited" || p.Quotas.MaxFileBytes.Value == nil || *p.Quotas.MaxFileBytes.Value != 7340032 || p.Quotas.MonthlyJobs.Mode != "limited" || p.Quotas.MonthlyJobs.Value == nil || *p.Quotas.MonthlyJobs.Value != 27 || p.Quotas.MonthlyBytes.Mode != "limited" || p.Quotas.MonthlyBytes.Value == nil || *p.Quotas.MonthlyBytes.Value != 9437184 {
		t.Fatalf("existing quota values changed during migration: %+v", p.Quotas)
	}
}
