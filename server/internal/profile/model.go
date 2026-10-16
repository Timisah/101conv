// Package profile models reusable conversion behavior separately from engine
// package and installation details.
package profile

import (
	"context"
	"fmt"
	"strings"

	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/version"
)

type Definition struct {
	SchemaVersion              int                 `json:"schemaVersion"`
	ID                         string              `json:"id"`
	Version                    string              `json:"version"`
	Name                       string              `json:"name"`
	Description                string              `json:"description,omitempty"`
	License                    string              `json:"license,omitempty"`
	MinimumAppVersion          string              `json:"minimumAppVersion,omitempty"`
	MaximumAppVersionExclusive string              `json:"maximumAppVersionExclusive,omitempty"`
	Priority                   int                 `json:"priority,omitempty"`
	Engines                    []EngineRequirement `json:"engines"`
	Rules                      []ConversionRule    `json:"rules"`
}

type EngineRequirement struct {
	EngineID     string              `json:"engineId"`
	Versions     engine.VersionRange `json:"versions,omitempty"`
	Capabilities []string            `json:"capabilities,omitempty"`
}

type ConversionRule struct {
	EngineID   string   `json:"engineId"`
	Executable string   `json:"executable"`
	Sources    []string `json:"sources"`
	Targets    []string `json:"targets"`
	// Arguments are argv tokens, never a shell command string.
	Arguments []string `json:"arguments"`
}

// Installation represents user state for a profile. It is independent of both
// the profile definition and the engine installation it references.
type Installation struct {
	ProfileID       string            `json:"profileId"`
	Version         string            `json:"version"`
	Enabled         bool              `json:"enabled"`
	RepositoryID    string            `json:"repositoryId,omitempty"`
	Customized      bool              `json:"customized,omitempty"`
	ResolvedEngines map[string]string `json:"resolvedEngines,omitempty"`
}

type Store interface {
	List(context.Context) ([]Definition, error)
	Get(context.Context, string) (Definition, bool, error)
	Save(context.Context, Definition) error
	Remove(context.Context, string) error
}

func (r EngineRequirement) Accepts(candidate engine.Definition) bool {
	if r.EngineID != candidate.ID || !r.Versions.Contains(candidate.Version) {
		return false
	}
	provided := make(map[string]struct{}, len(candidate.Capabilities))
	for _, capability := range candidate.Capabilities {
		provided[capability] = struct{}{}
	}
	for _, required := range r.Capabilities {
		if strings.TrimSpace(required) == "" {
			return false
		}
		if _, ok := provided[required]; !ok {
			return false
		}
	}
	return true
}

func (d Definition) Validate() error {
	if d.SchemaVersion != 1 {
		return fmt.Errorf("unsupported profile schema version %d", d.SchemaVersion)
	}
	if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.Version) == "" || strings.TrimSpace(d.Name) == "" {
		return fmt.Errorf("profile id, version, and name are required")
	}
	if _, err := version.Parse(d.Version); err != nil {
		return fmt.Errorf("invalid profile version: %w", err)
	}
	if d.MinimumAppVersion != "" {
		if _, err := version.Parse(d.MinimumAppVersion); err != nil {
			return fmt.Errorf("invalid minimum app version: %w", err)
		}
	}
	if d.MaximumAppVersionExclusive != "" {
		if _, err := version.Parse(d.MaximumAppVersionExclusive); err != nil {
			return fmt.Errorf("invalid maximum app version: %w", err)
		}
	}
	if d.MinimumAppVersion != "" && d.MaximumAppVersionExclusive != "" {
		cmp, _ := version.Compare(d.MinimumAppVersion, d.MaximumAppVersionExclusive)
		if cmp >= 0 {
			return fmt.Errorf("profile app-version maximum must be greater than its minimum")
		}
	}
	if len(d.Engines) == 0 || len(d.Rules) == 0 {
		return fmt.Errorf("profile must declare at least one engine and one conversion rule")
	}
	engines := make(map[string]struct{}, len(d.Engines))
	for _, requirement := range d.Engines {
		if strings.TrimSpace(requirement.EngineID) == "" {
			return fmt.Errorf("profile engine references must not be empty")
		}
		if err := requirement.Versions.Validate(); err != nil {
			return fmt.Errorf("engine %q: %w", requirement.EngineID, err)
		}
		if _, exists := engines[requirement.EngineID]; exists {
			return fmt.Errorf("profile repeats engine requirement %q", requirement.EngineID)
		}
		for _, capability := range requirement.Capabilities {
			if strings.TrimSpace(capability) == "" {
				return fmt.Errorf("engine %q has an empty required capability", requirement.EngineID)
			}
		}
		engines[requirement.EngineID] = struct{}{}
	}
	// Rules are selected by source/target pair at runtime. Reject overlaps at
	// install time so repository authors get one clear validation error rather
	// than a profile that only fails when a particular pair is converted.
	pairs := make(map[string]int)
	for i, rule := range d.Rules {
		if _, exists := engines[rule.EngineID]; !exists {
			return fmt.Errorf("rule %d references undeclared engine %q", i, rule.EngineID)
		}
		if strings.TrimSpace(rule.Executable) == "" || len(rule.Sources) == 0 || len(rule.Targets) == 0 {
			return fmt.Errorf("rule %d must declare an executable, sources, and targets", i)
		}
		for _, format := range append(append([]string(nil), rule.Sources...), rule.Targets...) {
			if strings.TrimSpace(format) == "" {
				return fmt.Errorf("rule %d contains an empty format", i)
			}
		}
		for _, source := range rule.Sources {
			for _, target := range rule.Targets {
				key := normalizeFormat(source) + "\x00" + normalizeFormat(target)
				if previous, exists := pairs[key]; exists {
					return fmt.Errorf("rules %d and %d both match %s to %s", previous, i, normalizeFormat(source), normalizeFormat(target))
				}
				pairs[key] = i
			}
		}
	}
	return nil
}

// ValidateWorkerArguments checks the version-1 repository profile argument
// contract. It is separate from Definition.Validate so legacy configurations
// can still be loaded through the compatibility adapter.
func ValidateWorkerArguments(arguments []string) error {
	if len(arguments) == 0 || len(arguments) > 128 {
		return fmt.Errorf("argument list must contain between 1 and 128 tokens")
	}
	seenSource, seenTarget := false, false
	for _, arg := range arguments {
		if len(arg) > 4096 || strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("argument is too long or contains NUL")
		}
		switch arg {
		case "{source}":
			seenSource = true
		case "{target}":
			seenTarget = true
		default:
			if strings.ContainsAny(arg, "{}") {
				return fmt.Errorf("only standalone {source} and {target} placeholders are supported")
			}
		}
	}
	if !seenSource || !seenTarget {
		return fmt.Errorf("arguments must include standalone {source} and {target} placeholders")
	}
	return nil
}

func normalizeFormat(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
}
