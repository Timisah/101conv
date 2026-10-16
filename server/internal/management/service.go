// Package management implements the durable engine and profile lifecycle.
// Repository installation can call the verified-package seam only after TUF
// validation; local registration and installation remain path-policy bound.
package management

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/gofrs/flock"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/profile"
	"github.com/timisah/101conv/server/internal/version"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

type ProfileRecord struct {
	Definition profile.Definition   `json:"definition"`
	State      profile.Installation `json:"state"`
}
type EngineRecord struct {
	Definition   engine.Definition   `json:"definition"`
	Installation engine.Installation `json:"installation"`
}

// VerifiedEnginePackage is a fully verified and safely extracted package staged
// by Repository Manager. A profile install commits all supplied engines and the
// profile record together in one JSON state transaction.
type VerifiedEnginePackage struct {
	Definition   engine.Definition
	SourceDir    string
	ArtifactHash string
	RepositoryID string
}

type FileTransactionError struct {
	Operation error
	Rollback  []error
}

func (e *FileTransactionError) Error() string {
	if len(e.Rollback) == 0 {
		return e.Operation.Error()
	}
	parts := make([]string, 0, len(e.Rollback))
	for _, err := range e.Rollback {
		parts = append(parts, err.Error())
	}
	return fmt.Sprintf("%v; rollback incomplete: %s", e.Operation, strings.Join(parts, "; "))
}
func (e *FileTransactionError) Unwrap() error { return e.Operation }

type state struct {
	SchemaVersion         int             `json:"schemaVersion"`
	Engines               []EngineRecord  `json:"engines"`
	Profiles              []ProfileRecord `json:"profiles"`
	SessionTimeoutMinutes int             `json:"sessionTimeoutMinutes,omitempty"`
}

const DefaultSessionTimeoutMinutes = 12 * 60

type Service struct {
	mu           sync.Mutex
	root         string
	stateFile    string
	data         state
	lock         *flock.Flock
	allowedRoots []string
	removeAll    func(string) error
}

func Open(root string) (*Service, error) {
	abs, _ := filepath.Abs(root)
	return OpenWithPolicy(root, []string{filepath.Join(abs, "imports")})
}

// OpenWithPolicy acquires an exclusive lifetime lock. Every process that writes
// this JSON store must use this constructor; multiple app instances are refused.
func OpenWithPolicy(root string, allowedRoots []string) (*Service, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("data directory is required")
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
	if err = os.MkdirAll(filepath.Join(abs, "engines"), 0700); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(abs, "management.lock"))
	locked, err := lock.TryLock()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, fmt.Errorf("management storage is already open by another process")
	}
	cleanRoots := make([]string, 0, len(allowedRoots))
	for _, r := range allowedRoots {
		a, e := filepath.Abs(r)
		if e != nil {
			lock.Unlock()
			return nil, e
		}
		if e = os.MkdirAll(a, 0700); e != nil {
			lock.Unlock()
			return nil, e
		}
		a, e = filepath.EvalSymlinks(a)
		if e != nil {
			lock.Unlock()
			return nil, e
		}
		cleanRoots = append(cleanRoots, a)
	}
	s := &Service{root: abs, stateFile: filepath.Join(abs, "management.json"), lock: lock, allowedRoots: cleanRoots, removeAll: os.RemoveAll, data: state{SchemaVersion: 1, Engines: []EngineRecord{}, Profiles: []ProfileRecord{}, SessionTimeoutMinutes: DefaultSessionTimeoutMinutes}}
	b, err := os.ReadFile(s.stateFile)
	if errors.Is(err, os.ErrNotExist) {
		if err = s.recoverFilesystem(); err != nil {
			lock.Unlock()
			return nil, fmt.Errorf("recover engine installations: %w", err)
		}
		return s, nil
	}
	if err != nil {
		lock.Unlock()
		return nil, err
	}
	if err = json.Unmarshal(b, &s.data); err != nil {
		lock.Unlock()
		return nil, fmt.Errorf("read management state: %w", err)
	}
	if err = s.expandPortablePaths(); err != nil {
		lock.Unlock()
		return nil, err
	}
	if s.data.SchemaVersion != 1 {
		lock.Unlock()
		return nil, fmt.Errorf("unsupported management state schema %d", s.data.SchemaVersion)
	}
	if s.data.Engines == nil {
		s.data.Engines = []EngineRecord{}
	}
	if s.data.Profiles == nil {
		s.data.Profiles = []ProfileRecord{}
	}
	if s.data.SessionTimeoutMinutes == 0 {
		s.data.SessionTimeoutMinutes = DefaultSessionTimeoutMinutes
	}
	if err := s.recoverFilesystem(); err != nil {
		lock.Unlock()
		return nil, fmt.Errorf("recover engine installations: %w", err)
	}
	return s, nil
}

func (s *Service) SessionTimeout() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.SessionTimeoutMinutes == 0 {
		return DefaultSessionTimeoutMinutes
	}
	return s.data.SessionTimeoutMinutes
}

func (s *Service) SetSessionTimeout(minutes int) error {
	if minutes < 5 || minutes > 43200 {
		return fmt.Errorf("session timeout must be between 5 and 43200 minutes")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.data
	next.SessionTimeoutMinutes = minutes
	if err := s.persist(next); err != nil {
		return err
	}
	s.data = next
	return nil
}

func (s *Service) Close() error {
	if s.lock == nil {
		return nil
	}
	err := s.lock.Unlock()
	s.lock = nil
	return err
}

func (s *Service) RootPath() string { return s.root }

func (s *Service) SnapshotDigest() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return digestInstallState(s.data.Engines, s.data.Profiles)
}

