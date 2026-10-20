package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestInitialAdministratorClaimIsAtomicAndResumable(t *testing.T) {
	dsn := integrationDatabaseURL()
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for PostgreSQL setup integration test")
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
	guestSettings, err := svc.AnonymousSettings(ctx)
	if err != nil || !guestSettings.Enabled {
		t.Fatalf("fresh setup should enable guest mode: %+v %v", guestSettings, err)
	}
	expandedGuestSettings := guestSettings
	expandedGuestSettings.MaxFileBytes = 150 << 20
	if err = svc.SetAnonymousSettings(ctx, expandedGuestSettings); err != nil {
		t.Fatalf("150 MiB guest quota should be accepted: %v", err)
	}
	guestSettings, err = svc.AnonymousSettings(ctx)
	if err != nil || guestSettings.MaxFileBytes != 150<<20 {
		t.Fatalf("expanded guest quota was not persisted: %+v %v", guestSettings, err)
	}
	guest, guestToken, guestExpires, _, err := svc.CreateGuestSession(ctx)
	if err != nil || !guest.Guest || guest.ID == "" || !guestExpires.After(time.Now()) {
		t.Fatalf("guest session: %+v expiry=%v err=%v", guest, guestExpires, err)
	}
	validatedGuest, _, err := svc.AuthenticateGuest(ctx, guestToken)
	if err != nil || !validatedGuest.Guest || validatedGuest.ID != guest.ID {
		t.Fatalf("guest validation returned a different identity: %+v %v", validatedGuest, err)
	}
	secondGuest, secondToken, _, _, err := svc.CreateGuestSession(ctx)
	if err != nil || secondGuest.ID == guest.ID {
		t.Fatalf("guest sessions must have isolated owners: %s != %s (%v)", guest.ID, secondGuest.ID, err)
	}
	for i, want := range []bool{true, false} {
		allowed, rateErr := svc.ConsumeGuestRate(ctx, guest.ID, "test-upload", 1, 3)
		if rateErr != nil || allowed != want {
			t.Fatalf("per-guest rate attempt %d allowed=%v err=%v, want %v", i+1, allowed, rateErr, want)
		}
	}
	if allowed, rateErr := svc.ConsumeGuestRate(ctx, secondGuest.ID, "test-upload", 1, 3); rateErr != nil || !allowed {
		t.Fatalf("second guest should fit global rate: allowed=%v err=%v", allowed, rateErr)
	}
	thirdOwnerAllowed, rateErr := svc.ConsumeGuestRate(ctx, "guest:third-test-owner", "test-upload", 1, 3)
	if rateErr != nil || thirdOwnerAllowed {
		t.Fatalf("global guest rate should reject the third upload: allowed=%v err=%v", thirdOwnerAllowed, rateErr)
	}
	settingsDisabled := guestSettings
	settingsDisabled.Enabled = false
	if err = svc.SetAnonymousSettings(ctx, settingsDisabled); err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.AuthenticateGuest(ctx, secondToken); !errors.Is(err, ErrDisabled) {
		t.Fatalf("guest session remained usable after guest access was disabled: %v", err)
	}
	settingsDisabled.Enabled = true
	if err = svc.SetAnonymousSettings(ctx, settingsDisabled); err != nil {
		t.Fatal(err)
	}
	if err = svc.RevokeGuestSession(ctx, guestToken); err != nil {
		t.Fatal(err)
	}
	if _, _, err = svc.AuthenticateGuest(ctx, guestToken); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("revoked guest credential authenticated: %v", err)
	}
	if err = svc.RevokeGuestSession(ctx, secondToken); err != nil {
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
