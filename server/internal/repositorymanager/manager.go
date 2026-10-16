package repositorymanager

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
	"github.com/timisah/101conv/server/internal/version"
)

const officialURL = "https://raw.githubusercontent.com/Timisah/101conv/main/catalog"
const maxIndexBytes int64 = 5 << 20
const maxPackageBytes int64 = 1 << 30
const maxPackageFiles = 10000
const maxExpandedBytes int64 = 2 << 30
const indexTarget = "catalog/index.json"

type Manager struct {
	mu                          sync.Mutex
	tufMu                       sync.Mutex
	root, stateFile, appVersion string
	lock                        *flock.Flock
	sources                     map[string]Source
	engines                     *management.Service
}
type AddRequest struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Kind        Kind   `json:"kind"`
	TrustedRoot string `json:"trustedRoot"`
	Fingerprint string `json:"fingerprint"`
	Approved    bool   `json:"approved"`
}
type storedSource struct {
	Source      Source `json:"source"`
	TrustedRoot string `json:"trustedRoot"`
}
type fileState struct {
	SchemaVersion int            `json:"schemaVersion"`
	Sources       []storedSource `json:"sources"`
}

func Open(root, appVersion string, engineService *management.Service) (*Manager, error) {
	if _, err := version.Parse(appVersion); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(abs, 0700); err != nil {
		return nil, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(abs, "repositories.lock"))
	ok, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("repository storage is already open by another process")
	}
	m := &Manager{root: abs, stateFile: filepath.Join(abs, "repositories.json"), appVersion: appVersion, lock: lock, sources: map[string]Source{}, engines: engineService}
	data, err := os.ReadFile(m.stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		lock.Unlock()
		return nil, err
	}
	var saved fileState
	if err = json.Unmarshal(data, &saved); err != nil {
		lock.Unlock()
		return nil, err
	}
	if saved.SchemaVersion != 1 {
		lock.Unlock()
		return nil, fmt.Errorf("unsupported repository state schema %d", saved.SchemaVersion)
	}
	for _, item := range saved.Sources {
		rootBytes, e := base64.StdEncoding.DecodeString(item.TrustedRoot)
		if e != nil {
			lock.Unlock()
			return nil, fmt.Errorf("invalid pinned TUF root for repository %q", item.Source.ID)
		}
		item.Source.TrustedRoot = string(rootBytes)
		m.sources[item.Source.ID] = item.Source
	}
	return m, nil
}
func (m *Manager) Close() error {
	if m.lock == nil {
		return nil
	}
	err := m.lock.Unlock()
	m.lock = nil
	return err
}

// List exposes the legacy official catalog as unavailable for trusted installs
// until the upstream project publishes a TUF root that can be pinned in a release.
func (m *Manager) List() []Source {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Source{{ID: "101conv-official", Name: "101conv Official Catalog", URL: officialURL, Kind: KindOfficial, Trust: TrustUnavailable}}
	for _, s := range m.sources {
		s.TrustedRoot = ""
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Manager) Add(req AddRequest) (Source, error) {
	if !repoID(req.ID) || req.ID == "101conv-official" {
		return Source{}, fmt.Errorf("repository id is invalid or reserved")
	}
	if strings.TrimSpace(req.Name) == "" || !req.Approved {
		return Source{}, fmt.Errorf("repository requires a name and explicit administrator approval")
	}
	u, err := url.Parse(req.URL)
	if err != nil {
		return Source{}, err
	}
	if u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return Source{}, fmt.Errorf("repository URL must not contain credentials or fragments")
	}
	if req.Kind == KindLocal {
		if u.Scheme != "file" {
			return Source{}, fmt.Errorf("local repository requires file URL")
		}
	} else if req.Kind == KindCommunity {
		if u.Scheme != "https" || u.Host == "" {
			return Source{}, fmt.Errorf("community repository requires HTTPS")
		}
	} else {
		return Source{}, fmt.Errorf("repository kind must be community or local")
	}
	rootBytes, err := base64.StdEncoding.DecodeString(req.TrustedRoot)
	if err != nil || len(rootBytes) == 0 {
		return Source{}, fmt.Errorf("TUF trusted root is required")
	}
	sum := sha256.Sum256(rootBytes)
	fp := hex.EncodeToString(sum[:])
	if !strings.EqualFold(fp, strings.TrimPrefix(req.Fingerprint, "sha256:")) {
		return Source{}, fmt.Errorf("TUF root fingerprint does not match the explicit administrator pin")
	}
	s := Source{ID: req.ID, Name: req.Name, URL: strings.TrimRight(req.URL, "/"), Kind: req.Kind, Trust: TrustPinned, Fingerprint: fp, TrustedRoot: string(rootBytes), ApprovedAt: time.Now().UTC().Format(time.RFC3339)}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sources[s.ID]; exists {
		return Source{}, fmt.Errorf("repository %q: %w", s.ID, management.ErrConflict)
	}
	if _, err = newUpdater(context.Background(), s, rootBytes, filepath.Join(m.root, "tuf", s.ID)); err != nil {
		return Source{}, fmt.Errorf("invalid pinned TUF root: %w", err)
	}
	m.sources[s.ID] = s
	if err = m.persist(); err != nil {
		delete(m.sources, s.ID)
		return Source{}, err
	}
	s.TrustedRoot = ""
	return s, nil
}
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "101conv-official" {
		return fmt.Errorf("bundled official source cannot be removed")
	}
	s, ok := m.sources[id]
	if !ok {
		return management.ErrNotFound
	}
	delete(m.sources, id)
	if err := m.persist(); err != nil {
		m.sources[id] = s
		return err
	}
	return os.RemoveAll(filepath.Join(m.root, "tuf", id))
}
func (m *Manager) getSource(id string) (Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sources[id]
	if !ok {
		return Source{}, management.ErrNotFound
	}
	return s, nil
}

