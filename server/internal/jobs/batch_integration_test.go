package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
	"github.com/timisah/101conv/server/internal/worker"
)

type unusedBatchRunner struct{}

func (unusedBatchRunner) Run(context.Context, worker.Invocation) (worker.Result, error) {
	return worker.Result{}, nil
}

func TestDurableBatchIdempotencyRetryCancelAndRestart(t *testing.T) {
	dsn := os.Getenv("101CONV_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set 101CONV_TEST_DATABASE_URL to run PostgreSQL batch integration test")
	}
	ctx := context.Background()
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("batch_%d", time.Now().UnixNano())
	if _, err = adminDB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer adminDB.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
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
	db.SetMaxOpenConns(8)
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
	engineDef := engine.Definition{SchemaVersion: 1, ID: "batch-engine", Version: "1.0.0", Name: "Batch engine", Platform: runtime.GOOS + "-" + runtime.GOARCH, Capabilities: []string{"convert"}, Executables: map[string]string{"tool": "tool"}}
	if _, err = manager.InstallEngine(ctx, engineDef, imports); err != nil {
		t.Fatal(err)
	}
	profileDef := profile.Definition{SchemaVersion: 1, Type: profile.TypeConversion, ID: "batch-profile", Version: "1.0.0", Name: "Batch profile", Engines: []profile.EngineRequirement{{EngineID: engineDef.ID, Versions: engine.VersionRange{Minimum: "1.0.0"}, Capabilities: []string{"convert"}}}, Rules: []profile.ConversionRule{{EngineID: engineDef.ID, Executable: "tool", Sources: []string{"ppm", "jpg"}, Targets: []string{"pgm"}, Arguments: []string{"{source}", "{target}"}}}}
	if _, err = manager.InstallProfile(ctx, profileDef, ""); err != nil {
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
		u, e := svc.CreateUploadFor(ctx, "owner", 1<<20, nil, name, strings.NewReader("input"))
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	uploads := []Upload{makeUpload("a.ppm"), makeUpload("b.jpg"), makeUpload("c.ppm")}
	req := BatchRequest{Mode: "convert", IdempotencyKey: "batch-request-001", Items: []BatchItemRequest{{UploadID: uploads[0].ID, TargetFormat: "pgm"}, {UploadID: uploads[1].ID, TargetFormat: "pgm"}, {UploadID: uploads[2].ID, TargetFormat: "pgm"}}}
	batch, duplicate, err := svc.CreateBatch(ctx, req, "owner", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if duplicate || batch.Total != 3 || batch.Queued != 3 {
		t.Fatalf("created batch=%+v duplicate=%v", batch, duplicate)
	}
	again, duplicate, err := svc.CreateBatch(ctx, req, "owner", Limits{})
	if err != nil || !duplicate || again.ID != batch.ID {
		t.Fatalf("idempotent create=%+v duplicate=%v err=%v", again, duplicate, err)
	}
	if _, _, err = svc.CreateBatch(ctx, BatchRequest{Mode: "convert", IdempotencyKey: "duplicate-upload-001", Items: []BatchItemRequest{{UploadID: uploads[0].ID, TargetFormat: "pgm"}, {UploadID: uploads[0].ID, TargetFormat: "pgm"}}}, "owner", Limits{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("duplicate upload should be rejected before partial submission: %v", err)
	}
	unsupported := makeUpload("unsupported.bmp")
	unsupportedBatch, _, err := svc.CreateBatch(ctx, BatchRequest{Mode: "convert", IdempotencyKey: "unsupported-batch-001", Items: []BatchItemRequest{{UploadID: unsupported.ID, TargetFormat: "pgm"}}}, "owner", Limits{})
	if err != nil || unsupportedBatch.Failed != 1 || unsupportedBatch.Items[0].ErrorCode != "unsupported_or_incompatible_profile" {
		t.Fatalf("unsupported batch item should have a stable error category: batch=%+v err=%v", unsupportedBatch, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='failed',error='fixture failure' WHERE id=(SELECT current_job_id FROM conv_batch_items WHERE batch_id=$1 AND item_index=0)`, batch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='succeeded',result_key=id||'-1.bin',result_size=1 WHERE id=(SELECT current_job_id FROM conv_batch_items WHERE batch_id=$1 AND item_index=1)`, batch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CancelBatchItem(ctx, batch.ID, 2, "owner"); err != nil {
		t.Fatal(err)
	}
	batch, err = svc.GetBatch(ctx, batch.ID, "owner", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Failed != 1 || batch.Succeeded != 1 || batch.Cancelled != 1 || batch.State != "partial" {
		t.Fatalf("partial batch=%+v", batch)
	}
	batch, err = svc.RetryBatchItems(ctx, batch.ID, "owner", nil, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Total != 3 || batch.Queued != 1 || batch.Succeeded != 1 || batch.Cancelled != 1 {
		t.Fatalf("retry changed successful/canceled item: %+v", batch)
	}
	var history int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM conv_batch_item_jobs WHERE batch_id=$1 AND item_index=0`, batch.ID).Scan(&history); err != nil || history != 2 {
		t.Fatalf("retry history=%d err=%v", history, err)
	}
	// A new service instance observes the same batch and immutable job references.
	restarted, err := NewService(db, manager, svc.UploadRoot(), svc.ResultRoot())
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	batch, err = restarted.GetBatch(ctx, batch.ID, "owner", Limits{})
	if err != nil || batch.Queued != 1 || batch.Succeeded != 1 {
		t.Fatalf("restart batch=%+v err=%v", batch, err)
	}
	if _, err = restarted.CancelBatch(ctx, batch.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	batch, err = restarted.GetBatch(ctx, batch.ID, "owner", Limits{})
	if err != nil || batch.Cancelled != 2 || batch.Succeeded != 1 {
		t.Fatalf("cancel batch=%+v err=%v", batch, err)
	}
	if _, err = restarted.GetBatch(ctx, batch.ID, "another-user", Limits{}); err != ErrNotFound {
		t.Fatalf("cross-owner access err=%v", err)
	}
	expiredUpload := makeUpload("expired.ppm")
	expiredBatch, _, err := restarted.CreateBatch(ctx, BatchRequest{Mode: "convert", IdempotencyKey: "expired-batch-001", Items: []BatchItemRequest{{UploadID: expiredUpload.ID, TargetFormat: "pgm"}}}, "owner", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='failed' WHERE id=(SELECT current_job_id FROM conv_batch_items WHERE batch_id=$1 AND item_index=0)`, expiredBatch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_uploads SET original_deleted_at=now() WHERE id=$1`, expiredUpload.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.RetryBatchItems(ctx, expiredBatch.ID, "owner", []int{0}, Limits{}); err != ErrFileExpired {
		t.Fatalf("retry of expired original err=%v", err)
	}
	createOwnerBatch := func(user string) Batch {
		uploadIDs := []string{}
		for i := 0; i < 2; i++ {
			u, createErr := svc.CreateUploadFor(ctx, user, 1<<20, nil, fmt.Sprintf("%s-%d.ppm", user, i), strings.NewReader("input"))
			if createErr != nil {
				t.Fatal(createErr)
			}
			uploadIDs = append(uploadIDs, u.ID)
		}
		items := []BatchItemRequest{{UploadID: uploadIDs[0], TargetFormat: "pgm"}, {UploadID: uploadIDs[1], TargetFormat: "pgm"}}
		result, _, createErr := svc.CreateBatch(ctx, BatchRequest{Mode: "convert", IdempotencyKey: user + "-fair-batch-001", Items: items}, user, Limits{})
		if createErr != nil {
			t.Fatal(createErr)
		}
		return result
	}
	firstOwnerBatch := createOwnerBatch("owner-a")
	_ = createOwnerBatch("owner-b")
	queueWorker, err := NewWorker(db, unusedBatchRunner{}, WorkerConfig{ID: "batch-fairness-test"})
	if err != nil {
		t.Fatal(err)
	}
	claimedOwners := []string{}
	for i := 0; i < 2; i++ {
		claim, claimErr := queueWorker.claim(ctx)
		if claimErr != nil || claim == nil {
			t.Fatalf("fair worker claim %d: claim=%+v err=%v", i, claim, claimErr)
		}
		var owner string
		if err = db.QueryRowContext(ctx, `SELECT owner_id FROM conv_jobs WHERE id=$1`, claim.ID).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		claimedOwners = append(claimedOwners, owner)
	}
	if claimedOwners[0] != "owner-a" || claimedOwners[1] != "owner-b" {
		t.Fatalf("queued batches should rotate owners fairly: %v (first batch %s)", claimedOwners, firstOwnerBatch.ID)
	}
}
