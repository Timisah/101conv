package repositorymanager

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/sorairolake/lzip-go"
	"github.com/theupdateframework/go-tuf/v2/metadata/updater"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
	"github.com/timisah/101conv/server/internal/version"
	"github.com/ulikunitz/xz"
)

const officialURL = "https://raw.githubusercontent.com/Timisah/101conv/main/catalog"
const maxIndexBytes int64 = 5 << 20
const maxPackageBytes int64 = 1 << 30
const maxPackageFiles = 10000
const maxExpandedBytes int64 = 2 << 30
const maxLzipDictionaryBytes uint64 = 128 << 20
const indexTarget = "catalog/index.json"

type Manager struct {
	mu                          sync.Mutex
	operationMu                 sync.Mutex
	tufMu                       sync.Mutex
	root, stateFile, appVersion string
	lock                        *flock.Flock
	officialRoot                []byte
	officialUnsignedApproved    bool
	officialStatus              Source
	sources                     map[string]Source
	engines                     *management.Service
	plans                       map[string]installationPlan
}

// official-root.json is intentionally empty until the project owner provisions
// and reviews the official TUF root. Releases with a real root embed the exact
// public bytes pinned by the official repository.
//
//go:embed official-root.json
var embeddedOfficialRoot []byte

type installationPlan struct {
	preview           InstallPreview
	enginePreview     *EngineInstallPreview
	profile           profile.Definition
	engineOnly        *engine.Definition
	engineDefinitions map[string]engine.Definition
	selections        map[string]string
	installedDigest   string
	update            bool
	reuseEngine       bool
	expires           time.Time
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
	SchemaVersion            int            `json:"schemaVersion"`
	Sources                  []storedSource `json:"sources"`
	OfficialUnsignedApproved bool           `json:"officialUnsignedApproved,omitempty"`
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
	m := &Manager{root: abs, stateFile: filepath.Join(abs, "repositories.json"), appVersion: appVersion, lock: lock, sources: map[string]Source{}, engines: engineService, plans: map[string]installationPlan{}, officialRoot: bytes.TrimSpace(embeddedOfficialRoot)}
	if len(m.officialRoot) > 0 {
		official := m.officialSource()
		if _, e := newUpdater(context.Background(), official, m.officialRoot, filepath.Join(abs, "tuf", official.ID)); e != nil {
			lock.Unlock()
			return nil, fmt.Errorf("embedded official TUF root is invalid: %w", e)
		}
	}
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
	m.officialUnsignedApproved = saved.OfficialUnsignedApproved
	for _, item := range saved.Sources {
		rootBytes, e := base64.StdEncoding.DecodeString(item.TrustedRoot)
		if e != nil {
			lock.Unlock()
			return nil, fmt.Errorf("invalid pinned TUF root for repository %q", item.Source.ID)
		}
		if item.Source.Status == "" { // Older Phase 3 repositories were enabled by default.
			item.Source.Enabled = true
			item.Source.Status = "configured"
		}
		if !repoID(item.Source.ID) || item.Source.ID == "101conv-official" || item.Source.Kind != KindLocal && item.Source.Kind != KindCommunity {
			lock.Unlock()
			return nil, fmt.Errorf("invalid persisted repository configuration for %q", item.Source.ID)
		}
		rootHash := sha256.Sum256(rootBytes)
		if hex.EncodeToString(rootHash[:]) != strings.TrimPrefix(item.Source.Fingerprint, "sha256:") {
			lock.Unlock()
			return nil, fmt.Errorf("persisted trust root fingerprint mismatch for repository %q", item.Source.ID)
		}
		fingerprint := sha256.Sum256(rootBytes)
		if hex.EncodeToString(fingerprint[:]) != strings.TrimPrefix(item.Source.Fingerprint, "sha256:") {
			lock.Unlock()
			return nil, fmt.Errorf("persisted trust root fingerprint mismatch for repository %q", item.Source.ID)
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

func (m *Manager) List() []Source {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Source{m.officialSource()}
	for _, s := range m.sources {
		s.TrustedRoot = ""
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (m *Manager) officialSource() Source {
	var source Source
	if len(m.officialRoot) == 0 {
		// The public GitHub catalog can be browsed as unsigned metadata. This does
		// not make it trusted. Installation requires a separate explicit admin approval.
		source = Source{ID: "101conv-official", Name: "101conv Official Catalog", URL: officialURL, Kind: KindOfficial, Trust: TrustUnavailable, Enabled: true, Status: "browse_only", LastError: "Unsigned catalog: browsing only; admin approval is required to install"}
		if m.officialUnsignedApproved {
			source.Trust = TrustApprovedUnsigned
			source.Status = "approved_unsigned"
			source.LastError = "Administrator approved installs from unsigned GitHub metadata"
		}
	} else {
		source = Source{ID: "101conv-official", Name: "101conv Official Catalog", URL: officialURL, Kind: KindOfficial, Trust: TrustPinned, TrustedRoot: string(m.officialRoot), Enabled: true, Status: "configured"}
	}
	if m.officialStatus.LastRefresh != "" {
		source.Status = m.officialStatus.Status
		source.LastRefresh = m.officialStatus.LastRefresh
		source.LastError = m.officialStatus.LastError
	}
	return source
}

// SetOfficialUnsignedApproval explicitly opts this instance into installing
// packages from the GitHub catalog without TUF signatures. The installer still
// enforces HTTPS, artifact hashes, archive limits, and atomic installation, but
// the publisher identity is not cryptographically authenticated.
func (m *Manager) SetOfficialUnsignedApproval(approved bool) (Source, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if len(m.officialRoot) > 0 {
		return Source{}, fmt.Errorf("official catalog already has a verified trust root")
	}
	m.mu.Lock()
	previous := m.officialUnsignedApproved
	m.officialUnsignedApproved = approved
	if err := m.persist(); err != nil {
		m.officialUnsignedApproved = previous
		m.mu.Unlock()
		return Source{}, fmt.Errorf("persist official catalog approval: %w", err)
	}
	for planID, plan := range m.plans {
		if plan.preview.Repository.ID == "101conv-official" || plan.enginePreview != nil && plan.enginePreview.Repository.ID == "101conv-official" {
			delete(m.plans, planID)
		}
	}
	m.officialStatus = Source{}
	source := m.officialSource()
	m.mu.Unlock()
	source.TrustedRoot = ""
	return source, nil
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
	s := Source{ID: req.ID, Name: strings.TrimSpace(req.Name), URL: strings.TrimRight(req.URL, "/"), Kind: req.Kind, Trust: TrustPinned, Fingerprint: fp, TrustedRoot: string(rootBytes), ApprovedAt: time.Now().UTC().Format(time.RFC3339), Enabled: true, Status: "configured"}
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
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
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.tufMu.Lock()
	defer m.tufMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "101conv-official" {
		return fmt.Errorf("bundled official source cannot be removed")
	}
	s, ok := m.sources[id]
	if !ok {
		return management.ErrNotFound
	}
	if err := os.RemoveAll(filepath.Join(m.root, "tuf", id)); err != nil {
		return fmt.Errorf("remove repository metadata cache: %w", err)
	}
	delete(m.sources, id)
	if err := m.persist(); err != nil {
		m.sources[id] = s
		return err
	}
	for planID, plan := range m.plans {
		if plan.preview.Repository.ID == id || plan.enginePreview != nil && plan.enginePreview.Repository.ID == id {
			delete(m.plans, planID)
		}
	}
	return nil
}

func (m *Manager) SetEnabled(id string, enabled bool) (Source, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if id == "101conv-official" {
		return Source{}, fmt.Errorf("official source enablement requires a provisioned trust root")
	}
	m.tufMu.Lock()
	defer m.tufMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sources[id]
	if !ok {
		return Source{}, management.ErrNotFound
	}
	previous := s
	s.Enabled = enabled
	if !enabled {
		s.Status = "disabled"
		s.LastError = ""
	} else if s.Status == "disabled" {
		s.Status = "configured"
	}
	m.sources[id] = s
	if err := m.persist(); err != nil {
		m.sources[id] = previous
		return Source{}, err
	}
	if !enabled {
		for planID, plan := range m.plans {
			if plan.preview.Repository.ID == id || plan.enginePreview != nil && plan.enginePreview.Repository.ID == id {
				delete(m.plans, planID)
			}
		}
	}
	s.TrustedRoot = ""
	return s, nil
}

func (m *Manager) Refresh(ctx context.Context, id string) (Source, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	source, err := m.getSource(id)
	if err != nil {
		return Source{}, err
	}
	if !source.Enabled {
		return Source{}, ErrRepositoryDisabled
	}
	if id == "101conv-official" && len(m.officialRoot) == 0 {
		_, err = loadUnsignedView(ctx, source, officialURL, m.appVersion)
	} else {
		m.tufMu.Lock()
		_, err = m.loadViewLocked(ctx, source)
		m.tufMu.Unlock()
	}
	if persistErr := m.recordRefresh(id, err); persistErr != nil {
		if err != nil {
			return Source{}, errors.Join(err, fmt.Errorf("persist repository status: %w", persistErr))
		}
		return Source{}, fmt.Errorf("persist repository status: %w", persistErr)
	}
	if err != nil {
		return Source{}, err
	}
	updated, _ := m.getSource(id)
	updated.TrustedRoot = ""
	return updated, nil
}
func (m *Manager) getSource(id string) (Source, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "101conv-official" {
		return m.officialSource(), nil
	}
	s, ok := m.sources[id]
	if !ok {
		return Source{}, management.ErrNotFound
	}
	return s, nil
}

func (m *Manager) Browse(ctx context.Context, id string) (Catalog, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	s, err := m.getSource(id)
	if err != nil {
		return Catalog{}, err
	}
	if !s.Enabled {
		return Catalog{}, ErrRepositoryDisabled
	}
	if id == "101conv-official" && len(m.officialRoot) == 0 {
		view, err := loadUnsignedView(ctx, s, officialURL, m.appVersion)
		m.mu.Lock()
		if err != nil {
			m.officialStatus = Source{Status: "error", LastRefresh: time.Now().UTC().Format(time.RFC3339), LastError: err.Error()}
		} else {
			status, message := "browse_only", "Unsigned catalog: browsing only; admin approval is required to install"
			if m.officialUnsignedApproved {
				status, message = "approved_unsigned", "Administrator approved installs from unsigned GitHub metadata"
			}
			m.officialStatus = Source{Status: status, LastRefresh: time.Now().UTC().Format(time.RFC3339), LastError: message}
		}
		m.mu.Unlock()
		if err != nil {
			return Catalog{}, err
		}
		return view.catalog, nil
	}
	if s.Trust != TrustPinned {
		return Catalog{}, fmt.Errorf("repository has no pinned TUF root; metadata inspection is disabled")
	}
	m.tufMu.Lock()
	view, err := m.loadViewLocked(ctx, s)
	m.tufMu.Unlock()
	if err != nil {
		return Catalog{}, errors.Join(err, m.recordRefresh(s.ID, err))
	}
	if err := m.recordRefresh(s.ID, nil); err != nil {
		return Catalog{}, fmt.Errorf("persist repository status: %w", err)
	}
	return view.catalog, nil
}

// loadUnsignedCatalog is a compatibility wrapper used by tests and browsing.
func loadUnsignedCatalog(ctx context.Context, source Source, base, appVersion string) (Catalog, error) {
	view, err := loadUnsignedView(ctx, source, base, appVersion)
	return view.catalog, err
}

// loadUnsignedView reads only public catalog metadata. It never fetches
// package artifacts; install code calls the separate opt-in downloader.
func loadUnsignedView(ctx context.Context, source Source, base, appVersion string) (verifiedView, error) {
	baseURL, err := url.Parse(strings.TrimRight(base, "/") + "/")
	if err != nil || (baseURL.Scheme != "https" && !(baseURL.Scheme == "http" && (baseURL.Hostname() == "127.0.0.1" || baseURL.Hostname() == "localhost"))) || baseURL.Host == "" {
		return verifiedView{}, fmt.Errorf("invalid official catalog URL")
	}
	client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || req.URL.Scheme != baseURL.Scheme || req.URL.Host != baseURL.Host {
			return fmt.Errorf("catalog redirect left the configured GitHub host")
		}
		return nil
	}}
	targets := map[string][]byte{}
	read := func(target string) ([]byte, error) {
		if !safeTarget(target) {
			return nil, fmt.Errorf("unsafe catalog target %q", target)
		}
		u := *baseURL
		u.Path = strings.TrimRight(baseURL.Path, "/") + "/targets/" + target
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if e != nil {
			return nil, e
		}
		resp, e := client.Do(req)
		if e != nil {
			return nil, fmt.Errorf("fetch unsigned catalog metadata: %w", e)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("fetch catalog target %q: HTTP %d", target, resp.StatusCode)
		}
		data, e := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes+1))
		if e != nil {
			return nil, e
		}
		if int64(len(data)) > maxIndexBytes {
			return nil, fmt.Errorf("catalog target %q exceeds size limit", target)
		}
		targets[target] = data
		return data, nil
	}
	data, err := read(indexTarget)
	if err != nil {
		return verifiedView{}, err
	}
	var index CatalogIndex
	if err = json.Unmarshal(data, &index); err != nil {
		return verifiedView{}, fmt.Errorf("decode unsigned catalog index: %w", err)
	}
	if index.SchemaVersion != 1 || !compatible(appVersion, index.MinimumAppVersion, index.MaximumAppVersionExclusive) {
		return verifiedView{}, fmt.Errorf("unsupported or incompatible catalog")
	}
	if len(index.Engines)+len(index.Profiles) > 500 {
		return verifiedView{}, fmt.Errorf("catalog contains too many entries")
	}
	out := Catalog{Repository: source, Engines: []engine.Definition{}, Profiles: []profile.Definition{}, IncompatibleProfiles: []IncompatibleProfile{}}
	for _, entry := range index.Engines {
		if !safeTarget(entry.ManifestTarget) {
			return verifiedView{}, fmt.Errorf("unsafe engine manifest target %q", entry.ManifestTarget)
		}
		raw, e := read(entry.ManifestTarget)
		if e != nil {
			return verifiedView{}, fmt.Errorf("read engine %q manifest: %w", entry.ID, e)
		}
		var manifest EngineManifest
		if e = json.Unmarshal(raw, &manifest); e != nil {
			return verifiedView{}, fmt.Errorf("decode engine %q manifest: %w", entry.ID, e)
		}
		d := manifest.Engine
		if d.ID != entry.ID || d.Version != entry.Version {
			return verifiedView{}, fmt.Errorf("catalog entry identity/version and engine manifest disagree for %q", entry.ID)
		}
		if e = d.Validate(); e != nil {
			return verifiedView{}, e
		}
		out.Engines = append(out.Engines, d)
	}
	for _, entry := range index.Profiles {
		if !safeTarget(entry.ManifestTarget) {
			return verifiedView{}, fmt.Errorf("unsafe profile manifest target %q", entry.ManifestTarget)
		}
		raw, e := read(entry.ManifestTarget)
		if e != nil {
			return verifiedView{}, fmt.Errorf("read profile %q manifest: %w", entry.ID, e)
		}
		var manifest ProfileManifest
		if e = json.Unmarshal(raw, &manifest); e != nil {
			return verifiedView{}, fmt.Errorf("decode profile %q manifest: %w", entry.ID, e)
		}
		d := manifest.Profile
		if d.ID != entry.ID || d.Version != entry.Version {
			return verifiedView{}, fmt.Errorf("catalog entry identity/version and profile manifest disagree for %q", entry.ID)
		}
		if e = d.Validate(); e != nil {
			return verifiedView{}, e
		}
		if compatible(appVersion, d.MinimumAppVersion, d.MaximumAppVersionExclusive) {
			out.Profiles = append(out.Profiles, d)
		} else {
			out.IncompatibleProfiles = append(out.IncompatibleProfiles, IncompatibleProfile{ID: d.ID, Version: d.Version, Reason: "profile is incompatible with this application version"})
		}
	}
	return verifiedView{catalog: out, digest: digestCatalogTargets(targets), targets: targets}, nil
}

type verifiedView struct {
	catalog Catalog
	digest  string
	updater *updater.Updater
	targets map[string][]byte
}

func (m *Manager) loadViewLocked(ctx context.Context, s Source) (verifiedView, error) {
	up, err := newUpdater(ctx, s, []byte(s.TrustedRoot), filepath.Join(m.root, "tuf", s.ID))
	if err != nil {
		return verifiedView{}, err
	}
	if err = up.Refresh(); err != nil {
		return verifiedView{}, fmt.Errorf("TUF metadata refresh failed: %w", classifyTUFError(err))
	}
	view := verifiedView{updater: up, targets: map[string][]byte{}}
	metadataDir := filepath.Join(m.root, "tuf", s.ID, "metadata")
	metadataFiles, err := os.ReadDir(metadataDir)
	if err != nil {
		return verifiedView{}, fmt.Errorf("read verified TUF metadata cache: %w", err)
	}
	for _, entry := range metadataFiles {
		if entry.IsDir() {
			continue
		}
		info, e := os.Lstat(filepath.Join(metadataDir, entry.Name()))
		if e != nil {
			return verifiedView{}, e
		}
		if !info.Mode().IsRegular() || info.Size() > maxIndexBytes {
			return verifiedView{}, fmt.Errorf("unsafe or oversized cached metadata file %q", entry.Name())
		}
		b, e := os.ReadFile(filepath.Join(metadataDir, entry.Name()))
		if e != nil {
			return verifiedView{}, e
		}
		view.targets["@metadata:"+entry.Name()] = b
	}
	read := func(target string, max int64) ([]byte, error) {
		b, e := downloadVerified(up, target, max)
		if e == nil {
			view.targets[target] = b
		}
		return b, e
	}
	data, err := read(indexTarget, maxIndexBytes)
	if err != nil {
		return verifiedView{}, err
	}
	var index CatalogIndex
	if err = json.Unmarshal(data, &index); err != nil {
		return verifiedView{}, fmt.Errorf("decode signed repository index: %w", err)
	}
	if index.SchemaVersion != 1 {
		return verifiedView{}, fmt.Errorf("unsupported repository index schema %d", index.SchemaVersion)
	}
	if !compatible(m.appVersion, index.MinimumAppVersion, index.MaximumAppVersionExclusive) {
		return verifiedView{}, coded("incompatible_version", fmt.Errorf("%w: repository does not support 101conv %s", ErrIncompatible, m.appVersion))
	}
	out := Catalog{Repository: s, Engines: []engine.Definition{}, Profiles: []profile.Definition{}, IncompatibleProfiles: []IncompatibleProfile{}}
	seenEngines, seenProfiles := map[string]bool{}, map[string]bool{}
	for _, entry := range index.Engines {
		if !safeTarget(entry.ManifestTarget) {
			return verifiedView{}, fmt.Errorf("unsafe engine manifest target %q", entry.ManifestTarget)
		}
		key := entry.ID + "\x00" + entry.Version
		if seenEngines[key] {
			return verifiedView{}, fmt.Errorf("duplicate engine release %q", entry.ID)
		}
		seenEngines[key] = true
		raw, e := read(entry.ManifestTarget, maxIndexBytes)
		if e != nil {
			return verifiedView{}, fmt.Errorf("engine %q manifest: %w", entry.ID, e)
		}
		var manifest EngineManifest
		if e = json.Unmarshal(raw, &manifest); e != nil {
			return verifiedView{}, e
		}
		d := manifest.Engine
		if d.ID != entry.ID || d.Version != entry.Version {
			return verifiedView{}, fmt.Errorf("catalog entry identity/version and engine manifest disagree for %q", entry.ID)
		}
		if e = d.Validate(); e != nil {
			return verifiedView{}, e
		}
		if d.Artifact != nil && !safeTarget(d.Artifact.URL) {
			return verifiedView{}, fmt.Errorf("engine %q artifact target is unsafe", d.ID)
		}
		if d.Artifact != nil && d.Artifact.DownloadURL != "" {
			if e := validateExternalArtifactURL(d.Artifact.DownloadURL); e != nil {
				return verifiedView{}, fmt.Errorf("engine %q has invalid upstream artifact URL: %w", d.ID, e)
			}
		}
		out.Engines = append(out.Engines, d)
	}
	for _, definition := range out.Engines {
		if definition.Artifact == nil {
			continue
		}
		info, e := up.GetTargetInfo(definition.Artifact.URL)
		if e != nil {
			return verifiedView{}, fmt.Errorf("engine %q artifact metadata: %w", definition.ID, e)
		}
		if info.Length < 0 || info.Length > maxPackageBytes {
			return verifiedView{}, fmt.Errorf("engine %q artifact exceeds package size limit", definition.ID)
		}
		if definition.Artifact.SizeBytes > 0 && info.Length != definition.Artifact.SizeBytes {
			return verifiedView{}, fmt.Errorf("engine %q artifact length differs from its signed manifest", definition.ID)
		}
		hash := info.Hashes["sha256"]
		if len(hash) == 0 || !strings.EqualFold(hex.EncodeToString(hash), definition.Artifact.SHA256) {
			return verifiedView{}, fmt.Errorf("engine %q artifact digest differs from its signed manifest", definition.ID)
		}
		if definition.Artifact.DownloadURL != "" {
			urlFromMetadata, e := targetExternalURL(info)
			if e != nil || urlFromMetadata != definition.Artifact.DownloadURL {
				return verifiedView{}, fmt.Errorf("engine %q upstream artifact URL does not match its signed TUF target metadata", definition.ID)
			}
		}
		view.targets["@artifact:"+definition.Artifact.URL] = []byte(fmt.Sprintf("%d:%s", info.Length, hex.EncodeToString(hash)))
	}
	for _, entry := range index.Profiles {
		if !safeTarget(entry.ManifestTarget) {
			return verifiedView{}, fmt.Errorf("unsafe profile manifest target %q", entry.ManifestTarget)
		}
		key := entry.ID + "\x00" + entry.Version
		if seenProfiles[key] {
			return verifiedView{}, fmt.Errorf("duplicate profile release %q", entry.ID)
		}
		seenProfiles[key] = true
		raw, e := read(entry.ManifestTarget, maxIndexBytes)
		if e != nil {
			return verifiedView{}, fmt.Errorf("profile %q manifest: %w", entry.ID, e)
		}
		var manifest ProfileManifest
		if e = json.Unmarshal(raw, &manifest); e != nil {
			return verifiedView{}, e
		}
		d := manifest.Profile
		if d.ID != entry.ID || d.Version != entry.Version {
			return verifiedView{}, fmt.Errorf("catalog entry identity/version and profile manifest disagree for %q", entry.ID)
		}
		if e = d.Validate(); e != nil {
			return verifiedView{}, e
		}
		if compatible(m.appVersion, d.MinimumAppVersion, d.MaximumAppVersionExclusive) {
			out.Profiles = append(out.Profiles, d)
		} else {
			out.IncompatibleProfiles = append(out.IncompatibleProfiles, IncompatibleProfile{ID: d.ID, Version: d.Version, Reason: fmt.Sprintf("profile does not support 101conv %s", m.appVersion)})
		}
	}
	view.catalog = out
	view.digest = digestCatalogTargets(view.targets)
	return view, nil
}

func (m *Manager) Preview(ctx context.Context, repoID, profileID string) (InstallPreview, error) {
	return m.CreateInstallPlan(ctx, repoID, profileID, false)
}

func (m *Manager) CreateInstallPlan(ctx context.Context, repoID, profileID string, update bool) (InstallPreview, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	s, err := m.getSource(repoID)
	if err != nil {
		return InstallPreview{}, err
	}
	if !s.Enabled {
		return InstallPreview{}, ErrRepositoryDisabled
	}
	m.tufMu.Lock()
	var view verifiedView
	if s.ID == "101conv-official" && s.Trust == TrustApprovedUnsigned {
		view, err = loadUnsignedView(ctx, s, officialURL, m.appVersion)
	} else if s.Trust == TrustPinned {
		view, err = m.loadViewLocked(ctx, s)
	} else {
		err = fmt.Errorf("repository is browse-only until its trust root is provisioned or an administrator approves unsigned installs")
	}
	m.tufMu.Unlock()
	if err != nil {
		return InstallPreview{}, errors.Join(err, m.recordRefresh(s.ID, err))
	}
	if err = m.recordRefresh(s.ID, nil); err != nil {
		return InstallPreview{}, fmt.Errorf("persist repository status: %w", err)
	}
	p, ok := highestProfile(view.catalog.Profiles, profileID)
	if !ok {
		for _, incompatible := range view.catalog.IncompatibleProfiles {
			if incompatible.ID == profileID {
				return InstallPreview{}, coded("incompatible_version", fmt.Errorf("%w: %s", ErrIncompatible, incompatible.Reason))
			}
		}
		return InstallPreview{}, management.ErrNotFound
	}
	installedProfiles, err := m.engines.ListProfiles(ctx)
	if err != nil {
		return InstallPreview{}, err
	}
	profileExists := false
	preferred := map[string]string{}
	for _, current := range installedProfiles {
		if current.Definition.ID == profileID {
			profileExists = true
			preferred = current.State.ResolvedEngines
			break
		}
	}
	if update && !profileExists {
		return InstallPreview{}, management.ErrNotFound
	}
	if !update && profileExists {
		return InstallPreview{}, fmt.Errorf("profile %q: %w", profileID, management.ErrConflict)
	}
	installed, err := m.engines.ListEngines(ctx)
	if err != nil {
		return InstallPreview{}, err
	}
	engineDefinitions := make(map[string]engine.Definition)
	selections := make(map[string]string)
	preview := InstallPreview{CatalogDigest: view.digest, Repository: view.catalog.Repository, Publisher: view.catalog.Repository.Name, Trust: view.catalog.Repository.Trust, License: p.License, Profile: p, Dependencies: []DependencyPreview{}, PlannedChanges: []string{"install profile " + p.ID + " " + p.Version}}
	for _, req := range p.Engines {
		dep := DependencyPreview{EngineID: req.EngineID, VersionRange: req.Versions, Capabilities: req.Capabilities, Action: "install engine"}
		for _, current := range installed {
			if current.Installation.Version == preferred[req.EngineID] && req.Accepts(current.Definition) && profileExecutablesAvailable(p, current.Installation) {
				dep.InstalledVersion = current.Installation.Version
				dep.EngineName = current.Definition.Name
				dep.EngineLicense = current.Definition.License
				dep.Action = "reuse installed engine"
				break
			}
		}
		if dep.InstalledVersion == "" {
			for _, current := range installed {
				if req.Accepts(current.Definition) && profileExecutablesAvailable(p, current.Installation) && (dep.InstalledVersion == "" || newer(current.Installation.Version, dep.InstalledVersion)) {
					dep.InstalledVersion = current.Installation.Version
					dep.EngineName = current.Definition.Name
					dep.EngineLicense = current.Definition.License
				}
			}
		}
		if dep.InstalledVersion != "" {
			dep.Action = "reuse installed engine"
			selections[req.EngineID] = dep.InstalledVersion
		}
		if dep.Action == "install engine" {
			best := ""
			for _, d := range view.catalog.Engines {
				if req.Accepts(d) && (best == "" || newer(d.Version, best)) {
					best = d.Version
					dep.EngineName = d.Name
					dep.EngineLicense = d.License
				}
			}
			if best == "" {
				return InstallPreview{}, coded("missing_dependency", fmt.Errorf("%w: profile %q needs engine %q range [%s,%s) and capabilities %v, but no compatible engine is available", ErrMissingDependency, p.ID, req.EngineID, req.Versions.Minimum, req.Versions.MaximumExclusive, req.Capabilities))
			}
			selections[req.EngineID] = best
			for _, def := range view.catalog.Engines {
				if def.ID == req.EngineID && def.Version == best {
					engineDefinitions[req.EngineID+"\x00"+best] = def
				}
			}
			preview.PlannedChanges = append(preview.PlannedChanges, fmt.Sprintf("install engine %s %s", req.EngineID, best))
		}
		preview.Dependencies = append(preview.Dependencies, dep)
	}
	installedDigest, err := m.engines.SnapshotDigest()
	if err != nil {
		return InstallPreview{}, err
	}
	planID, err := randomPlanID()
	if err != nil {
		return InstallPreview{}, err
	}
	expires := time.Now().UTC().Add(10 * time.Minute)
	preview.PlanID = planID
	preview.ExpiresAt = expires.Format(time.RFC3339)
	storedPreview, err := cloneJSON(preview)
	if err != nil {
		return InstallPreview{}, err
	}
	m.mu.Lock()
	m.prunePlansLocked(time.Now())
	m.plans[planID] = installationPlan{preview: storedPreview, profile: storedPreview.Profile, engineDefinitions: engineDefinitions, selections: selections, installedDigest: installedDigest, update: update, expires: expires}
	m.mu.Unlock()
	return cloneJSON(preview)
}

func (m *Manager) InstallProfile(ctx context.Context, repoID, profileID string, update bool) (management.ProfileRecord, error) {
	preview, err := m.CreateInstallPlan(ctx, repoID, profileID, update)
	if err != nil {
		return management.ProfileRecord{}, err
	}
	return m.InstallPlan(ctx, preview.PlanID)
}

func (m *Manager) InstallProfilePlan(ctx context.Context, repoID, profileID, planID string, update bool) (management.ProfileRecord, error) {
	m.mu.Lock()
	plan, ok := m.plans[planID]
	m.mu.Unlock()
	if !ok || plan.preview.Repository.ID != repoID || plan.profile.ID != profileID || plan.update != update {
		return management.ProfileRecord{}, ErrStalePlan
	}
	return m.InstallPlan(ctx, planID)
}

func (m *Manager) InstallPlan(ctx context.Context, planID string) (management.ProfileRecord, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.mu.Lock()
	plan, ok := m.plans[planID]
	if ok && time.Now().After(plan.expires) {
		delete(m.plans, planID)
		ok = false
	}
	m.mu.Unlock()
	if !ok {
		return management.ProfileRecord{}, ErrPlanExpired
	}
	source, err := m.getSource(plan.preview.Repository.ID)
	if err != nil {
		return management.ProfileRecord{}, err
	}
	if !source.Enabled {
		return management.ProfileRecord{}, ErrRepositoryDisabled
	}
	unsignedApproval := source.ID == "101conv-official" && source.Trust == TrustApprovedUnsigned
	if source.Trust != TrustPinned && !unsignedApproval {
		return management.ProfileRecord{}, fmt.Errorf("repository approval was removed or trust is unavailable")
	}

	stateDigest, err := m.engines.SnapshotDigest()
	if err != nil {
		return management.ProfileRecord{}, err
	}
	if stateDigest != plan.installedDigest {
		return management.ProfileRecord{}, ErrStalePlan
	}

	m.tufMu.Lock()
	var view verifiedView
	if unsignedApproval {
		view, err = loadUnsignedView(ctx, source, officialURL, m.appVersion)
	} else {
		view, err = m.loadViewLocked(ctx, source)
	}
	if err != nil {
		m.tufMu.Unlock()
		return management.ProfileRecord{}, errors.Join(err, m.recordRefresh(source.ID, err))
	}
	if view.digest != plan.preview.CatalogDigest {
		m.tufMu.Unlock()
		return management.ProfileRecord{}, errors.Join(ErrStalePlan, m.recordRefresh(source.ID, nil))
	}
	var packages []management.VerifiedEnginePackage
	var cleanupPaths []string
	cleanup := func() error {
		var errs []error
		for i := len(cleanupPaths) - 1; i >= 0; i-- {
			if e := os.RemoveAll(cleanupPaths[i]); e != nil {
				errs = append(errs, fmt.Errorf("clean package staging %s: %w", cleanupPaths[i], e))
			}
		}
		if len(errs) > 0 {
			return errors.Join(errs...)
		}
		return nil
	}
	for _, dep := range plan.preview.Dependencies {
		if dep.Action == "reuse installed engine" {
			continue
		}
		key := dep.EngineID + "\x00" + plan.selections[dep.EngineID]
		def, found := plan.engineDefinitions[key]
		if !found || def.Artifact == nil {
			err = fmt.Errorf("planned engine %q has no artifact definition", dep.EngineID)
			break
		}
		var archive []byte
		var e error
		if unsignedApproval {
			archive, e = downloadPackageUnsigned(ctx, def.Artifact, maxPackageBytes)
		} else {
			archive, e = downloadPackageVerified(ctx, view.updater, def.Artifact, maxPackageBytes)
		}
		if e != nil {
			err = e
			break
		}
		sum := sha256.Sum256(archive)
		digest := hex.EncodeToString(sum[:])
		if !strings.EqualFold(digest, def.Artifact.SHA256) {
			if unsignedApproval {
				err = coded("package_integrity_failure", fmt.Errorf("package digest changed for engine %q", def.ID))
			} else {
				err = coded("signature_failure", fmt.Errorf("%w: verified artifact digest changed for engine %q", ErrSignature, def.ID))
			}
			break
		}
		stage, _, e := m.engines.CreateVerifiedStaging()
		if e != nil {
			err = e
			break
		}
		cleanupPaths = append(cleanupPaths, stage)
		extractRoot := filepath.Join(stage, "contents")
		if e = os.Mkdir(extractRoot, 0700); e != nil {
			err = e
			break
		}
		if e = extractArchive(archive, def.Artifact.Format, extractRoot); e != nil {
			err = coded("unsafe_package", fmt.Errorf("%w: %v", ErrUnsafePackage, e))
			break
		}
		packages = append(packages, management.VerifiedEnginePackage{Definition: def, SourceDir: extractRoot, ArtifactHash: digest, RepositoryID: source.ID})
	}
	m.tufMu.Unlock()
	if err != nil {
		statusErr := m.recordRefresh(source.ID, err)
		if cleanupErr := cleanup(); cleanupErr != nil {
			rollbackErrors := []error{cleanupErr}
			if statusErr != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("persist repository status: %w", statusErr))
			}
			return management.ProfileRecord{}, &management.FileTransactionError{Operation: err, Rollback: rollbackErrors}
		}
		if statusErr != nil {
			return management.ProfileRecord{}, errors.Join(err, fmt.Errorf("persist repository status: %w", statusErr))
		}
		return management.ProfileRecord{}, err
	}
	record, installErr := m.engines.InstallVerifiedProfile(plan.profile, source.ID, packages, plan.selections, plan.installedDigest, plan.update)
	cleanupErr := cleanup()
	if cleanupErr != nil {
		if installErr != nil {
			return record, &management.FileTransactionError{Operation: installErr, Rollback: []error{cleanupErr}}
		}
		return record, &management.FileTransactionError{Operation: fmt.Errorf("installation committed but package staging cleanup failed"), Rollback: []error{cleanupErr}}
	}
	if installErr != nil {
		return management.ProfileRecord{}, installErr
	}
	statusErr := m.recordRefresh(source.ID, nil)
	m.mu.Lock()
	delete(m.plans, planID)
	m.mu.Unlock()
	if statusErr != nil {
		return record, coded("status_persistence_failure", fmt.Errorf("profile installation committed, but repository status could not be saved: %w", statusErr))
	}
	return record, nil
}

