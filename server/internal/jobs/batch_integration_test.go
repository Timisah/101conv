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
	dsn := integrationDatabaseURL()
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL batch integration test")
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
	// A saturated batch and canceled queue entries must not mask eligible work
	// owned by another user.
	for _, owner := range []string{"owner-limit-a", "owner-limit-b", "owner-cancelled"} {
		if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='cancelled',updated_at=now() WHERE owner_id=$1 AND state='queued'`, owner); err != nil {
			t.Fatal(err)
		}
	}
	makeUserUpload := func(user, name string) Upload {
		t.Helper()
		u, e := svc.CreateUploadFor(ctx, user, 1<<20, nil, name, strings.NewReader("input"))
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	makeUserBatch := func(user, key string, files int) Batch {
		t.Helper()
		items := make([]BatchItemRequest, 0, files)
		for i := 0; i < files; i++ {
			u := makeUserUpload(user, fmt.Sprintf("%s-%d.ppm", user, i))
			items = append(items, BatchItemRequest{UploadID: u.ID, TargetFormat: "pgm"})
		}
		b, _, e := svc.CreateBatch(ctx, BatchRequest{Mode: "convert", IdempotencyKey: key, Items: items}, user, Limits{})
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	limitBatch := makeUserBatch("owner-limit-a", "limit-batch-0001", 3)
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='running',updated_at=now() WHERE id IN (SELECT current_job_id FROM conv_batch_items WHERE batch_id=$1 ORDER BY item_index LIMIT 2)`, limitBatch.ID); err != nil {
		t.Fatal(err)
	}
	_ = makeUserBatch("owner-limit-b", "other-batch-0001", 1)
	_ = makeUserBatch("owner-cancelled", "cancel-batch-0001", 1)
	if _, err = restarted.CancelBatch(ctx, func() string {
		var id string
		_ = db.QueryRowContext(ctx, `SELECT id FROM conv_batches WHERE owner_id='owner-cancelled'`).Scan(&id)
		return id
	}(), "owner-cancelled"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_worker_scheduler SET last_owner='owner-limit-a' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	limitedWorker, err := NewWorker(db, unusedBatchRunner{}, WorkerConfig{ID: "batch-limit-test", Concurrency: 1, BatchConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := limitedWorker.claim(ctx)
	if err != nil || claim == nil {
		t.Fatalf("ineligible saturated/canceled work stalled queue: claim=%+v err=%v", claim, err)
	}
	var claimedOwner string
	if err = db.QueryRowContext(ctx, `SELECT owner_id FROM conv_jobs WHERE id=$1`, claim.ID).Scan(&claimedOwner); err != nil || claimedOwner != "owner-limit-b" {
		t.Fatalf("expected eligible owner despite saturated earlier batch, got owner=%q err=%v", claimedOwner, err)
	}

	// Max-size batches remain one manageable record. Listing is aggregate-only;
	// detail hydration uses bounded queries rather than one query per item/job.
	large := makeUserBatch("owner-hundred", "hundred-batch-0001", MaxBatchItems)
	if large.Total != 100 || len(large.Items) != 100 || large.Queued != 100 {
		t.Fatalf("100-item batch was not represented consistently: total=%d items=%d queued=%d", large.Total, len(large.Items), large.Queued)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='failed',error='fixture failure',updated_at=now() WHERE id=(SELECT current_job_id FROM conv_batch_items WHERE batch_id=$1 AND item_index=0)`, large.ID); err != nil {
		t.Fatal(err)
	}
	readBack, err := restarted.GetBatch(ctx, large.ID, "owner-hundred", Limits{})
	if err != nil || readBack.Queued != 99 || readBack.Failed != 1 {
		t.Fatalf("job transition should atomically update durable counters: queued=%d failed=%d err=%v", readBack.Queued, readBack.Failed, err)
	}
	var storedItemState string
	if err = db.QueryRowContext(ctx, `SELECT state FROM conv_batch_items WHERE batch_id=$1 AND item_index=0`, large.ID).Scan(&storedItemState); err != nil || storedItemState != "failed" {
		t.Fatalf("job transition did not persist item state: %q err=%v", storedItemState, err)
	}
	listed, total, err := restarted.ListBatches(ctx, "owner-hundred", 10, 0)
	if err != nil || total != 1 || len(listed) != 1 || listed[0].Queued != 99 || listed[0].Failed != 1 {
		t.Fatalf("batch list aggregate mismatch: total=%d list=%+v err=%v", total, listed, err)
	}
	sharedUpload := makeUserUpload("owner-idempotent", "parallel-idempotency.ppm")
	sharedRequest := BatchRequest{Mode: "convert", IdempotencyKey: "same-batch-key-0001", Items: []BatchItemRequest{{UploadID: sharedUpload.ID, TargetFormat: "pgm"}}}
	type createResult struct {
		batch     Batch
		duplicate bool
		err       error
	}
	created := make(chan createResult, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			request := sharedRequest
			request.Items = append([]BatchItemRequest(nil), sharedRequest.Items...)
			b, duplicate, e := restarted.CreateBatch(ctx, request, "owner-idempotent", Limits{})
			created <- createResult{b, duplicate, e}
		}()
	}
	close(start)
	first, second := <-created, <-created
	if first.err != nil || second.err != nil || first.batch.ID == "" || first.batch.ID != second.batch.ID || first.duplicate == second.duplicate {
		t.Fatalf("concurrent same-key batch requests were not idempotent: first=%+v second=%+v", first, second)
	}
	var duplicateJobs int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM conv_batch_item_jobs WHERE batch_id=$1`, first.batch.ID).Scan(&duplicateJobs); err != nil || duplicateJobs != 1 {
		t.Fatalf("same idempotency key created %d jobs, err=%v", duplicateJobs, err)
	}
	// Concurrent claims obey the per-batch cap, recover expired leases, and
	// resume work after replacing the Worker instance.
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='cancelled',updated_at=now() WHERE state='queued'`); err != nil {
		t.Fatal(err)
	}
	parallelBatch := makeUserBatch("owner-parallel", "parallel-batch-0001", 8)
	parallelWorker, err := NewWorker(db, unusedBatchRunner{}, WorkerConfig{ID: "parallel-claim-worker", Concurrency: 4, BatchConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	claims := make(chan *claimedJob, 8)
	claimErrors := make(chan error, 8)
	claimStart := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			<-claimStart
			c, e := parallelWorker.claim(ctx)
			if e != nil {
				claimErrors <- e
				return
			}
			claims <- c
		}()
	}
	close(claimStart)
	claimed := []*claimedJob{}
	for i := 0; i < 8; i++ {
		select {
		case e := <-claimErrors:
			t.Fatal(e)
		case c := <-claims:
			if c != nil {
				claimed = append(claimed, c)
			}
		}
	}
	if len(claimed) != 2 {
		t.Fatalf("concurrent claims exceeded or failed to fill per-batch limit: got %d, want 2", len(claimed))
	}
	var activeInBatch int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM conv_jobs WHERE batch_id=$1 AND state='running'`, parallelBatch.ID).Scan(&activeInBatch); err != nil || activeInBatch != 2 {
		t.Fatalf("parallel batch running count=%d err=%v", activeInBatch, err)
	}
	expiredID := claimed[0].ID
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, expiredID); err != nil {
		t.Fatal(err)
	}
	recovered, err := parallelWorker.claim(ctx)
	if err != nil || recovered == nil || recovered.ID != expiredID || recovered.Attempt != 2 {
		t.Fatalf("expired lease was not reclaimed with a new attempt: claim=%+v err=%v", recovered, err)
	}
	var expiredAttemptState string
	if err = db.QueryRowContext(ctx, `SELECT state FROM conv_job_attempts WHERE job_id=$1 AND attempt_number=1`, expiredID).Scan(&expiredAttemptState); err != nil || expiredAttemptState != "lease_expired" {
		t.Fatalf("expired attempt state=%q err=%v", expiredAttemptState, err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET lease_until=now()-interval '1 second' WHERE batch_id=$1 AND state='running'`, parallelBatch.ID); err != nil {
		t.Fatal(err)
	}
	restartedWorker, err := NewWorker(db, unusedBatchRunner{}, WorkerConfig{ID: "parallel-claim-worker-restarted", Concurrency: 2, BatchConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restartedWorker.claim(ctx); err != nil {
		t.Fatalf("claim after Worker restart did not recover leases: %v", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM conv_job_attempts a JOIN conv_jobs j ON j.id=a.job_id WHERE j.batch_id=$1 AND a.state='lease_expired'`, parallelBatch.ID).Scan(&activeInBatch); err != nil || activeInBatch < 2 {
		t.Fatalf("restart did not persist expired attempts: count=%d err=%v", activeInBatch, err)
	}

	// The persistent round-robin cursor must rotate through all eligible owners,
	// not just alternate between a fixed pair.
	if _, err = db.ExecContext(ctx, `UPDATE conv_jobs SET state='cancelled',updated_at=now() WHERE state='queued'`); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"fair-round-a", "fair-round-b", "fair-round-c"} {
		_ = makeUserBatch(owner, owner+"-batch-0001", 2)
	}
	if _, err = db.ExecContext(ctx, `UPDATE conv_worker_scheduler SET last_owner='fair-round-a' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	fairRound := []string{}
	for i := 0; i < 6; i++ {
		c, e := restartedWorker.claim(ctx)
		if e != nil || c == nil {
			t.Fatalf("round-robin claim %d: claim=%+v err=%v", i, c, e)
		}
		var owner string
		if err = db.QueryRowContext(ctx, `SELECT owner_id FROM conv_jobs WHERE id=$1`, c.ID).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		fairRound = append(fairRound, owner)
	}
	if strings.Join(fairRound[:3], ",") != "fair-round-b,fair-round-c,fair-round-a" {
		t.Fatalf("eligible owner rotation starved a user: %v", fairRound)
	}
	guestOwner := "guest:capacity-fixture"
	guestLimits := Limits{Guest: true, MaxBatchFiles: 5, MaxGuestActiveJobs: 10, MaxGlobalGuestJobs: 20, MaxGlobalGuestStorageBytes: MaxOutputBytes + (1 << 20)}
	guestUpload, err := svc.CreateUploadWithGuestCapacity(ctx, guestOwner, 1<<20, nil, guestLimits.MaxGlobalGuestStorageBytes, "guest-one.ppm", strings.NewReader("guest"))
	if err != nil {
		t.Fatal(err)
	}
	guestBatch, _, err := svc.CreateBatch(ctx, BatchRequest{Mode: "convert", IdempotencyKey: "guest-capacity-batch-01", Items: []BatchItemRequest{{UploadID: guestUpload.ID, TargetFormat: "pgm"}}}, guestOwner, guestLimits)
	if err != nil || guestBatch.Queued != 1 {
		t.Fatalf("first guest batch should reserve one maximum output: batch=%+v err=%v", guestBatch, err)
	}
	guestUploadTwo, err := svc.CreateUploadWithGuestCapacity(ctx, guestOwner, 1<<20, nil, guestLimits.MaxGlobalGuestStorageBytes, "guest-two.ppm", strings.NewReader("guest"))
	if err != nil {
		t.Fatalf("a small upload should fit beside one reserved output: %v", err)
	}
	if _, _, err = svc.CreateBatch(ctx, BatchRequest{Mode: "convert", IdempotencyKey: "guest-capacity-batch-02", Items: []BatchItemRequest{{UploadID: guestUploadTwo.ID, TargetFormat: "pgm"}}}, guestOwner, guestLimits); !errors.Is(err, ErrGuestCapacity) {
		t.Fatalf("a second maximum output reservation exceeded guest storage but was accepted: %v", err)
	}
}
