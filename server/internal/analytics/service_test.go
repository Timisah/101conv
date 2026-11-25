package analytics

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/jobs"
	"github.com/timisah/101conv/server/internal/management"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("101CONV_TEST_DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL analytics tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestAnalyticsLedgerTriggerBackfillRetentionAndPrivacy(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	root := t.TempDir()
	mgr, err := management.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	jobSvc, err := jobs.NewService(db, mgr, root+"/uploads", root+"/results")
	if err != nil {
		t.Fatal(err)
	}
	if err := jobSvc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	authSvc, err := auth.NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := authSvc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Clean tables for deterministic assertions.
	_, _ = db.ExecContext(ctx, `DROP TABLE IF EXISTS conv_processing_ledger, conv_analytics_meta CASCADE`)
	_, _ = db.ExecContext(ctx, `DELETE FROM conv_jobs; DELETE FROM conv_uploads;`)

	// Seed one retained terminal job before analytics.Migrate to test backfill.
	_, err = db.ExecContext(ctx, `INSERT INTO conv_uploads(id,owner_id,file_name,extension,size_bytes,sha256) VALUES('up-legacy','alice','secret-report.pdf','pdf',1000,'00')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO conv_jobs(id,idempotency_key,request_hash,owner_id,upload_id,profile_id,profile_version,source_format,target_format,mode,state,result_size,execution) VALUES('job-legacy','k-legacy','h-legacy','alice','up-legacy','p1','1.0.0','pdf','png','convert','succeeded',400,'{}'::jsonb)`)
	if err != nil {
		t.Fatal(err)
	}

	svc, err := NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	// Re-running Migrate must be idempotent.
	if err := svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	// Insert live queued jobs (batch items, guest sessions, failed, cancelled) and transition them.
	_, err = db.ExecContext(ctx, `
INSERT INTO conv_uploads(id,owner_id,file_name,extension,size_bytes,sha256) VALUES
 ('up-b1','guest:sess-1','a.jpg','jpg',2000,'01'),
 ('up-b2','guest:sess-2','b.jpg','jpg',3000,'02'),
 ('up-fail','alice','bad.jpg','jpg',5000,'03'),
 ('up-cancel','deleted-user-id','c.jpg','jpg',4000,'04')
`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
INSERT INTO conv_jobs(id,idempotency_key,request_hash,owner_id,upload_id,batch_id,profile_id,profile_version,source_format,target_format,mode,state,execution) VALUES
 ('job-b1','k-b1','h-b1','guest:sess-1','up-b1','batch-1','p1','1.0.0','jpg','webp','compress','queued','{}'::jsonb),
 ('job-b2','k-b2','h-b2','guest:sess-2','up-b2','batch-1','p1','1.0.0','jpg','webp','compress','queued','{}'::jsonb),
 ('job-fail','k-fail','h-fail','alice','up-fail',NULL,'p1','1.0.0','jpg','webp','convert','queued','{}'::jsonb),
 ('job-cancel','k-cancel','h-cancel','deleted-user-id','up-cancel',NULL,'p1','1.0.0','jpg','webp','convert','queued','{}'::jsonb)
`)
	if err != nil {
		t.Fatal(err)
	}

	// Transition to terminal states; update job-b1 twice to verify idempotency.
	_, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='succeeded', result_size=800 WHERE id='job-b1'`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='succeeded', result_size=800, updated_at=now() WHERE id='job-b1'`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='succeeded', result_size=1200 WHERE id='job-b2'`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='failed' WHERE id='job-fail'`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='cancelled' WHERE id='job-cancel'`)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate retention cleanup deleting all job & upload records: ledger must survive intact!
	if _, err = db.ExecContext(ctx, `DELETE FROM conv_jobs; DELETE FROM conv_uploads;`); err != nil {
		t.Fatal(err)
	}

	ov, err := svc.Overview(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ov.BackfilledJobs != 1 {
		t.Fatalf("backfilledJobs = %d, want 1", ov.BackfilledJobs)
	}
	// Processed input bytes: 1000 (legacy) + 2000 (b1) + 3000 (b2) = 6000 (failed & cancelled excluded!)
	if ov.ProcessedInputBytes != 6000 || ov.ProcessedOutputBytes != 2400 || ov.SavedBytes != 3600 {
		t.Fatalf("unexpected byte totals: %+v", ov)
	}
	if ov.TotalItems != 5 || ov.SucceededItems != 3 || ov.FailedItems != 1 || ov.CancelledItems != 1 {
		t.Fatalf("unexpected item counts: %+v", ov)
	}
	if ov.ConvertItems != 1 || ov.CompressItems != 2 || ov.StandaloneJobs != 3 || ov.BatchItems != 2 || ov.BatchExecutions != 1 {
		t.Fatalf("unexpected mode/batch split: %+v", ov)
	}

	users, total, err := svc.ByUser(ctx, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	// guest:sess-1 and guest:sess-2 collapse into 1 "Guest sessions" bucket -> 3 rows total (guest, alice, deleted-user-id).
	if total != 3 || len(users) != 3 {
		t.Fatalf("expected 3 grouped user rows, got total=%d len=%d (%+v)", total, len(users), users)
	}
	if users[0].OwnerID != "guest" || users[0].DisplayName != "Guest sessions" || users[0].ProcessedInputBytes != 5000 {
		t.Fatalf("expected guest bucket first with 5000 input bytes, got %+v", users[0])
	}
	var foundDeleted bool
	for _, u := range users {
		if u.OwnerID == "deleted-user-id" && u.DisplayName == "Deleted user" && u.Category == "deleted" {
			foundDeleted = true
		}
	}
	if !foundDeleted {
		t.Fatalf("expected deleted user row anonymized as 'Deleted user', got %+v", users)
	}
}
