package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const OfficialURL = "https://raw.githubusercontent.com/Timisah/101conv/main/catalog/index.json"

//go:embed official.json
var officialIndex []byte

type Index struct {
	Version                    int     `json:"version"`
	MinimumAppVersion          string  `json:"minimumAppVersion,omitempty"`
	MaximumAppVersionExclusive string  `json:"maximumAppVersionExclusive,omitempty"`
	Profiles                   []Entry `json:"profiles"`
}

type Entry struct {
	ID                         string `json:"id"`
	Version                    string `json:"version"`
	Name                       string `json:"name"`
	Description                string `json:"description,omitempty"`
	ManifestURL                string `json:"manifestUrl"`
	MinimumAppVersion          string `json:"minimumAppVersion,omitempty"`
	MaximumAppVersionExclusive string `json:"maximumAppVersionExclusive,omitempty"`
}

type Source struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	Kind string `json:"kind"`
}

type Service struct {
	appVersion string
	index      Index
}

func New(appVersion string) (*Service, error) {
	if _, err := parseVersion(appVersion); err != nil {
		return nil, fmt.Errorf("invalid APP_VERSION %q: %w", appVersion, err)
	}
	var index Index
	if err := json.Unmarshal(officialIndex, &index); err != nil {
		return nil, err
	}
	if index.Version != 1 {
		return nil, fmt.Errorf("unsupported built-in catalog schema %d", index.Version)
	}
	if err := validateIndex(index); err != nil {
		return nil, fmt.Errorf("invalid built-in catalog: %w", err)
	}
	return &Service{appVersion: appVersion, index: index}, nil
}

func validateIndex(index Index) error {
	for label, bound := range map[string]string{
		"minimumAppVersion":          index.MinimumAppVersion,
		"maximumAppVersionExclusive": index.MaximumAppVersionExclusive,
	} {
		if bound != "" {
			if _, err := parseVersion(bound); err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
		}
	}
	for i, entry := range index.Profiles {
		if entry.ID == "" || entry.Version == "" || entry.Name == "" || entry.ManifestURL == "" {
			return fmt.Errorf("profile %d is missing a required field", i)
		}
		if _, err := parseVersion(entry.Version); err != nil {
			return fmt.Errorf("profile %q has invalid version: %w", entry.ID, err)
		}
		for label, bound := range map[string]string{
			"minimumAppVersion":          entry.MinimumAppVersion,
			"maximumAppVersionExclusive": entry.MaximumAppVersionExclusive,
		} {
			if bound != "" {
				if _, err := parseVersion(bound); err != nil {
					return fmt.Errorf("profile %q %s: %w", entry.ID, label, err)
				}
			}
		}
	}
	return nil
}

func (s *Service) Source() Source {
	return Source{ID: "101conv-official", Name: "101conv Official Catalog", URL: OfficialURL, Kind: "official"}
}

// Available returns only profiles compatible with this running 101conv release.
// A catalog whose own range excludes this release is not available at all.
func (s *Service) Available() ([]Entry, error) {
	if !compatible(s.appVersion, s.index.MinimumAppVersion, s.index.MaximumAppVersionExclusive) {
		return nil, fmt.Errorf("official catalog does not support 101conv %s", s.appVersion)
	}
	entries := make([]Entry, 0, len(s.index.Profiles))
	for _, entry := range s.index.Profiles {
		if compatible(s.appVersion, entry.MinimumAppVersion, entry.MaximumAppVersionExclusive) {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

var semver = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

func compatible(app, minimum, maximum string) bool {
	if minimum != "" && compare(app, minimum) < 0 {
		return false
	}
	return maximum == "" || compare(app, maximum) < 0
}

func compare(left, right string) int {
	a, _ := parseVersion(left)
	b, _ := parseVersion(right)
	for i := range a {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

func parseVersion(value string) ([3]int, error) {
	match := semver.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return [3]int{}, fmt.Errorf("expected major.minor.patch")
	}
	var result [3]int
	for i := range result {
		n, err := strconv.Atoi(match[i+1])
		if err != nil {
			return [3]int{}, err
		}
		result[i] = n
	}
	return result, nil
}
