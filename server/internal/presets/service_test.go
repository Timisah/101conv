package presets

import (
	"errors"
	"testing"

	"github.com/timisah/101conv/server/internal/capability"
)

func TestValidateWriteBoundsAndNormalizesFormats(t *testing.T) {
	request := WriteRequest{Name: "  Photos  ", Mode: "CONVERT", SourceFormat: ".JPG", TargetFormat: "JXL", Options: map[string]string{"quality": "80"}}
	normalizeWrite(&request)
	if err := validateWrite(request); err != nil {
		t.Fatalf("validateWrite() error = %v", err)
	}
	if request.Name != "Photos" || request.SourceFormat != "jpg" || request.TargetFormat != "jxl" {
		t.Fatalf("request was not normalized: %#v", request)
	}
	tooLong := request
	tooLong.Name = string(make([]byte, 81))
	if !errors.Is(validateWrite(tooLong), ErrInvalidInput) {
		t.Fatal("expected long names to be rejected")
	}
}

func TestValidateOptionsHonorsTypedProviderSchema(t *testing.T) {
	min, max := 1.0, 100.0
	provider := capability.Provider{Capabilities: []capability.ProviderCapability{{Options: []capability.Option{
		{Key: "quality", Type: "number", Min: &min, Max: &max},
		{Key: "mode", Type: "select", Choices: []string{"fast", "best"}},
		{Key: "metadata", Type: "boolean"},
	}}}}
	if err := validateOptions(provider, map[string]string{"quality": "80", "mode": "best", "metadata": "true"}); err != nil {
		t.Fatalf("valid options rejected: %v", err)
	}
	for _, values := range []map[string]string{{"quality": "0"}, {"quality": "101"}, {"quality": "oops"}, {"mode": "unknown"}, {"metadata": "yes"}, {"unknown": "x"}} {
		if !errors.Is(validateOptions(provider, values), ErrIncompatible) {
			t.Errorf("expected options %v to be rejected", values)
		}
	}
}

func TestNewIDIsUUIDShape(t *testing.T) {
	id, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 36 || id[8] != '-' || id[13] != '-' || id[18] != '-' || id[23] != '-' {
		t.Fatalf("unexpected id %q", id)
	}
}