func digestInstallState(engines []EngineRecord, profiles []ProfileRecord) (string, error) {
	data, err := json.Marshal(struct {
		Engines  []EngineRecord  `json:"engines"`
		Profiles []ProfileRecord `json:"profiles"`
	}{engines, profiles})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (s *Service) recoverFilesystem() error {
	if err := os.RemoveAll(filepath.Join(s.root, ".repository-staging")); err != nil {
		return fmt.Errorf("remove interrupted repository staging: %w", err)
	}
	for _, pattern := range []string{".management-*.tmp", ".repositories-*.tmp"} {
		matches, err := filepath.Glob(filepath.Join(s.root, pattern))
		if err != nil {
			return err
		}
		for _, tmp := range matches {
			if err = os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove interrupted state file %s: %w", tmp, err)
			}
		}
	}
	if err := s.recoverRemovalTombstones(); err != nil {
		return err
	}
	known := map[string]bool{}
	for _, record := range s.data.Engines {
		if record.Installation.Managed {
			if !safeID(record.Installation.EngineID) {
				return fmt.Errorf("unsafe engine id %q in persisted state", record.Installation.EngineID)
			}
			root, err := filepath.Abs(record.Installation.Root)
			if err != nil {
				return err
			}
			expected, err := filepath.Abs(filepath.Join(s.root, "engines", record.Installation.EngineID, record.Installation.Version))
			if err != nil {
				return err
			}
			if root != expected {
				return fmt.Errorf("engine %q installation root is outside managed storage", record.Installation.EngineID)
			}
			known[root] = true
			if _, err = os.Stat(root); err != nil {
				return fmt.Errorf("engine %q %s is recorded but its installation is missing: %w", record.Installation.EngineID, record.Installation.Version, err)
			}
			for name, path := range record.Installation.ExecutablePaths {
				relative, err := filepath.Rel(root, path)
				if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					return fmt.Errorf("engine %q executable %q escapes its installation", record.Installation.EngineID, name)
				}
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0111 == 0 {
					return fmt.Errorf("engine %q executable %q is missing or unusable", record.Installation.EngineID, name)
				}
			}
		}
	}
	base := filepath.Join(s.root, "engines")
	ids, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !id.IsDir() || strings.HasPrefix(id.Name(), ".") {
			continue
		}
		idPath := filepath.Join(base, id.Name())
		versions, e := os.ReadDir(idPath)
		if e != nil {
			return e
		}
		for _, v := range versions {
			entry := filepath.Join(idPath, v.Name())
			if strings.HasPrefix(v.Name(), ".staging-") {
				if e = os.RemoveAll(entry); e != nil {
					return e
				}
				continue
			}
			if !v.IsDir() {
				continue
			}
			absolute, _ := filepath.Abs(entry)
			if !known[absolute] {
				if e = os.RemoveAll(entry); e != nil {
					return e
				}
			}
		}
	}
	return nil
}

func (s *Service) recoverRemovalTombstones() error {
	base := filepath.Join(s.root, "engines")
	ids, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if !id.IsDir() || id.Type()&os.ModeSymlink != 0 {
			continue
		}
		engineDir := filepath.Join(base, id.Name())
		entries, e := os.ReadDir(engineDir)
		if e != nil {
			return e
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), ".removing-") {
				continue
			}
			entryPath := filepath.Join(engineDir, entry.Name())
			entryInfo, statErr := os.Lstat(entryPath)
			if statErr != nil {
				return fmt.Errorf("inspect interrupted engine-removal tombstone %s: %w", entryPath, statErr)
			}
			if !entryInfo.IsDir() || entryInfo.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("unsafe interrupted engine-removal tombstone %s", entryPath)
			}
			version := strings.TrimPrefix(entry.Name(), ".removing-")
			tombstone := entryPath
			final := filepath.Join(engineDir, version)
			recorded := false
			for _, record := range s.data.Engines {
				if record.Installation.Managed && record.Installation.EngineID == id.Name() && record.Installation.Version == version {
					recorded = true
					break
				}
			}
			if recorded {
				if _, e = os.Lstat(final); errors.Is(e, os.ErrNotExist) {
					if e = os.Rename(tombstone, final); e != nil {
						return fmt.Errorf("restore interrupted engine removal %s: %w", version, e)
					}
				} else if e != nil {
					return e
				} else if e = os.RemoveAll(tombstone); e != nil {
					return e
				}
			} else if e = os.RemoveAll(tombstone); e != nil {
				return fmt.Errorf("remove committed engine-removal tombstone %s: %w", tombstone, e)
			}
		}
	}
	return nil
}

func (s *Service) ListEngines(context.Context) ([]EngineRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]EngineRecord{}, s.data.Engines...)
	return out, nil
}
func (s *Service) ListProfiles(context.Context) ([]ProfileRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]ProfileRecord{}, s.data.Profiles...)
	return out, nil
}

// InstallEngine copies only explicitly named local files into a private staging
// directory. It never fetches URLs or extracts archives. Definition executable
// paths are relative file names under sourceDir.
func (s *Service) InstallEngine(_ context.Context, d engine.Definition, sourceDir string) (EngineRecord, error) {
	return s.installEngine(d, sourceDir, false, "", "", "")
}

// InstallVerifiedEngine is reserved for the repository manager after TUF target
// verification. This function copies only declared executables; it never runs code.
func (s *Service) InstallVerifiedEngine(d engine.Definition, sourceDir, artifactSHA256, repositoryID string) (EngineRecord, error) {
	return s.InstallVerifiedEnginePlan(d, sourceDir, artifactSHA256, repositoryID, "")
}

// InstallVerifiedEnginePlan binds the commit to the management state captured
// by its immutable repository preview.
func (s *Service) InstallVerifiedEnginePlan(d engine.Definition, sourceDir, artifactSHA256, repositoryID, expectedStateDigest string) (EngineRecord, error) {
	if d.Artifact == nil || !strings.EqualFold(d.Artifact.SHA256, artifactSHA256) {
		return EngineRecord{}, fmt.Errorf("verified package digest does not match the engine manifest")
	}
	return s.installEngine(d, sourceDir, true, artifactSHA256, repositoryID, expectedStateDigest)
}

