// Package capability exposes the canonical, user-facing operation model that
// is derived from the legacy profile and engine records. Profiles remain the
// durable compatibility boundary; callers of this package never receive
// executable paths or converter argv.
package capability

import (
	"fmt"
	"sort"
	"strings"

	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
	"github.com/timisah/101conv/server/internal/version"
)

const (
	OperationConvert  = "convert"
	OperationCompress = "compress"
)

// Option is the safe, typed option schema shown to a user. It intentionally
// omits profile argument templates and engine executable paths.
type Option struct {
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Type         string   `json:"type"`
	Default      string   `json:"default"`
	Min          *float64 `json:"min,omitempty"`
	Max          *float64 `json:"max,omitempty"`
	Step         *float64 `json:"step,omitempty"`
	Choices      []string `json:"choices,omitempty"`
	TargetSizeMB bool     `json:"targetSizeMb,omitempty"`
}

// ProviderCapability describes one operation mapping implemented by a
// provider. Options belong to the provider because two implementations of the
// same source/target mapping can expose different safe controls.
type ProviderCapability struct {
	CapabilityID string   `json:"capabilityId"`
	Options      []Option `json:"options,omitempty"`
}

// Capability is a canonical operation mapping shared by compatible providers.
type Capability struct {
	ID           string   `json:"id"`
	Operation    string   `json:"operation"`
	SourceFormat string   `json:"sourceFormat"`
	TargetFormat string   `json:"targetFormat"`
	ProviderIDs  []string `json:"providerIds"`
}

// Provider is a versioned implementation of one or more capabilities. The
// profile ID is retained as a migration and preference key, while execution
// details remain private to Core and Worker.
type Provider struct {
	ID            string               `json:"id"`
	ProfileID     string               `json:"profileId"`
	Version       string               `json:"version"`
	Name          string               `json:"name"`
	Description   string               `json:"description,omitempty"`
	License       string               `json:"license,omitempty"`
	EngineID      string               `json:"engineId"`
	EngineVersion string               `json:"engineVersion,omitempty"`
	Enabled       bool                 `json:"enabled"`
	Installed     bool                 `json:"installed"`
	Available     bool                 `json:"available"`
	Capabilities  []ProviderCapability `json:"capabilities"`
}

// Catalog is the additive read model used by the new capability API.
type Catalog struct {
	SchemaVersion int          `json:"schemaVersion"`
	Capabilities  []Capability `json:"capabilities"`
	Providers     []Provider   `json:"providers"`
}

type Request struct {
	Operation    string `json:"operation"`
	SourceFormat string `json:"sourceFormat"`
	TargetFormat string `json:"targetFormat"`
}

type Candidate struct {
	ProviderID string `json:"providerId"`
	Available  bool   `json:"available"`
	Selected   bool   `json:"selected"`
	Reason     string `json:"reason"`
}

// Resolution is deliberately explainable: the strategy and candidate reasons
// make an automatic choice inspectable by a UI or support tool.
type Resolution struct {
	Request    Request     `json:"request"`
	Capability Capability  `json:"capability"`
	Provider   Provider    `json:"provider"`
	Strategy   string      `json:"strategy"`
	Candidates []Candidate `json:"candidates"`
}

type ResolutionError struct {
	Request    Request     `json:"request"`
	Candidates []Candidate `json:"candidates,omitempty"`
	message    string
}

func (e *ResolutionError) Error() string { return e.message }

