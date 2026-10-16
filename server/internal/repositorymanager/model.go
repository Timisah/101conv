package repositorymanager

import (
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/profile"
)

type Kind string

const (
	KindOfficial  Kind = "official"
	KindCommunity Kind = "community"
	KindLocal     Kind = "local"
)

type Trust string

const (
	TrustPinned      Trust = "pinned"
	TrustUnavailable Trust = "unavailable"
)

type Source struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Kind        Kind   `json:"kind"`
	Trust       Trust  `json:"trust"`
	Fingerprint string `json:"fingerprint"`
	TrustedRoot string `json:"-"`
	ApprovedAt  string `json:"approvedAt,omitempty"`
}
type CatalogIndex struct {
	SchemaVersion              int            `json:"schemaVersion"`
	MinimumAppVersion          string         `json:"minimumAppVersion,omitempty"`
	MaximumAppVersionExclusive string         `json:"maximumAppVersionExclusive,omitempty"`
	Engines                    []EngineEntry  `json:"engines"`
	Profiles                   []ProfileEntry `json:"profiles"`
}
type EngineEntry struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Name           string `json:"name"`
	ManifestTarget string `json:"manifestTarget"`
}
type ProfileEntry struct {
	ID             string `json:"id"`
	Version        string `json:"version"`
	Name           string `json:"name"`
	ManifestTarget string `json:"manifestTarget"`
}
type EngineManifest struct {
	Engine engine.Definition `json:"engine"`
}
type ProfileManifest struct {
	Profile profile.Definition `json:"profile"`
}
type Catalog struct {
	Repository Source               `json:"repository"`
	Engines    []engine.Definition  `json:"engines"`
	Profiles   []profile.Definition `json:"profiles"`
}
type InstallPreview struct {
	Repository     Source              `json:"repository"`
	Publisher      string              `json:"publisher"`
	Trust          Trust               `json:"trust"`
	License        string              `json:"license,omitempty"`
	Profile        profile.Definition  `json:"profile"`
	Dependencies   []DependencyPreview `json:"dependencies"`
	PlannedChanges []string            `json:"plannedChanges"`
}
type DependencyPreview struct {
	EngineID         string              `json:"engineId"`
	EngineName       string              `json:"engineName,omitempty"`
	EngineLicense    string              `json:"engineLicense,omitempty"`
	VersionRange     engine.VersionRange `json:"versionRange"`
	Capabilities     []string            `json:"requiredCapabilities"`
	InstalledVersion string              `json:"installedVersion,omitempty"`
	Action           string              `json:"action"`
}
