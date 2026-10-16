package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
)

func TestPostgresOwnerIsolationAndQuotas(t *testing.T) {
	dsn := os.Getenv("101CONV_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set 101CONV_TEST_DATABASE_URL to run PostgreSQL authorization integration tests")
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
	first, err := service.CreateUploadFor(ctx, "user-one", 100, 25, "first.png", bytes.NewReader(bytes.Repeat([]byte("a"), 20)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.CreateUploadFor(ctx, "user-one", 100, 25, "too-much.png", bytes.NewReader(bytes.Repeat([]byte("b"), 6))); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("monthly input quota error=%v", err)
	}
	second, err := service.CreateUploadFor(ctx, "user-one", 100, 25, "second.png", bytes.NewReader([]byte("small")))
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateUploadFor(ctx, "user-two", 100, 25, "other.png", bytes.NewReader([]byte("private")))
	if err != nil {
		t.Fatal(err)
	}
	compressionUpload, err := service.CreateUploadFor(ctx, "user-two", 100, 25, "compressed.png", bytes.NewReader([]byte("compress me")))
	if err != nil {
		t.Fatal(err)
	}
	request := func(upload string, key string) SubmitRequest {
		return SubmitRequest{UploadID: upload, ProfileID: "test-profile", TargetFormat: "jpg", IdempotencyKey: key}
	}
	if _, _, err = service.SubmitFor(ctx, request(first.ID, "user-one-key-0001"), "user-one", Limits{MonthlyJobs: 1}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.SubmitFor(ctx, request(second.ID, "user-one-key-0002"), "user-one", Limits{MonthlyJobs: 1}); !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("monthly job quota error=%v", err)
	}
	if _, _, err = service.SubmitFor(ctx, request(other.ID, "user-two-key-0001"), "user-two", Limits{MonthlyJobs: 1}); err != nil {
		t.Fatal(err)
	}
	compressionRequest := request(compressionUpload.ID, "user-two-compress-001")
	compressionRequest.ProfileID = compressionProfile.ID
	compressionRequest.Mode = "compress"
	compressionRequest.Options = map[string]string{"targetSizeMb": "2"}
	compressedJob, _, err := service.SubmitFor(ctx, compressionRequest, "user-two", Limits{MonthlyJobs: 2})
	if err != nil {
		t.Fatalf("compression job submission failed: %v", err)
	}
	if compressedJob.Mode != "compress" {
		t.Fatalf("compression job mode=%q", compressedJob.Mode)
	}
	wrongConversion := request(compressionUpload.ID, "cross-mode-convert-001")
	wrongConversion.ProfileID = compressionProfile.ID
	if _, _, err = service.SubmitFor(ctx, wrongConversion, "user-two", Limits{MonthlyJobs: 3}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("conversion with a compression profile should fail, got %v", err)
	}
	wrongCompression := request(compressionUpload.ID, "cross-mode-compress-01")
	wrongCompression.Mode = "compress"
	wrongCompression.Options = map[string]string{"targetSizeMb": "2"}
	if _, _, err = service.SubmitFor(ctx, wrongCompression, "user-two", Limits{MonthlyJobs: 3}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("compression with a conversion profile should fail, got %v", err)
	}
	if _, _, err = service.SubmitFor(ctx, request(other.ID, "user-three-key-0001"), "user-three", Limits{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account upload access error=%v", err)
	}
	items, total, err := service.List(ctx, ListFilter{Limit: 10, OwnerID: "user-one"})
	if err != nil || total != 1 || len(items) != 1 {
		t.Fatalf("user-one jobs=%d items=%d err=%v", total, len(items), err)
	}
	if _, err = service.GetFor(ctx, items[0].ID, "user-two"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-account job detail access error=%v", err)
	}
}