func (m *Manager) CreateEngineInstallPlan(ctx context.Context, repoID, engineID, engineVersion string) (EngineInstallPreview, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	source, err := m.getSource(repoID)
	if err != nil {
		return EngineInstallPreview{}, err
	}
	if !source.Enabled {
		return EngineInstallPreview{}, ErrRepositoryDisabled
	}
	m.tufMu.Lock()
	var view verifiedView
	if repoID == "101conv-official" && source.Trust == TrustApprovedUnsigned {
		view, err = loadUnsignedView(ctx, source, officialURL, m.appVersion)
	} else if source.Trust == TrustPinned {
		view, err = m.loadViewLocked(ctx, source)
	} else {
		err = fmt.Errorf("repository is browse-only until its trust root is provisioned or an administrator approves unsigned installs")
	}
	m.tufMu.Unlock()
	if err != nil {
		return EngineInstallPreview{}, errors.Join(err, m.recordRefresh(repoID, err))
	}
	if err = m.recordRefresh(repoID, nil); err != nil {
		return EngineInstallPreview{}, fmt.Errorf("persist repository status: %w", err)
	}
	var def engine.Definition
	found := false
	for _, candidate := range view.catalog.Engines {
		if candidate.ID == engineID && candidate.Version == engineVersion {
			def, found = candidate, true
			break
		}
	}
	if !found {
		return EngineInstallPreview{}, management.ErrNotFound
	}
	if def.Artifact == nil {
		return EngineInstallPreview{}, fmt.Errorf("repository engine manifest has no artifact")
	}
	installed, err := m.engines.ListEngines(ctx)
	if err != nil {
		return EngineInstallPreview{}, err
	}
	reuse := false
	for _, record := range installed {
		if record.Installation.EngineID == engineID && record.Installation.Version == engineVersion {
			if !strings.EqualFold(record.Installation.PackageSHA256, def.Artifact.SHA256) {
				return EngineInstallPreview{}, fmt.Errorf("installed engine digest conflicts with repository: %w", management.ErrConflict)
			}
			reuse = true
		}
	}
	stateDigest, err := m.engines.SnapshotDigest()
	if err != nil {
		return EngineInstallPreview{}, err
	}
	planID, err := randomPlanID()
	if err != nil {
		return EngineInstallPreview{}, err
	}
	expires := time.Now().UTC().Add(10 * time.Minute)
	preview := EngineInstallPreview{PlanID: planID, CatalogDigest: view.digest, ExpiresAt: expires.Format(time.RFC3339), Repository: view.catalog.Repository, Publisher: view.catalog.Repository.Name, Trust: view.catalog.Repository.Trust, Engine: def, ArtifactSHA256: def.Artifact.SHA256, Action: "install engine"}
	if reuse {
		preview.Action = "reuse installed engine"
	}
	storedPreview, err := cloneJSON(preview)
	if err != nil {
		return EngineInstallPreview{}, err
	}
	m.mu.Lock()
	m.prunePlansLocked(time.Now())
	definition := def
	m.plans[planID] = installationPlan{enginePreview: &storedPreview, engineOnly: &definition, installedDigest: stateDigest, expires: expires, reuseEngine: reuse}
	m.mu.Unlock()
	return cloneJSON(preview)
}