func (m *Manager) Browse(ctx context.Context, id string) (Catalog, error) {
	s, err := m.getSource(id)
	if err != nil {
		return Catalog{}, err
	}
	if s.Trust != TrustPinned {
		return Catalog{}, fmt.Errorf("repository has no pinned TUF root; metadata inspection is disabled")
	}
	data, err := m.readTarget(ctx, s, indexTarget, maxIndexBytes)
	if err != nil {
		return Catalog{}, err
	}
	var index CatalogIndex
	if err = json.Unmarshal(data, &index); err != nil {
		return Catalog{}, fmt.Errorf("decode signed repository index: %w", err)
	}
	if index.SchemaVersion != 1 {
		return Catalog{}, fmt.Errorf("unsupported repository index schema %d", index.SchemaVersion)
	}
	if !compatible(m.appVersion, index.MinimumAppVersion, index.MaximumAppVersionExclusive) {
		return Catalog{}, fmt.Errorf("repository is incompatible with 101conv %s", m.appVersion)
	}
	out := Catalog{Repository: s, Engines: []engine.Definition{}, Profiles: []profile.Definition{}}
	for _, entry := range index.Engines {
		if !safeTarget(entry.ManifestTarget) {
			return Catalog{}, fmt.Errorf("unsafe engine manifest target %q", entry.ManifestTarget)
		}
		raw, e := m.readTarget(ctx, s, entry.ManifestTarget, maxIndexBytes)
		if e != nil {
			return Catalog{}, fmt.Errorf("engine %q manifest: %w", entry.ID, e)
		}
		var manifest EngineManifest
		if e = json.Unmarshal(raw, &manifest); e != nil {
			return Catalog{}, e
		}
		d := manifest.Engine
		if d.ID != entry.ID || d.Version != entry.Version || d.Name != entry.Name {
			return Catalog{}, fmt.Errorf("catalog entry and engine manifest disagree for %q", entry.ID)
		}
		if e = d.Validate(); e != nil {
			return Catalog{}, e
		}
		if d.Artifact != nil && !safeTarget(d.Artifact.URL) {
			return Catalog{}, fmt.Errorf("engine %q artifact target is unsafe", d.ID)
		}
		out.Engines = append(out.Engines, d)
	}
	for _, entry := range index.Profiles {
		if !safeTarget(entry.ManifestTarget) {
			return Catalog{}, fmt.Errorf("unsafe profile manifest target %q", entry.ManifestTarget)
		}
		raw, e := m.readTarget(ctx, s, entry.ManifestTarget, maxIndexBytes)
		if e != nil {
			return Catalog{}, fmt.Errorf("profile %q manifest: %w", entry.ID, e)
		}
		var manifest ProfileManifest
		if e = json.Unmarshal(raw, &manifest); e != nil {
			return Catalog{}, e
		}
		d := manifest.Profile
		if d.ID != entry.ID || d.Version != entry.Version || d.Name != entry.Name {
			return Catalog{}, fmt.Errorf("catalog entry and profile manifest disagree for %q", entry.ID)
		}
		if e = d.Validate(); e != nil {
			return Catalog{}, e
		}
		if compatible(m.appVersion, d.MinimumAppVersion, d.MaximumAppVersionExclusive) {
			out.Profiles = append(out.Profiles, d)
		}
	}
	return out, nil
}