// InstallVerifiedProfile atomically adds missing verified engines and installs
// or updates a profile with exact version selections from an immutable plan.
func (s *Service) InstallVerifiedProfile(d profile.Definition, repositoryID string, packages []VerifiedEnginePackage, selections map[string]string, expectedStateDigest string, update bool) (ProfileRecord, error) {
	d = d.WithInferredType()
	if err := d.Validate(); err != nil {
		return ProfileRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	currentDigest, err := digestInstallState(s.data.Engines, s.data.Profiles)
	if err != nil {
		return ProfileRecord{}, err
	}
	if currentDigest != expectedStateDigest {
		return ProfileRecord{}, fmt.Errorf("management state changed after preview: %w", ErrConflict)
	}

	profileIdx := profileIndex(s.data.Profiles, d.ID)
	if update && profileIdx < 0 {
		return ProfileRecord{}, ErrNotFound
	}
	if !update && profileIdx >= 0 {
		return ProfileRecord{}, fmt.Errorf("profile %q: %w", d.ID, ErrConflict)
	}

	packageByKey := make(map[string]VerifiedEnginePackage, len(packages))
	for _, pkg := range packages {
		if err := pkg.Definition.Validate(); err != nil {
			return ProfileRecord{}, err
		}
		if pkg.Definition.Artifact == nil || !strings.EqualFold(pkg.Definition.Artifact.SHA256, pkg.ArtifactHash) {
			return ProfileRecord{}, fmt.Errorf("engine %q package digest does not match its signed manifest", pkg.Definition.ID)
		}
		if !safeID(pkg.Definition.ID) || pkg.SourceDir == "" {
			return ProfileRecord{}, fmt.Errorf("engine package has an invalid id or staging directory")
		}
		requirementFound := false
		for _, req := range d.Engines {
			if req.EngineID == pkg.Definition.ID && selections[req.EngineID] == pkg.Definition.Version && req.Accepts(pkg.Definition) {
				requirementFound = true
			}
		}
		if !requirementFound {
			return ProfileRecord{}, fmt.Errorf("engine package %q is not part of the selected profile plan", pkg.Definition.ID)
		}
		resolved, err := filepath.EvalSymlinks(pkg.SourceDir)
		if err != nil {
			return ProfileRecord{}, err
		}
		rel, err := filepath.Rel(filepath.Join(s.root, ".repository-staging"), resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ProfileRecord{}, fmt.Errorf("verified package staging directory is outside private storage")
		}
		key := pkg.Definition.ID + "\x00" + pkg.Definition.Version
		if _, exists := packageByKey[key]; exists {
			return ProfileRecord{}, fmt.Errorf("duplicate engine package %q: %w", key, ErrConflict)
		}
		packageByKey[key] = pkg
	}

	available := append([]EngineRecord(nil), s.data.Engines...)
	for key, pkg := range packageByKey {
		found := false
		for _, existing := range available {
			if existing.Installation.EngineID == pkg.Definition.ID && existing.Installation.Version == pkg.Definition.Version {
				found = true
				if !strings.EqualFold(existing.Installation.PackageSHA256, pkg.ArtifactHash) {
					return ProfileRecord{}, fmt.Errorf("engine %q version %s has a conflicting installed digest: %w", pkg.Definition.ID, pkg.Definition.Version, ErrConflict)
				}
			}
		}
		if found {
			delete(packageByKey, key)
		}
	}

	for _, req := range d.Engines {
		selected := selections[req.EngineID]
		if selected == "" {
			return ProfileRecord{}, fmt.Errorf("profile %q has no planned version for engine %q", d.ID, req.EngineID)
		}
		valid := false
		for _, candidate := range available {
			if candidate.Installation.EngineID == req.EngineID && candidate.Installation.Version == selected && req.Accepts(candidate.Definition) {
				valid = true
			}
		}
		if pkg, ok := packageByKey[req.EngineID+"\x00"+selected]; ok && req.Accepts(pkg.Definition) {
			valid = true
		}
		if !valid {
			return ProfileRecord{}, fmt.Errorf("profile %q requires compatible engine %q version %s: %w", d.ID, req.EngineID, selected, ErrNotFound)
		}
	}

	type stagedInstall struct {
		pkg   VerifiedEnginePackage
		stage string
		final string
		rec   EngineRecord
	}
	staged := make([]stagedInstall, 0, len(packageByKey))
	rollback := func(operation error) error {
		var rollbackErrs []error
		for i := len(staged) - 1; i >= 0; i-- {
			if staged[i].stage != "" {
				if err := s.removeAll(staged[i].stage); err != nil {
					rollbackErrs = append(rollbackErrs, fmt.Errorf("remove staging %s: %w", staged[i].stage, err))
				}
			}
			if staged[i].final != "" {
				if err := s.removeAll(staged[i].final); err != nil {
					rollbackErrs = append(rollbackErrs, fmt.Errorf("remove installed files %s: %w", staged[i].final, err))
				}
			}
		}
		if len(rollbackErrs) > 0 {
			return &FileTransactionError{Operation: operation, Rollback: rollbackErrs}
		}
		return operation
	}

	keys := make([]string, 0, len(packageByKey))
	for key := range packageByKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		pkg := packageByKey[key]
		engineRoot := filepath.Join(s.root, "engines", safeSegment(pkg.Definition.ID))
		if err := os.MkdirAll(engineRoot, 0700); err != nil {
			return ProfileRecord{}, rollback(err)
		}
		info, err := os.Lstat(engineRoot)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			if err == nil {
				err = fmt.Errorf("engine storage path must be a real directory")
			}
			return ProfileRecord{}, rollback(err)
		}
		stage, err := os.MkdirTemp(engineRoot, ".staging-")
		if err != nil {
			return ProfileRecord{}, rollback(err)
		}
		item := stagedInstall{pkg: pkg, stage: stage}
		staged = append(staged, item)
		paths := make(map[string]string, len(pkg.Definition.Executables))
		for name, rel := range pkg.Definition.Executables {
			if !safeFilePath(rel) {
				return ProfileRecord{}, rollback(fmt.Errorf("executable %q must be a safe relative file path", name))
			}
			if err = rejectSymlinkPath(pkg.SourceDir, rel); err != nil {
				return ProfileRecord{}, rollback(fmt.Errorf("executable %q: %w", name, err))
			}
			src := filepath.Join(pkg.SourceDir, filepath.FromSlash(rel))
			finfo, e := os.Lstat(src)
			if e != nil || !finfo.Mode().IsRegular() || finfo.Mode()&os.ModeSymlink != 0 || finfo.Mode().Perm()&0111 == 0 {
				if e == nil {
					e = fmt.Errorf("is not a regular executable file")
				}
				return ProfileRecord{}, rollback(fmt.Errorf("executable %q: %w", name, e))
			}
			dest := filepath.Join(stage, filepath.FromSlash(rel))
			if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				return ProfileRecord{}, rollback(err)
			}
			in, e := os.Open(src)
			if e != nil {
				return ProfileRecord{}, rollback(e)
			}
			opened, e := in.Stat()
			if e != nil || !os.SameFile(finfo, opened) {
				in.Close()
				if e == nil {
					e = fmt.Errorf("executable changed during installation")
				}
				return ProfileRecord{}, rollback(e)
			}
			out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
			if e != nil {
				in.Close()
				return ProfileRecord{}, rollback(e)
			}
			_, copyErr := io.Copy(out, in)
			syncErr := out.Sync()
			closeOutErr := out.Close()
			closeInErr := in.Close()
			for _, closeErr := range []error{copyErr, syncErr, closeOutErr, closeInErr} {
				if closeErr != nil {
					return ProfileRecord{}, rollback(closeErr)
				}
			}
			if err = os.Chmod(dest, 0700); err != nil {
				return ProfileRecord{}, rollback(err)
			}
			paths[name] = filepath.Join(engineRoot, pkg.Definition.Version, filepath.FromSlash(rel))
		}
		if err = syncDirectories(stage); err != nil {
			return ProfileRecord{}, rollback(err)
		}
		final := filepath.Join(engineRoot, pkg.Definition.Version)
		if _, err = os.Lstat(final); err == nil {
			return ProfileRecord{}, rollback(fmt.Errorf("engine install path already exists: %w", ErrConflict))
		} else if !errors.Is(err, os.ErrNotExist) {
			return ProfileRecord{}, rollback(err)
		}
		if err = os.Rename(stage, final); err != nil {
			return ProfileRecord{}, rollback(err)
		}
		staged[len(staged)-1].stage = ""
		staged[len(staged)-1].final = final
		if err = syncDirectory(engineRoot); err != nil {
			return ProfileRecord{}, rollback(err)
		}
		rec := EngineRecord{Definition: pkg.Definition, Installation: engine.Installation{EngineID: pkg.Definition.ID, Version: pkg.Definition.Version, Platform: pkg.Definition.Platform, Root: final, ExecutablePaths: paths, Managed: true, PackageSHA256: pkg.ArtifactHash, RepositoryID: pkg.RepositoryID}}
		staged[len(staged)-1].rec = rec
		available = append(available, rec)
	}

	_, pins, err := resolveWithPins(d, available, selections)
	if err != nil {
		return ProfileRecord{}, rollback(err)
	}
	next := s.data
	next.Engines = append([]EngineRecord(nil), s.data.Engines...)
	for _, item := range staged {
		next.Engines = append(next.Engines, item.rec)
	}
	profileState := profile.Installation{ProfileID: d.ID, Version: d.Version, Enabled: true, RepositoryID: repositoryID, ResolvedEngines: pins}
	if update {
		profileState.Enabled = s.data.Profiles[profileIdx].State.Enabled
		next.Profiles = append([]ProfileRecord(nil), s.data.Profiles...)
		next.Profiles[profileIdx] = ProfileRecord{Definition: d, State: profileState}
	} else {
		next.Profiles = append(append([]ProfileRecord(nil), s.data.Profiles...), ProfileRecord{Definition: d, State: profileState})
	}
	if err = s.persist(next); err != nil {
		return ProfileRecord{}, rollback(err)
	}
	s.data = next
	return next.Profiles[profileIndex(next.Profiles, d.ID)], nil
}

