// Package repository models catalog sources and trust metadata. It deliberately
// does not install engines or profiles.
package repository

import (
	"context"
	"time"
)

type Kind string

const (
	KindOfficial  Kind = "official"
	KindCommunity Kind = "community"
	KindLocal     Kind = "local"
	KindLegacy    Kind = "legacy"
)

type TrustState string

const (
	TrustBundled    TrustState = "bundled"
	TrustPinned     TrustState = "pinned"
	TrustUnverified TrustState = "unverified"
	TrustUntrusted  TrustState = "untrusted"
)

type Source struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	URL            string     `json:"url"`
	Kind           Kind       `json:"kind"`
	Trust          TrustState `json:"trust"`
	KeyFingerprint string     `json:"keyFingerprint,omitempty"`
}

type Index struct {
	Version                    int                `json:"version"`
	MinimumAppVersion          string             `json:"minimumAppVersion,omitempty"`
	MaximumAppVersionExclusive string             `json:"maximumAppVersionExclusive,omitempty"`
	Engines                    []EngineReference  `json:"engines,omitempty"`
	Profiles                   []ProfileReference `json:"profiles"`
}

type EngineReference struct {
	ID                         string `json:"id"`
	Version                    string `json:"version"`
	Name                       string `json:"name"`
	Description                string `json:"description,omitempty"`
	ManifestURL                string `json:"manifestUrl"`
	MinimumAppVersion          string `json:"minimumAppVersion,omitempty"`
	MaximumAppVersionExclusive string `json:"maximumAppVersionExclusive,omitempty"`
}

type ProfileReference struct {
	ID                         string `json:"id"`
	Version                    string `json:"version"`
	Name                       string `json:"name"`
	Description                string `json:"description,omitempty"`
	ManifestURL                string `json:"manifestUrl"`
	MinimumAppVersion          string `json:"minimumAppVersion,omitempty"`
	MaximumAppVersionExclusive string `json:"maximumAppVersionExclusive,omitempty"`
}

type TrustRecord struct {
	SourceID       string     `json:"sourceId"`
	State          TrustState `json:"state"`
	KeyFingerprint string     `json:"keyFingerprint,omitempty"`
	TrustedAt      *time.Time `json:"trustedAt,omitempty"`
}

// MetadataVerifier is a future signature-verification seam. It authenticates
// repository metadata; it is separate from artifact hashing and installation.
type MetadataVerifier interface {
	Verify(context.Context, Source, []byte, []byte) (TrustRecord, error)
}

// TrustStore persists trust decisions independently from repository fetching
// and engine installation.
type TrustStore interface {
	Get(context.Context, string) (TrustRecord, bool, error)
	Trust(context.Context, TrustRecord) error
	Revoke(context.Context, string) error
}

// Client reads repository metadata only. It never installs packages.
type Client interface {
	// FetchIndex returns original bytes for signature verification before parsing.
	FetchIndex(context.Context, Source) ([]byte, error)
	FetchEngineManifest(context.Context, Source, EngineReference) ([]byte, error)
	FetchProfileManifest(context.Context, Source, ProfileReference) ([]byte, error)
}
