package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"io"
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
	dsn := integrationDatabaseURL()
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for PostgreSQL cleanup integration test")
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
	var tombstone bool
	if err = db.QueryRowContext(ctx, `SELECT original_deleted_at IS NOT NULL FROM conv_uploads WHERE id=$1`, interrupted.ID).Scan(&tombstone); err != nil || !tombstone {
		t.Fatalf("cleanup must preserve an explicit expired upload record: expired=%v err=%v", tombstone, err)
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

func TestSuccessfulRetentionExpiresFilesButPreservesJobHistory(t *testing.T) {
	dsn := integrationDatabaseURL()
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for PostgreSQL retention integration test")
	}
	ctx := context.Background()
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("retention_%d", time.Now().UnixNano())
	if _, err = adminDB.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	defer adminDB.ExecContext(ctx, `DROP SCHEMA `+schema+` CASCADE`)
	address, _ := url.Parse(dsn)
	q := address.Query()
	q.Set("search_path", schema)
	address.RawQuery = q.Encode()
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
	if _, err = manager.InstallEngine(ctx, engine.Definition{SchemaVersion: 1, ID: "retain-engine", Version: "1.0.0", Name: "retention engine", Platform: runtime.GOOS + "-" + runtime.GOARCH, Capabilities: []string{"convert"}, Executables: map[string]string{"tool": "tool"}}, imports); err != nil {
		t.Fatal(err)
	}
	p := profile.Definition{SchemaVersion: 1, Type: profile.TypeConversion, ID: "retain-profile", Version: "1.0.0", Name: "Retention profile", Engines: []profile.EngineRequirement{{EngineID: "retain-engine", Versions: engine.VersionRange{Minimum: "1.0.0"}, Capabilities: []string{"convert"}}}, Rules: []profile.ConversionRule{{EngineID: "retain-engine", Executable: "tool", Sources: []string{"ppm"}, Targets: []string{"pgm"}, Arguments: []string{"{source}", "{target}"}}}}
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
	initialScan, err := svc.StorageScan(ctx)
	if err != nil || initialScan.Owners == nil {
		t.Fatalf("empty storage scan must serialize owners as an empty list: %+v err=%v", initialScan, err)
	}
	u, err := svc.CreateUploadFor(ctx, "owner", 100, nil, "image.ppm", strings.NewReader("P3\n1 1\n255\n255 0 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	failedUpload, err := svc.CreateUploadFor(ctx, "owner", 100, nil, "failed.ppm", strings.NewReader("P3\n1 1\n255\n255 0 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	failedJob, _, err := svc.SubmitFor(ctx, SubmitRequest{UploadID: failedUpload.ID, ProfileID: p.ID, TargetFormat: "pgm", IdempotencyKey: "failed-retention-job"}, "owner", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='failed',updated_at=now()-interval '2 days' WHERE id=$1`, failedJob.ID); err != nil {
		t.Fatal(err)
	}
	job, _, err := svc.SubmitFor(ctx, SubmitRequest{UploadID: u.ID, ProfileID: p.ID, TargetFormat: "pgm", IdempotencyKey: "retention-job"}, "owner", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	key := job.ID + "-1.bin"
	result := []byte("verified result fixture")
	if err = os.WriteFile(filepath.Join(svc.resultRoot, key), result, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='succeeded',result_key=$2,result_size=$3,updated_at=now()-interval '2 days' WHERE id=$1`, job.ID, key, len(result)); err != nil {
		t.Fatal(err)
	}
	activeDownload, _, _, err := svc.OpenResult(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_uploads SET created_at=now()-interval '2 days' WHERE id=$1`, u.ID); err != nil {
		t.Fatal(err)
	}
	days := 1
	unsubmittedDays := 7
	policy := RetentionPolicy{Unsubmitted: RetentionRule{Mode: "after_days", Days: &unsubmittedDays}, SubmittedOriginals: RetentionRule{Mode: "after_success"}, SuccessfulResults: RetentionRule{Mode: "after_days", Days: &days}, FailedOriginals: RetentionRule{Mode: "keep"}, CanceledOriginals: RetentionRule{Mode: "keep"}}
	if err = svc.SetRetentionPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	reloadedPolicy, err := svc.GetRetentionPolicy(ctx)
	if err != nil || reloadedPolicy.Unsubmitted.Mode != "after_days" || reloadedPolicy.Unsubmitted.Days == nil || *reloadedPolicy.Unsubmitted.Days != unsubmittedDays || reloadedPolicy.SuccessfulResults.Mode != "after_days" || reloadedPolicy.SuccessfulResults.Days == nil || *reloadedPolicy.SuccessfulResults.Days != days {
		t.Fatalf("retention policy did not round-trip: %+v err=%v", reloadedPolicy, err)
	}
	originalPreview, err := svc.PreviewCleanupCategory(ctx, "successful_originals", 0)
	if err != nil || originalPreview.Candidates != 1 {
		t.Fatalf("after-success preview=%+v err=%v", originalPreview, err)
	}
	resultPreview, err := svc.PreviewCleanupCategory(ctx, "successful_results", 1)
	if err != nil || resultPreview.Candidates != 1 {
		t.Fatalf("result preview=%+v err=%v", resultPreview, err)
	}
	report, err := svc.CleanupRetainedFiles(ctx, 100)
	if err != nil || report.Deleted != 2 {
		t.Fatalf("cleanup report=%+v err=%v", report, err)
	}
	streamed, err := io.ReadAll(activeDownload)
	if closeErr := activeDownload.Close(); err != nil || closeErr != nil || string(streamed) != string(result) {
		t.Fatalf("open download corrupted by cleanup: data=%q read=%v close=%v", streamed, err, closeErr)
	}
	got, err := svc.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != Succeeded || got.OriginalState != "expired" || got.ResultState != "expired" {
		t.Fatalf("job history/state not preserved: %+v", got)
	}
	if _, _, _, err = svc.OpenResult(ctx, job.ID); err != ErrFileExpired {
		t.Fatalf("expired output must not be downloadable, got %v", err)
	}
	if _, err = os.Stat(filepath.Join(svc.UploadRoot(), u.ID)); !os.IsNotExist(err) {
		t.Fatalf("eligible original remains: %v", err)
	}
	if _, err = os.Stat(filepath.Join(svc.UploadRoot(), failedUpload.ID)); err != nil {
		t.Fatalf("failed job original was removed: %v", err)
	}
	guestOriginal, err := svc.CreateUploadFor(ctx, "guest:retention-fixture", 100, nil, "guest.ppm", strings.NewReader("P3\n1 1\n255\n255 0 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	guestFailedOriginal, err := svc.CreateUploadFor(ctx, "guest:retention-fixture", 100, nil, "guest-failed.ppm", strings.NewReader("P3\n1 1\n255\n255 0 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	guestJob, _, err := svc.SubmitFor(ctx, SubmitRequest{UploadID: guestOriginal.ID, ProfileID: p.ID, TargetFormat: "pgm", IdempotencyKey: "guest-retention-job"}, "guest:retention-fixture", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	guestFailedJob, _, err := svc.SubmitFor(ctx, SubmitRequest{UploadID: guestFailedOriginal.ID, ProfileID: p.ID, TargetFormat: "pgm", IdempotencyKey: "guest-failed-retention-job"}, "guest:retention-fixture", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	guestResultKey := guestJob.ID + "-1.bin"
	guestResultData := []byte("guest verified result")
	if err = os.WriteFile(filepath.Join(svc.resultRoot, guestResultKey), guestResultData, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='succeeded',result_key=$2,result_size=$3,updated_at=now()-interval '2 days' WHERE id=$1`, guestJob.ID, guestResultKey, len(guestResultData)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='failed',updated_at=now()-interval '2 days' WHERE id=$1`, guestFailedJob.ID); err != nil {
		t.Fatal(err)
	}
	guestDownload, _, _, err := svc.OpenResultFor(ctx, guestJob.ID, "guest:retention-fixture")
	if err != nil {
		t.Fatal(err)
	}
	guestCleanup, err := svc.CleanupGuestExpired(ctx, GuestRetentionPolicy{UploadHours: 24, ResultHours: 1, FailedHours: 72, MetadataHours: 168}, 100)
	if err != nil || guestCleanup.Deleted != 2 {
		guestDownload.Close()
		t.Fatalf("guest result/original cleanup: %+v err=%v", guestCleanup, err)
	}
	guestStreamed, readErr := io.ReadAll(guestDownload)
	closeErr := guestDownload.Close()
	if readErr != nil || closeErr != nil || string(guestStreamed) != string(guestResultData) {
		t.Fatalf("guest cleanup disrupted active result download: %q read=%v close=%v", guestStreamed, readErr, closeErr)
	}
	guestExpired, err := svc.GetFor(ctx, guestJob.ID, "guest:retention-fixture")
	if err != nil || guestExpired.ResultState != "expired" || guestExpired.OriginalState != "expired" {
		t.Fatalf("guest tombstones/history missing after cleanup: %+v err=%v", guestExpired, err)
	}
	if _, err = os.Stat(filepath.Join(svc.UploadRoot(), guestFailedOriginal.ID)); err != nil {
		t.Fatalf("failed guest original was deleted before its own retention deadline: %v", err)
	}
	if _, err = svc.StartStorageScan(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	var inventory StorageScan
	for time.Now().Before(deadline) {
		inventory, err = svc.StorageScan(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if inventory.Status == "complete" || inventory.Status == "interrupted" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if inventory.Status != "complete" {
		t.Fatalf("filesystem scan status=%s error=%s", inventory.Status, inventory.Error)
	}
	originals := inventory.Categories["originals"]
	if originals.Files != 2 || originals.MissingMetadata != 0 || originals.UnreferencedFiles != 0 {
		t.Fatalf("original reconciliation mismatch: %+v", originals)
	}
	if originals.AgeBuckets["0-7 days"].Count != 2 {
		t.Fatalf("filesystem modification-age bucket was not measured: %+v", originals.AgeBuckets)
	}
	if inventory.Categories["engines"].Files == 0 {
		t.Fatal("installed engine files were not included in measured inventory")
	}
	var engineAgeCount int64
	for _, bucket := range inventory.Categories["engines"].AgeBuckets {
		engineAgeCount += bucket.Count
	}
	if engineAgeCount != inventory.Categories["engines"].Files {
		t.Fatalf("engine package age buckets=%d actual files=%d", engineAgeCount, inventory.Categories["engines"].Files)
	}
	overview, err := svc.StorageOverview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if overview.DatabaseBytes <= 0 || overview.ReservedInputBytes != 0 {
		t.Fatalf("database and reserved input measurements are inconsistent: %+v", overview)
	}
}

// Force a guest-policy writer between the scheduler's outer read lock and
// retained-file cleanup. Recursive RLock would deadlock once a writer queues.
func TestRetentionStartupAllowsQueuedPolicyWriter(t *testing.T) {
	dsn := integrationDatabaseURL()
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for retention scheduler regression")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("retention_lock_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
	address, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := address.Query()
	q.Set("search_path", schema)
	address.RawQuery = q.Encode()
	db, err := sql.Open("pgx", address.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := &Service{db: db}
	if err = svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `LOCK TABLE conv_storage_settings IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	go func() { defer close(stopped); svc.RunRetention(ctx) }()
	until := time.Now().Add(5 * time.Second)
	for svc.retentionMu.TryLock() {
		svc.retentionMu.Unlock()
		if time.Now().After(until) {
			t.Fatal("scheduler did not acquire read lock")
		}
		time.Sleep(time.Millisecond)
	}
	acquired := make(chan struct{})
	go func() { unlock := svc.LockGuestRetention(); defer unlock(); close(acquired) }()
	until = time.Now().Add(5 * time.Second)
	for svc.retentionMu.TryRLock() {
		svc.retentionMu.RUnlock()
		if time.Now().After(until) {
			t.Fatal("guest writer did not queue")
		}
		time.Sleep(time.Millisecond)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("queued policy writer deadlocked retention startup")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("retention scheduler did not stop")
	}
}