func (s *Service) CreateVerifiedStaging() (string, func() error, error) {
	base := filepath.Join(s.root, ".repository-staging")
	if err := os.MkdirAll(base, 0700); err != nil {
		return "", nil, err
	}
	stage, err := os.MkdirTemp(base, "package-")
	if err != nil {
		return "", nil, err
	}
	return stage, func() error { return os.RemoveAll(stage) }, nil
}

func (s *Service) installEngine(d engine.Definition, sourceDir string, verified bool, artifactSHA256, repositoryID, expectedStateDigest string) (record EngineRecord, retErr error) {
	if d.Artifact != nil && !verified {
		return EngineRecord{}, fmt.Errorf("remote engine artifacts are disabled until repository signatures are verified")
	}
	if err := d.Validate(); err != nil {
		return EngineRecord{}, err
	}
	if !safeID(d.ID) {
		return EngineRecord{}, fmt.Errorf("engine id must contain only letters, digits, dot, underscore, or hyphen")
	}
	if sourceDir == "" {
		return EngineRecord{}, fmt.Errorf("local source directory is required")
	}
	resolvedSource, err := filepath.EvalSymlinks(sourceDir)
	if err != nil {
		return EngineRecord{}, err
	}
	if verified {
		rel, e := filepath.Rel(filepath.Join(s.root, ".repository-staging"), resolvedSource)
		if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return EngineRecord{}, fmt.Errorf("verified package staging directory is outside private storage")
		}
	} else if err = s.requireAllowedPath(resolvedSource); err != nil {
		return EngineRecord{}, fmt.Errorf("source directory: %w", err)
	}
	sourceDir = resolvedSource
	s.mu.Lock()
	defer s.mu.Unlock()
	if expectedStateDigest != "" {
		currentDigest, digestErr := digestInstallState(s.data.Engines, s.data.Profiles)
		if digestErr != nil {
			return EngineRecord{}, digestErr
		}
		if currentDigest != expectedStateDigest {
			return EngineRecord{}, fmt.Errorf("management state changed after preview: %w", ErrConflict)
		}
	}
	for _, r := range s.data.Engines {
		if r.Installation.EngineID == d.ID && r.Installation.Version == d.Version {
			return EngineRecord{}, fmt.Errorf("engine %q version %s: %w", d.ID, d.Version, ErrConflict)
		}
	}
	engineRoot := filepath.Join(s.root, "engines", safeSegment(d.ID))
	if err := os.MkdirAll(engineRoot, 0700); err != nil {
		return EngineRecord{}, err
	}
	engineRootInfo, err := os.Lstat(engineRoot)
	if err != nil {
		return EngineRecord{}, err
	}
	if !engineRootInfo.IsDir() || engineRootInfo.Mode()&os.ModeSymlink != 0 {
		return EngineRecord{}, fmt.Errorf("engine storage path must be a real directory")
	}
	stage, err := os.MkdirTemp(engineRoot, ".staging-")
	if err != nil {
		return EngineRecord{}, err
	}
	defer func() {
		if cleanupErr := s.removeAll(stage); cleanupErr != nil {
			operation := retErr
			if operation == nil {
				operation = fmt.Errorf("engine installation committed but staging cleanup failed")
			}
			retErr = &FileTransactionError{Operation: operation, Rollback: []error{fmt.Errorf("remove staging %s: %w", stage, cleanupErr)}}
		}
	}()
	paths := make(map[string]string, len(d.Executables))
	for name, rel := range d.Executables {
		if !safeFilePath(rel) {
			return EngineRecord{}, fmt.Errorf("executable %q must be a safe relative file path", name)
		}
		src := filepath.Join(sourceDir, filepath.FromSlash(rel))
		if err := rejectSymlinkPath(sourceDir, rel); err != nil {
			return EngineRecord{}, fmt.Errorf("executable %q: %w", name, err)
		}
		info, e := os.Lstat(src)
		if e != nil {
			return EngineRecord{}, fmt.Errorf("executable %q: %w", name, e)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return EngineRecord{}, fmt.Errorf("executable %q is not a regular file", name)
		}
		if info.Mode().Perm()&0111 == 0 {
			return EngineRecord{}, fmt.Errorf("executable %q has no execute permission", name)
		}
		dest := filepath.Join(stage, filepath.FromSlash(rel))
		if e = os.MkdirAll(filepath.Dir(dest), 0700); e != nil {
			return EngineRecord{}, e
		}
		in, e := os.Open(src)
		if e != nil {
			return EngineRecord{}, e
		}
		openedInfo, e := in.Stat()
		if e != nil || !os.SameFile(info, openedInfo) {
			in.Close()
			if e != nil {
				return EngineRecord{}, e
			}
			return EngineRecord{}, fmt.Errorf("executable %q changed while being installed", name)
		}
		out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if e != nil {
			in.Close()
			return EngineRecord{}, e
		}
		_, copyErr := io.Copy(out, in)
		syncErr := out.Sync()
		closeOutErr := out.Close()
		closeInErr := in.Close()
		for _, x := range []error{copyErr, syncErr, closeOutErr, closeInErr} {
			if x != nil {
				return EngineRecord{}, x
			}
		}
		if e = os.Chmod(dest, 0700); e != nil {
			return EngineRecord{}, e
		}
		paths[name] = filepath.Join(engineRoot, d.Version, filepath.FromSlash(rel))
	}
	if err = syncDirectories(stage); err != nil {
		return EngineRecord{}, err
	}
	final := filepath.Join(engineRoot, d.Version)
	if _, e := os.Lstat(final); e == nil {
		return EngineRecord{}, fmt.Errorf("engine install path already exists: %w", ErrConflict)
	} else if !errors.Is(e, os.ErrNotExist) {
		return EngineRecord{}, e
	}
	if err = os.Rename(stage, final); err != nil {
		return EngineRecord{}, err
	}
	if err = syncDirectory(engineRoot); err != nil {
		if cleanupErr := s.removeAll(final); cleanupErr != nil {
			return EngineRecord{}, &FileTransactionError{Operation: err, Rollback: []error{fmt.Errorf("remove installed files %s: %w", final, cleanupErr)}}
		}
		return EngineRecord{}, err
	}
	inst := engine.Installation{EngineID: d.ID, Version: d.Version, Platform: d.Platform, Root: final, ExecutablePaths: paths, Managed: true, PackageSHA256: artifactSHA256, RepositoryID: repositoryID}
	record = EngineRecord{Definition: d, Installation: inst}
	next := s.data
	next.Engines = append(append([]EngineRecord(nil), s.data.Engines...), record)
	if err = s.persist(next); err != nil {
		if cleanupErr := s.removeAll(final); cleanupErr != nil {
			return EngineRecord{}, &FileTransactionError{Operation: err, Rollback: []error{fmt.Errorf("remove uncommitted files %s: %w", final, cleanupErr)}}
		}
		return EngineRecord{}, err
	}
	s.data = next
	return record, nil
}

