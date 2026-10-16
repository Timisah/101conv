package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/timisah/101conv/server/internal/repository"
	"github.com/timisah/101conv/server/internal/version"
)

const OfficialURL = "https://raw.githubusercontent.com/Timisah/101conv/main/catalog/index.json"

//go:embed official.json
var officialIndex []byte

// Compatibility aliases preserve the existing catalog API while the catalog
// service begins using the separated repository model.
type Index = repository.Index
type Entry = repository.ProfileReference
type Source = repository.Source

type Service struct {
	appVersion string
	index      Index
}

func New(appVersion string) (*Service, error) {
	if _, err := version.Parse(appVersion); err != nil {
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
			if _, err := version.Parse(bound); err != nil {
				return fmt.Errorf("%s: %w", label, err)
			}
		}
	}
	for i, entry := range index.Engines {
		if err := validateReference("engine", i, entry.ID, entry.Version, entry.Name, entry.ManifestURL,
			entry.MinimumAppVersion, entry.MaximumAppVersionExclusive); err != nil {
			return err
		}
	}
	for i, entry := range index.Profiles {
		if err := validateReference("profile", i, entry.ID, entry.Version, entry.Name, entry.ManifestURL,
			entry.MinimumAppVersion, entry.MaximumAppVersionExclusive); err != nil {
			return err
		}
	}
	return nil
}

func validateReference(kind string, index int, id, release, name, manifestURL, minimum, maximum string) error {
	if id == "" || release == "" || name == "" || manifestURL == "" {
		return fmt.Errorf("%s %d is missing a required field", kind, index)
	}
	if _, err := version.Parse(release); err != nil {
		return fmt.Errorf("%s %q has invalid version: %w", kind, id, err)
	}
	for label, bound := range map[string]string{
		"minimumAppVersion":          minimum,
		"maximumAppVersionExclusive": maximum,
	} {
		if bound != "" {
			if _, err := version.Parse(bound); err != nil {
				return fmt.Errorf("%s %q %s: %w", kind, id, label, err)
			}
		}
	}
	return nil
}

func (s *Service) Source() Source {
	return Source{ID: "101conv-official", Name: "101conv Official Catalog", URL: OfficialURL, Kind: repository.KindOfficial, Trust: repository.TrustUnverified}
}

// Available returns only profiles compatible with this running 101conv release.
// A catalog whose own range excludes this release is not available at all.
func (s *Service) Available() ([]Entry, error) {
	if err := s.ensureCompatible(); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(s.index.Profiles))
	for _, entry := range s.index.Profiles {
		if compatible(s.appVersion, entry.MinimumAppVersion, entry.MaximumAppVersionExclusive) {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func (s *Service) AvailableEngines() ([]repository.EngineReference, error) {
	if err := s.ensureCompatible(); err != nil {
		return nil, err
	}
	entries := make([]repository.EngineReference, 0, len(s.index.Engines))
	for _, entry := range s.index.Engines {
		if compatible(s.appVersion, entry.MinimumAppVersion, entry.MaximumAppVersionExclusive) {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func (s *Service) ensureCompatible() error {
	if !compatible(s.appVersion, s.index.MinimumAppVersion, s.index.MaximumAppVersionExclusive) {
		return fmt.Errorf("official catalog does not support 101conv %s", s.appVersion)
	}
	return nil
}

func compatible(app, minimum, maximum string) bool {
	if minimum != "" {
		cmp, err := version.Compare(app, minimum)
		if err != nil || cmp < 0 {
			return false
		}
	}
	if maximum == "" {
		return true
	}
	cmp, err := version.Compare(app, maximum)
	if err != nil {
		return false
	}
	return cmp < 0
}
