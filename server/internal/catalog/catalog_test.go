package catalog

import (
	"testing"

	"github.com/timisah/101conv/server/internal/repository"
)

func TestVersionCompatibilityBoundaries(t *testing.T) {
	tests := []struct {
		app, minimum, maximum string
		want                  bool
	}{
		{"1.0.0", "1.0.0", "2.0.0", true},
		{"1.9.9", "1.0.0", "2.0.0", true},
		{"2.0.0", "1.0.0", "2.0.0", false},
		{"0.9.9", "1.0.0", "", false},
		{"1.2.3-dev", "1.2.3", "1.2.4", true},
		{"1.2.3", "", "", true},
	}
	for _, test := range tests {
		t.Run(test.app, func(t *testing.T) {
			if got := compatible(test.app, test.minimum, test.maximum); got != test.want {
				t.Fatalf("compatible(%q, %q, %q) = %v, want %v", test.app, test.minimum, test.maximum, got, test.want)
			}
		})
	}
}

func TestOfficialCatalogIsPreconfiguredAndVersionSensitive(t *testing.T) {
	service, err := New("0.1.0-dev")
	if err != nil {
		t.Fatal(err)
	}
	if service.Source().ID != "101conv-official" {
		t.Fatalf("unexpected source: %+v", service.Source())
	}
	if service.Source().Trust != "unverified" {
		t.Fatalf("unsigned catalog must not be reported as signature-verified: %+v", service.Source())
	}
	entries, err := service.Available()
	if err != nil {
		t.Fatalf("catalog should support this app: %v", err)
	}
	if len(entries) != 2 || entries[0].ID != "ffmpeg-linux-amd64" || entries[1].ID != "ffmpeg-image-linux-amd64" {
		t.Fatalf("expected the official FFmpeg profiles, got %+v", entries)
	}
	old, err := New("0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Available(); err == nil {
		t.Fatal("expected exclusive maximum app version to be rejected")
	}
}

func TestIndexAcceptsSeparateEngineReferencesAlongsideProfiles(t *testing.T) {
	index := Index{
		Version: 1,
		Engines: []repository.EngineReference{{
			ID: "ffmpeg", Version: "9.0.0", Name: "FFmpeg", ManifestURL: "https://example.org/engines/ffmpeg.json",
		}},
		Profiles: []Entry{{
			ID: "video-webm", Version: "1.0.0", Name: "Video WebM", ManifestURL: "https://example.org/profiles/video-webm.json",
		}},
	}
	if err := validateIndex(index); err != nil {
		t.Fatalf("separated engine/profile references should validate: %v", err)
	}
}

func TestAvailableEnginesFiltersIncompatibleEngineReferences(t *testing.T) {
	service, err := New("0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	service.index = Index{Version: 1, Engines: []repository.EngineReference{
		{ID: "current", Version: "1.0.0", Name: "Current", ManifestURL: "https://example.org/current.json", MinimumAppVersion: "0.0.1", MaximumAppVersionExclusive: "0.2.0"},
		{ID: "future", Version: "1.0.0", Name: "Future", ManifestURL: "https://example.org/future.json", MinimumAppVersion: "0.2.0"},
	}}
	entries, err := service.AvailableEngines()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "current" {
		t.Fatalf("available engines = %+v, want only current engine", entries)
	}
}

func TestAvailableFiltersIncompatibleProfiles(t *testing.T) {
	service, err := New("0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	service.index = Index{Version: 1, Profiles: []Entry{
		{ID: "current", Version: "1.0.0", Name: "Current", ManifestURL: "https://example.org/current.json", MinimumAppVersion: "0.0.1", MaximumAppVersionExclusive: "0.2.0"},
		{ID: "future", Version: "1.0.0", Name: "Future", ManifestURL: "https://example.org/future.json", MinimumAppVersion: "0.2.0"},
	}}
	entries, err := service.Available()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != "current" {
		t.Fatalf("available entries = %+v, want only current profile", entries)
	}
}
