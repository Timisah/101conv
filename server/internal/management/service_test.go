package management

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("test executable"), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestSharedEnginePersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	src := filepath.Join(root, "imports")
	writeEngineFile(t, src, "ffmpeg")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
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
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
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
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	src := filepath.Join(root, "imports")
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
	if _, err = s.InstallEngine(ctx, testEngine("5.0.0", "video.decode"), src); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InstallProfile(ctx, testProfile("active-wide", engine.VersionRange{Minimum: "4.0.0"}, "video.decode"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InstallProfile(ctx, testProfile("active-narrow", engine.VersionRange{Minimum: "4.0.0", MaximumExclusive: "5.0.0"}, "video.decode"), ""); err != nil {
		t.Fatal(err)
	}
	if err = s.RemoveEngine(ctx, "ffmpeg", "4.0.0", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected uninstall dependency conflict for narrow profile, got %v", err)
	}
	if err = s.RemoveEngine(ctx, "ffmpeg", "4.0.0", true); err != nil {
		t.Fatal(err)
	}
	profiles, _ := s.ListProfiles(ctx)
	byID := map[string]ProfileRecord{}
	for _, p := range profiles {
		byID[p.Definition.ID] = p
	}
	if !byID["active-wide"].State.Enabled || byID["active-wide"].State.ResolvedEngines["ffmpeg"] != "5.0.0" {
		t.Fatalf("compatible profile should stay enabled on remaining engine: %+v", byID["active-wide"])
	}
	if byID["active-narrow"].State.Enabled {
		t.Fatal("explicit resolution should disable only dependent profile without alternative")
	}
	resolved, err := s.ResolveProfile("active-wide")
	if err != nil || resolved[0].Version != "5.0.0" {
		t.Fatalf("profile did not move to remaining installation: %+v %v", resolved, err)
	}
}

func TestFailedEngineInstallRollsBackFilesAndState(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	src := filepath.Join(root, "imports")
	writeEngineFile(t, src, "ffmpeg")
	d := testEngine("1.0.0")
	d.Executables["missing"] = "missing"
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
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

func TestEngineInstallReportsStagingRollbackFailureAndRecoversOnRestart(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "imports")
	writeEngineFile(t, src, "ffmpeg")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	d := testEngine("1.0.0")
	d.Executables["missing"] = "missing"
	removeAll := s.removeAll
	s.removeAll = func(path string) error {
		if strings.Contains(path, ".staging-") {
			return errors.New("simulated staging cleanup failure")
		}
		return removeAll(path)
	}
	_, err = s.InstallEngine(context.Background(), d, src)
	var transaction *FileTransactionError
	if !errors.As(err, &transaction) || !strings.Contains(err.Error(), "simulated staging cleanup failure") {
		t.Fatalf("staging rollback failure hidden: %v", err)
	}
	s.removeAll = removeAll
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	engines, _ := restarted.ListEngines(context.Background())
	entries, err := os.ReadDir(filepath.Join(root, "engines", "ffmpeg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(engines) != 0 || len(entries) != 0 {
		t.Fatalf("restart did not clean failed installation: engines=%d entries=%v", len(engines), entries)
	}
}

func TestInterruptedEngineRemovalRestoresCommittedInstallationOnRestart(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "imports")
	writeEngineFile(t, src, "ffmpeg")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	installed, err := s.InstallEngine(context.Background(), testEngine("1.0.0", "video.encode"), src)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	final := installed.Installation.Root
	tombstone := filepath.Join(filepath.Dir(final), ".removing-1.0.0")
	if err = os.Rename(final, tombstone); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err = os.Stat(final); err != nil {
		t.Fatalf("pending removal was not rolled back on restart: %v", err)
	}
	if _, err = os.Stat(tombstone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tombstone remained after recovery: %v", err)
	}
}

func TestEngineRemovalCleanupFailureIsReportedAndRecovered(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "imports")
	writeEngineFile(t, src, "ffmpeg")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.InstallEngine(context.Background(), testEngine("1.0.0"), src); err != nil {
		t.Fatal(err)
	}
	removeAll := s.removeAll
	s.removeAll = func(path string) error {
		if strings.Contains(path, ".removing-") {
			return errors.New("simulated removal cleanup failure")
		}
		return removeAll(path)
	}
	err = s.RemoveEngine(context.Background(), "ffmpeg", "1.0.0", false)
	var transaction *FileTransactionError
	if !errors.As(err, &transaction) || !strings.Contains(err.Error(), "simulated removal cleanup failure") {
		t.Fatalf("removal cleanup failure hidden: %v", err)
	}
	engines, _ := s.ListEngines(context.Background())
	if len(engines) != 0 {
		t.Fatalf("committed engine removal retained state: %d", len(engines))
	}
	s.removeAll = removeAll
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	entries, err := os.ReadDir(filepath.Join(root, "engines", "ffmpeg"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("restart did not remove committed tombstone: %v", entries)
	}
}

func TestVerifiedProfileBundleRollsBackStagedEngineOnCopyFailure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	stage, cleanup, err := s.CreateVerifiedStaging()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err = os.WriteFile(filepath.Join(stage, "ffmpeg"), []byte("verified"), 0700); err != nil {
		t.Fatal(err)
	}
	d := testEngine("7.1.0", "video.encode")
	d.Executables["missing"] = "missing"
	d.Artifact = &engine.Artifact{URL: "packages/ffmpeg.tar.gz", SHA256: strings.Repeat("a", 64), Format: "tar.gz"}
	p := testProfile("bundle-fails", engine.VersionRange{Minimum: "7.0.0", MaximumExclusive: "8.0.0"}, "video.encode")
	digest, err := s.SnapshotDigest()
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.InstallVerifiedProfile(p, "trusted", []VerifiedEnginePackage{{Definition: d, SourceDir: stage, ArtifactHash: d.Artifact.SHA256, RepositoryID: "trusted"}}, map[string]string{"ffmpeg": "7.1.0"}, digest, false)
	if err == nil {
		t.Fatal("expected package copy failure")
	}
	engines, _ := s.ListEngines(ctx)
	profiles, _ := s.ListProfiles(ctx)
	if len(engines) != 0 || len(profiles) != 0 {
		t.Fatalf("failed transaction committed state: engines=%d profiles=%d", len(engines), len(profiles))
	}
	versionPath := filepath.Join(root, "engines", "ffmpeg", "7.1.0")
	if _, err = os.Stat(versionPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed transaction left installed files: %v", err)
	}
}

