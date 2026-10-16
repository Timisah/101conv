package management

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/profile"
)

func testEngine(version string, caps ...string) engine.Definition {
	return engine.Definition{SchemaVersion: 1, ID: "ffmpeg", Version: version, Name: "FFmpeg", Platform: "linux-amd64", Capabilities: caps, Executables: map[string]string{"ffmpeg": "ffmpeg"}}
}
func testProfile(id string, r engine.VersionRange, caps ...string) profile.Definition {
	return profile.Definition{SchemaVersion: 1, ID: id, Version: "1.0.0", Name: id,
		Engines: []profile.EngineRequirement{{EngineID: "ffmpeg", Versions: r, Capabilities: caps}},
		Rules:   []profile.ConversionRule{{EngineID: "ffmpeg", Executable: "ffmpeg", Sources: []string{"mp4"}, Targets: []string{"webm"}, Arguments: []string{"{source}", "{target}"}}}}
}
func writeEngineFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("test executable"), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestSharedEnginePersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	src := t.TempDir()
	writeEngineFile(t, src, "ffmpeg")
	s, err := Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	installed, err := s.InstallEngine(ctx, testEngine("7.1.0", "video.decode", "video.encode"), src)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []profile.Definition{testProfile("mp4-webm", engine.VersionRange{Minimum: "7.0.0"}, "video.encode"), testProfile("mov-webm", engine.VersionRange{Minimum: "7.0.0", MaximumExclusive: "8.0.0"}, "video.encode")} {
		if _, err = s.InstallProfile(ctx, p, ""); err != nil {
			t.Fatal(err)
		}
	}
	resolvedA, err := s.ResolveProfile("mp4-webm")
	if err != nil {
		t.Fatal(err)
	}
	resolvedB, err := s.ResolveProfile("mov-webm")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolvedA) != 1 || len(resolvedB) != 1 || resolvedA[0].Root != installed.Installation.Root || resolvedA[0].Root != resolvedB[0].Root {
		t.Fatalf("profiles do not share engine installation: %v %v", resolvedA, resolvedB)
	}
	restarted, err := Open(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	engines, _ := restarted.ListEngines(ctx)
	profiles, _ := restarted.ListProfiles(ctx)
	if len(engines) != 1 || len(profiles) != 2 {
		t.Fatalf("state did not persist: engines=%d profiles=%d", len(engines), len(profiles))
	}
	again, err := restarted.ResolveProfile("mov-webm")
	if err != nil || again[0].Root != installed.Installation.Root {
		t.Fatalf("engine resolution after restart = %v, %v", again, err)
	}
}
func TestDependencyErrorsAndUninstallResolution(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	writeEngineFile(t, src, "ffmpeg")
	if _, err = s.InstallEngine(ctx, testEngine("4.0.0", "video.decode"), src); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InstallProfile(ctx, testProfile("new-version", engine.VersionRange{Minimum: "7.0.0"}), ""); err == nil {
		t.Fatal("expected incompatible engine version error")
	}
	if _, err = s.InstallProfile(ctx, testProfile("missing-cap", engine.VersionRange{}, "video.encode"), ""); err == nil {
		t.Fatal("expected missing capability error")
	}
	missingExecutable := testProfile("missing-executable", engine.VersionRange{}, "video.decode")
	missingExecutable.Rules[0].Executable = "ffprobe"
	if _, err = s.InstallProfile(ctx, missingExecutable, ""); err == nil {
		t.Fatal("expected missing executable binding error")
	}
	if _, err = s.InstallProfile(ctx, testProfile("active", engine.VersionRange{Minimum: "4.0.0"}, "video.decode"), ""); err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveEngine(ctx, "ffmpeg", "4.0.0", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected uninstall dependency conflict, got %v", err)
	}
	if err = s.RemoveEngine(ctx, "ffmpeg", "4.0.0", true); err != nil {
		t.Fatal(err)
	}
	profiles, _ := s.ListProfiles(ctx)
	if profiles[0].State.Enabled {
		t.Fatal("explicit dependency resolution should disable active profile")
	}
	if _, err = s.ResolveProfile("active"); err == nil {
		t.Fatal("disabled profile should report its now-missing dependency when resolution is requested")
	}
}
func TestFailedEngineInstallRollsBackFilesAndState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	src := t.TempDir()
	writeEngineFile(t, src, "ffmpeg")
	d := testEngine("1.0.0")
	d.Executables["missing"] = "missing"
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.InstallEngine(ctx, d, src); err == nil {
		t.Fatal("expected copy failure")
	}
	engines, err := s.ListEngines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(engines) != 0 {
		t.Fatalf("failed installation became usable: %+v", engines)
	}
	entries, err := os.ReadDir(filepath.Join(root, "engines", "ffmpeg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging directory was not cleaned up: %v", entries)
	}
}