// Build converts legacy management records into a stable canonical catalog.
// Input order never affects output order or the selected engine version.
func Build(profiles []management.ProfileRecord, engines []management.EngineRecord) Catalog {
	orderedProfiles := append([]management.ProfileRecord(nil), profiles...)
	sort.SliceStable(orderedProfiles, func(i, j int) bool {
		a, b := orderedProfiles[i].Definition, orderedProfiles[j].Definition
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if cmp := compareVersions(a.Version, b.Version); cmp != 0 {
			return cmp > 0
		}
		return orderedProfiles[i].State.Enabled && !orderedProfiles[j].State.Enabled
	})

	providers := make([]Provider, 0)
	for _, record := range orderedProfiles {
		d := record.Definition.WithInferredType()
		if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.Version) == "" {
			continue
		}
		for _, requirement := range d.Engines {
			chosen, installed := chooseEngine(requirement, record.State.ResolvedEngines, engines)
			engineID, engineVersion := requirement.EngineID, ""
			if installed {
				engineID, engineVersion = chosen.Installation.EngineID, chosen.Installation.Version
			}
			provider := Provider{
				ID:            providerID(d.ID, d.Version, engineID, engineVersion),
				ProfileID:     d.ID,
				Version:       d.Version,
				Name:          d.Name,
				Description:   d.Description,
				License:       d.License,
				EngineID:      engineID,
				EngineVersion: engineVersion,
				Enabled:       record.State.Enabled,
				Installed:     installed,
				Available:     installed && chosen.Installation.Managed,
			}
			for _, rule := range d.Rules {
				if rule.EngineID != requirement.EngineID {
					continue
				}
				if !installed || chosen.Installation.ExecutablePaths[rule.Executable] == "" || chosen.Definition.Executables[rule.Executable] == "" {
					provider.Available = false
				}
				for _, source := range rule.Sources {
					for _, target := range rule.Targets {
						for _, operation := range operationsFor(d, rule) {
							id := capabilityID(operation, source, target)
							options := rule.Options
							if operation == OperationCompress && d.Compression != nil {
								options = d.Compression.Options
							}
							provider.Capabilities = append(provider.Capabilities, ProviderCapability{CapabilityID: id, Options: copyOptions(options)})
						}
					}
				}
			}
			provider.Capabilities = uniqueProviderCapabilities(provider.Capabilities)
			if len(provider.Capabilities) > 0 {
				providers = append(providers, provider)
			}
		}
	}

	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	capabilityMap := make(map[string]Capability)
	for _, provider := range providers {
		for _, item := range provider.Capabilities {
			operation, source, target := parseCapabilityID(item.CapabilityID)
			entry := capabilityMap[item.CapabilityID]
			if entry.ID == "" {
				entry = Capability{ID: item.CapabilityID, Operation: operation, SourceFormat: source, TargetFormat: target}
			}
			entry.ProviderIDs = append(entry.ProviderIDs, provider.ID)
			capabilityMap[item.CapabilityID] = entry
		}
	}
	capabilities := make([]Capability, 0, len(capabilityMap))
	for _, item := range capabilityMap {
		item.ProviderIDs = uniqueStrings(item.ProviderIDs)
		capabilities = append(capabilities, item)
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].ID < capabilities[j].ID })
	return Catalog{SchemaVersion: 1, Capabilities: capabilities, Providers: providers}
}

