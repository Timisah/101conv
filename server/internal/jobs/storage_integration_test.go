package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
)

func TestAbandonedCleanupPreservesJobsAndResumes(t *testing.T) {
	dsn := os.Getenv("101CONV_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set 101CONV_TEST_DATABASE_URL for PostgreSQL cleanup integration test")
	}
	ctx := context.Background()
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("cleanup_%d", time.Now().UnixNano())
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
	db.SetMaxOpenConns(6)
	root := t.TempDir()
	imports := filepath.Join(root, "imports")
	if err = os.MkdirAll(imports, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(imports, "tool"), []byte("fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := management.OpenWithPolicy(filepath.Join(root, "state"), []string{imports})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if _, err = manager.InstallEngine(ctx, engine.Definition{SchemaVersion: 1, ID: "cleanup-engine", Version: "1.0.0", Name: "Cleanup engine", Platform: runtime.GOOS + "-" + runtime.GOARCH, Capabilities: []string{"convert"}, Executables: map[string]string{"tool": "tool"}}, imports); err != nil {
		t.Fatal(err)
	}
	p := profile.Definition{SchemaVersion: 1, Type: profile.TypeConversion, ID: "cleanup-profile", Version: "1.0.0", Name: "Cleanup profile", Engines: []profile.EngineRequirement{{EngineID: "cleanup-engine", Versions: engine.VersionRange{Minimum: "1.0.0"}, Capabilities: []string{"convert"}}}, Rules: []profile.ConversionRule{{EngineID: "cleanup-engine", Executable: "tool", Sources: []string{"ppm"}, Targets: []string{"pgm"}, Arguments: []string{"{source}", "{target}"}}}}
	if _, err = manager.InstallProfile(ctx, p, ""); err != nil {
		t.Fatal(err)
	}
	svc, err := NewService(db, manager, filepath.Join(root, "uploads"), filepath.Join(root, "results"))
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	makeUpload := func(name string) Upload {
		t.Helper()
		u, e := svc.CreateUploadFor(ctx, "owner", 100, nil, name, strings.NewReader("P3\n1 1\n255\n255 0 0\n"))
		if e != nil {
			t.Fatal(e)
		}
		if _, e = db.ExecContext(ctx, `UPDATE conv_uploads SET created_at=now()-interval '2 days' WHERE id=$1`, u.ID); e != nil {
			t.Fatal(e)
		}
		return u
	}
	abandoned := makeUpload("abandoned.ppm")
	active := makeUpload("active.ppm")
	if _, _, err = svc.SubmitFor(ctx, SubmitRequest{UploadID: active.ID, ProfileID: p.ID, TargetFormat: "pgm", IdempotencyKey: "active-cleanup-key"}, "owner", Limits{}); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewAbandonedCleanup(ctx, 1)
	if err != nil || preview.Candidates != 1 {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	report, err := svc.CleanupAbandoned(ctx, 1, 100)
	if err != nil || report.Deleted != 1 || report.ReclaimedBytes <= 0 {
		t.Fatalf("cleanup: %+v %v", report, err)
	}
	if _, err = os.Stat(filepath.Join(svc.UploadRoot(), abandoned.ID)); !os.IsNotExist(err) {
		t.Fatalf("abandoned original retained: %v", err)
	}
	if _, err = os.Stat(filepath.Join(svc.UploadRoot(), active.ID)); err != nil {
		t.Fatalf("active original removed: %v", err)
	}
	interrupted := makeUpload("interrupted.ppm")
	if _, err = db.ExecContext(ctx, `UPDATE conv_uploads SET cleanup_pending=true,cleanup_claim_until=now()-interval '1 minute',cleanup_observed_bytes=$2 WHERE id=$1`, interrupted.ID, interrupted.Size); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(svc.UploadRoot(), interrupted.ID)); err != nil {
		t.Fatal(err)
	}
	report, err = svc.CleanupAbandoned(ctx, 0, 100)
	if err != nil || report.Deleted != 1 || report.ReclaimedBytes != interrupted.Size {
		t.Fatalf("restart recovery: %+v %v", report, err)
	}
	concurrent := makeUpload("concurrent.ppm")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := svc.CleanupAbandoned(ctx, 1, 100); results <- e }()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatalf("concurrent cleanup: %v", e)
		}
	}
	var events int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM conv_cleanup_events WHERE file_id=$1`, concurrent.ID).Scan(&events); err != nil || events != 1 {
		t.Fatalf("cleanup event count=%d err=%v", events, err)
	}
}