// RegisterManualEngine records administrator-provided executable paths without
// copying or taking ownership of the binaries.
func (s *Service) RegisterManualEngine(_ context.Context, d engine.Definition, paths map[string]string) (EngineRecord, error) {
	if d.Artifact != nil {
		return EngineRecord{}, fmt.Errorf("remote engine artifacts are disabled")
	}
	if err := d.Validate(); err != nil {
		return EngineRecord{}, err
	}
	if !safeID(d.ID) {
		return EngineRecord{}, fmt.Errorf("engine id must contain only letters, digits, dot, underscore, or hyphen")
	}
	validatedPaths := make(map[string]string, len(d.Executables))
	for name := range d.Executables {
		p := paths[name]
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			return EngineRecord{}, fmt.Errorf("executable %q: %w", name, err)
		}
		if err = s.requireAllowedPath(resolved); err != nil {
			return EngineRecord{}, fmt.Errorf("executable %q: %w", name, err)
		}
		p = resolved
		info, err := os.Stat(p)
		if err != nil {
			return EngineRecord{}, fmt.Errorf("executable %q: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return EngineRecord{}, fmt.Errorf("executable %q is not a regular file", name)
		}
		if info.Mode().Perm()&0111 == 0 {
			return EngineRecord{}, fmt.Errorf("executable %q has no execute permission", name)
		}
		validatedPaths[name] = p
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.data.Engines {
		if r.Installation.EngineID == d.ID && r.Installation.Version == d.Version {
			return EngineRecord{}, ErrConflict
		}
	}
	inst := engine.Installation{EngineID: d.ID, Version: d.Version, Platform: d.Platform, ExecutablePaths: validatedPaths, Managed: false}
	rec := EngineRecord{Definition: d, Installation: inst}
	next := s.data
	next.Engines = append(append([]EngineRecord(nil), s.data.Engines...), rec)
	if err := s.persist(next); err != nil {
		return EngineRecord{}, err
	}
	s.data = next
	return rec, nil
}

// UpdateEngine installs a new immutable version beside existing releases. Active
// profiles keep their selected version until explicitly upgraded or rolled back.
func (s *Service) UpdateEngine(ctx context.Context, id string, d engine.Definition, sourceDir string) (EngineRecord, error) {
	if id != d.ID {
		return EngineRecord{}, fmt.Errorf("engine id must match the URL")
	}
	s.mu.Lock()
	exists := false
	for _, record := range s.data.Engines {
		if record.Installation.EngineID == id {
			exists = true
			break
		}
	}
	s.mu.Unlock()
	if !exists {
		return EngineRecord{}, ErrNotFound
	}
	return s.InstallEngine(ctx, d, sourceDir)
}

func (s *Service) InstallProfile(_ context.Context, d profile.Definition, repositoryID string) (ProfileRecord, error) {
	d = d.WithInferredType()
	if err := d.Validate(); err != nil {
		return ProfileRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.data.Profiles {
		if p.Definition.ID == d.ID {
			return ProfileRecord{}, fmt.Errorf("profile %q: %w", d.ID, ErrConflict)
		}
	}
	_, pins, err := resolveWithPins(d, s.data.Engines, nil)
	if err != nil {
		return ProfileRecord{}, err
	}
	rec := ProfileRecord{Definition: d, State: profile.Installation{ProfileID: d.ID, Version: d.Version, Enabled: true, RepositoryID: repositoryID, ResolvedEngines: pins}}
	next := s.data
	next.Profiles = append(append([]ProfileRecord(nil), s.data.Profiles...), rec)
	if err := s.persist(next); err != nil {
		return ProfileRecord{}, err
	}
	s.data = next
	return rec, nil
}

func (s *Service) UpdateProfile(_ context.Context, d profile.Definition, repositoryID string) (ProfileRecord, error) {
	d = d.WithInferredType()
	if err := d.Validate(); err != nil {
		return ProfileRecord{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, p := range s.data.Profiles {
		if p.Definition.ID == d.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ProfileRecord{}, ErrNotFound
	}
	_, pins, err := resolveWithPins(d, s.data.Engines, s.data.Profiles[idx].State.ResolvedEngines)
	if err != nil {
		return ProfileRecord{}, err
	}
	rec := s.data.Profiles[idx]
	rec.Definition = d
	rec.State.Version = d.Version
	rec.State.RepositoryID = repositoryID
	rec.State.ResolvedEngines = pins
	next := s.data
	next.Profiles = append([]ProfileRecord(nil), s.data.Profiles...)
	next.Profiles[idx] = rec
	if err := s.persist(next); err != nil {
		return ProfileRecord{}, err
	}
	s.data = next
	return rec, nil
}

// SelectProfileEngine explicitly upgrades or rolls back one profile's engine pin.
func (s *Service) SelectProfileEngine(_ context.Context, profileID, engineID, engineVersion string) (ProfileRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := profileIndex(s.data.Profiles, profileID)
	if idx < 0 {
		return ProfileRecord{}, ErrNotFound
	}
	rec := s.data.Profiles[idx]
	requirementFound, versionFound := false, false
	for _, req := range rec.Definition.Engines {
		if req.EngineID == engineID {
			requirementFound = true
			for _, candidate := range s.data.Engines {
				if candidate.Installation.EngineID == engineID && candidate.Installation.Version == engineVersion && req.Accepts(candidate.Definition) {
					versionFound = true
				}
			}
		}
	}
	if !requirementFound {
		return ProfileRecord{}, fmt.Errorf("profile %q does not require engine %q", profileID, engineID)
	}
	if !versionFound {
		return ProfileRecord{}, fmt.Errorf("engine %q version %s is not installed or is incompatible with profile %q", engineID, engineVersion, profileID)
	}
	pins := make(map[string]string, len(rec.State.ResolvedEngines))
	for k, v := range rec.State.ResolvedEngines {
		pins[k] = v
	}
	pins[engineID] = engineVersion
	_, selected, err := resolveWithPins(rec.Definition, s.data.Engines, pins)
	if err != nil {
		return ProfileRecord{}, err
	}
	rec.State.ResolvedEngines = selected
	next := s.data
	next.Profiles = append([]ProfileRecord(nil), s.data.Profiles...)
	next.Profiles[idx] = rec
	if err = s.persist(next); err != nil {
		return ProfileRecord{}, err
	}
	s.data = next
	return rec, nil
}
func (s *Service) SetProfileEnabled(_ context.Context, id string, enabled bool) (ProfileRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := profileIndex(s.data.Profiles, id)
	if idx < 0 {
		return ProfileRecord{}, ErrNotFound
	}
	rec := s.data.Profiles[idx]
	if enabled {
		_, pins, err := resolveWithPins(rec.Definition, s.data.Engines, rec.State.ResolvedEngines)
		if err != nil {
			return ProfileRecord{}, err
		}
		rec.State.ResolvedEngines = pins
	}
	rec.State.Enabled = enabled
	next := s.data
	next.Profiles = append([]ProfileRecord(nil), s.data.Profiles...)
	next.Profiles[idx] = rec
	if err := s.persist(next); err != nil {
		return ProfileRecord{}, err
	}
	s.data = next
	return rec, nil
}
func (s *Service) RemoveProfile(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := profileIndex(s.data.Profiles, id)
	if idx < 0 {
		return ErrNotFound
	}
	next := s.data
	next.Profiles = append([]ProfileRecord(nil), s.data.Profiles[:idx]...)
	next.Profiles = append(next.Profiles, s.data.Profiles[idx+1:]...)
	if err := s.persist(next); err != nil {
		return err
	}
	s.data = next
	return nil
}

// RemoveEngine rejects removal while enabled profiles depend on this exact
// installation. resolveDependencies disables the conflicting profiles atomically.
func (s *Service) RemoveEngine(_ context.Context, id, version string, resolveDependencies bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := engineIndex(s.data.Engines, id, version)
	if idx < 0 {
		return ErrNotFound
	}
	conflicts := []int{}
	removed := s.data.Engines[idx]
	remaining := append([]EngineRecord(nil), s.data.Engines[:idx]...)
	remaining = append(remaining, s.data.Engines[idx+1:]...)
	resolvedProfiles := append([]ProfileRecord(nil), s.data.Profiles...)
	for i, p := range s.data.Profiles {
		if !p.State.Enabled {
			continue
		}
		_, pins, err := resolveWithPins(p.Definition, remaining, p.State.ResolvedEngines)
		if err != nil {
			conflicts = append(conflicts, i)
		} else {
			resolvedProfiles[i].State.ResolvedEngines = pins
		}
	}
	if len(conflicts) > 0 && !resolveDependencies {
		return fmt.Errorf("engine %q %s is required by enabled profiles; resolve dependencies explicitly: %w", id, version, ErrConflict)
	}
	tombstone := ""
	if removed.Installation.Managed && removed.Installation.Root != "" {
		engineDir := filepath.Dir(removed.Installation.Root)
		info, err := os.Lstat(engineDir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("engine storage path must be a real directory")
		}
		rootInfo, err := os.Lstat(removed.Installation.Root)
		if err != nil {
			return err
		}
		if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("engine installation root must be a real directory")
		}
		reservation, err := os.CreateTemp(engineDir, ".removing-")
		if err != nil {
			return err
		}
		tombstone = reservation.Name()
		if err = reservation.Close(); err != nil {
			if removeErr := os.Remove(tombstone); removeErr != nil {
				return &FileTransactionError{Operation: err, Rollback: []error{fmt.Errorf("remove removal reservation %s: %w", tombstone, removeErr)}}
			}
			return err
		}
		if err = os.Remove(tombstone); err != nil {
			return err
		}
		tombstone = filepath.Join(engineDir, ".removing-"+version)
		if _, err = os.Lstat(tombstone); err == nil {
			return fmt.Errorf("engine removal is already pending: %w", ErrConflict)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err = os.Rename(removed.Installation.Root, tombstone); err != nil {
			return err
		}
		if err = syncDirectory(engineDir); err != nil {
			if rollbackErr := os.Rename(tombstone, removed.Installation.Root); rollbackErr != nil {
				return &FileTransactionError{Operation: err, Rollback: []error{rollbackErr}}
			}
			return err
		}
	}
	next := s.data
	next.Engines = append([]EngineRecord(nil), s.data.Engines...)
	next.Engines = append(next.Engines[:idx], next.Engines[idx+1:]...)
	next.Profiles = resolvedProfiles
	for _, i := range conflicts {
		next.Profiles[i].State.Enabled = false
	}
	if err := s.persist(next); err != nil {
		if tombstone != "" {
			if rollbackErr := os.Rename(tombstone, removed.Installation.Root); rollbackErr != nil {
				return &FileTransactionError{Operation: err, Rollback: []error{rollbackErr}}
			}
			if syncErr := syncDirectory(filepath.Dir(removed.Installation.Root)); syncErr != nil {
				return &FileTransactionError{Operation: err, Rollback: []error{syncErr}}
			}
		}
		return err
	}
	s.data = next
	if tombstone != "" {
		if err := s.removeAll(tombstone); err != nil {
			return &FileTransactionError{Operation: fmt.Errorf("engine removal committed; filesystem cleanup is pending"), Rollback: []error{fmt.Errorf("remove tombstone %s: %w", tombstone, err)}}
		}
		if err := syncDirectory(filepath.Dir(removed.Installation.Root)); err != nil {
			return &FileTransactionError{Operation: fmt.Errorf("engine removal committed; directory sync is pending"), Rollback: []error{err}}
		}
	}
	return nil
}

func (s *Service) ResolveProfile(id string) ([]engine.Installation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := profileIndex(s.data.Profiles, id)
	if idx < 0 {
		return nil, ErrNotFound
	}
	resolved, _, err := resolveWithPins(s.data.Profiles[idx].Definition, s.data.Engines, s.data.Profiles[idx].State.ResolvedEngines)
	return resolved, err
}

// ResolveConversion selects an enabled profile rule and its currently pinned
// engine installation. Clients supply formats, never executable paths or argv.
func (s *Service) ResolveConversion(profileID, sourceFormat, targetFormat string) (profile.Definition, profile.ConversionRule, engine.Definition, engine.Installation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := profileIndex(s.data.Profiles, profileID)
	if idx < 0 {
		return profile.Definition{}, profile.ConversionRule{}, engine.Definition{}, engine.Installation{}, ErrNotFound
	}
	p := s.data.Profiles[idx]
	if !p.State.Enabled {
		return profile.Definition{}, profile.ConversionRule{}, engine.Definition{}, engine.Installation{}, fmt.Errorf("profile %q is disabled: %w", profileID, ErrConflict)
	}
	if err := p.Definition.Validate(); err != nil {
		return profile.Definition{}, profile.ConversionRule{}, engine.Definition{}, engine.Installation{}, fmt.Errorf("installed profile is invalid: %w", err)
	}
	sourceFormat = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(sourceFormat)), ".")
	targetFormat = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(targetFormat)), ".")
	var selected *profile.ConversionRule
	for i := range p.Definition.Rules {
		rule := &p.Definition.Rules[i]
		matches := false
		for _, ext := range rule.Sources {
			if strings.EqualFold(strings.TrimPrefix(ext, "."), sourceFormat) {
				for _, out := range rule.Targets {
					if strings.EqualFold(strings.TrimPrefix(out, "."), targetFormat) {
						matches = true
						break
					}
				}
			}
		}
		if matches {
			if selected != nil {
				return profile.Definition{}, profile.ConversionRule{}, engine.Definition{}, engine.Installation{}, fmt.Errorf("profile %q has ambiguous rules for %s to %s: %w", profileID, sourceFormat, targetFormat, ErrConflict)
			}
			selected = rule
		}
	}
	if selected == nil {
		return profile.Definition{}, profile.ConversionRule{}, engine.Definition{}, engine.Installation{}, fmt.Errorf("profile %q does not support %s to %s", profileID, sourceFormat, targetFormat)
	}
	resolved, _, err := resolveWithPins(p.Definition, s.data.Engines, p.State.ResolvedEngines)
	if err != nil {
		return profile.Definition{}, profile.ConversionRule{}, engine.Definition{}, engine.Installation{}, err
	}
	for i, requirement := range p.Definition.Engines {
		if requirement.EngineID != selected.EngineID {
			continue
		}
		for _, record := range s.data.Engines {
			if record.Installation.EngineID == resolved[i].EngineID && record.Installation.Version == resolved[i].Version {
				if !record.Installation.Managed {
					return profile.Definition{}, profile.ConversionRule{}, engine.Definition{}, engine.Installation{}, fmt.Errorf("worker requires a managed engine installation")
				}
				if record.Installation.ExecutablePaths[selected.Executable] == "" || record.Definition.Executables[selected.Executable] == "" {
					return profile.Definition{}, profile.ConversionRule{}, engine.Definition{}, engine.Installation{}, fmt.Errorf("profile executable is not part of the managed engine")
				}
				return p.Definition, *selected, record.Definition, record.Installation, nil
			}
		}
	}
	return profile.Definition{}, profile.ConversionRule{}, engine.Definition{}, engine.Installation{}, fmt.Errorf("profile engine %q is not resolved", selected.EngineID)
}

