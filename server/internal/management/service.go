// Package management implements durable local engine and profile lifecycle.
// Remote package installation is intentionally unsupported until repositories
// can authenticate their manifests.
package management

import (
	"context"
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
type state struct {
	SchemaVersion int             `json:"schemaVersion"`
	Engines       []EngineRecord  `json:"engines"`
	Profiles      []ProfileRecord `json:"profiles"`
}

type Service struct {
	mu        sync.Mutex
	root      string
	stateFile string
	data      state
}

func Open(root string) (*Service, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Join(abs, "engines"), 0700); err != nil {
		return nil, err
	}
	s := &Service{root: abs, stateFile: filepath.Join(abs, "management.json"), data: state{SchemaVersion: 1, Engines: []EngineRecord{}, Profiles: []ProfileRecord{}}}
	b, err := os.ReadFile(s.stateFile)
	if errors.Is(err, os.ErrNotExist) {
		if err = s.recoverFilesystem(); err != nil {
			return nil, fmt.Errorf("recover engine installations: %w", err)
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(b, &s.data); err != nil {
		return nil, fmt.Errorf("read management state: %w", err)
	}
	if s.data.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported management state schema %d", s.data.SchemaVersion)
	}
	if s.data.Engines == nil {
		s.data.Engines = []EngineRecord{}
	}
	if s.data.Profiles == nil {
		s.data.Profiles = []ProfileRecord{}
	}
	if err := s.recoverFilesystem(); err != nil {
		return nil, fmt.Errorf("recover engine installations: %w", err)
	}
	return s, nil
}

func (s *Service) recoverFilesystem() error {
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
	if d.Artifact != nil {
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
	s.mu.Lock()
	defer s.mu.Unlock()
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
	defer os.RemoveAll(stage)
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
		_ = os.RemoveAll(final)
		return EngineRecord{}, err
	}
	inst := engine.Installation{EngineID: d.ID, Version: d.Version, Platform: d.Platform, Root: final, ExecutablePaths: paths, Managed: true}
	record := EngineRecord{Definition: d, Installation: inst}
	next := s.data
	next.Engines = append(append([]EngineRecord(nil), s.data.Engines...), record)
	if err = s.persist(next); err != nil {
		_ = os.RemoveAll(final)
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
	for name := range d.Executables {
		p := paths[name]
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
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.data.Engines {
		if r.Installation.EngineID == d.ID && r.Installation.Version == d.Version {
			return EngineRecord{}, ErrConflict
		}
	}
	inst := engine.Installation{EngineID: d.ID, Version: d.Version, Platform: d.Platform, ExecutablePaths: clone(paths), Managed: false}
	rec := EngineRecord{Definition: d, Installation: inst}
	next := s.data
	next.Engines = append(append([]EngineRecord(nil), s.data.Engines...), rec)
	if err := s.persist(next); err != nil {
		return EngineRecord{}, err
	}
	s.data = next
	return rec, nil
}

// UpdateEngine installs a new immutable version beside the existing one. Active
// profiles continue resolving the newest version in their declared range.
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
	if _, err := resolve(d, s.data.Engines); err != nil {
		return ProfileRecord{}, err
	}
	rec := ProfileRecord{Definition: d, State: profile.Installation{ProfileID: d.ID, Version: d.Version, Enabled: true, RepositoryID: repositoryID}}
	next := s.data
	next.Profiles = append(append([]ProfileRecord(nil), s.data.Profiles...), rec)
	if err := s.persist(next); err != nil {
		return ProfileRecord{}, err
	}
	s.data = next
	return rec, nil
}

func (s *Service) UpdateProfile(_ context.Context, d profile.Definition, repositoryID string) (ProfileRecord, error) {
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
	if _, err := resolve(d, s.data.Engines); err != nil {
		return ProfileRecord{}, err
	}
	rec := s.data.Profiles[idx]
	rec.Definition = d
	rec.State.Version = d.Version
	rec.State.RepositoryID = repositoryID
	next := s.data
	next.Profiles = append([]ProfileRecord(nil), s.data.Profiles...)
	next.Profiles[idx] = rec
	if err := s.persist(next); err != nil {
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
		if _, err := resolve(rec.Definition, s.data.Engines); err != nil {
			return ProfileRecord{}, err
		}
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
	candidate := removed.Definition
	for i, p := range s.data.Profiles {
		if !p.State.Enabled {
			continue
		}
		for _, req := range p.Definition.Engines {
			if req.EngineID == id && req.Accepts(candidate) {
				conflicts = append(conflicts, i)
				break
			}
		}
	}
	if len(conflicts) > 0 && !resolveDependencies {
		return fmt.Errorf("engine %q %s is required by enabled profiles; resolve dependencies explicitly: %w", id, version, ErrConflict)
	}
	next := s.data
	next.Engines = append([]EngineRecord(nil), s.data.Engines...)
	next.Engines = append(next.Engines[:idx], next.Engines[idx+1:]...)
	next.Profiles = append([]ProfileRecord(nil), s.data.Profiles...)
	for _, i := range conflicts {
		next.Profiles[i].State.Enabled = false
	}
	if err := s.persist(next); err != nil {
		return err
	}
	s.data = next
	if removed.Installation.Managed && removed.Installation.Root != "" {
		_ = os.RemoveAll(removed.Installation.Root)
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
	return resolve(s.data.Profiles[idx].Definition, s.data.Engines)
}

func resolve(d profile.Definition, engines []EngineRecord) ([]engine.Installation, error) {
	out := make([]engine.Installation, 0, len(d.Engines))
	for _, req := range d.Engines {
		var choices []EngineRecord
		for _, candidate := range engines {
			if req.Accepts(candidate.Definition) {
				choices = append(choices, candidate)
			}
		}
		if len(choices) == 0 {
			return nil, fmt.Errorf("profile %q requires engine %q version range [%s,%s) with capabilities %v; no installed engine matches", d.ID, req.EngineID, req.Versions.Minimum, req.Versions.MaximumExclusive, req.Capabilities)
		}
		sort.Slice(choices, func(i, j int) bool {
			cmp, _ := version.Compare(choices[i].Installation.Version, choices[j].Installation.Version)
			return cmp > 0
		})
		chosen := choices[0].Installation
		for _, rule := range d.Rules {
			if rule.EngineID != req.EngineID {
				continue
			}
			path := chosen.ExecutablePaths[rule.Executable]
			if path == "" {
				return nil, fmt.Errorf("profile %q requires executable %q from engine %q, but it is not registered", d.ID, rule.Executable, req.EngineID)
			}
			info, err := os.Stat(path)
			if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
				return nil, fmt.Errorf("profile %q requires executable %q from engine %q, but it is unavailable or not executable", d.ID, rule.Executable, req.EngineID)
			}
		}
		out = append(out, chosen)
	}
	return out, nil
}
func (s *Service) persist(next state) error {
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, ".management-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
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
	dir, err := os.Open(s.root)
	if err == nil {
		err = dir.Sync()
		dir.Close()
	}
	return err
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
