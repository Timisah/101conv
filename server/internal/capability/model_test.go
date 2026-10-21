package capability

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
)

func TestBuildIsCanonicalAndKeepsExecutionDetailsPrivate(t *testing.T) {
	min, max, step := 1.0, 10.0, 1.0
	profiles := []management.ProfileRecord{
		{
			Definition: profile.Definition{
				SchemaVersion: 1,
				Type:          profile.TypeConversion,
				ID:            "z-profile",
				Version:       "1.0.0",
				Name:          "Z profile",
				Engines: []profile.EngineRequirement{{
					EngineID: "ffmpeg",
				}},
				Rules: []profile.ConversionRule{{
					EngineID:   "ffmpeg",
					Executable: "ffmpeg",
					Sources:    []string{".MP4"},
					Targets:    []string{"WEBM"},
					Arguments:  []string{"{source}", "--unsafe-internal-detail", "{target}"},
					Options: []profile.OptionDefinition{{
						Key: "quality", Label: "Quality", Type: "number", Default: "5", Min: &min, Max: &max, Step: &step,
					}},
				}},
			},
			State: profile.Installation{ProfileID: "z-profile", Version: "1.0.0", Enabled: true},
		},
		{
			Definition: profile.Definition{
				SchemaVersion: 1,
				Type:          profile.TypeConversion,
				ID:            "a-profile",
				Version:       "1.0.0",
				Name:          "A profile",
				Engines:       []profile.EngineRequirement{{EngineID: "ffmpeg"}},
				Rules: []profile.ConversionRule{{
					EngineID: "ffmpeg", Executable: "ffmpeg", Sources: []string{"mp4"}, Targets: []string{"webm"}, Arguments: []string{"{source}", "{target}"},
				}},
			},
			State: profile.Installation{ProfileID: "a-profile", Version: "1.0.0", Enabled: false},
		},
	}
	engines := []management.EngineRecord{{
		Definition:   engine.Definition{SchemaVersion: 1, ID: "ffmpeg", Version: "7.1.0", Name: "FFmpeg", Platform: "linux-amd64", Capabilities: []string{"video.encode"}, Executables: map[string]string{"ffmpeg": "ffmpeg"}},
		Installation: engine.Installation{EngineID: "ffmpeg", Version: "7.1.0", Managed: true, ExecutablePaths: map[string]string{"ffmpeg": "/engines/ffmpeg"}},
	}}

	catalog := Build(profiles, engines)
	if len(catalog.Capabilities) != 1 {
		t.Fatalf("capabilities = %#v, want only the enabled profile", catalog.Capabilities)
	}
	if got := catalog.Capabilities[0].ID; got != "convert:mp4:webm" {
		t.Fatalf("capability id = %q", got)
	}
	if len(catalog.Providers) != 2 || catalog.Providers[0].ID >= catalog.Providers[1].ID {
		t.Fatalf("providers are not stable and sorted: %#v", catalog.Providers)
	}
	resolution, err := catalog.FilterEnabled().Resolve(Request{Operation: "CONVERT", SourceFormat: ".MP4", TargetFormat: ".WEBM"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Provider.ProfileID != "z-profile" {
		t.Fatalf("selected provider = %q, want z-profile", resolution.Provider.ProfileID)
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"unsafe-internal-detail", "ExecutablePaths", "/engines/ffmpeg"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("canonical catalog leaked %q: %s", secret, encoded)
		}
	}
}

func TestResolvePreferenceAndDiagnosticsAreDeterministic(t *testing.T) {
	base := management.EngineRecord{
		Definition:   engine.Definition{SchemaVersion: 1, ID: "tool", Version: "1.0.0", Name: "Tool", Platform: "linux-amd64", Executables: map[string]string{"tool": "tool"}},
		Installation: engine.Installation{EngineID: "tool", Version: "1.0.0", Managed: true, ExecutablePaths: map[string]string{"tool": "/tool"}},
	}
	makeProfile := func(id string, enabled bool) management.ProfileRecord {
		return management.ProfileRecord{Definition: profile.Definition{SchemaVersion: 1, Type: profile.TypeConversion, ID: id, Version: "1.0.0", Name: id, Engines: []profile.EngineRequirement{{EngineID: "tool"}}, Rules: []profile.ConversionRule{{EngineID: "tool", Executable: "tool", Sources: []string{"png"}, Targets: []string{"jpg"}, Arguments: []string{"{source}", "{target}"}}}}, State: profile.Installation{ProfileID: id, Version: "1.0.0", Enabled: enabled}}
	}
	catalog := Build([]management.ProfileRecord{makeProfile("second", true), makeProfile("first", true), makeProfile("off", false)}, []management.EngineRecord{base})
	result, err := catalog.Resolve(Request{Operation: OperationConvert, SourceFormat: "png", TargetFormat: "jpg"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Provider.ProfileID != "first" || result.Strategy != "stable provider order" {
		t.Fatalf("fallback result = %#v", result)
	}
	for _, candidate := range result.Candidates {
		if strings.HasPrefix(candidate.ProviderID, "legacy/off@") && candidate.Reason != "provider is disabled" {
			t.Fatalf("disabled candidate reason = %#v", candidate)
		}
	}
	preferred, err := catalog.Resolve(Request{Operation: OperationConvert, SourceFormat: "png", TargetFormat: "jpg"}, result.Provider.ID)
	if err != nil || preferred.Provider.ProfileID != "first" || preferred.Strategy != "explicit provider preference" {
		t.Fatalf("preferred result = %#v, err=%v", preferred, err)
	}
	if _, err = catalog.FilterEnabled().Resolve(Request{Operation: OperationConvert, SourceFormat: "png", TargetFormat: "pdf"}, ""); err == nil {
		t.Fatal("unsupported mapping should fail")
	}
	resolution, err := catalog.Resolve(Request{Operation: OperationConvert, SourceFormat: "png", TargetFormat: "jpg"}, "legacy/missing@1.0.0/tool@1.0.0")
	if err != nil || resolution.Strategy != "stable fallback; preferred provider unavailable" {
		t.Fatalf("unavailable preference fallback = %#v, err=%v", resolution, err)
	}
}

func TestCompressionUsesTypedCompressionOptions(t *testing.T) {
	profileRecord := management.ProfileRecord{Definition: profile.Definition{SchemaVersion: 1, Type: profile.TypeCompression, ID: "compressor", Version: "1.0.0", Name: "Compressor", Engines: []profile.EngineRequirement{{EngineID: "tool"}}, Compression: &profile.CompressionConfig{Options: []profile.OptionDefinition{{Key: "quality", Label: "Quality", Type: "select", Default: "balanced", Choices: []string{"balanced", "small"}}}}, Rules: []profile.ConversionRule{{EngineID: "tool", Executable: "tool", Sources: []string{"jpg"}, Targets: []string{"jpg"}, Arguments: []string{"{source}", "{compression.args}", "{target}"}, CompressionArguments: []string{"-q", "{option:quality}"}}}}, State: profile.Installation{ProfileID: "compressor", Version: "1.0.0", Enabled: true}}
	engineRecord := management.EngineRecord{Definition: engine.Definition{SchemaVersion: 1, ID: "tool", Version: "1.0.0", Name: "Tool", Platform: "linux-amd64", Executables: map[string]string{"tool": "tool"}}, Installation: engine.Installation{EngineID: "tool", Version: "1.0.0", Managed: true, ExecutablePaths: map[string]string{"tool": "/tool"}}}
	catalog := Build([]management.ProfileRecord{profileRecord}, []management.EngineRecord{engineRecord})
	if len(catalog.Capabilities) != 1 || catalog.Capabilities[0].ID != "compress:jpg:jpg" {
		t.Fatalf("compression capabilities = %#v", catalog.Capabilities)
	}
	provider := catalog.Providers[0]
	if len(provider.Capabilities) != 1 || len(provider.Capabilities[0].Options) != 1 || provider.Capabilities[0].Options[0].Key != "quality" {
		t.Fatalf("compression options = %#v", provider.Capabilities)
	}
}

func TestCompressionOptionsFollowTheCodecRule(t *testing.T) {
	min, max, step := 2.0, 31.0, 1.0
	quality := profile.OptionDefinition{Key: "quality", Label: "JPEG quality", Type: "number", Default: "5", Min: &min, Max: &max, Step: &step}
	levelMin, levelMax, levelStep := 0.0, 9.0, 1.0
	level := profile.OptionDefinition{Key: "compressionLevel", Label: "PNG compression level", Type: "number", Default: "6", Min: &levelMin, Max: &levelMax, Step: &levelStep}
	d := profile.Definition{
		SchemaVersion: 1, Type: profile.TypeCompression, ID: "image-compressor", Version: "1.0.0", Name: "Image compressor",
		Engines: []profile.EngineRequirement{{EngineID: "tool"}},
		Compression: &profile.CompressionConfig{Options: []profile.OptionDefinition{quality, level}},
		Rules: []profile.ConversionRule{
			{EngineID: "tool", Executable: "tool", Sources: []string{"jpg"}, Targets: []string{"jpg"}, Arguments: []string{"{source}", "{compression.args}", "{target}"}, CompressionArguments: []string{"-q", "{option:quality}"}, CompressionOptions: []profile.OptionDefinition{quality}},
			{EngineID: "tool", Executable: "tool", Sources: []string{"jpg"}, Targets: []string{"png"}, Arguments: []string{"{source}", "{compression.args}", "{target}"}, CompressionArguments: []string{"-compression_level", "{option:compressionLevel}"}, CompressionOptions: []profile.OptionDefinition{level}},
		},
	}
	engineRecord := management.EngineRecord{Definition: engine.Definition{SchemaVersion: 1, ID: "tool", Version: "1.0.0", Name: "Tool", Executables: map[string]string{"tool": "tool"}}, Installation: engine.Installation{EngineID: "tool", Version: "1.0.0", Managed: true, ExecutablePaths: map[string]string{"tool": "/tool"}}}
	catalog := Build([]management.ProfileRecord{{Definition: d, State: profile.Installation{ProfileID: d.ID, Version: d.Version, Enabled: true}}}, []management.EngineRecord{engineRecord})
	for _, item := range catalog.Providers[0].Capabilities {
		if item.CapabilityID == "compress:jpg:jpg" && len(item.Options) != 1 || item.CapabilityID == "compress:jpg:jpg" && item.Options[0].Key != "quality" {
			t.Fatalf("JPEG options = %#v", item.Options)
		}
		if item.CapabilityID == "compress:jpg:png" && len(item.Options) != 1 || item.CapabilityID == "compress:jpg:png" && item.Options[0].Key != "compressionLevel" {
			t.Fatalf("PNG options = %#v", item.Options)
		}
	}
}
