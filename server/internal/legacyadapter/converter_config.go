// Package legacyadapter translates existing Java-style converter config files
// into independent engine and profile models without changing their behavior.
package legacyadapter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/profile"
)

var variableToken = regexp.MustCompile(`^\{var:([A-Za-z0-9][A-Za-z0-9._-]{0,79})}$`)
var inlineVariable = regexp.MustCompile(`\{var:([A-Za-z0-9][A-Za-z0-9._-]{0,79})}`)

type ConverterFileDefinition struct {
	ID           string            `json:"id"`
	FriendlyName string            `json:"friendlyName"`
	Priority     *int              `json:"priority"`
	Enabled      *bool             `json:"enabled"`
	Variables    map[string]string `json:"variables"`
	Rules        []RuleDefinition  `json:"rules"`
}

type RuleDefinition struct {
	Sources []string `json:"sources"`
	Targets []string `json:"targets"`
	Command []string `json:"command"`
}

type Adapted struct {
	Engine       engine.Definition    `json:"engine"`
	Installation engine.Installation  `json:"installation"`
	Profile      profile.Definition   `json:"profile"`
	ProfileState profile.Installation `json:"profileState"`
}

// Adapt parses the existing converter-config JSON shape. Legacy executable
// paths stay local/manual; they are never treated as repository package paths.
func Adapt(data []byte) (Adapted, error) {
	var legacy ConverterFileDefinition
	if err := json.Unmarshal(data, &legacy); err != nil {
		return Adapted{}, fmt.Errorf("decode legacy converter config: %w", err)
	}
	if strings.TrimSpace(legacy.ID) == "" || len(legacy.Rules) == 0 {
		return Adapted{}, fmt.Errorf("legacy converter config requires an id and at least one rule")
	}

	engineID := "manual-legacy:" + legacy.ID
	name := strings.TrimSpace(legacy.FriendlyName)
	if name == "" {
		name = legacy.ID
	}
	variables := make(map[string]string, len(legacy.Variables))
	for key, value := range legacy.Variables {
		variables[key] = value
	}
	executablePaths := make(map[string]string, len(legacy.Variables)+1)
	executableNames := make(map[string]string, len(legacy.Variables)+1)
	for variable, executablePath := range legacy.Variables {
		if strings.TrimSpace(variable) == "" {
			return Adapted{}, fmt.Errorf("legacy converter contains an empty variable name")
		}
		_ = executablePath
	}

	definition := engine.Definition{
		SchemaVersion: 1,
		ID:            engineID,
		Version:       "0.0.0+legacy",
		Name:          name,
		Platform:      "manual",
		Executables:   executableNames,
	}
	installation := engine.Installation{
		EngineID:        engineID,
		Version:         definition.Version,
		Platform:        definition.Platform,
		ExecutablePaths: executablePaths,
		Variables:       variables,
		Managed:         false,
	}
	profileID := "legacy-config:" + legacy.ID
	converted := profile.Definition{
		SchemaVersion: 1,
		Type:          profile.TypeConversion,
		ID:            profileID,
		Version:       "0.0.0+legacy",
		Name:          name,
		Priority:      100,
		Engines: []profile.EngineRequirement{{
			EngineID: engineID,
			Versions: engine.VersionRange{Minimum: "0.0.0"},
		}},
	}
	if legacy.Priority != nil {
		converted.Priority = *legacy.Priority
	}
	enabled := legacy.Enabled == nil || *legacy.Enabled
	profileState := profile.Installation{
		ProfileID:  converted.ID,
		Version:    converted.Version,
		Enabled:    enabled,
		Customized: true,
	}

	for i, rule := range legacy.Rules {
		command := make([]string, 0, len(rule.Command))
		for _, token := range rule.Command {
			if strings.TrimSpace(token) != "" {
				command = append(command, strings.TrimSpace(token))
			}
		}
		if len(command) == 0 || len(rule.Sources) == 0 || len(rule.Targets) == 0 {
			return Adapted{}, fmt.Errorf("legacy converter rule %d is incomplete", i)
		}
		first := command[0]
		match := variableToken.FindStringSubmatch(first)
		binaryName := "default"
		if match != nil {
			binaryName = match[1]
			path, exists := variables[binaryName]
			if !exists || strings.TrimSpace(path) == "" {
				return Adapted{}, fmt.Errorf("legacy converter rule %d references missing executable variable %q", i, binaryName)
			}
			executablePaths[binaryName] = path
			executableNames[binaryName] = ""
		} else {
			resolved, err := expandExecutable(first, variables)
			if err != nil {
				return Adapted{}, fmt.Errorf("legacy converter rule %d executable: %w", i, err)
			}
			if _, exists := executablePaths[binaryName]; !exists {
				executablePaths[binaryName] = resolved
				executableNames[binaryName] = ""
			}
		}
		args := make([]string, len(command)-1)
		for argIndex, arg := range command[1:] {
			for _, match := range inlineVariable.FindAllStringSubmatch(arg, -1) {
				if _, exists := variables[match[1]]; !exists {
					return Adapted{}, fmt.Errorf("legacy converter rule %d references missing executable variable %q", i, match[1])
				}
			}
			args[argIndex] = inlineVariable.ReplaceAllString(arg, "{engine.variable:$1}")
		}
		converted.Rules = append(converted.Rules, profile.ConversionRule{
			EngineID:   engineID,
			Executable: binaryName,
			Sources:    append([]string(nil), rule.Sources...),
			Targets:    append([]string(nil), rule.Targets...),
			Arguments:  args,
		})
	}
	if err := definition.Validate(); err != nil {
		return Adapted{}, fmt.Errorf("adapted engine is invalid: %w", err)
	}
	if err := converted.Validate(); err != nil {
		return Adapted{}, fmt.Errorf("adapted profile is invalid: %w", err)
	}
	return Adapted{Engine: definition, Installation: installation, Profile: converted, ProfileState: profileState}, nil
}

func expandExecutable(value string, variables map[string]string) (string, error) {
	var missing string
	resolved := inlineVariable.ReplaceAllStringFunc(value, func(token string) string {
		match := inlineVariable.FindStringSubmatch(token)
		path, exists := variables[match[1]]
		if !exists {
			missing = match[1]
			return ""
		}
		return path
	})
	if missing != "" {
		return "", fmt.Errorf("references missing executable variable %q", missing)
	}
	if strings.TrimSpace(resolved) == "" {
		return "", fmt.Errorf("executable path is empty")
	}
	return resolved, nil
}
