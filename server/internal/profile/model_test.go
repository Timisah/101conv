package profile

import (
	"testing"

	"github.com/timisah/101conv/server/internal/engine"
)

func TestMultipleProfilesCanShareOneEngineWithinCompatibleRange(t *testing.T) {
	makeProfile := func(id, source, target string) Definition {
		return Definition{
			SchemaVersion: 1, ID: id, Version: "1.0.0", Name: id,
			Engines: []EngineRequirement{{
				EngineID: "ffmpeg", Versions: engine.VersionRange{Minimum: "9.0.0", MaximumExclusive: "10.0.0"},
				Capabilities: []string{"video.transcode"},
			}},
			Rules: []ConversionRule{{EngineID: "ffmpeg", Executable: "ffmpeg", Sources: []string{source}, Targets: []string{target}, Arguments: []string{"-i", "{source}", "{target}"}}},
		}
	}
	first := makeProfile("video-mp4", "mkv", "mp4")
	second := makeProfile("video-webm", "mp4", "webm")
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := second.Validate(); err != nil {
		t.Fatal(err)
	}
	if first.Engines[0].EngineID != second.Engines[0].EngineID || first.Engines[0].Versions != second.Engines[0].Versions {
		t.Fatal("profiles should be able to share the same engine and version range")
	}
	candidate := engine.Definition{ID: "ffmpeg", Version: "9.5.1", Capabilities: []string{"video.transcode", "audio.transcode"}}
	if !first.Engines[0].Accepts(candidate) || !second.Engines[0].Accepts(candidate) {
		t.Fatal("a compatible installed engine should satisfy both profiles")
	}
	candidate.Capabilities = []string{"audio.transcode"}
	if first.Engines[0].Accepts(candidate) {
		t.Fatal("an engine missing a required capability should not satisfy the profile")
	}
}

func TestDefinitionRejectsRuleWithoutDeclaredEngine(t *testing.T) {
	definition := Definition{
		SchemaVersion: 1, ID: "broken", Version: "1.0.0", Name: "Broken",
		Engines: []EngineRequirement{{EngineID: "ffmpeg"}},
		Rules:   []ConversionRule{{EngineID: "imagemagick", Executable: "convert", Sources: []string{"png"}, Targets: []string{"jpg"}}},
	}
	if err := definition.Validate(); err == nil {
		t.Fatal("expected undeclared engine reference to be rejected")
	}
}

func TestDefinitionRejectsAmbiguousFormatPairs(t *testing.T) {
	definition := Definition{
		SchemaVersion: 1, ID: "ambiguous", Version: "1.0.0", Name: "Ambiguous",
		Engines: []EngineRequirement{{EngineID: "ffmpeg"}},
		Rules: []ConversionRule{
			{EngineID: "ffmpeg", Executable: "ffmpeg", Sources: []string{".JPG"}, Targets: []string{"png"}},
			{EngineID: "ffmpeg", Executable: "ffmpeg", Sources: []string{"jpg"}, Targets: []string{"PNG"}},
		},
	}
	if err := definition.Validate(); err == nil {
		t.Fatal("expected overlapping source/target rules to be rejected")
	}
}

func TestValidateWorkerArguments(t *testing.T) {
	valid := []string{"--input", "{source}", "{target}"}
	if err := ValidateWorkerArguments(valid); err != nil {
		t.Fatalf("valid argv rejected: %v", err)
	}
	for _, invalid := range [][]string{
		{"--input", "{source}", "{target}.jxl"},
		{"--input", "{source}"},
		{"{source}", "{target}", "{engine.variable:QUALITY}"},
	} {
		if err := ValidateWorkerArguments(invalid); err == nil {
			t.Errorf("expected invalid arguments rejected: %#v", invalid)
		}
	}
}
