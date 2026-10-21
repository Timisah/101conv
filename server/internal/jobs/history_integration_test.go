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
)

func TestHistoryGroupsBatchesFiltersAndIsolatesOwners(t *testing.T) {
	dsn := integrationDatabaseURL()
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL history integration test")
	}
	ctx := context.Background()
	adminDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()
	schema := fmt.Sprintf("history_%d", time.Now().UnixNano())
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
	engineDef := engine.Definition{SchemaVersion: 1, ID: "history-engine", Version: "1.0.0", Name: "History engine", Platform: runtime.GOOS + "-" + runtime.GOARCH, Capabilities: []string{"convert"}, Executables: map[string]string{"tool": "tool"}}
	if _, err = manager.InstallEngine(ctx, engineDef, imports); err != nil {
		t.Fatal(err)
	}
	profileDef := profile.Definition{SchemaVersion: 1, Type: profile.TypeConversion, ID: "history-profile", Version: "1.0.0", Name: "History profile", Engines: []profile.EngineRequirement{{EngineID: engineDef.ID, Versions: engine.VersionRange{Minimum: "1.0.0"}, Capabilities: []string{"convert"}}}, Rules: []profile.ConversionRule{{EngineID: engineDef.ID, Executable: "tool", Sources: []string{"ppm"}, Targets: []string{"pgm"}, Arguments: []string{"{source}", "{target}"}}}}
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
	upload := func(owner, name string) Upload {
		t.Helper()
		u, e := svc.CreateUploadFor(ctx, owner, 1<<20, nil, name, strings.NewReader("input"))
		if e != nil {
			t.Fatal(e)
		}
		return u
	}
	single := func(owner, name, key string) Job {
		t.Helper()
		u := upload(owner, name)
		job, _, e := svc.SubmitFor(ctx, SubmitRequest{UploadID: u.ID, ProfileID: "history-profile", TargetFormat: "pgm", IdempotencyKey: key}, owner, Limits{})
		if e != nil {
			t.Fatal(e)
		}
		return job
	}
	soloJob := single("alice", "solo-photo.ppm", "history-solo-0001")
	otherJob := single("bob", "bobs-secret.ppm", "history-bob-00001")
	batchUploads := []Upload{upload("alice", "album-one.ppm"), upload("alice", "album-two.ppm")}
	batch, _, err := svc.CreateBatch(ctx, BatchRequest{Mode: "convert", IdempotencyKey: "history-batch-0001", Items: []BatchItemRequest{{UploadID: batchUploads[0].ID, TargetFormat: "pgm"}, {UploadID: batchUploads[1].ID, TargetFormat: "pgm"}}}, "alice", Limits{})
	if err != nil {
		t.Fatal(err)
	}

	list := func(f HistoryFilter) []HistoryEntry {
		t.Helper()
		entries, total, e := svc.History(ctx, f)
		if e != nil {
			t.Fatalf("history %+v: %v", f, e)
		}
		if total < len(entries) {
			t.Fatalf("total %d is smaller than the page %d", total, len(entries))
		}
		return entries
	}

	// A batch is one grouped entry and its member jobs never appear alone.
	alice := list(HistoryFilter{ScopeOwnerID: "alice"})
	if len(alice) != 2 {
		t.Fatalf("alice should see her standalone job and one batch, got %d: %+v", len(alice), alice)
	}
	kinds := map[string]HistoryEntry{}
	for _, entry := range alice {
		kinds[entry.Type] = entry
	}
	if kinds[HistoryJob].Job == nil || kinds[HistoryJob].Job.ID != soloJob.ID || kinds[HistoryJob].FileName != "solo-photo.ppm" {
		t.Fatalf("standalone entry=%+v", kinds[HistoryJob])
	}
	if b := kinds[HistoryBatch].Batch; b == nil || b.ID != batch.ID || b.Total != 2 || kinds[HistoryBatch].FileName != "album-one.ppm" || len(b.Items) != 0 {
		t.Fatalf("batch entry=%+v", kinds[HistoryBatch])
	}
	if alice[0].CreatedAt.Before(alice[1].CreatedAt) {
		t.Fatal("history must be newest first")
	}

	// Ownership is enforced by the scope, never by the optional filter.
	for _, entry := range list(HistoryFilter{ScopeOwnerID: "bob"}) {
		if entry.Job == nil || entry.Job.ID != otherJob.ID {
			t.Fatalf("bob received another owner's entry: %+v", entry)
		}
	}
	if got := list(HistoryFilter{ScopeOwnerID: "bob", OwnerID: "alice"}); len(got) != 0 {
		t.Fatalf("an owner filter must not widen the scope: %+v", got)
	}
	if got := list(HistoryFilter{ScopeOwnerID: "alice", Query: "bobs-secret"}); len(got) != 0 {
		t.Fatalf("search must not reach another owner's files: %+v", got)
	}

	// Administrators (empty scope) see everything and may filter by owner.
	if got := list(HistoryFilter{}); len(got) != 3 {
		t.Fatalf("admin history should include all owners, got %d", len(got))
	}
	if got := list(HistoryFilter{OwnerID: "bob"}); len(got) != 1 || got[0].Job.ID != otherJob.ID {
		t.Fatalf("admin owner filter=%+v", got)
	}
	named := list(HistoryFilter{IncludeOwners: true})
	for _, entry := range named {
		if entry.OwnerID == "" || entry.OwnerName == "" {
			t.Fatalf("admin entries must carry the owner: %+v", entry)
		}
	}
	if hidden := list(HistoryFilter{ScopeOwnerID: "alice"}); hidden[0].OwnerID != "" || hidden[0].OwnerName != "" {
		t.Fatalf("non-admin entries must not expose owner identifiers: %+v", hidden[0])
	}

	// Type, operation, status and search filters compose.
	if got := list(HistoryFilter{ScopeOwnerID: "alice", Kind: HistoryBatch}); len(got) != 1 || got[0].Batch == nil {
		t.Fatalf("type=batch: %+v", got)
	}
	if got := list(HistoryFilter{ScopeOwnerID: "alice", Kind: HistoryJob}); len(got) != 1 || got[0].Job == nil {
		t.Fatalf("type=job: %+v", got)
	}
	if got := list(HistoryFilter{ScopeOwnerID: "alice", Mode: "compress"}); len(got) != 0 {
		t.Fatalf("mode=compress: %+v", got)
	}
	if got := list(HistoryFilter{ScopeOwnerID: "alice", Status: "queued"}); len(got) != 2 {
		t.Fatalf("status=queued: %+v", got)
	}
	if got := list(HistoryFilter{ScopeOwnerID: "alice", Status: "failed"}); len(got) != 0 {
		t.Fatalf("status=failed: %+v", got)
	}
	if got := list(HistoryFilter{ScopeOwnerID: "alice", Query: "album-two"}); len(got) != 1 || got[0].Batch == nil {
		t.Fatalf("search must match a batch member's file name: %+v", got)
	}
	if got := list(HistoryFilter{ScopeOwnerID: "alice", Query: "solo-photo"}); len(got) != 1 || got[0].Job == nil {
		t.Fatalf("search must match a job's file name: %+v", got)
	}
	if got := list(HistoryFilter{ScopeOwnerID: "alice", Query: batch.ID[:8]}); len(got) != 1 || got[0].Batch == nil {
		t.Fatalf("search must match an ID prefix: %+v", got)
	}
	var memberJobID string
	if err = db.QueryRowContext(ctx, `SELECT current_job_id FROM conv_batch_items WHERE batch_id=$1 AND item_index=1`, batch.ID).Scan(&memberJobID); err != nil || memberJobID == "" {
		t.Fatalf("batch member job: %q %v", memberJobID, err)
	}
	if got := list(HistoryFilter{ScopeOwnerID: "alice", Query: memberJobID}); len(got) != 1 || got[0].Batch == nil {
		t.Fatalf("search must find a batch by a member job ID: %+v", got)
	}
	if got := list(HistoryFilter{ScopeOwnerID: "alice", Query: "100%_"}); len(got) != 0 {
		t.Fatalf("LIKE wildcards in the query must be literal: %+v", got)
	}

	// Time range and pagination.
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	if got := list(HistoryFilter{ScopeOwnerID: "alice", From: &future}); len(got) != 0 {
		t.Fatalf("from in the future: %+v", got)
	}
	if got := list(HistoryFilter{ScopeOwnerID: "alice", From: &past, To: &future}); len(got) != 2 {
		t.Fatalf("surrounding range: %+v", got)
	}
	page1, total, err := svc.History(ctx, HistoryFilter{ScopeOwnerID: "alice", Limit: 1})
	if err != nil || total != 2 || len(page1) != 1 {
		t.Fatalf("page 1 total=%d len=%d err=%v", total, len(page1), err)
	}
	page2, _, err := svc.History(ctx, HistoryFilter{ScopeOwnerID: "alice", Limit: 1, Offset: 1})
	if err != nil || len(page2) != 1 || page2[0].Type == page1[0].Type {
		t.Fatalf("page 2=%+v err=%v", page2, err)
	}

	// Batch status flows through the grouped entry; partial applies to batches only.
	if _, err = db.ExecContext(ctx, `UPDATE conv_batches SET state='partial' WHERE id=$1`, batch.ID); err != nil {
		t.Fatal(err)
	}
	if got := list(HistoryFilter{ScopeOwnerID: "alice", Status: "partial"}); len(got) != 1 || got[0].Batch == nil {
		t.Fatalf("status=partial: %+v", got)
	}
	for _, bad := range []HistoryFilter{{Mode: "x"}, {Kind: "x"}, {Status: "x"}, {Kind: HistoryJob, Status: "partial"}, {From: &future, To: &past}} {
		if _, _, err = svc.History(ctx, bad); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("filter %+v should be rejected, got %v", bad, err)
		}
	}
}
