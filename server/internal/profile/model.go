// Package profile models reusable conversion behavior separately from engine
// package and installation details.
package profile

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/version"
)

type Definition struct {
	SchemaVersion int `json:"schemaVersion"`
	// Type separates profiles intended for ordinary conversion from profiles
	// intended for the compression workflow. An empty value is accepted only
	// for pre-Type definitions loaded through the legacy compatibility path.
	Type                       string              `json:"type,omitempty"`
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
	Compression                *CompressionConfig  `json:"compression,omitempty"`
}

const (
	TypeConversion  = "conversion"
	TypeCompression = "compression"
)

// SupportsMode reports whether this profile can be selected by a job mode.
// Definitions without a type predate the split and retain their old behavior.
func (d Definition) SupportsMode(mode string) bool {
	switch d.Type {
	case TypeConversion:
		return mode == "convert"
	case TypeCompression:
		return mode == "compress"
	default:
		return mode == "convert" || mode == "compress"
	}
}

// WithInferredType upgrades a pre-Type definition at an installation boundary.
// Existing persisted definitions are left untouched until an administrator
// explicitly installs or updates them.
func (d Definition) WithInferredType() Definition {
	if d.Type == "" {
		if d.Compression != nil {
			d.Type = TypeCompression
		} else {
			d.Type = TypeConversion
		}
	}
	return d
}

// CompressionConfig describes controls that a profile deliberately exposes in
// the compression workflow. Values are still validated against this schema by
// Core, while converter-specific argument expansion remains in the Worker.
type CompressionConfig struct {
	Options          []OptionDefinition `json:"options"`
	ProbeExecutable  string             `json:"probeExecutable,omitempty"`
	AudioBitrateKbps int                `json:"audioBitrateKbps,omitempty"`
	MinimumVideoKbps int                `json:"minimumVideoKbps,omitempty"`
	RequireSmaller   bool               `json:"requireSmallerOutput,omitempty"`
	// TargetQualityOption enables a bounded quality search when a target-size
	// option is selected. The option's range is the search domain; the value
	// supplied by the user caps the best quality that may be attempted.
	TargetQualityOption       string `json:"targetQualityOption,omitempty"`
	TargetQualityHigherBetter bool   `json:"targetQualityHigherIsBetter,omitempty"`
	TargetSizeAttempts        int    `json:"targetSizeAttempts,omitempty"`
}

type OptionDefinition struct {
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Type         string   `json:"type"` // number or select
	Default      string   `json:"default"`
	Min          *float64 `json:"min,omitempty"`
	Max          *float64 `json:"max,omitempty"`
	Step         *float64 `json:"step,omitempty"`
	Choices      []string `json:"choices,omitempty"`
	TargetSizeMB bool     `json:"targetSizeMb,omitempty"`
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
	// Options exposes typed, bounded controls for this source/target mapping.
	// Values are substituted only into standalone {option:key} argv tokens.
	Options []OptionDefinition `json:"options,omitempty"`
	// CompressionArguments replace the {compression.args} marker when this rule
	// is selected in compression mode. Supported tokens are declared option
	// placeholders and {compression.videoKbps}, {compression.audioKbps}, and
	// {compression.bufferKbps}.
	CompressionArguments []string `json:"compressionArguments,omitempty"`
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
		if err := validateOptions(rule.Options); err != nil {
			return fmt.Errorf("rule %d options: %w", i, err)
		}
		optionKeys := make(map[string]bool, len(rule.Options))
		for _, option := range rule.Options {
			optionKeys[option.Key] = true
		}
		for _, argument := range rule.Arguments {
			if strings.Contains(argument, "{option:") {
				if err := validateOptionPlaceholders(argument, optionKeys); err != nil {
					return fmt.Errorf("rule %d: %w", i, err)
				}
			}
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
		if len(rule.CompressionArguments) > 0 {
			if err := d.ValidateCompressionArguments(rule.CompressionArguments); err != nil {
				return fmt.Errorf("rule %d compression arguments: %w", i, err)
			}
		}
		compressionSlots := 0
		for _, arg := range rule.Arguments {
			if arg == "{compression.args}" {
				compressionSlots++
			}
		}
		if compressionSlots != 0 && (compressionSlots != 1 || len(rule.CompressionArguments) == 0) {
			return fmt.Errorf("rule %d must use exactly one {compression.args} marker when compression arguments are declared", i)
		}
		if len(rule.CompressionArguments) > 0 && compressionSlots != 1 {
			return fmt.Errorf("rule %d compression arguments require one {compression.args} marker", i)
		}
	}
	if err := d.ValidateCompression(); err != nil {
		return err
	}
	switch d.Type {
	case "": // Existing on-disk definitions are handled by the legacy adapter.
	case TypeConversion:
		if d.Compression != nil {
			return fmt.Errorf("conversion profiles must not declare compression settings")
		}
		for _, rule := range d.Rules {
			if len(rule.CompressionArguments) != 0 {
				return fmt.Errorf("conversion profiles must not declare compression arguments")
			}
		}
	case TypeCompression:
		if d.Compression == nil {
			return fmt.Errorf("compression profiles must declare compression settings")
		}
		hasCompressionRule := false
		for _, rule := range d.Rules {
			hasCompressionRule = hasCompressionRule || len(rule.CompressionArguments) != 0
		}
		if !hasCompressionRule {
			return fmt.Errorf("compression profiles must declare at least one compression rule")
		}
	default:
		return fmt.Errorf("profile type must be conversion or compression")
	}
	return nil
}

