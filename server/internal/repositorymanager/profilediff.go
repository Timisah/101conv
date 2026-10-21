package repositorymanager

import (
	"fmt"
	"sort"
	"strings"

	"github.com/timisah/101conv/server/internal/profile"
)

const maxChangeLines = 12

// DescribeProfileChanges summarizes what an update changes for an
// administrator: supported format mappings, option controls and their default
// values, license, and engine requirements. It is advisory text derived from the
// two manifests; it never drives installation. An empty result means nothing
// user-visible differs.
func DescribeProfileChanges(installed, offered profile.Definition) []string {
	var lines []string
	if installed.Name != offered.Name {
		lines = append(lines, fmt.Sprintf("Name changes from %q to %q", installed.Name, offered.Name))
	}
	if strings.TrimSpace(installed.License) != strings.TrimSpace(offered.License) {
		lines = append(lines, "License text changes; review it before approving")
	}

	oldPairs, newPairs := formatPairs(installed), formatPairs(offered)
	lines = append(lines, setDiff("Adds support for", newPairs, oldPairs)...)
	lines = append(lines, setDiff("Removes support for", oldPairs, newPairs)...)

	oldOptions, newOptions := optionSummaries(installed), optionSummaries(offered)
	for _, key := range sortedKeys(newOptions) {
		next := newOptions[key]
		previous, existed := oldOptions[key]
		switch {
		case !existed:
			lines = append(lines, fmt.Sprintf("New option: %s", next.label))
		case previous.def != next.def:
			lines = append(lines, fmt.Sprintf("%s: default changes from %s to %s", next.label, previous.def, next.def))
		}
	}
	for _, key := range sortedKeys(oldOptions) {
		if _, kept := newOptions[key]; !kept {
			lines = append(lines, fmt.Sprintf("Option removed: %s", oldOptions[key].label))
		}
	}

	oldEngines := map[string]string{}
	for _, req := range installed.Engines {
		oldEngines[req.EngineID] = rangeText(req.Versions.Minimum, req.Versions.MaximumExclusive)
	}
	for _, req := range offered.Engines {
		next := rangeText(req.Versions.Minimum, req.Versions.MaximumExclusive)
		if previous, existed := oldEngines[req.EngineID]; !existed {
			lines = append(lines, fmt.Sprintf("Now requires engine %s %s", req.EngineID, next))
		} else if previous != next {
			lines = append(lines, fmt.Sprintf("Engine %s requirement changes from %s to %s", req.EngineID, previous, next))
		}
	}
	if len(lines) > maxChangeLines {
		extra := len(lines) - maxChangeLines
		lines = append(lines[:maxChangeLines], fmt.Sprintf("…and %d more", extra))
	}
	return lines
}

func rangeText(min, maxExclusive string) string {
	switch {
	case min != "" && maxExclusive != "":
		return ">=" + min + ", <" + maxExclusive
	case min != "":
		return ">=" + min
	case maxExclusive != "":
		return "<" + maxExclusive
	}
	return "any version"
}

func formatPairs(d profile.Definition) map[string]bool {
	pairs := map[string]bool{}
	for _, rule := range d.Rules {
		for _, source := range rule.Sources {
			for _, target := range rule.Targets {
				pairs[strings.ToUpper(strings.TrimPrefix(source, ".")+" → "+strings.TrimPrefix(target, "."))] = true
			}
		}
	}
	return pairs
}

// setDiff lists members of a that are not in b, as one line capped to a few
// examples so a large catalog change stays readable.
func setDiff(prefix string, a, b map[string]bool) []string {
	var only []string
	for item := range a {
		if !b[item] {
			only = append(only, item)
		}
	}
	if len(only) == 0 {
		return nil
	}
	sort.Strings(only)
	shown := only
	suffix := ""
	if len(shown) > 6 {
		suffix = fmt.Sprintf(" and %d more", len(shown)-6)
		shown = shown[:6]
	}
	return []string{prefix + " " + strings.Join(shown, ", ") + suffix}
}

type optionSummary struct{ label, def string }

func optionSummaries(d profile.Definition) map[string]optionSummary {
	out := map[string]optionSummary{}
	add := func(options []profile.OptionDefinition) {
		for _, option := range options {
			def := option.Default
			if option.Optional && def == "" {
				def = "none (optional)"
			} else if def == "" {
				def = "empty"
			}
			if _, exists := out[option.Key]; !exists {
				out[option.Key] = optionSummary{label: option.Label, def: def}
			}
		}
	}
	for _, rule := range d.Rules {
		add(rule.Options)
		add(rule.CompressionOptions)
	}
	if d.Compression != nil {
		add(d.Compression.Options)
	}
	return out
}

func sortedKeys(m map[string]optionSummary) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