func (m *Manager) Preview(ctx context.Context, repoID, profileID string) (InstallPreview, error) {
	c, err := m.Browse(ctx, repoID)
	if err != nil {
		return InstallPreview{}, err
	}
	var p profile.Definition
	found := false
	for _, candidate := range c.Profiles {
		if candidate.ID == profileID {
			p = candidate
			found = true
			break
		}
	}
	if !found {
		return InstallPreview{}, management.ErrNotFound
	}
	installed, err := m.engines.ListEngines(ctx)
	if err != nil {
		return InstallPreview{}, err
	}
	preview := InstallPreview{Repository: c.Repository, Publisher: c.Repository.Name, Trust: c.Repository.Trust, License: p.License, Profile: p, Dependencies: []DependencyPreview{}, PlannedChanges: []string{"install profile " + p.ID + " " + p.Version}}
	for _, req := range p.Engines {
		dep := DependencyPreview{EngineID: req.EngineID, VersionRange: req.Versions, Capabilities: req.Capabilities, Action: "install engine"}
		for _, current := range installed {
			if req.Accepts(current.Definition) && profileExecutablesAvailable(p, current.Installation) && (dep.InstalledVersion == "" || newer(current.Installation.Version, dep.InstalledVersion)) {
				dep.InstalledVersion = current.Installation.Version
				dep.EngineName = current.Definition.Name
				dep.EngineLicense = current.Definition.License
				dep.Action = "reuse installed engine"
				break
			}
		}
		if dep.Action == "install engine" {
			best := ""
			for _, d := range c.Engines {
				if req.Accepts(d) && (best == "" || newer(d.Version, best)) {
					best = d.Version
					dep.EngineName = d.Name
					dep.EngineLicense = d.License
				}
			}
			if best == "" {
				return InstallPreview{}, fmt.Errorf("profile %q needs engine %q range [%s,%s) and capabilities %v, but no compatible engine is available", p.ID, req.EngineID, req.Versions.Minimum, req.Versions.MaximumExclusive, req.Capabilities)
			}
			preview.PlannedChanges = append(preview.PlannedChanges, fmt.Sprintf("install engine %s %s", req.EngineID, best))
		}
		preview.Dependencies = append(preview.Dependencies, dep)
	}
	return preview, nil
}

func (m *Manager) InstallProfile(ctx context.Context, repoID, profileID string, update bool) (management.ProfileRecord, error) {
	preview, err := m.Preview(ctx, repoID, profileID)
	if err != nil {
		return management.ProfileRecord{}, err
	}
	catalog, err := m.Browse(ctx, repoID)
	if err != nil {
		return management.ProfileRecord{}, err
	}
	before, err := m.engines.ListEngines(ctx)
	if err != nil {
		return management.ProfileRecord{}, err
	}
	known := map[string]bool{}
	for _, record := range before {
		known[record.Installation.EngineID+"\x00"+record.Installation.Version] = true
	}
	var newlyInstalled []management.EngineRecord
	for _, dependency := range preview.Dependencies {
		if dependency.Action == "reuse installed engine" {
			continue
		}
		var best engine.Definition
		found := false
		for _, candidate := range catalog.Engines {
			for _, req := range preview.Profile.Engines {
				if req.EngineID == dependency.EngineID && req.Accepts(candidate) && (!found || newer(candidate.Version, best.Version)) {
					best = candidate
					found = true
				}
			}
		}
		if !found {
			return management.ProfileRecord{}, fmt.Errorf("repository has no compatible engine %q", dependency.EngineID)
		}
		var record management.EngineRecord
		if record, err = m.installEngine(ctx, catalog.Repository, best); err != nil {
			m.rollbackNewEngines(newlyInstalled)
			return management.ProfileRecord{}, err
		}
		if !known[record.Installation.EngineID+"\x00"+record.Installation.Version] {
			newlyInstalled = append(newlyInstalled, record)
			known[record.Installation.EngineID+"\x00"+record.Installation.Version] = true
		}
	}
	if update {
		record, err := m.engines.UpdateProfile(ctx, preview.Profile, repoID)
		if err != nil {
			m.rollbackNewEngines(newlyInstalled)
		}
		return record, err
	}
	record, err := m.engines.InstallProfile(ctx, preview.Profile, repoID)
	if err != nil {
		m.rollbackNewEngines(newlyInstalled)
	}
	return record, err
}