func (m *Manager) InstallEnginePlan(ctx context.Context, repoID, engineID, engineVersion, planID string) (management.EngineRecord, error) {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	m.mu.Lock()
	plan, ok := m.plans[planID]
	if ok && time.Now().After(plan.expires) {
		delete(m.plans, planID)
		ok = false
	}
	m.mu.Unlock()
	if !ok || plan.enginePreview == nil || plan.enginePreview.Repository.ID != repoID || plan.engineOnly.ID != engineID || plan.engineOnly.Version != engineVersion {
		return management.EngineRecord{}, ErrStalePlan
	}
	source, err := m.getSource(repoID)
	if err != nil {
		return management.EngineRecord{}, err
	}
	if !source.Enabled {
		return management.EngineRecord{}, ErrRepositoryDisabled
	}
	unsignedApproval := source.ID == "101conv-official" && source.Trust == TrustApprovedUnsigned
	if source.Trust != TrustPinned && !unsignedApproval {
		return management.EngineRecord{}, fmt.Errorf("repository approval was removed or trust is unavailable")
	}
	stateDigest, err := m.engines.SnapshotDigest()
	if err != nil {
		return management.EngineRecord{}, err
	}
	if stateDigest != plan.installedDigest {
		return management.EngineRecord{}, ErrStalePlan
	}
	m.tufMu.Lock()
	var view verifiedView
	if unsignedApproval {
		view, err = loadUnsignedView(ctx, source, officialURL, m.appVersion)
	} else {
		view, err = m.loadViewLocked(ctx, source)
	}
	if err != nil {
		m.tufMu.Unlock()
		return management.EngineRecord{}, errors.Join(err, m.recordRefresh(repoID, err))
	}
	if view.digest != plan.enginePreview.CatalogDigest {
		m.tufMu.Unlock()
		return management.EngineRecord{}, errors.Join(ErrStalePlan, m.recordRefresh(repoID, nil))
	}
	if plan.reuseEngine {
		m.tufMu.Unlock()
		installed, e := m.engines.ListEngines(ctx)
		if e != nil {
			return management.EngineRecord{}, e
		}
		for _, record := range installed {
			if record.Installation.EngineID == engineID && record.Installation.Version == engineVersion {
				m.mu.Lock()
				delete(m.plans, planID)
				m.mu.Unlock()
				return record, nil
			}
		}
		return management.EngineRecord{}, ErrStalePlan
	}
	definition := *plan.engineOnly
	if definition.Artifact == nil {
		m.tufMu.Unlock()
		return management.EngineRecord{}, fmt.Errorf("repository engine manifest has no artifact")
	}
	var archive []byte
	if unsignedApproval {
		archive, err = downloadPackageUnsigned(ctx, definition.Artifact, maxPackageBytes)
	} else {
		archive, err = downloadPackageVerified(ctx, view.updater, definition.Artifact, maxPackageBytes)
	}
	m.tufMu.Unlock()
	if err != nil {
		return management.EngineRecord{}, errors.Join(err, m.recordRefresh(repoID, err))
	}
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	if !strings.EqualFold(digest, definition.Artifact.SHA256) {
		if unsignedApproval {
			err = coded("package_integrity_failure", fmt.Errorf("package SHA-256 differs from the unsigned catalog manifest"))
		} else {
			err = coded("signature_failure", fmt.Errorf("%w: package SHA-256 does not match signed engine manifest", ErrSignature))
		}
		return management.EngineRecord{}, errors.Join(err, m.recordRefresh(repoID, err))
	}
	stage, cleanup, err := m.engines.CreateVerifiedStaging()
	if err != nil {
		return management.EngineRecord{}, err
	}
	if err = extractArchive(archive, definition.Artifact.Format, stage); err != nil {
		err = coded("unsafe_package", fmt.Errorf("%w: %v", ErrUnsafePackage, err))
		if cleanErr := cleanup(); cleanErr != nil {
			rollbackErrors := []error{cleanErr}
			if statusErr := m.recordRefresh(repoID, err); statusErr != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("persist repository status: %w", statusErr))
			}
			return management.EngineRecord{}, &management.FileTransactionError{Operation: err, Rollback: rollbackErrors}
		}
		return management.EngineRecord{}, errors.Join(err, m.recordRefresh(repoID, err))
	}
	record, installErr := m.engines.InstallVerifiedEnginePlan(definition, stage, digest, source.ID, plan.installedDigest)
	cleanErr := cleanup()
	if cleanErr != nil {
		if installErr != nil {
			return management.EngineRecord{}, &management.FileTransactionError{Operation: installErr, Rollback: []error{cleanErr}}
		}
		return record, &management.FileTransactionError{Operation: fmt.Errorf("engine installation committed but package staging cleanup failed"), Rollback: []error{cleanErr}}
	}
	if installErr == nil {
		m.mu.Lock()
		delete(m.plans, planID)
		m.mu.Unlock()
		if statusErr := m.recordRefresh(repoID, nil); statusErr != nil {
			return record, coded("status_persistence_failure", fmt.Errorf("engine installation committed, but repository status could not be saved: %w", statusErr))
		}
	} else {
		if statusErr := m.recordRefresh(repoID, installErr); statusErr != nil {
			return record, errors.Join(installErr, fmt.Errorf("persist repository status: %w", statusErr))
		}
	}
	return record, installErr
}