func TestVerifiedProfileReportsRollbackFailure(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	stage, cleanup, err := s.CreateVerifiedStaging()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err = os.WriteFile(filepath.Join(stage, "ffmpeg"), []byte("verified"), 0700); err != nil {
		t.Fatal(err)
	}
	d := testEngine("7.1.0", "video.encode")
	d.Executables["missing"] = "missing"
	d.Artifact = &engine.Artifact{URL: "packages/ffmpeg.tar.gz", SHA256: strings.Repeat("b", 64), Format: "tar.gz"}
	p := testProfile("rollback-report", engine.VersionRange{Minimum: "7.0.0", MaximumExclusive: "8.0.0"}, "video.encode")
	digest, err := s.SnapshotDigest()
	if err != nil {
		t.Fatal(err)
	}
	removeAll := s.removeAll
	s.removeAll = func(path string) error {
		if strings.Contains(path, ".staging-") {
			return errors.New("simulated remove failure")
		}
		return removeAll(path)
	}
	_, err = s.InstallVerifiedProfile(p, "trusted", []VerifiedEnginePackage{{Definition: d, SourceDir: stage, ArtifactHash: d.Artifact.SHA256, RepositoryID: "trusted"}}, map[string]string{"ffmpeg": "7.1.0"}, digest, false)
	var transaction *FileTransactionError
	if !errors.As(err, &transaction) || !strings.Contains(err.Error(), "rollback incomplete") || !strings.Contains(err.Error(), "simulated remove failure") {
		t.Fatalf("rollback failure was hidden: %v", err)
	}
	engines, _ := s.ListEngines(context.Background())
	profiles, _ := s.ListProfiles(context.Background())
	if len(engines) != 0 || len(profiles) != 0 {
		t.Fatalf("failed transaction committed state: engines=%d profiles=%d", len(engines), len(profiles))
	}
	s.removeAll = removeAll
	entries, err := os.ReadDir(filepath.Join(root, "engines", "ffmpeg"))
	if err == nil && len(entries) > 0 {
		for _, entry := range entries {
			if err = os.RemoveAll(filepath.Join(root, "engines", "ffmpeg", entry.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestStartupRemovesIncompleteAndUnrecordedEngineDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".repository-staging", "interrupted"), 0700); err != nil {
		t.Fatal(err)
	}
	engineRoot := filepath.Join(root, "engines", "ffmpeg")
	if err := os.MkdirAll(filepath.Join(engineRoot, ".staging-crash"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(engineRoot, "8.0.0"), 0700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entries, err := os.ReadDir(engineRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("orphaned installation dirs survived recovery: %v", entries)
	}
	if _, err = os.Stat(filepath.Join(root, ".repository-staging")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("interrupted repository install staging survived restart")
	}
}

func TestRemoteArtifactInstallIsRejected(t *testing.T) {
	root := t.TempDir()
	s, _ := Open(root)
	t.Cleanup(func() { _ = s.Close() })
	d := testEngine("7.0.0")
	d.Artifact = &engine.Artifact{URL: "https://example.invalid/ffmpeg.tar.gz", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Format: "tar.gz"}
	if _, err := s.InstallEngine(context.Background(), d, t.TempDir()); err == nil {
		t.Fatal("unsigned remote engine installation must be rejected")
	}
}
func TestManualEngineCanBeRegisteredWithoutCopyingBinary(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, _ := Open(root)
	t.Cleanup(func() { _ = s.Close() })
	src := filepath.Join(root, "imports")
	writeEngineFile(t, src, "ffmpeg")
	d := testEngine("7.0.0", "video.encode")
	link := filepath.Join(src, "ffmpeg-link")
	if err := os.Symlink(filepath.Join(src, "ffmpeg"), link); err != nil {
		t.Fatal(err)
	}
	rec, err := s.RegisterManualEngine(ctx, d, map[string]string{"ffmpeg": link})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Installation.Managed || rec.Installation.ExecutablePaths["ffmpeg"] != filepath.Join(src, "ffmpeg") {
		t.Fatalf("manual engine registration lost paths: %+v", rec)
	}
}

func TestProfileLifecycleAndEngineUpdateKeepCompatibleVersions(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	source := filepath.Join(root, "imports")
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
	if _, err = s.UpdateEngine(ctx, "ffmpeg", testEngine("7.5.0", "video.encode"), source); err != nil {
		t.Fatal(err)
	}
	resolved, err = s.ResolveProfile("stable-profile")
	if err != nil || resolved[0].Version != "7.0.0" {
		t.Fatalf("installing a newer compatible engine changed a pinned profile: %+v %v", resolved, err)
	}
	if _, err = s.SelectProfileEngine(ctx, "stable-profile", "ffmpeg", "7.5.0"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SelectProfileEngine(ctx, "stable-profile", "ffmpeg", "7.0.0"); err != nil {
		t.Fatal("explicit rollback selection failed:", err)
	}
	if err = s.RemoveProfile(ctx, "stable-profile"); err != nil {
		t.Fatal(err)
	}
	profiles, _ = s.ListProfiles(ctx)
	if len(profiles) != 0 {
		t.Fatalf("profile uninstall left a record: %+v", profiles)
	}
}

func TestStorageIsSingleWriterAndManagedPathsSurviveMove(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "imports")
	writeEngineFile(t, src, "ffmpeg")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Open(root); err == nil {
		t.Fatal("expected second process to be rejected")
	}
	if _, err = s.InstallEngine(context.Background(), testEngine("1.0.0"), src); err != nil {
		t.Fatal(err)
	}
	stateBytes, err := os.ReadFile(filepath.Join(root, "management.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateBytes), root) {
		t.Fatal("managed state persisted an absolute host path")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	moved := root + "-moved"
	if err = os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(moved)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	records, err := reopened.ListEngines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := records[0].Installation.ExecutablePaths["ffmpeg"]
	if !filepath.IsAbs(p) || !strings.HasPrefix(p, filepath.Join(moved, "engines")) {
		t.Fatalf("managed path was not relocated portably: %s", p)
	}
}