func (m *Manager) rollbackNewEngines(records []management.EngineRecord) {
	for i := len(records) - 1; i >= 0; i-- {
		_ = m.engines.RemoveEngine(context.Background(), records[i].Installation.EngineID, records[i].Installation.Version, false)
	}
}
func (m *Manager) InstallEngine(ctx context.Context, repoID, engineID, engineVersion string) (management.EngineRecord, error) {
	catalog, err := m.Browse(ctx, repoID)
	if err != nil {
		return management.EngineRecord{}, err
	}
	for _, d := range catalog.Engines {
		if d.ID == engineID && d.Version == engineVersion {
			return m.installEngine(ctx, catalog.Repository, d)
		}
	}
	return management.EngineRecord{}, management.ErrNotFound
}
func (m *Manager) installEngine(ctx context.Context, source Source, d engine.Definition) (management.EngineRecord, error) {
	installed, err := m.engines.ListEngines(ctx)
	if err != nil {
		return management.EngineRecord{}, err
	}
	if d.Artifact == nil {
		return management.EngineRecord{}, fmt.Errorf("repository engine manifest has no artifact")
	}
	for _, existing := range installed {
		if existing.Installation.EngineID == d.ID && existing.Installation.Version == d.Version {
			if strings.EqualFold(existing.Installation.PackageSHA256, d.Artifact.SHA256) {
				return existing, nil
			}
			return management.EngineRecord{}, fmt.Errorf("engine %q version %s has a conflicting installed digest: %w", d.ID, d.Version, management.ErrConflict)
		}
	}
	archive, err := m.readTarget(ctx, source, d.Artifact.URL, maxPackageBytes)
	if err != nil {
		return management.EngineRecord{}, err
	}
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	if !strings.EqualFold(digest, d.Artifact.SHA256) {
		return management.EngineRecord{}, fmt.Errorf("package SHA-256 does not match signed engine manifest")
	}
	stage, cleanup, err := m.engines.CreateVerifiedStaging()
	if err != nil {
		return management.EngineRecord{}, err
	}
	defer cleanup()
	if err = extractArchive(archive, d.Artifact.Format, stage); err != nil {
		return management.EngineRecord{}, err
	}
	return m.engines.InstallVerifiedEngine(d, stage, digest, source.ID)
}

func (m *Manager) readTarget(ctx context.Context, s Source, target string, max int64) ([]byte, error) {
	if !safeTarget(target) {
		return nil, fmt.Errorf("unsafe TUF target %q", target)
	}
	m.tufMu.Lock()
	defer m.tufMu.Unlock()
	up, err := newUpdater(ctx, s, []byte(s.TrustedRoot), filepath.Join(m.root, "tuf", s.ID))
	if err != nil {
		return nil, err
	}
	if err = up.Refresh(); err != nil {
		return nil, fmt.Errorf("TUF metadata refresh failed: %w", err)
	}
	info, err := up.GetTargetInfo(target)
	if err != nil {
		return nil, err
	}
	if info.Length < 0 || info.Length > max {
		return nil, fmt.Errorf("target %q exceeds size limit", target)
	}
	_, data, err := up.DownloadTarget(info, "", "")
	if err != nil {
		return nil, fmt.Errorf("TUF target verification failed: %w", err)
	}
	return data, nil
}

func (m *Manager) persist() error {
	st := fileState{SchemaVersion: 1, Sources: []storedSource{}}
	for _, s := range m.sources {
		raw := base64.StdEncoding.EncodeToString([]byte(s.TrustedRoot))
		safe := s
		safe.TrustedRoot = ""
		st.Sources = append(st.Sources, storedSource{Source: safe, TrustedRoot: raw})
	}
	sort.Slice(st.Sources, func(i, j int) bool { return st.Sources[i].Source.ID < st.Sources[j].Source.ID })
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(m.root, ".repositories-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, m.stateFile); err != nil {
		return err
	}
	dir, err := os.Open(m.root)
	if err == nil {
		_ = dir.Sync()
		dir.Close()
	}
	return nil // rename committed the state; never roll back memory after the commit point.
}