func resolve(d profile.Definition, engines []EngineRecord) ([]engine.Installation, error) {
	resolved, _, err := resolveWithPins(d, engines, nil)
	return resolved, err
}

func resolveWithPins(d profile.Definition, engines []EngineRecord, preferred map[string]string) ([]engine.Installation, map[string]string, error) {
	out := make([]engine.Installation, 0, len(d.Engines))
	pins := make(map[string]string, len(d.Engines))
	for _, req := range d.Engines {
		var choices []EngineRecord
		for _, candidate := range engines {
			if req.Accepts(candidate.Definition) {
				choices = append(choices, candidate)
			}
		}
		if len(choices) == 0 {
			return nil, nil, fmt.Errorf("profile %q requires engine %q version range [%s,%s) with capabilities %v; no installed engine matches", d.ID, req.EngineID, req.Versions.Minimum, req.Versions.MaximumExclusive, req.Capabilities)
		}
		sort.Slice(choices, func(i, j int) bool {
			cmp, _ := version.Compare(choices[i].Installation.Version, choices[j].Installation.Version)
			if cmp != 0 {
				return cmp > 0
			}
			return choices[i].Installation.Version < choices[j].Installation.Version
		})
		chosenIndex := 0
		if preferred[req.EngineID] != "" {
			for i, candidate := range choices {
				if candidate.Installation.Version == preferred[req.EngineID] {
					chosenIndex = i
					break
				}
			}
		}
		chosen := choices[chosenIndex].Installation
		for _, rule := range d.Rules {
			if rule.EngineID != req.EngineID {
				continue
			}
			path := chosen.ExecutablePaths[rule.Executable]
			if path == "" {
				return nil, nil, fmt.Errorf("profile %q requires executable %q from engine %q, but it is not registered", d.ID, rule.Executable, req.EngineID)
			}
			info, err := os.Lstat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0111 == 0 {
				return nil, nil, fmt.Errorf("profile %q requires executable %q from engine %q, but it is unavailable or not executable", d.ID, rule.Executable, req.EngineID)
			}
		}
		out = append(out, chosen)
		pins[req.EngineID] = chosen.Version
	}
	return out, pins, nil
}
func (s *Service) persist(next state) (retErr error) {
	stored := next
	stored.Engines = append([]EngineRecord(nil), next.Engines...)
	for i := range stored.Engines {
		r := &stored.Engines[i]
		if !r.Installation.Managed {
			continue
		}
		root, err := filepath.Rel(s.root, r.Installation.Root)
		if err != nil {
			return err
		}
		r.Installation.Root = root
		paths := make(map[string]string, len(r.Installation.ExecutablePaths))
		for name, p := range r.Installation.ExecutablePaths {
			rel, e := filepath.Rel(filepath.Join(s.root, root), p)
			if e != nil {
				return e
			}
			paths[name] = rel
		}
		r.Installation.ExecutablePaths = paths
	}
	b, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, ".management-*.tmp")
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
				operation = fmt.Errorf("state committed but temporary state-file cleanup failed")
			}
			retErr = &FileTransactionError{Operation: operation, Rollback: []error{fmt.Errorf("remove temporary state file %s: %w", name, cleanupErr)}}
		}
	}()
	if _, err = tmp.Write(b); err != nil {
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
	if err = os.Rename(name, s.stateFile); err != nil {
		return err
	}
	committed = true
	dir, err := os.Open(s.root)
	if err != nil {
		return nil
	} // the rename is committed; keep in-memory and disk state consistent.
	defer dir.Close()
	_ = dir.Sync() // a post-rename sync error cannot safely be reported as a failed transaction.
	return nil
}

