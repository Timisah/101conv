package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
)

func integrationDatabaseURL() string {
	if value := os.Getenv("TEST_DATABASE_URL"); value != "" {
		return value
	}
	return os.Getenv("101CONV_TEST_DATABASE_URL")
}

func TestPostgresOwnerIsolationAndQuotas(t *testing.T) {
	dsn := integrationDatabaseURL()
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL authorization integration tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	imports := filepath.Join(root, "imports")
	if err = os.MkdirAll(imports, 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(imports, "tool")
	if err = os.WriteFile(executable, []byte("test fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	probeExecutable := filepath.Join(imports, "ffprobe")
	if err = os.WriteFile(probeExecutable, []byte("test probe fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := management.OpenWithPolicy(filepath.Join(root, "state"), []string{imports})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	definition := engine.Definition{SchemaVersion: 1, ID: "test-engine", Version: "1.0.0", Name: "Test Engine", Platform: runtime.GOOS + "-" + runtime.GOARCH, Executables: map[string]string{"tool": "tool", "ffprobe": "ffprobe"}}
	if _, err = manager.InstallEngine(context.Background(), definition, imports); err != nil {
		t.Fatal(err)
	}
	min, max, step := 1.0, 8.0, 1.0
	p := profile.Definition{SchemaVersion: 1, Type: profile.TypeConversion, ID: "test-profile", Version: "1.0.0", Name: "Test Profile", Engines: []profile.EngineRequirement{{EngineID: "test-engine", Versions: engine.VersionRange{Minimum: "1.0.0"}}}, Rules: []profile.ConversionRule{{EngineID: "test-engine", Executable: "tool", Sources: []string{"png"}, Targets: []string{"jpg"}, Arguments: []string{"{source}", "{target}"}}}}
	if _, err = manager.InstallProfile(context.Background(), p, ""); err != nil {
		t.Fatal(err)
	}
	compressionProfile := profile.Definition{SchemaVersion: 1, Type: profile.TypeCompression, ID: "test-compression-profile", Version: "1.0.0", Name: "Test Compression Profile", Engines: p.Engines, Compression: &profile.CompressionConfig{Options: []profile.OptionDefinition{{Key: "targetSizeMb", Label: "Target size", Type: "number", Default: "2", Min: &min, Max: &max, Step: &step, TargetSizeMB: true}}, ProbeExecutable: "ffprobe", AudioBitrateKbps: 128, MinimumVideoKbps: 64}, Rules: []profile.ConversionRule{{EngineID: "test-engine", Executable: "tool", Sources: []string{"png"}, Targets: []string{"jpg"}, Arguments: []string{"{source}", "{compression.args}", "{target}"}, CompressionArguments: []string{"-b:a", "{compression.audioKbps}k"}}}}
	if _, err = manager.InstallProfile(context.Background(), compressionProfile, ""); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(db, manager, filepath.Join(root, "uploads"), filepath.Join(root, "results"))
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ownerSuffix, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	oneOwner := "user-one-" + ownerSuffix[:8]
	twoOwner := "user-two-" + ownerSuffix[:8]
	threeOwner := "user-three-" + ownerSuffix[:8]
	quotaLimit := func(n int64) *int64 { return &n }
	first, err := service.CreateUploadFor(ctx, oneOwner, 100, quotaLimit(25), "first.png", bytes.NewReader(bytes.Repeat([]byte("a"), 20)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreateUploadFor(ctx, oneOwner, 100, quotaLimit(25), "too-much.png", bytes.NewReader(bytes.Repeat([]byte("b"), 6))); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("monthly input quota error=%v", err)
	}
	second, err := service.CreateUploadFor(ctx, oneOwner, 100, quotaLimit(25), "second.png", bytes.NewReader([]byte("small")))
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateUploadFor(ctx, twoOwner, 100, quotaLimit(25), "other.png", bytes.NewReader([]byte("private")))
	if err != nil {
		t.Fatal(err)
	}
	compressionUpload, err := service.CreateUploadFor(ctx, twoOwner, 100, quotaLimit(25), "compressed.png", bytes.NewReader([]byte("compress me")))
	if err != nil {
		t.Fatal(err)
	}
	request := func(upload string, key string) SubmitRequest {
		return SubmitRequest{UploadID: upload, ProfileID: "test-profile", TargetFormat: "jpg", IdempotencyKey: key}
	}
	if _, _, err = service.SubmitFor(ctx, request(first.ID, "user-one-key-"+ownerSuffix[:8]+"-1"), oneOwner, Limits{MonthlyJobs: quotaLimit(1)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.SubmitFor(ctx, request(second.ID, "user-one-key-"+ownerSuffix[:8]+"-2"), oneOwner, Limits{MonthlyJobs: quotaLimit(1)}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("monthly job quota error=%v", err)
	}
	if _, _, err = service.SubmitFor(ctx, request(other.ID, "user-two-key-"+ownerSuffix[:8]+"-1"), twoOwner, Limits{MonthlyJobs: quotaLimit(1)}); err != nil {
		t.Fatal(err)
	}
	compressionRequest := request(compressionUpload.ID, "user-two-compress-"+ownerSuffix[:8])
	compressionRequest.ProfileID = compressionProfile.ID
	compressionRequest.Mode = "compress"
	compressionRequest.Options = map[string]string{"targetSizeMb": "2"}
	compressedJob, _, err := service.SubmitFor(ctx, compressionRequest, twoOwner, Limits{MonthlyJobs: quotaLimit(2)})
	if err != nil {
		t.Fatalf("compression job submission failed: %v", err)
	}
	if compressedJob.Mode != "compress" {
		t.Fatalf("compression job mode=%q", compressedJob.Mode)
	}
	wrongConversion := request(compressionUpload.ID, "cross-mode-convert-001")
	wrongConversion.ProfileID = compressionProfile.ID
	if _, _, err = service.SubmitFor(ctx, wrongConversion, twoOwner, Limits{MonthlyJobs: quotaLimit(3)}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("conversion with a compression profile should fail, got %v", err)
	}
	wrongCompression := request(compressionUpload.ID, "cross-mode-compress-01")
	wrongCompression.Mode = "compress"
	wrongCompression.Options = map[string]string{"targetSizeMb": "2"}
	if _, _, err = service.SubmitFor(ctx, wrongCompression, twoOwner, Limits{MonthlyJobs: quotaLimit(3)}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("compression with a conversion profile should fail, got %v", err)
	}
	if _, _, err = service.SubmitFor(ctx, request(other.ID, "user-three-key-"+ownerSuffix[:8]), threeOwner, Limits{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account upload access error=%v", err)
	}
	raceOwner := "quota-race-" + ownerSuffix
	uploadA, err := service.CreateUploadFor(ctx, raceOwner, 100, nil, "race-a.png", bytes.NewReader([]byte("a")))
	if err != nil {
		t.Fatal(err)
	}
	uploadB, err := service.CreateUploadFor(ctx, raceOwner, 100, nil, "race-b.png", bytes.NewReader([]byte("b")))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan error, 2)
	for i, upload := range []Upload{uploadA, uploadB} {
		go func(i int, u Upload) {
			defer wg.Done()
			limit := int64(1)
			_, _, e := service.SubmitFor(ctx, request(u.ID, fmt.Sprintf("race-submit-key-%s-%02d", ownerSuffix[:8], i)), raceOwner, Limits{MonthlyJobs: &limit})
			results <- e
		}(i, upload)
	}
	wg.Wait()
	close(results)
	accepted, quotaRejected := 0, 0
	for e := range results {
		if e == nil {
			accepted++
		} else if errors.Is(e, ErrQuotaExceeded) {
			quotaRejected++
		} else {
			t.Errorf("unexpected concurrent quota result: %v", e)
		}
	}
	if accepted != 1 || quotaRejected != 1 {
		t.Fatalf("concurrent quota accepted=%d rejected=%d, want one each", accepted, quotaRejected)
	}
	items, total, err := service.List(ctx, ListFilter{Limit: 10, OwnerID: oneOwner})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("user-one jobs=%d items=%d err=%v", total, len(items), err)
	}
	if _, err = service.GetFor(ctx, items[0].ID, twoOwner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account job detail access error=%v", err)
	}
}
