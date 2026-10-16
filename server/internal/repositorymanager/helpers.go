package repositorymanager

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/updater"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
)

func downloadVerified(up *updater.Updater, target string, max int64) ([]byte, error) {
	if !safeTarget(target) {
		return nil, fmt.Errorf("unsafe TUF target %q", target)
	}
	info, err := up.GetTargetInfo(target)
	if err != nil {
		return nil, classifyTUFError(err)
	}
	if info.Length < 0 || info.Length > max {
		return nil, fmt.Errorf("target %q exceeds size limit", target)
	}
	_, data, err := up.DownloadTarget(info, "", "")
	if err != nil {
		return nil, fmt.Errorf("TUF target verification failed: %w", classifyTUFError(err))
	}
	return data, nil
}

func digestCatalogTargets(targets map[string][]byte) string {
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		_, _ = h.Write([]byte(name))
		_, _ = h.Write([]byte{0})
		sum := sha256.Sum256(targets[name])
		_, _ = h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func randomPlanID() (string, error) {
	var id [32]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

func cloneJSON[T any](value T) (T, error) {
	var clone T
	data, err := json.Marshal(value)
	if err != nil {
		return clone, err
	}
	err = json.Unmarshal(data, &clone)
	return clone, err
}

func highestProfile(profiles []profile.Definition, id string) (profile.Definition, bool) {
	var selected profile.Definition
	found := false
	for _, candidate := range profiles {
		if candidate.ID == id && (!found || newer(candidate.Version, selected.Version)) {
			selected = candidate
			found = true
		}
	}
	return selected, found
}

func (m *Manager) prunePlansLocked(now time.Time) {
	for id, plan := range m.plans {
		if now.After(plan.expires) {
			delete(m.plans, id)
		}
	}
}

func (m *Manager) recordRefresh(id string, refreshErr error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "101conv-official" {
		source := m.officialSource()
		source.LastRefresh = time.Now().UTC().Format(time.RFC3339)
		if refreshErr == nil {
			source.Status = "ready"
			source.LastError = ""
		} else {
			source.Status = statusForError(refreshErr)
			source.LastError = refreshErr.Error()
			if len(source.LastError) > 1024 {
				source.LastError = source.LastError[:1024]
			}
		}
		m.officialStatus = source
		return nil
	}
	source, ok := m.sources[id]
	if !ok {
		return management.ErrNotFound
	}
	previous := source
	source.LastRefresh = time.Now().UTC().Format(time.RFC3339)
	if refreshErr == nil {
		source.Status = "ready"
		source.LastError = ""
	} else {
		source.Status = statusForError(refreshErr)
		source.LastError = refreshErr.Error()
		if len(source.LastError) > 1024 {
			source.LastError = source.LastError[:1024]
		}
	}
	m.sources[id] = source
	if err := m.persist(); err != nil {
		m.sources[id] = previous
		return err
	}
	return nil
}

func statusForError(err error) string {
	if errors.Is(err, ErrSignature) || errors.Is(err, ErrRollback) {
		return "trust_error"
	}
	if errors.Is(err, ErrExpiredMetadata) {
		return "expired"
	}
	if strings.Contains(strings.ToLower(err.Error()), "expired") {
		return "expired"
	}
	if strings.Contains(strings.ToLower(err.Error()), "signature") || strings.Contains(strings.ToLower(err.Error()), "hash") || strings.Contains(strings.ToLower(err.Error()), "rollback") {
		return "trust_error"
	}
	return "error"
}

func classifyTUFError(err error) error {
	if err == nil {
		return nil
	}
	var expired *metadata.ErrExpiredMetadata
	if errors.As(err, &expired) {
		return coded("expired_metadata", fmt.Errorf("%w: %v", ErrExpiredMetadata, err))
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "rollback") || strings.Contains(lower, "bad version number") || strings.Contains(lower, "version") && (strings.Contains(lower, "less") || strings.Contains(lower, "must be >=") || strings.Contains(lower, "must be greater")) {
		return coded("metadata_rollback", fmt.Errorf("%w: %v", ErrRollback, err))
	}
	if strings.Contains(strings.ToLower(err.Error()), "signature") || strings.Contains(strings.ToLower(err.Error()), "verification") || strings.Contains(strings.ToLower(err.Error()), "threshold") {
		return coded("signature_failure", fmt.Errorf("%w: %v", ErrSignature, err))
	}
	return err
}