func (d Definition) ValidateCompression() error {
	if d.Compression == nil {
		for _, rule := range d.Rules {
			if len(rule.CompressionArguments) != 0 {
				return fmt.Errorf("compression arguments require a compression configuration")
			}
		}
		return nil
	}
	c := d.Compression
	if len(c.Options) == 0 || len(c.Options) > 16 {
		return fmt.Errorf("compression must define between 1 and 16 options")
	}
	if c.ProbeExecutable != "" && strings.TrimSpace(c.ProbeExecutable) != c.ProbeExecutable {
		return fmt.Errorf("compression probe executable is invalid")
	}
	if c.AudioBitrateKbps < 0 || c.AudioBitrateKbps > 512 || c.MinimumVideoKbps < 0 || c.MinimumVideoKbps > 10000 {
		return fmt.Errorf("compression bitrate settings are invalid")
	}
	if c.TargetSizeAttempts == 1 || c.TargetSizeAttempts < 0 || c.TargetSizeAttempts > 12 {
		return fmt.Errorf("compression target-size attempts must be 0 or between 2 and 12")
	}
	seen := map[string]bool{}
	hasTargetSize := false
	optionsByKey := make(map[string]OptionDefinition, len(c.Options))
	for _, option := range c.Options {
		if !safeOptionKey(option.Key) || strings.TrimSpace(option.Label) == "" || seen[option.Key] {
			return fmt.Errorf("compression option key and label must be valid and unique")
		}
		seen[option.Key] = true
		optionsByKey[option.Key] = option
		hasTargetSize = hasTargetSize || option.TargetSizeMB
		switch option.Type {
		case "number":
			if option.Min == nil || option.Max == nil || *option.Min > *option.Max {
				return fmt.Errorf("compression option %q requires a valid numeric range", option.Key)
			}
			if option.Step != nil && *option.Step <= 0 {
				return fmt.Errorf("compression option %q has an invalid step", option.Key)
			}
			defaultValue, err := parseOptionNumber(option.Default)
			if err != nil || defaultValue < *option.Min || defaultValue > *option.Max {
				return fmt.Errorf("compression option %q has an invalid default", option.Key)
			}
			if option.Step != nil && math.Mod(defaultValue-*option.Min, *option.Step) > 1e-9 {
				return fmt.Errorf("compression option %q default does not follow its step", option.Key)
			}
		case "select":
			if len(option.Choices) == 0 || len(option.Choices) > 128 {
				return fmt.Errorf("compression option %q requires choices", option.Key)
			}
			found := false
			for _, choice := range option.Choices {
				if choice == option.Default {
					found = true
				}
				if len(choice) == 0 || len(choice) > 128 || strings.ContainsRune(choice, '\x00') {
					return fmt.Errorf("compression option %q has an invalid choice", option.Key)
				}
			}
			if !found {
				return fmt.Errorf("compression option %q default must be one of its choices", option.Key)
			}
		default:
			return fmt.Errorf("compression option %q uses unsupported type %q", option.Key, option.Type)
		}
		if option.TargetSizeMB && (option.Type != "number" || (c.TargetQualityOption == "" && (c.ProbeExecutable == "" || c.AudioBitrateKbps == 0 || c.MinimumVideoKbps == 0))) {
			return fmt.Errorf("target-size option %q requires either adaptive quality metadata or a probe executable and bitrate settings", option.Key)
		}
	}
	if c.TargetQualityOption != "" {
		quality, ok := optionsByKey[c.TargetQualityOption]
		if !ok || quality.Type != "number" || quality.TargetSizeMB {
			return fmt.Errorf("target quality option %q must name a numeric non-target-size option", c.TargetQualityOption)
		}
		if !hasTargetSize {
			return fmt.Errorf("target quality option requires a target-size option")
		}
	}
	usesTargetSize := false
	usesTargetQuality := false
	usesBitrate := false
	for _, rule := range d.Rules {
		if len(rule.CompressionArguments) == 0 {
			continue
		}
		for _, arg := range rule.CompressionArguments {
			usesBitrate = usesBitrate || strings.Contains(arg, "{compression.videoKbps}") || strings.Contains(arg, "{compression.audioKbps}") || strings.Contains(arg, "{compression.bufferKbps}")
			usesTargetSize = usesTargetSize || usesBitrate || strings.Contains(arg, "{compression.quality}")
			usesTargetQuality = usesTargetQuality || strings.Contains(arg, "{compression.quality}")
			if strings.HasPrefix(arg, "{option:") && strings.HasSuffix(arg, "}") {
				key := strings.TrimSuffix(strings.TrimPrefix(arg, "{option:"), "}")
				if !seen[key] {
					return fmt.Errorf("compression argument references undeclared option %q", key)
				}
			} else {
				unwrapped := arg
				for _, token := range []string{"{compression.videoKbps}", "{compression.audioKbps}", "{compression.bufferKbps}", "{compression.quality}"} {
					unwrapped = strings.ReplaceAll(unwrapped, token, "0")
				}
				if strings.ContainsAny(unwrapped, "{}") || strings.ContainsRune(unwrapped, '\x00') {
					return fmt.Errorf("compression argument contains unsupported placeholder or NUL")
				}
			}
		}
	}
	if hasTargetSize && !usesTargetSize {
		return fmt.Errorf("target-size option must be used by a compression rule")
	}
	if usesTargetSize && !hasTargetSize {
		return fmt.Errorf("compression target-size placeholders require a target-size option")
	}
	if usesTargetQuality != (c.TargetQualityOption != "") {
		return fmt.Errorf("compression quality placeholder and target quality option must be declared together")
	}
	if usesTargetQuality && usesBitrate {
		return fmt.Errorf("compression quality search cannot be combined with bitrate placeholders")
	}
	return nil
}