func (m *Manager) InstallEngine(ctx context.Context, repoID, engineID, engineVersion string) (management.EngineRecord, error) {
	preview, err := m.CreateEngineInstallPlan(ctx, repoID, engineID, engineVersion)
	if err != nil {
		return management.EngineRecord{}, err
	}
	return m.InstallEnginePlan(ctx, repoID, engineID, engineVersion, preview.PlanID)
}

func (m *Manager) persist() (retErr error) {
	st := fileState{SchemaVersion: 1, Sources: []storedSource{}, OfficialUnsignedApproved: m.officialUnsignedApproved}
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
	committed := false
	defer func() {
		if committed {
			return
		}
		if cleanupErr := os.Remove(name); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			operation := retErr
			if operation == nil {
				operation = fmt.Errorf("repository state committed but temporary state-file cleanup failed")
			}
			retErr = errors.Join(operation, fmt.Errorf("remove temporary repository state file %s: %w", name, cleanupErr))
		}
	}()
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
	committed = true
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
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0111 == 0 {
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
	case "tar.xz":
		reader, err := (xz.ReaderConfig{DictCap: 128 << 20}).NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		return extractTar(reader, root)
	case "tar.lz":
		reader, err := newSafeLzipReader(data)
		if err != nil {
			return err
		}
		return extractTar(reader, root)
	case "zip":
		return extractZip(data, root)
	default:
		return fmt.Errorf("unsupported archive format %q (supported: tar.gz, tar.xz, tar.lz, zip)", format)
	}
}