// FilterEnabled returns the public view for guests and other callers that must
// not see disabled implementations.
func (c Catalog) FilterEnabled() Catalog {
	allowed := make(map[string]bool)
	providers := make([]Provider, 0, len(c.Providers))
	for _, provider := range c.Providers {
		if !provider.Enabled {
			continue
		}
		allowed[provider.ID] = true
		providers = append(providers, provider)
	}
	capabilities := make([]Capability, 0, len(c.Capabilities))
	for _, item := range c.Capabilities {
		ids := make([]string, 0, len(item.ProviderIDs))
		for _, id := range item.ProviderIDs {
			if allowed[id] {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			item.ProviderIDs = ids
			capabilities = append(capabilities, item)
		}
	}
	return Catalog{SchemaVersion: c.SchemaVersion, Capabilities: capabilities, Providers: providers}
}

// Resolve chooses a provider using an explicit provider preference first and
// then a stable profile/version/engine order. It never uses the legacy raw
// priority field as an opaque visibility contest.
func (c Catalog) Resolve(request Request, preferredProviderID string) (Resolution, error) {
	normalized, err := normalizeRequest(request)
	if err != nil {
		return Resolution{}, err
	}
	capabilityID := capabilityID(normalized.Operation, normalized.SourceFormat, normalized.TargetFormat)
	var found Capability
	for _, item := range c.Capabilities {
		if item.ID == capabilityID {
			found = item
			break
		}
	}
	if found.ID == "" {
		return Resolution{}, &ResolutionError{Request: normalized, message: fmt.Sprintf("no capability is registered for %s %s to %s", normalized.Operation, normalized.SourceFormat, normalized.TargetFormat)}
	}
	byID := make(map[string]Provider, len(c.Providers))
	for _, provider := range c.Providers {
		byID[provider.ID] = provider
	}
	candidates := make([]Candidate, 0, len(found.ProviderIDs))
	available := make([]Provider, 0, len(found.ProviderIDs))
	for _, id := range found.ProviderIDs {
		provider, ok := byID[id]
		if !ok {
			continue
		}
		item := Candidate{ProviderID: id, Available: provider.Enabled && provider.Available}
		switch {
		case !provider.Enabled:
			item.Reason = "provider is disabled"
		case !provider.Installed:
			item.Reason = "required engine is not installed"
		case !provider.Available:
			item.Reason = "required executable is unavailable"
		default:
			item.Reason = "eligible"
			available = append(available, provider)
		}
		candidates = append(candidates, item)
	}
	if len(available) == 0 {
		return Resolution{}, &ResolutionError{Request: normalized, Candidates: candidates, message: fmt.Sprintf("no available provider is registered for %s %s to %s", normalized.Operation, normalized.SourceFormat, normalized.TargetFormat)}
	}
	strategy := "stable provider order"
	selected := -1
	if strings.TrimSpace(preferredProviderID) != "" {
		for i := range available {
			if available[i].ID == preferredProviderID {
				selected = i
				strategy = "explicit provider preference"
				break
			}
		}
		if selected < 0 {
			strategy = "stable fallback; preferred provider unavailable"
		}
	}
	if selected < 0 {
		sort.SliceStable(available, func(i, j int) bool { return providerLess(available[i], available[j]) })
		selected = 0
	}
	selectedID := available[selected].ID
	for i := range candidates {
		if candidates[i].ProviderID == selectedID {
			candidates[i].Selected = true
		}
	}
	return Resolution{Request: normalized, Capability: found, Provider: available[selected], Strategy: strategy, Candidates: candidates}, nil
}

func normalizeRequest(request Request) (Request, error) {
	request.Operation = strings.ToLower(strings.TrimSpace(request.Operation))
	request.SourceFormat = normalizeFormat(request.SourceFormat)
	request.TargetFormat = normalizeFormat(request.TargetFormat)
	if request.Operation != OperationConvert && request.Operation != OperationCompress {
		return Request{}, fmt.Errorf("operation must be %q or %q", OperationConvert, OperationCompress)
	}
	if request.SourceFormat == "" || request.TargetFormat == "" {
		return Request{}, fmt.Errorf("source and target formats are required")
	}
	return request, nil
}

func chooseEngine(requirement profile.EngineRequirement, pins map[string]string, engines []management.EngineRecord) (management.EngineRecord, bool) {
	choices := make([]management.EngineRecord, 0)
	for _, candidate := range engines {
		if requirement.Accepts(candidate.Definition) {
			choices = append(choices, candidate)
		}
	}
	if len(choices) == 0 {
		return management.EngineRecord{}, false
	}
	sort.SliceStable(choices, func(i, j int) bool {
		if cmp := compareVersions(choices[i].Installation.Version, choices[j].Installation.Version); cmp != 0 {
			return cmp > 0
		}
		return choices[i].Installation.Version < choices[j].Installation.Version
	})
	if pinned := pins[requirement.EngineID]; pinned != "" {
		for _, choice := range choices {
			if choice.Installation.Version == pinned {
				return choice, true
			}
		}
	}
	return choices[0], true
}

func operationsFor(d profile.Definition, rule profile.ConversionRule) []string {
	result := make([]string, 0, 2)
	if d.SupportsMode(OperationConvert) {
		result = append(result, OperationConvert)
	}
	if d.SupportsMode(OperationCompress) && d.Compression != nil && len(rule.CompressionArguments) > 0 {
		result = append(result, OperationCompress)
	}
	return result
}

func providerID(profileID, profileVersion, engineID, engineVersion string) string {
	return "legacy/" + profileID + "@" + profileVersion + "/" + engineID + "@" + engineVersion
}

func capabilityID(operation, source, target string) string {
	return strings.Join([]string{operation, normalizeFormat(source), normalizeFormat(target)}, ":")
}

func parseCapabilityID(id string) (string, string, string) {
	parts := strings.SplitN(id, ":", 3)
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

func normalizeFormat(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func copyOptions(options []profile.OptionDefinition) []Option {
	result := make([]Option, 0, len(options))
	for _, item := range options {
		result = append(result, Option{Key: item.Key, Label: item.Label, Type: item.Type, Default: item.Default, Min: item.Min, Max: item.Max, Step: item.Step, Choices: append([]string(nil), item.Choices...), TargetSizeMB: item.TargetSizeMB})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func uniqueProviderCapabilities(items []ProviderCapability) []ProviderCapability {
	seen := map[string]bool{}
	result := make([]ProviderCapability, 0, len(items))
	for _, item := range items {
		if seen[item.CapabilityID] {
			continue
		}
		seen[item.CapabilityID] = true
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CapabilityID < result[j].CapabilityID })
	return result
}

func uniqueStrings(items []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	sort.Strings(result)
	return result
}

func compareVersions(a, b string) int {
	cmp, err := version.Compare(a, b)
	if err == nil {
		return cmp
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func providerLess(a, b Provider) bool {
	if a.ProfileID != b.ProfileID {
		return a.ProfileID < b.ProfileID
	}
	if cmp := compareVersions(a.Version, b.Version); cmp != 0 {
		return cmp > 0
	}
	if a.EngineID != b.EngineID {
		return a.EngineID < b.EngineID
	}
	if cmp := compareVersions(a.EngineVersion, b.EngineVersion); cmp != 0 {
		return cmp > 0
	}
	return a.ID < b.ID
}