func compatible(app, min, max string) bool {
	if min != "" {
		cmp, e := version.Compare(app, min)
		if e != nil || cmp < 0 {
			return false
		}
	}
	if max != "" {
		cmp, e := version.Compare(app, max)
		return e == nil && cmp < 0
	}
	return true
}
func newer(a, b string) bool {
	c, e := version.Compare(a, b)
	return e == nil && (c > 0 || c == 0 && a < b)
}
func profileExecutablesAvailable(p profile.Definition, inst engine.Installation) bool {
	for _, rule := range p.Rules {
		if rule.EngineID == inst.EngineID {
			path := inst.ExecutablePaths[rule.Executable]
			if path == "" {
				return false
			}
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
				return false
			}
		}
	}
	return true
}
func repoID(v string) bool {
	if v == "" || len(v) > 100 {
		return false
	}
	for i, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && (c == '.' || c == '_' || c == '-')) {
			return false
		}
	}
	return v != "." && v != ".."
}
func safeTarget(v string) bool {
	if v == "" || strings.Contains(v, `\`) || strings.HasPrefix(v, "/") {
		return false
	}
	for _, p := range strings.Split(v, "/") {
		if p == "" || p == "." || p == ".." {
			return false
		}
	}
	return true
}

func extractArchive(data []byte, format, root string) error {
	switch format {
	case "tar.gz":
		return extractTarGzip(data, root)
	case "zip":
		return extractZip(data, root)
	default:
		return fmt.Errorf("unsupported archive format %q (supported: tar.gz, zip)", format)
	}
}
func safeArchivePath(root, name string) (string, error) {
	if name == "" || strings.Contains(name, `\`) || filepath.IsAbs(name) {
		return "", fmt.Errorf("unsafe archive path %q", name)
	}
	for _, p := range strings.Split(name, "/") {
		if p == "" || p == "." || p == ".." {
			return "", fmt.Errorf("unsafe archive path %q", name)
		}
	}
	dest := filepath.Join(root, filepath.FromSlash(name))
	rel, err := filepath.Rel(root, dest)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive path escapes extraction root")
	}
	return dest, nil
}
func writeArchiveFile(root, name string, mode os.FileMode, r io.Reader, size int64, total *int64) error {
	dest, err := safeArchivePath(root, name)
	if err != nil {
		return err
	}
	if size < 0 || size > maxExpandedBytes-*total {
		return fmt.Errorf("archive exceeds expansion limit")
	}
	if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(r, size+1))
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n != size {
		return fmt.Errorf("archive entry %q size mismatch", name)
	}
	modeBits := os.FileMode(0600)
	if mode&0111 != 0 {
		modeBits = 0700
	}
	if err = os.Chmod(dest, modeBits); err != nil {
		return err
	}
	*total += n
	return nil
}
func extractTarGzip(data []byte, root string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	var total int64
	count := 0
	for {
		h, e := tr.Next()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return e
		}
		count++
		if count > maxPackageFiles {
			return fmt.Errorf("archive has too many entries")
		}
		if h.Typeflag == tar.TypeDir {
			dest, e := safeArchivePath(root, strings.TrimSuffix(h.Name, "/"))
			if e != nil {
				return e
			}
			if e = os.MkdirAll(dest, 0700); e != nil {
				return e
			}
			continue
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			return fmt.Errorf("archive contains link or special entry %q", h.Name)
		}
		if e = writeArchiveFile(root, h.Name, os.FileMode(h.Mode), tr, h.Size, &total); e != nil {
			return e
		}
	}
	return nil
}
func extractZip(data []byte, root string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if len(zr.File) > maxPackageFiles {
		return fmt.Errorf("archive has too many entries")
	}
	var total int64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			dest, e := safeArchivePath(root, strings.TrimSuffix(f.Name, "/"))
			if e != nil {
				return e
			}
			if e = os.MkdirAll(dest, 0700); e != nil {
				return e
			}
			continue
		}
		if f.Mode()&os.ModeSymlink != 0 || !f.Mode().IsRegular() {
			return fmt.Errorf("archive contains link or special entry %q", f.Name)
		}
		in, e := f.Open()
		if e != nil {
			return e
		}
		e = writeArchiveFile(root, f.Name, f.Mode(), in, int64(f.UncompressedSize64), &total)
		closeErr := in.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
