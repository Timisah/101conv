package repositorymanager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
)

type fixture struct {
	root []byte
	keys map[string]ed25519.PrivateKey
	repo string
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	keys := map[string]ed25519.PrivateKey{}
	root := metadata.Root(time.Now().UTC().Add(365 * 24 * time.Hour))
	root.Signed.ConsistentSnapshot = false
	for _, role := range []string{metadata.ROOT, metadata.TARGETS, metadata.SNAPSHOT, metadata.TIMESTAMP} {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		keys[role] = priv
		pub, err := metadata.KeyFromPublicKey(priv.Public())
		if err != nil {
			t.Fatal(err)
		}
		if err = root.Signed.AddKey(pub, role); err != nil {
			t.Fatal(err)
		}
	}
	sign(t, root, keys[metadata.ROOT])
	rootBytes, err := root.ToBytes(true)
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	return fixture{root: rootBytes, keys: keys, repo: repo}
}
func sign[T metadata.Roles](t *testing.T, m *metadata.Metadata[T], key ed25519.PrivateKey) {
	t.Helper()
	s, err := signature.LoadSigner(key, crypto.Hash(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Sign(s); err != nil {
		t.Fatal(err)
	}
}
func (f fixture) write(t *testing.T, targets map[string][]byte, version int64, expires time.Time) {
	t.Helper()
	md := filepath.Join(f.repo, "metadata")
	td := filepath.Join(md, "targets")
	if err := os.MkdirAll(td, 0700); err != nil {
		t.Fatal(err)
	}
	targetMeta := metadata.Targets(expires)
	targetMeta.Signed.Version = version
	for name, data := range targets {
		info, err := metadata.TargetFile().FromBytes(name, data, "sha256")
		if err != nil {
			t.Fatal(err)
		}
		targetMeta.Signed.Targets[name] = info
		path := filepath.Join(td, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	sign(t, targetMeta, f.keys[metadata.TARGETS])
	targetBytes, err := targetMeta.ToBytes(true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := metadata.Snapshot(expires)
	snapshot.Signed.Version = version
	snapshot.Signed.Meta["targets.json"] = metaFile(targetBytes, version)
	sign(t, snapshot, f.keys[metadata.SNAPSHOT])
	snapshotBytes, err := snapshot.ToBytes(true)
	if err != nil {
		t.Fatal(err)
	}
	timestamp := metadata.Timestamp(expires)
	timestamp.Signed.Version = version
	timestamp.Signed.Meta["snapshot.json"] = metaFile(snapshotBytes, version)
	sign(t, timestamp, f.keys[metadata.TIMESTAMP])
	timestampBytes, err := timestamp.ToBytes(true)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(md, "root.json"), f.root, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(md, "targets.json"), targetBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(md, "snapshot.json"), snapshotBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(md, "timestamp.json"), timestampBytes, 0600); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) rotatedRoot(t *testing.T) []byte {
	t.Helper()
	root := metadata.Root(time.Now().UTC().Add(365 * 24 * time.Hour))
	root.Signed.Version = 2
	root.Signed.ConsistentSnapshot = false
	for role, oldRole := range mustRoot(t, f.root).Signed.Roles {
		if role == metadata.ROOT {
			continue
		}
		copied := *oldRole
		copied.KeyIDs = append([]string(nil), oldRole.KeyIDs...)
		root.Signed.Roles[role] = &copied
	}
	oldRoot, err := metadata.Root().FromBytes(f.root)
	if err != nil {
		t.Fatal(err)
	}
	for id, key := range oldRoot.Signed.Keys {
		if id == "" {
			continue
		}
		root.Signed.Keys[id] = key
	}
	_, newPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	newPublic, err := metadata.KeyFromPublicKey(newPrivate.Public())
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := newPublic.ID()
	if err != nil {
		t.Fatal(err)
	}
	root.Signed.Keys[keyID] = newPublic
	root.Signed.Roles[metadata.ROOT] = &metadata.Role{KeyIDs: []string{keyID}, Threshold: 1}
	oldPrivate := f.keys[metadata.ROOT]
	sign(t, root, oldPrivate)
	sign(t, root, newPrivate)
	data, err := root.ToBytes(true)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustRoot(t *testing.T, raw []byte) *metadata.Metadata[metadata.RootType] {
	t.Helper()
	root, err := metadata.Root().FromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func metaFile(data []byte, ver int64) *metadata.MetaFiles {
	sum := sha256.Sum256(data)
	return &metadata.MetaFiles{Version: ver, Length: int64(len(data)), Hashes: metadata.Hashes{"sha256": metadata.HexBytes(sum[:])}}
}
func makeCatalog(t *testing.T, _ []byte) map[string][]byte {
	return makeCatalogVersion(t, "7.1.0", []byte("fake converter; must never run"))
}

func makeCatalogVersion(t *testing.T, engineVersion string, payload []byte) map[string][]byte {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "ffmpeg", Mode: 0700, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	artifact := archive.Bytes()
	sum := sha256.Sum256(artifact)
	eng := engine.Definition{SchemaVersion: 1, ID: "ffmpeg", Version: engineVersion, Name: "FFmpeg", Platform: "linux-amd64", Capabilities: []string{"video.encode"}, Executables: map[string]string{"ffmpeg": "ffmpeg"}, Artifact: &engine.Artifact{URL: "packages/ffmpeg.tar.gz", SHA256: hex.EncodeToString(sum[:]), Format: "tar.gz"}}
	p := profile.Definition{SchemaVersion: 1, ID: "video-one", Version: "1.0.0", Name: "Video One", License: "MIT", Engines: []profile.EngineRequirement{{EngineID: "ffmpeg", Versions: engine.VersionRange{Minimum: "7.0.0", MaximumExclusive: "8.0.0"}, Capabilities: []string{"video.encode"}}}, Rules: []profile.ConversionRule{{EngineID: "ffmpeg", Executable: "ffmpeg", Sources: []string{"mp4"}, Targets: []string{"webm"}, Arguments: []string{"{source}", "{target}"}}}}
	p2 := p
	p2.ID = "video-two"
	p2.Name = "Video Two"
	p2.Rules = append([]profile.ConversionRule(nil), p.Rules...)
	engineBytes, _ := json.Marshal(EngineManifest{Engine: eng})
	profile1, _ := json.Marshal(ProfileManifest{Profile: p})
	profile2, _ := json.Marshal(ProfileManifest{Profile: p2})
	index, _ := json.Marshal(CatalogIndex{SchemaVersion: 1, Engines: []EngineEntry{{ID: eng.ID, Version: eng.Version, Name: eng.Name, ManifestTarget: "engines/ffmpeg.json"}}, Profiles: []ProfileEntry{{ID: p.ID, Version: p.Version, Name: p.Name, ManifestTarget: "profiles/video-one.json"}, {ID: p2.ID, Version: p2.Version, Name: p2.Name, ManifestTarget: "profiles/video-two.json"}}})
	return map[string][]byte{"catalog/index.json": index, "engines/ffmpeg.json": engineBytes, "profiles/video-one.json": profile1, "profiles/video-two.json": profile2, "packages/ffmpeg.tar.gz": artifact}
}

func TestMetadataChangeDuringArtifactRetrievalUsesOriginalVerifiedDigest(t *testing.T) {
	f, repos, _ := setup(t)
	ctx := context.Background()
	source, err := repos.getSource("trusted-test")
	if err != nil {
		t.Fatal(err)
	}
	repos.tufMu.Lock()
	view, err := repos.loadViewLocked(ctx, source)
	repos.tufMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}

	// Change the local repository after the verified updater snapshot has been
	// loaded. The updater must continue enforcing the old signed target digest.
	f.write(t, makeCatalogVersion(t, "7.2.0", []byte("substituted package")), 2, time.Now().UTC().Add(time.Hour))
	if _, err = downloadVerified(view.updater, "packages/ffmpeg.tar.gz", maxPackageBytes); err == nil {
		t.Fatal("artifact retrieval accepted bytes changed after the verified snapshot")
	}
}
func addLocal(t *testing.T, m *Manager, f fixture) Source {
	t.Helper()
	sum := sha256.Sum256(f.root)
	returnAdd, err := m.Add(AddRequest{ID: "trusted-test", Name: "Trusted Test", URL: "file://" + f.repo, Kind: KindLocal, TrustedRoot: base64.StdEncoding.EncodeToString(f.root), Fingerprint: hex.EncodeToString(sum[:]), Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	return returnAdd
}
func setup(t *testing.T) (fixture, *Manager, *management.Service) {
	t.Helper()
	f := newFixture(t)
	f.write(t, makeCatalog(t, nil), 1, time.Now().UTC().Add(time.Hour))
	data := t.TempDir()
	eng, err := management.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	repos, err := Open(data, "0.1.0", eng)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repos.Close() })
	addLocal(t, repos, f)
	return f, repos, eng
}

func TestBrowsePreviewInstallSharedEngineRestartAndRollbackSelection(t *testing.T) {
	f, repos, eng := setup(t)
	ctx := context.Background()
	catalog, err := repos.Browse(ctx, "trusted-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Profiles) != 2 || len(catalog.Engines) != 1 {
		t.Fatalf("unexpected catalog: %+v", catalog)
	}
	preview, err := repos.Preview(ctx, "trusted-test", "video-one")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Trust != TrustPinned || preview.License != "MIT" || len(preview.Dependencies) != 1 || preview.Dependencies[0].Action != "install engine" {
		t.Fatalf("incomplete preview: %+v", preview)
	}
	if _, err = repos.InstallProfile(ctx, "trusted-test", "video-one", false); err != nil {
		t.Fatal(err)
	}
	preview, err = repos.Preview(ctx, "trusted-test", "video-two")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Dependencies[0].Action != "reuse installed engine" {
		t.Fatalf("second profile should reuse shared engine: %+v", preview.Dependencies)
	}
	if _, err = repos.InstallProfile(ctx, "trusted-test", "video-two", false); err != nil {
		t.Fatal(err)
	}
	engines, _ := eng.ListEngines(ctx)
	if len(engines) != 1 {
		t.Fatalf("engine was downloaded/installed more than once: %d", len(engines))
	}
	uncommittedPreview, err := repos.CreateInstallPlan(ctx, "trusted-test", "video-one", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = eng.Close(); err != nil {
		t.Fatal(err)
	}
	if err = repos.Close(); err != nil {
		t.Fatal(err)
	}
	eng2, err := management.Open(engRoot(eng))
	if err != nil {
		t.Fatal(err)
	}
	defer eng2.Close()
	repos2, err := Open(engRoot(eng), "0.1.0", eng2)
	if err != nil {
		t.Fatal(err)
	}
	defer repos2.Close()
	if _, err = repos2.InstallPlan(ctx, uncommittedPreview.PlanID); !errors.Is(err, ErrPlanExpired) {
		t.Fatalf("uncommitted preview unexpectedly survived restart: %v", err)
	}
	profiles, _ := eng2.ListProfiles(ctx)
	installed, _ := eng2.ListEngines(ctx)
	if len(profiles) != 2 || len(installed) != 1 {
		t.Fatalf("restart state missing: %d profiles, %d engines", len(profiles), len(installed))
	}
	a, _ := eng2.ResolveProfile("video-one")
	b, _ := eng2.ResolveProfile("video-two")
	if a[0].Root != b[0].Root {
		t.Fatalf("profiles don't share persisted engine: %s != %s", a[0].Root, b[0].Root)
	}
	_ = f
}

func TestImmutablePlanRejectsRepositoryAndInstalledStateChanges(t *testing.T) {
	f, repos, eng := setup(t)
	ctx := context.Background()
	preview, err := repos.CreateInstallPlan(ctx, "trusted-test", "video-one", false)
	if err != nil {
		t.Fatal(err)
	}
	// A new signed snapshot changes the selected release/catalog digest. The old
	// preview must not silently resolve against the new metadata.
	f.write(t, makeCatalog(t, nil), 2, time.Now().UTC().Add(time.Hour))
	if _, err = repos.InstallPlan(ctx, preview.PlanID); !errors.Is(err, ErrStalePlan) {
		t.Fatalf("expected stale catalog plan, got %v", err)
	}
	installed, _ := eng.ListEngines(ctx)
	if len(installed) != 0 {
		t.Fatalf("stale plan installed engines: %+v", installed)
	}

	// A second preview also becomes stale if local management state changes after
	// dependency selection, even when repository metadata is unchanged.
	preview, err = repos.CreateInstallPlan(ctx, "trusted-test", "video-one", false)
	if err != nil {
		t.Fatal(err)
	}
	imports := filepath.Join(eng.RootPath(), "imports")
	if err = os.MkdirAll(imports, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(imports, "tool"), []byte("fake"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = eng.RegisterManualEngine(ctx, testEngineForRepo("6.0.0"), map[string]string{"tool": filepath.Join(imports, "tool")}); err != nil {
		t.Fatal(err)
	}
	if _, err = repos.InstallPlan(ctx, preview.PlanID); !errors.Is(err, ErrStalePlan) {
		t.Fatalf("expected stale management snapshot plan, got %v", err)
	}
}

func TestConcurrentPlansCommitAtMostOneProfileInstall(t *testing.T) {
	_, repos, eng := setup(t)
	ctx := context.Background()
	first, err := repos.CreateInstallPlan(ctx, "trusted-test", "video-one", false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repos.CreateInstallPlan(ctx, "trusted-test", "video-one", false)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, id := range []string{first.PlanID, second.PlanID} {
		go func(planID string) { <-start; _, installErr := repos.InstallPlan(ctx, planID); results <- installErr }(id)
	}
	close(start)
	var success, stale int
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			success++
		} else if errors.Is(err, ErrStalePlan) {
			stale++
		} else {
			t.Fatalf("concurrent install failed unexpectedly: %v", err)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatalf("expected one commit and one stale plan, got success=%d stale=%d", success, stale)
	}
	profiles, _ := eng.ListProfiles(ctx)
	engines, _ := eng.ListEngines(ctx)
	if len(profiles) != 1 || len(engines) != 1 {
		t.Fatalf("concurrent plans left inconsistent state: profiles=%d engines=%d", len(profiles), len(engines))
	}
}

func TestRepositoryDisablementInvalidatesPlansAndRefreshStatus(t *testing.T) {
	_, repos, _ := setup(t)
	ctx := context.Background()
	preview, err := repos.CreateInstallPlan(ctx, "trusted-test", "video-one", false)
	if err != nil {
		t.Fatal(err)
	}
	disabled, err := repos.SetEnabled("trusted-test", false)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Enabled || disabled.Status != "disabled" {
		t.Fatalf("repository disable state not exposed: %+v", disabled)
	}
	if _, err = repos.Browse(ctx, "trusted-test"); !errors.Is(err, ErrRepositoryDisabled) {
		t.Fatalf("disabled repository was browsable: %v", err)
	}
	if _, err = repos.InstallPlan(ctx, preview.PlanID); !errors.Is(err, ErrPlanExpired) {
		t.Fatalf("disabled repository plan remained usable: %v", err)
	}
	enabled, err := repos.SetEnabled("trusted-test", true)
	if err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled {
		t.Fatal("repository did not re-enable")
	}
	refreshed, err := repos.Refresh(ctx, "trusted-test")
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Status != "ready" || refreshed.LastRefresh == "" || refreshed.LastError != "" {
		t.Fatalf("refresh status is not useful: %+v", refreshed)
	}
}

func testEngineForRepo(version string) engine.Definition {
	return engine.Definition{SchemaVersion: 1, ID: "manual-test", Version: version, Name: "Manual", Platform: "linux-amd64", Executables: map[string]string{"tool": "tool"}}
}

func engRoot(s *management.Service) string { return s.RootPath() }

func TestTUFSignatureExpiryAndRootPinFailures(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	targets := makeCatalog(t, nil)
	f.write(t, targets, 1, time.Now().UTC().Add(time.Hour))
	data := t.TempDir()
	eng, _ := management.Open(data)
	defer eng.Close()
	m, _ := Open(data, "0.1.0", eng)
	defer m.Close()
	sum := sha256.Sum256(f.root)
	req := AddRequest{ID: "trusted-test", Name: "Test", URL: "file://" + f.repo, Kind: KindLocal, TrustedRoot: base64.StdEncoding.EncodeToString(f.root), Fingerprint: hex.EncodeToString(sum[:]), Approved: true}
	req.Fingerprint = "00"
	if _, err := m.Add(req); err == nil {
		t.Fatal("expected root pin mismatch")
	}
	req.Fingerprint = hex.EncodeToString(sum[:])
	if _, err := m.Add(req); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Browse(ctx, "trusted-test"); err != nil {
		t.Fatal(err)
	}
	f2 := newFixture(t)
	f2.write(t, targets, 1, time.Now().UTC().Add(-time.Hour))
	req.ID = "expired"
	req.URL = "file://" + f2.repo
	req.TrustedRoot = base64.StdEncoding.EncodeToString(f2.root)
	pin := sha256.Sum256(f2.root)
	req.Fingerprint = hex.EncodeToString(pin[:])
	if _, err := m.Add(req); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Browse(ctx, "expired"); err == nil {
		t.Fatal("expired repository metadata was accepted")
	} else if !errors.Is(err, ErrExpiredMetadata) {
		t.Fatalf("expiry error was not classified: %v", err)
	}
	for _, source := range m.List() {
		if source.ID == "expired" && source.Status != "expired" {
			t.Fatalf("expired trust state not exposed: %+v", source)
		}
	}
}

func TestTUFRejectsBadSignatureAndRollback(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	targets := makeCatalog(t, nil)
	f.write(t, targets, 2, time.Now().UTC().Add(time.Hour))
	data := t.TempDir()
	eng, _ := management.Open(data)
	defer eng.Close()
	m, _ := Open(data, "0.1.0", eng)
	defer m.Close()
	addLocal(t, m, f)
	if _, err := m.Browse(ctx, "trusted-test"); err != nil {
		t.Fatal(err)
	}
	f.write(t, targets, 1, time.Now().UTC().Add(time.Hour))
	if _, err := m.Browse(ctx, "trusted-test"); err == nil {
		t.Fatal("metadata rollback was accepted")
	} else if !errors.Is(err, ErrRollback) {
		t.Fatalf("rollback error was not classified: %v", err)
	}
	status := ""
	for _, source := range m.List() {
		if source.ID == "trusted-test" {
			status = source.Status
		}
	}
	if status != "trust_error" {
		t.Fatalf("rollback trust state not exposed: %q", status)
	}
	if _, err := m.Refresh(ctx, "trusted-test"); err == nil {
		t.Fatal("corrupt signature was accepted during refresh")
	}
	raw, err := os.ReadFile(filepath.Join(f.repo, "metadata", "targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte(" ")...)
	if err = os.WriteFile(filepath.Join(f.repo, "metadata", "targets.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Browse(ctx, "trusted-test"); err == nil {
		t.Fatal("invalid TUF signature/hash accepted")
	}
}

func TestTUFSequentialRootRotationKeepsOriginalPinAndTrust(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	targets := makeCatalog(t, nil)
	f.write(t, targets, 1, time.Now().UTC().Add(time.Hour))
	data := t.TempDir()
	eng, err := management.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	repos, err := Open(data, "0.1.0", eng)
	if err != nil {
		t.Fatal(err)
	}
	defer repos.Close()
	initial := addLocal(t, repos, f)
	if _, err = repos.Browse(ctx, initial.ID); err != nil {
		t.Fatal(err)
	}

	rotated := f.rotatedRoot(t)
	f.write(t, targets, 2, time.Now().UTC().Add(time.Hour))
	metadataDir := filepath.Join(f.repo, "metadata")
	if err = os.WriteFile(filepath.Join(metadataDir, "2.root.json"), rotated, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(metadataDir, "root.json"), rotated, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = repos.Browse(ctx, initial.ID); err != nil {
		t.Fatalf("authorized sequential root rotation failed: %v", err)
	}
	var after Source
	for _, source := range repos.List() {
		if source.ID == initial.ID {
			after = source
		}
	}
	if after.Fingerprint != initial.Fingerprint || after.Status != "ready" {
		t.Fatalf("rotation changed bootstrap pin or trust state: before=%+v after=%+v", initial, after)
	}
}

func TestCorruptPackageAndUnsafeArchivesRejected(t *testing.T) {
	ctx := context.Background()
	f, repos, eng := setup(t)
	pkg := filepath.Join(f.repo, "metadata", "targets", "packages", "ffmpeg.tar.gz")
	if err := os.WriteFile(pkg, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := repos.InstallProfile(ctx, "trusted-test", "video-one", false); err == nil {
		t.Fatal("corrupted package was installed")
	}
	records, _ := eng.ListEngines(ctx)
	if len(records) != 0 {
		t.Fatalf("corrupt package created engine state: %+v", records)
	}
	stage := t.TempDir()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	payload := []byte("escape")
	_ = tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0700, Size: int64(len(payload)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(payload)
	_ = tw.Close()
	_ = gz.Close()
	if err := extractArchive(archive.Bytes(), "tar.gz", stage); err == nil {
		t.Fatal("unsafe archive traversal was extracted")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(stage), "escape")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("archive wrote outside extraction root")
	}
}

func TestUntrustedRemoteHTTPAndUnapprovedRepositoryRejected(t *testing.T) {
	f := newFixture(t)
	data := t.TempDir()
	eng, _ := management.Open(data)
	defer eng.Close()
	m, _ := Open(data, "0.1.0", eng)
	defer m.Close()
	sum := sha256.Sum256(f.root)
	req := AddRequest{ID: "bad", Name: "Community", URL: "http://example.org/repo", Kind: KindCommunity, TrustedRoot: base64.StdEncoding.EncodeToString(f.root), Fingerprint: hex.EncodeToString(sum[:]), Approved: true}
	if _, err := m.Add(req); err == nil {
		t.Fatal("HTTP community repository accepted")
	}
	req.URL = "https://example.org/repo"
	req.Approved = false
	if _, err := m.Add(req); err == nil {
		t.Fatal("unapproved community repository accepted")
	}
}

func TestTUFRejectsBadSignature(t *testing.T) {
	f := newFixture(t)
	f.write(t, makeCatalog(t, nil), 1, time.Now().UTC().Add(time.Hour))
	path := filepath.Join(f.repo, "metadata", "targets.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte(" ")...)
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	eng, err := management.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()
	m, err := Open(root, "0.1.0", eng)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	addLocal(t, m, f)
	if _, err = m.Browse(context.Background(), "trusted-test"); err == nil {
		t.Fatal("tampered targets metadata passed signature/hash validation")
	}
}