func (d Definition) ValidateCompressionArguments(arguments []string) error {
	if len(arguments) == 0 || len(arguments) > 64 {
		return fmt.Errorf("argument list must contain between 1 and 64 tokens")
	}
	for _, arg := range arguments {
		if len(arg) > 4096 || strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("argument is too long or contains NUL")
		}
	}
	return nil
}

func safeOptionKey(key string) bool {
	if len(key) == 0 || len(key) > 64 {
		return false
	}
	for _, r := range key {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func parseOptionNumber(value string) (float64, error) {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("invalid number")
	}
	return number, nil
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
		case "{compression.args}":
		default:
			if strings.Contains(arg, "{option:") {
				if err := validateOptionPlaceholders(arg, nil); err == nil {
					continue
				}
			}
			if strings.ContainsAny(arg, "{}") {
				return fmt.Errorf("only standalone {source}, {target}, {compression.args}, and {option:key} placeholders are supported")
			}
		}
	}
	if !seenSource || !seenTarget {
		return fmt.Errorf("arguments must include standalone {source} and {target} placeholders")
	}
	return nil
}

func validateOptionPlaceholders(argument string, declared map[string]bool) error {
	remaining := argument
	for {
		start := strings.Index(remaining, "{option:")
		if start < 0 {
			break
		}
		end := strings.IndexByte(remaining[start:], '}')
		if end < 0 {
			return fmt.Errorf("unterminated option placeholder")
		}
		end += start
		key := remaining[start+len("{option:") : end]
		if !safeOptionKey(key) || declared != nil && !declared[key] {
			return fmt.Errorf("references undeclared option %q", key)
		}
		remaining = remaining[end+1:]
	}
	if strings.ContainsAny(remaining, "{}") {
		return fmt.Errorf("unsupported option placeholder")
	}
	return nil
}

// ValidateOptions validates a conversion rule's public option schema. It is
// deliberately small: every option must be representable as one safe argv
// token, with server-side range/choice checking before a job is persisted.
func ValidateOptions(options []OptionDefinition) error { return validateOptions(options) }

func validateOptions(options []OptionDefinition) error {
	if len(options) > 32 {
		return fmt.Errorf("at most 32 conversion options are allowed")
	}
	seen := map[string]bool{}
	for _, option := range options {
		if !safeOptionKey(option.Key) || strings.TrimSpace(option.Label) == "" || seen[option.Key] {
			return fmt.Errorf("option key and label must be valid and unique")
		}
		seen[option.Key] = true
		switch option.Type {
		case "number":
			if option.Min == nil || option.Max == nil || *option.Min > *option.Max {
				return fmt.Errorf("option %q requires a valid numeric range", option.Key)
			}
			value, err := parseOptionNumber(option.Default)
			if err != nil || value < *option.Min || value > *option.Max {
				return fmt.Errorf("option %q has an invalid default", option.Key)
			}
			if option.Step != nil && (*option.Step <= 0 || math.Mod(value-*option.Min, *option.Step) > 1e-9) {
				return fmt.Errorf("option %q has an invalid step or default", option.Key)
			}
		case "select", "boolean":
			if option.Type == "boolean" {
				if option.Default != "true" && option.Default != "false" {
					return fmt.Errorf("option %q has an invalid boolean default", option.Key)
				}
			} else {
				if len(option.Choices) == 0 || len(option.Choices) > 128 {
					return fmt.Errorf("option %q requires choices", option.Key)
				}
				found := false
				for _, choice := range option.Choices {
					if choice == option.Default {
						found = true
					}
					if len(choice) == 0 || len(choice) > 128 || strings.ContainsAny(choice, "{}\x00") {
						return fmt.Errorf("option %q has an invalid choice", option.Key)
					}
				}
				if !found {
					return fmt.Errorf("option %q default must be one of its choices", option.Key)
				}
			}
		default:
			return fmt.Errorf("option %q uses unsupported type %q", option.Key, option.Type)
		}
	}
	return nil
}

func normalizeFormat(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
}