func (s *Service) expandPortablePaths() error {
	for i := range s.data.Engines {
		r := &s.data.Engines[i]
		if !r.Installation.Managed {
			continue
		}
		root := r.Installation.Root
		if !filepath.IsAbs(root) {
			root = filepath.Join(s.root, root)
		}
		r.Installation.Root = filepath.Clean(root)
		for name, p := range r.Installation.ExecutablePaths {
			if !filepath.IsAbs(p) {
				r.Installation.ExecutablePaths[name] = filepath.Join(r.Installation.Root, p)
			}
		}
	}
	return nil
}

func (s *Service) requireAllowedPath(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for _, root := range s.allowedRoots {
		rel, e := filepath.Rel(root, abs)
		if e == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))) {
			return nil
		}
	}
	return fmt.Errorf("path is outside administrator-approved import roots")
}

func syncDirectory(name string) error {
	dir, err := os.Open(name)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func syncDirectories(root string) error {
	var dirs []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err = syncDirectory(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

var safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

func safeID(v string) bool        { return safeIDPattern.MatchString(v) && v != "." && v != ".." }
func safeSegment(v string) string { return v }
func safeFilePath(v string) bool {
	if v == "" || filepath.IsAbs(v) || strings.Contains(v, `\`) {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(v), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
func profileIndex(v []ProfileRecord, id string) int {
	for i, r := range v {
		if r.Definition.ID == id {
			return i
		}
	}
	return -1
}
func engineIndex(v []EngineRecord, id, ver string) int {
	for i, r := range v {
		if r.Installation.EngineID == id && r.Installation.Version == ver {
			return i
		}
	}
	return -1
}
func clone(v map[string]string) map[string]string {
	o := make(map[string]string, len(v))
	for k, x := range v {
		o[k] = x
	}
	return o
}

func rejectSymlinkPath(root, rel string) error {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("source directory must be a real directory")
	}
	current := root
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		current = filepath.Join(current, filepath.FromSlash(part))
		info, e := os.Lstat(current)
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic links are not allowed")
		}
		if current != filepath.Join(root, filepath.FromSlash(rel)) && !info.IsDir() {
			return fmt.Errorf("executable path parent is not a directory")
		}
	}
	return nil
}
