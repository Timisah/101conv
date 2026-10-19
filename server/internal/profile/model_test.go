package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestBundledProfilesSeparateConversionAndCompression(t *testing.T) {
	read := func(name string) Definition {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "catalog", "targets", "profiles", name))
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Profile Definition `json:"profile"`
		}
		if err = json.Unmarshal(raw, &manifest); err != nil {
			t.Fatal(err)
		}
		if err = manifest.Profile.Validate(); err != nil {
			t.Fatalf("bundled profile %s rejected: %v", name, err)
		}
		return manifest.Profile
	}
	conversion := read("ffmpeg-linux-amd64.json")
	compression := read("ffmpeg-compression-linux-amd64.json")
	if conversion.Type != TypeConversion || conversion.Compression != nil {
		t.Fatal("FFmpeg conversion profile must not include compression controls")
	}
	if compression.Type != TypeCompression || compression.Compression == nil || compression.Compression.ProbeExecutable != "ffprobe" {
		t.Fatal("FFmpeg compressor profile should declare target-size controls and ffprobe")
	}
	if conversion.SupportsMode("compress") || !compression.SupportsMode("compress") || compression.SupportsMode("convert") {
		t.Fatal("separated profiles must be confined to their selected workflow")
	}
}

func TestCompressionRequiresDeclaredAndBoundedOptions(t *testing.T) {
	min, max, step := 1.0, 8.0, 1.0
	definition := Definition{
		SchemaVersion: 1, Type: TypeCompression, ID: "compression-test", Version: "1.0.0", Name: "Compression test",
		Engines:     []EngineRequirement{{EngineID: "ffmpeg"}},
		Compression: &CompressionConfig{Options: []OptionDefinition{{Key: "quality", Label: "Quality", Type: "number", Default: "4", Min: &min, Max: &max, Step: &step}}},
		Rules:       []ConversionRule{{EngineID: "ffmpeg", Executable: "ffmpeg", Sources: []string{"mp4"}, Targets: []string{"mp4"}, Arguments: []string{"-i", "{source}", "{compression.args}", "{target}"}, CompressionArguments: []string{"-crf", "{option:quality}"}}},
	}
	if err := definition.Validate(); err != nil {
		t.Fatal(err)
	}
	definition.Rules[0].Arguments = []string{"-i", "{source}", "{target}"}
	if err := definition.Validate(); err == nil {
		t.Fatal("compression arguments without a placement marker should be rejected")
	}
	definition.Rules[0].Arguments = []string{"-i", "{source}", "{compression.args}", "{target}"}
	definition.Rules[0].CompressionArguments[1] = "{option:missing}"
	if err := definition.Validate(); err == nil {
		t.Fatal("profile with undeclared option placeholder should be rejected")
	}
}

func TestExplicitProfileTypeRejectsMixedConfiguration(t *testing.T) {
	min, max, step := 1.0, 8.0, 1.0
	definition := Definition{
		SchemaVersion: 1, Type: TypeConversion, ID: "conversion-test", Version: "1.0.0", Name: "Conversion test",
		Engines:     []EngineRequirement{{EngineID: "ffmpeg"}},
		Compression: &CompressionConfig{Options: []OptionDefinition{{Key: "quality", Label: "Quality", Type: "number", Default: "4", Min: &min, Max: &max, Step: &step}}},
		Rules:       []ConversionRule{{EngineID: "ffmpeg", Executable: "ffmpeg", Sources: []string{"mp4"}, Targets: []string{"mp4"}, Arguments: []string{"-i", "{source}", "{compression.args}", "{target}"}, CompressionArguments: []string{"-crf", "{option:quality}"}}},
	}
	if err := definition.Validate(); err == nil {
		t.Fatal("explicit conversion profile must reject compression configuration")
	}
	definition.Type = TypeCompression
	definition.Compression = nil
	if err := definition.Validate(); err == nil {
		t.Fatal("explicit compression profile must require compression configuration")
	}
}

func TestWithInferredTypeClassifiesLegacyDefinitions(t *testing.T) {
	conversion := (Definition{}).WithInferredType()
	if conversion.Type != TypeConversion {
		t.Fatalf("legacy profile without compression settings inferred as %q", conversion.Type)
	}
	compression := (Definition{Compression: &CompressionConfig{}}).WithInferredType()
	if compression.Type != TypeCompression {
		t.Fatalf("legacy profile with compression settings inferred as %q", compression.Type)
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

func TestConversionRuleOptionsAreTypedAndBoundToArgv(t *testing.T) {
	min, max, step := 0.0, 10000.0, 1.0
	d := Definition{SchemaVersion: 1, Type: TypeConversion, ID: "image", Version: "1.0.0", Name: "Image",
		Engines: []EngineRequirement{{EngineID: "ffmpeg"}}, Rules: []ConversionRule{{
			EngineID: "ffmpeg", Executable: "ffmpeg", Sources: []string{"png"}, Targets: []string{"jpg"},
			Arguments: []string{"-vf", "scale={option:width}:0", "{source}", "{target}"},
			Options:   []OptionDefinition{{Key: "width", Label: "Width", Type: "number", Default: "0", Min: &min, Max: &max, Step: &step}},
		}}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	d.Rules[0].Arguments[1] = "scale={option:unknown}:0"
	if err := d.Validate(); err == nil {
		t.Fatal("undeclared embedded option should be rejected")
	}
}
