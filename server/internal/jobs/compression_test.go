package jobs

import (
	"errors"
	"testing"

	"github.com/timisah/101conv/server/internal/profile"
)

func TestValidateCompressionOptionsAndArgumentExpansion(t *testing.T) {
	min, max, step := 1.0, 64.0, 1.0
	config := profile.CompressionConfig{Options: []profile.OptionDefinition{{Key: "targetSizeMb", Label: "Target size (MB)", Type: "number", Default: "8", Min: &min, Max: &max, Step: &step, TargetSizeMB: true}}}
	values, targetBytes, err := validateCompressionOptions(config, map[string]string{"targetSizeMb": "4"})
	if err != nil {
		t.Fatal(err)
	}
	if values["targetSizeMb"] != "4" || targetBytes != 4<<20 {
		t.Fatalf("normalized compression settings values=%v bytes=%d", values, targetBytes)
	}
	if _, _, err = validateCompressionOptions(config, map[string]string{"targetSizeMb": "65"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("out-of-range option error=%v", err)
	}
	if _, _, err = validateCompressionOptions(config, map[string]string{"extra": "-filter_complex"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("unknown option error=%v", err)
	}
	expanded, err := expandCompressionOptions([]string{"-crf", "{option:targetSizeMb}", "{compression.videoKbps}"}, values)
	if err != nil || expanded[1] != "4" || expanded[2] != "{compression.videoKbps}" {
		t.Fatalf("compression argument expansion args=%v err=%v", expanded, err)
	}
}
