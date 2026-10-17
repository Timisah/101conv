package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestInitialAdministratorClaimIsAtomicAndResumable(t *testing.T) {
	dsn := os.Getenv("101CONV_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set 101CONV_TEST_DATABASE_URL for PostgreSQL setup integration test")
	}
	ctx := context.Background()
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("setup_%d", time.Now().UnixNano())
	if _, err = adminDB.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer adminDB.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	address, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := address.Query()
	query.Set("search_path", schema)
	address.RawQuery = query.Encode()
	db, err := sql.Open("pgx", address.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(5)
	svc, err := NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	status, err := svc.InitialSetupStatus(ctx)
	if err != nil || status.Completed || status.AdministratorCreated {
		t.Fatalf("fresh status: %+v %v", status, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"firstadmin", "secondadmin"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, createErr := svc.CreateInitialAdmin(ctx, name, "long-admin-password-123")
			results <- createErr
		}(name)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for result := range results {
		if result == nil {
			success++
		} else if errors.Is(result, ErrConflict) {
			conflict++
		} else {
			t.Fatalf("unexpected claim error: %v", result)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("claims: success=%d conflict=%d", success, conflict)
	}
	status, err = svc.InitialSetupStatus(ctx)
	if err != nil || status.Completed || !status.AdministratorCreated {
		t.Fatalf("resumable status: %+v %v", status, err)
	}
	var adminID string
	if err = db.QueryRowContext(ctx, `SELECT id FROM conv_users WHERE role='admin'`).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	if err = svc.CompleteInitialSetup(ctx, adminID); err != nil {
		t.Fatal(err)
	}
	status, err = svc.InitialSetupStatus(ctx)
	if err != nil || !status.Completed || !status.AdministratorCreated {
		t.Fatalf("finished status: %+v %v", status, err)
	}
	if _, err = svc.CreateInitialAdmin(ctx, "thirdadmin", "long-admin-password-123"); !errors.Is(err, ErrConflict) {
		t.Fatalf("claim after completion: %v", err)
	}
}
