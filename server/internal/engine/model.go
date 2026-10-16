// Package engine models installable converter software independently from the
// profiles that describe how to invoke it.
package engine

import (
	"context"
	"encoding/hex"
	"fmt"
	"path"
	"strings"

	"github.com/timisah/101conv/server/internal/version"
)

type Definition struct {
	SchemaVersion int      `json:"schemaVersion"`
	ID            string   `json:"id"`
	Version       string   `json:"version"`
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	License       string   `json:"license,omitempty"`
	Platform      string   `json:"platform"`
	Capabilities  []string `json:"capabilities,omitempty"`
	// Executables maps stable executable names to package-relative paths. A
	// manual engine leaves paths empty and supplies resolved paths in Installation.
	Executables map[string]string `json:"executables"`
	Artifact    *Artifact         `json:"artifact,omitempty"`
}

type Artifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Format string `json:"format"`
}

// Installation is local runtime state. ExecutablePaths are resolved paths for
// the named executables; profiles refer to executable names, never these paths.
type Installation struct {
	EngineID        string            `json:"engineId"`
	Version         string            `json:"version"`
	Platform        string            `json:"platform"`
	Root            string            `json:"root,omitempty"`
	ExecutablePaths map[string]string `json:"executablePaths"`
	Variables       map[string]string `json:"variables,omitempty"`
	PackageSHA256   string            `json:"packageSha256,omitempty"`
	RepositoryID    string            `json:"repositoryId,omitempty"`
	Managed         bool              `json:"managed"`
}

// VersionRange is inclusive at Minimum and exclusive at MaximumExclusive.
// Empty bounds mean unbounded. A range lets profiles accept compatible engine
// releases without requiring an exact version pin.
type VersionRange struct {
	Minimum          string `json:"minimum,omitempty"`
	MaximumExclusive string `json:"maximumExclusive,omitempty"`
}

type Manager interface {
	List(context.Context) ([]Installation, error)
	FindCompatible(context.Context, string, VersionRange, []string) (Installation, bool, error)
	// Install is called only after repository trust and user approval checks.
	// It is idempotent for a matching engine id, version, platform, and digest;
	// existing installations with different artifact digests must be rejected.
	Install(context.Context, Definition, string) (Installation, error)
	Remove(context.Context, string, string) error
}

func (d Definition) Validate() error {
	if d.SchemaVersion != 1 {
		return fmt.Errorf("unsupported engine schema version %d", d.SchemaVersion)
	}
	if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.Version) == "" || strings.TrimSpace(d.Name) == "" || strings.TrimSpace(d.Platform) == "" {
		return fmt.Errorf("engine id, version, name, and platform are required")
	}
	if _, err := version.Parse(d.Version); err != nil {
		return fmt.Errorf("invalid engine version: %w", err)
	}
	if len(d.Executables) == 0 {
		return fmt.Errorf("engine must declare at least one executable")
	}
	for name, executablePath := range d.Executables {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("engine executable names must not be empty")
		}
		if d.Artifact != nil && !safeRelativePath(executablePath) {
			return fmt.Errorf("engine executable %q must use a safe package-relative path", name)
		}
	}
	for _, capability := range d.Capabilities {
		if strings.TrimSpace(capability) == "" {
			return fmt.Errorf("engine capabilities must not be empty strings")
		}
	}
	if d.Artifact != nil {
		if strings.TrimSpace(d.Artifact.URL) == "" || strings.TrimSpace(d.Artifact.Format) == "" || len(d.Artifact.SHA256) != 64 {
			return fmt.Errorf("engine artifact metadata is incomplete")
		}
		if _, err := hex.DecodeString(d.Artifact.SHA256); err != nil {
			return fmt.Errorf("engine artifact SHA-256 must be hexadecimal")
		}
	}
	return nil
}

func (r VersionRange) Validate() error {
	if r.Minimum == "" && r.MaximumExclusive == "" {
		return nil
	}
	if r.Minimum != "" {
		if _, err := version.Parse(r.Minimum); err != nil {
			return fmt.Errorf("invalid minimum engine version: %w", err)
		}
	}
	if r.MaximumExclusive != "" {
		if _, err := version.Parse(r.MaximumExclusive); err != nil {
			return fmt.Errorf("invalid maximum engine version: %w", err)
		}
	}
	if r.Minimum != "" && r.MaximumExclusive != "" {
		cmp, _ := version.Compare(r.Minimum, r.MaximumExclusive)
		if cmp >= 0 {
			return fmt.Errorf("engine version range maximum must be greater than its minimum")
		}
	}
	return nil
}

func (r VersionRange) Contains(candidate string) bool {
	if r.Validate() != nil {
		return false
	}
	if _, err := version.Parse(candidate); err != nil {
		return false
	}
	if r.Minimum != "" {
		cmp, _ := version.Compare(candidate, r.Minimum)
		if cmp < 0 {
			return false
		}
	}
	if r.MaximumExclusive != "" {
		cmp, _ := version.Compare(candidate, r.MaximumExclusive)
		if cmp >= 0 {
			return false
		}
	}
	return true
}

func safeRelativePath(value string) bool {
	if value == "" || strings.Contains(value, `\`) || path.IsAbs(value) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." || segment == "." || segment == "" {
			return false
		}
	}
	return true
}
