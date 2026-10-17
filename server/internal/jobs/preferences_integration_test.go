package jobs

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
)

func TestProfilePreferencePrecedenceAndConcurrentUpdates(t *testing.T) {
	dsn := os.Getenv("101CONV_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set 101CONV_TEST_DATABASE_URL to run PostgreSQL profile preference integration tests")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
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
	if _, err = manager.InstallEngine(ctx, engine.Definition{SchemaVersion: 1, ID: "preferences-engine", Version: "1.0.0", Name: "Preferences Engine", Platform: runtime.GOOS + "-" + runtime.GOARCH, Capabilities: []string{"convert"}, Executables: map[string]string{"tool": "tool"}}, imports); err != nil {
		t.Fatal(err)
	}
	makeConversion := func(id, source, target string) profile.Definition {
		return profile.Definition{SchemaVersion: 1, Type: profile.TypeConversion, ID: id, Version: "1.0.0", Name: id, Engines: []profile.EngineRequirement{{EngineID: "preferences-engine", Versions: engine.VersionRange{Minimum: "1.0.0"}, Capabilities: []string{"convert"}}}, Rules: []profile.ConversionRule{{EngineID: "preferences-engine", Executable: "tool", Sources: []string{source}, Targets: []string{target}, Arguments: []string{"{source}", "{target}"}}}}
	}
	for _, id := range []string{"a-convert", "b-convert", "z-incompatible"} {
		target := "jpg"
		if id == "z-incompatible" {
			target = "webp"
		}
		if _, err = manager.InstallProfile(ctx, makeConversion(id, "png", target), ""); err != nil {
			t.Fatal(err)
		}
	}
	compression := profile.Definition{SchemaVersion: 1, Type: profile.TypeCompression, ID: "c-compress", Version: "2.0.0", Name: "Compression", Engines: []profile.EngineRequirement{{EngineID: "preferences-engine", Versions: engine.VersionRange{Minimum: "1.0.0"}, Capabilities: []string{"convert"}}}, Compression: &profile.CompressionConfig{Options: []profile.OptionDefinition{{Key: "quality", Label: "Quality", Type: "select", Default: "balanced", Choices: []string{"balanced", "small"}}}}, Rules: []profile.ConversionRule{{EngineID: "preferences-engine", Executable: "tool", Sources: []string{"jpg"}, Targets: []string{"jpg"}, Arguments: []string{"{source}", "{compression.args}", "{target}"}, CompressionArguments: []string{"-q", "{option:quality}"}}}}
	if _, err = manager.InstallProfile(ctx, compression, ""); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(db, manager, filepath.Join(root, "uploads"), filepath.Join(root, "results"))
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	suffix, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	owner := "preference-user-" + suffix[:8]
	if err = service.SetProfilePreference(ctx, owner, "convert", "png", "jpg", "a-convert", false); err != nil {
		t.Fatal(err)
	}
	if err = service.SetProfilePreference(ctx, "admin", "convert", "png", "jpg", "b-convert", true); err != nil {
		t.Fatal(err)
	}
	if got, from, err := service.ResolvePreferredProfile(ctx, owner, "convert", "PNG", ".JPG"); err != nil || got != "a-convert" || from != "personal preference" {
		t.Fatalf("personal precedence = %q %q %v", got, from, err)
	}
	if err = service.DeleteProfilePreference(ctx, owner, "convert", "png", "jpg", false); err != nil {
		t.Fatal(err)
	}
	if got, from, err := service.ResolvePreferredProfile(ctx, owner, "convert", "png", "jpg"); err != nil || got != "b-convert" || from != "administrator default" {
		t.Fatalf("admin precedence = %q %q %v", got, from, err)
	}
	if _, err = manager.SetProfileEnabled(ctx, "b-convert", false); err != nil {
		t.Fatal(err)
	}
	if err = service.SetProfilePreference(ctx, owner, "convert", "png", "jpg", "b-convert", false); err == nil {
		t.Fatal("disabled profile preference should be rejected")
	}
	if got, from, err := service.ResolvePreferredProfile(ctx, owner, "convert", "png", "jpg"); err != nil || got != "a-convert" || from != "automatic fallback" {
		t.Fatalf("disabled default fallback = %q %q %v", got, from, err)
	}
	rows, err := service.ListProfilePreferences(ctx, owner, "convert")
	if err != nil {
		t.Fatal(err)
	}
	foundStale := false
	for _, row := range rows {
		if row.SourceFormat == "png" && row.TargetFormat == "jpg" {
			foundStale = row.GlobalProfileID == "b-convert" && !row.GlobalAvailable && row.EffectiveProfile == "a-convert"
		}
	}
	if !foundStale {
		t.Fatalf("disabled saved default was lost or selected: %+v", rows)
	}
	if err = service.SetProfilePreference(ctx, owner, "compress", "jpg", "jpg", "c-compress", false); err != nil {
		t.Fatalf("same-format compression preference: %v", err)
	}
	if err = service.SetProfilePreference(ctx, owner, "convert", "png", "jpg", "z-incompatible", false); err == nil {
		t.Fatal("incompatible preference should be rejected")
	}
	if _, err = manager.SetProfileEnabled(ctx, "b-convert", true); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "a-convert"
			if i%2 == 1 {
				id = "b-convert"
			}
			errs <- service.SetProfilePreference(ctx, owner, "convert", "png", "jpg", id, false)
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("concurrent upsert: %v", e)
		}
	}
	chosen, err := service.storedPreference(ctx, "user", owner, "convert", "png", "jpg")
	if err != nil || chosen != "a-convert" && chosen != "b-convert" {
		t.Fatalf("concurrent preference final value=%q error=%v", chosen, err)
	}
	conversionUpload, err := service.CreateUploadFor(ctx, owner, 100, nil, "mixed-source.png", strings.NewReader("png"))
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := service.SubmitFor(ctx, SubmitRequest{UploadID: conversionUpload.ID, TargetFormat: "jpg", IdempotencyKey: owner + "-convert-key"}, owner, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if job.ProfileID != chosen || job.ProfileVer != "1.0.0" {
		t.Fatalf("automatic conversion resolved profile/version %s@%s, expected %s@1.0.0", job.ProfileID, job.ProfileVer, chosen)
	}
	explicitID := "a-convert"
	if explicitID == chosen {
		explicitID = "b-convert"
	}
	explicitUpload, err := service.CreateUploadFor(ctx, owner, 100, nil, "explicit-source.png", strings.NewReader("png"))
	if err != nil {
		t.Fatal(err)
	}
	explicitJob, _, err := service.SubmitFor(ctx, SubmitRequest{UploadID: explicitUpload.ID, ProfileID: explicitID, TargetFormat: "jpg", IdempotencyKey: owner + "-explicit-key"}, owner, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if explicitJob.ProfileID != explicitID {
		t.Fatalf("explicit profile selection was not honored: got %q want %q", explicitJob.ProfileID, explicitID)
	}
	compressionUpload, err := service.CreateUploadFor(ctx, owner, 100, nil, "same-format.jpg", strings.NewReader("jpg"))
	if err != nil {
		t.Fatal(err)
	}
	compressionJob, _, err := service.SubmitFor(ctx, SubmitRequest{UploadID: compressionUpload.ID, TargetFormat: "jpg", Mode: "compress", IdempotencyKey: owner + "-compress-key"}, owner, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if compressionJob.ProfileID != "c-compress" || compressionJob.ProfileVer != "2.0.0" || compressionJob.SourceFormat != compressionJob.TargetFormat {
		t.Fatalf("automatic same-format compression resolution = %+v", compressionJob)
	}
}