func newSafeLzipReader(data []byte) (io.Reader, error) {
	// lzip-go currently buffers the compressed member while checking its
	// trailer. Reject undersized input before parsing and cap the archive's
	// attacker-controlled LZMA dictionary request before the decoder allocates.
	if len(data) < 26 || string(data[:4]) != "LZIP" || data[4] != 1 {
		return nil, fmt.Errorf("invalid or truncated lzip archive")
	}
	code := data[5]
	dictionary := uint64(1) << (code & 0x1f)
	dictionary -= (dictionary / 16) * uint64((code>>5)&0x07)
	if dictionary > maxLzipDictionaryBytes {
		return nil, fmt.Errorf("lzip dictionary exceeds %d-byte limit", maxLzipDictionaryBytes)
	}
	decoder, err := lzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return &validatedLzipReader{decoder: decoder, archive: data}, nil
}

type validatedLzipReader struct {
	decoder   io.Reader
	archive   []byte
	crc       uint32
	size      uint64
	validated bool
}

func (r *validatedLzipReader) Read(p []byte) (int, error) {
	n, err := r.decoder.Read(p)
	if n > 0 {
		r.crc = crc32.Update(r.crc, crc32.IEEETable, p[:n])
		r.size += uint64(n)
	}
	if errors.Is(err, io.EOF) && !r.validated {
		if verifyErr := r.verifyFooter(); verifyErr != nil {
			return n, verifyErr
		}
		r.validated = true
	}
	return n, err
}