func TestStartupRemovesIncompleteAndUnrecordedEngineDirectories(t *testing.T) {
	root := t.TempDir()
	engineRoot := filepath.Join(root, "engines", "ffmpeg")
	if err := os.MkdirAll(filepath.Join(engineRoot, ".staging-crash"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(engineRoot, "8.0.0"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(engineRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("orphaned installation dirs survived recovery: %v", entries)
	}
}

func TestRemoteArtifactInstallIsRejected(t *testing.T) {
	s, _ := Open(t.TempDir())
	d := testEngine("7.0.0")
	d.Artifact = &engine.Artifact{URL: "https://example.invalid/ffmpeg.tar.gz", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Format: "tar.gz"}
	if _, err := s.InstallEngine(context.Background(), d, t.TempDir()); err == nil {
		t.Fatal("unsigned remote engine installation must be rejected")
	}
}
func TestManualEngineCanBeRegisteredWithoutCopyingBinary(t *testing.T) {
	ctx := context.Background()
	s, _ := Open(t.TempDir())
	src := t.TempDir()
	writeEngineFile(t, src, "ffmpeg")
	d := testEngine("7.0.0", "video.encode")
	rec, err := s.RegisterManualEngine(ctx, d, map[string]string{"ffmpeg": filepath.Join(src, "ffmpeg")})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Installation.Managed || rec.Installation.ExecutablePaths["ffmpeg"] != filepath.Join(src, "ffmpeg") {
		t.Fatalf("manual engine registration lost paths: %+v", rec)
	}
}

func TestProfileLifecycleAndEngineUpdateKeepCompatibleVersions(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	writeEngineFile(t, source, "ffmpeg")
	if _, err = s.InstallEngine(ctx, testEngine("7.0.0", "video.encode"), source); err != nil {
		t.Fatal(err)
	}
	p := testProfile("stable-profile", engine.VersionRange{Minimum: "7.0.0", MaximumExclusive: "8.0.0"}, "video.encode")
	if _, err = s.InstallProfile(ctx, p, "local"); err != nil {
		t.Fatal(err)
	}
	updated := testProfile("stable-profile", engine.VersionRange{Minimum: "7.0.0", MaximumExclusive: "8.0.0"}, "video.encode")
	updated.Version = "1.1.0"
	updated.Name = "Updated"
	if _, err = s.UpdateProfile(ctx, updated, "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetProfileEnabled(ctx, "stable-profile", false); err != nil {
		t.Fatal(err)
	}
	profiles, _ := s.ListProfiles(ctx)
	if profiles[0].State.Enabled || profiles[0].State.Version != "1.1.0" {
		t.Fatalf("profile update/disable not persisted: %+v", profiles[0])
	}
	if _, err = s.SetProfileEnabled(ctx, "stable-profile", true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateEngine(ctx, "ffmpeg", testEngine("8.0.0", "video.encode"), source); err != nil {
		t.Fatal(err)
	}
	resolved, err := s.ResolveProfile("stable-profile")
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].Version != "7.0.0" {
		t.Fatalf("profile should remain on its compatible engine release: %+v", resolved)
	}
	if err = s.RemoveProfile(ctx, "stable-profile"); err != nil {
		t.Fatal(err)
	}
	profiles, _ = s.ListProfiles(ctx)
	if len(profiles) != 0 {
		t.Fatalf("profile uninstall left a record: %+v", profiles)
	}
}
