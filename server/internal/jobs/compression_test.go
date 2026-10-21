package jobs

import (
	"errors"
	"reflect"
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

func TestOptionalTargetSizeAppliesNoLimitUnlessRequested(t *testing.T) {
	qMin, qMax, qStep := 1.0, 100.0, 1.0
	sMin, sMax, sStep := 1.0, 200.0, 1.0
	config := profile.CompressionConfig{
		TargetQualityOption: "quality",
		Options: []profile.OptionDefinition{
			{Key: "quality", Label: "Quality", Type: "number", Default: "85", Min: &qMin, Max: &qMax, Step: &qStep},
			{Key: "targetSizeMb", Label: "Maximum output size (MB)", Type: "number", Default: "", Min: &sMin, Max: &sMax, Step: &sStep, TargetSizeMB: true, Optional: true},
		},
	}
	for name, requested := range map[string]map[string]string{"absent": nil, "blank": {"targetSizeMb": ""}, "whitespace": {"targetSizeMb": "  "}} {
		values, targetBytes, err := validateCompressionOptions(config, requested)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if targetBytes != 0 {
			t.Fatalf("%s: an unset optional maximum must not impose a cap, got %d bytes", name, targetBytes)
		}
		if _, present := values["targetSizeMb"]; present {
			t.Fatalf("%s: an unset optional maximum must be absent from the frozen options: %v", name, values)
		}
		if values["quality"] != "85" {
			t.Fatalf("%s: quality default was not preserved: %v", name, values)
		}
	}
	values, targetBytes, err := validateCompressionOptions(config, map[string]string{"targetSizeMb": "2"})
	if err != nil || targetBytes != 2<<20 || values["targetSizeMb"] != "2" {
		t.Fatalf("explicit cap values=%v bytes=%d err=%v", values, targetBytes, err)
	}
	if _, _, err = validateCompressionOptions(config, map[string]string{"targetSizeMb": "0"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("an out-of-range explicit cap must still be rejected: %v", err)
	}
	if _, _, err = validateCompressionOptions(config, map[string]string{"targetSizeMb": "abc"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("a non-numeric explicit cap must still be rejected: %v", err)
	}
	// Non-optional options keep rejecting blank values (older profile releases).
	legacy := profile.CompressionConfig{Options: []profile.OptionDefinition{{Key: "targetSizeMb", Label: "Target", Type: "number", Default: "8", Min: &sMin, Max: &sMax, Step: &sStep, TargetSizeMB: true}}}
	if _, _, err = validateCompressionOptions(legacy, map[string]string{"targetSizeMb": ""}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("a blank non-optional target must still be rejected: %v", err)
	}
	if _, targetBytes, err = validateCompressionOptions(legacy, nil); err != nil || targetBytes != 8<<20 {
		t.Fatalf("legacy default must be unchanged: bytes=%d err=%v", targetBytes, err)
	}
}

func TestAllocateCompressionBudget(t *testing.T) {
	items := []compressionBudgetItem{
		{SizeBytes: 100, MinMB: 1, MaxMB: 20},
		{SizeBytes: 300, MinMB: 1, MaxMB: 20},
	}
	allocated, err := allocateCompressionBudget(items, 20)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{5, 15}; !reflect.DeepEqual(allocated, want) {
		t.Fatalf("proportional allocation=%v want=%v", allocated, want)
	}

	allocated, err = allocateCompressionBudget([]compressionBudgetItem{
		{SizeBytes: 100, MinMB: 1, MaxMB: 20},
		{SizeBytes: 1, MinMB: 3, MaxMB: 20},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{7, 3}; !reflect.DeepEqual(allocated, want) {
		t.Fatalf("minimum-constrained allocation=%v want=%v", allocated, want)
	}

	if _, err = allocateCompressionBudget([]compressionBudgetItem{
		{SizeBytes: 100, MinMB: 3, MaxMB: 20},
		{SizeBytes: 100, MinMB: 3, MaxMB: 20},
	}, 5); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("infeasible combined budget error=%v", err)
	}
	allocated, err = allocateCompressionBudget([]compressionBudgetItem{
		{SizeBytes: 100, MinMB: 1, MaxMB: 2},
		{SizeBytes: 100, MinMB: 1, MaxMB: 3},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{2, 3}; !reflect.DeepEqual(allocated, want) {
		t.Fatalf("best-effort capped allocation=%v want=%v", allocated, want)
	}
}