func (r *validatedLzipReader) verifyFooter() error {
	if len(r.archive) < 26 {
		return fmt.Errorf("truncated lzip footer")
	}
	footer := r.archive[len(r.archive)-20:]
	if got := binary.LittleEndian.Uint32(footer[:4]); got != r.crc {
		return fmt.Errorf("lzip CRC mismatch")
	}
	if got := binary.LittleEndian.Uint64(footer[4:12]); got != r.size {
		return fmt.Errorf("lzip uncompressed size mismatch")
	}
	if got := binary.LittleEndian.Uint64(footer[12:20]); got != uint64(len(r.archive)) {
		return fmt.Errorf("lzip member size mismatch")
	}
	return nil
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
	return extractTar(gz, root)
}

func extractTar(compressed io.Reader, root string) error {
	tr := tar.NewReader(compressed)
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
	// tar.Reader may stop at the tar end markers before the compression
	// decoder reaches EOF. Drain the bounded remainder so gzip/xz/lzip can
	// validate checksums and footers; malicious trailing expansion stays capped.
	remaining := maxExpandedBytes - total
	n, err := io.Copy(io.Discard, io.LimitReader(compressed, remaining+1))
	if err != nil {
		return err
	}
	if n > remaining {
		return fmt.Errorf("archive exceeds expansion limit")
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
