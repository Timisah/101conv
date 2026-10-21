package repositorymanager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/profile"
)

func readBundledProfile(t *testing.T, name string) profile.Definition {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "catalog", "targets", "profiles", name))
	if err != nil {
		t.Fatal(err)
	}
	var manifest ProfileManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest.Profile
}

func TestDescribeProfileChangesReportsTheOptionalMaximumSize(t *testing.T) {
	for _, pair := range [][2]string{
		{"jpeg-xl-compression-linux-amd64.json", "jpeg-xl-compression-linux-amd64-1.1.0.json"},
		{"ffmpeg-image-compression-linux-amd64.json", "ffmpeg-image-compression-linux-amd64-1.1.0.json"},
	} {
		changes := DescribeProfileChanges(readBundledProfile(t, pair[0]), readBundledProfile(t, pair[1]))
		joined := strings.Join(changes, "\n")
		if !strings.Contains(joined, "Maximum output size (MB): default changes from 8 to none (optional)") {
			t.Fatalf("%s: the changed default must be reported, got %v", pair[1], changes)
		}
		if strings.Contains(joined, "Adds support for") || strings.Contains(joined, "Removes support for") {
			t.Fatalf("%s: formats are unchanged, got %v", pair[1], changes)
		}
	}
}

func TestDescribeProfileChangesFormatsOptionsAndRequirements(t *testing.T) {
	min, max := 1.0, 10.0
	old := profile.Definition{ID: "p", Name: "Old", License: "MIT",
		Engines: []profile.EngineRequirement{{EngineID: "ffmpeg"}},
		Rules:   []profile.ConversionRule{{Sources: []string{"png"}, Targets: []string{"webp"}, Options: []profile.OptionDefinition{{Key: "quality", Label: "Quality", Type: "number", Default: "5", Min: &min, Max: &max}}}}}
	next := profile.Definition{ID: "p", Name: "New", License: "GPL",
		Engines: []profile.EngineRequirement{{EngineID: "ffmpeg", Versions: engineRange("9.0.0", "10.0.0")}, {EngineID: "extra"}},
		Rules:   []profile.ConversionRule{{Sources: []string{"png", ".jpg"}, Targets: []string{"avif"}, Options: []profile.OptionDefinition{{Key: "quality", Label: "Quality", Type: "number", Default: "7", Min: &min, Max: &max}, {Key: "speed", Label: "Speed", Type: "number", Default: "1", Min: &min, Max: &max}}}}}
	got := strings.Join(DescribeProfileChanges(old, next), "\n")
	for _, want := range []string{`Name changes from "Old" to "New"`, "License text changes", "Adds support for JPG → AVIF, PNG → AVIF", "Removes support for PNG → WEBP", "Quality: default changes from 5 to 7", "New option: Speed", "Engine ffmpeg requirement changes from any version to >=9.0.0, <10.0.0", "Now requires engine extra any version"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if same := DescribeProfileChanges(old, old); len(same) != 0 {
		t.Fatalf("identical definitions must report no changes, got %v", same)
	}
}

func engineRange(min, max string) engine.VersionRange {
	return engine.VersionRange{Minimum: min, MaximumExclusive: max}
}
