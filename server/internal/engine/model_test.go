package engine

import "testing"

func TestVersionRangeIsInclusiveExclusiveAndUnpinned(t *testing.T) {
	range_ := VersionRange{Minimum: "9.0.0", MaximumExclusive: "10.0.0"}
	if err := range_.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		version string
		want    bool
	}{
		{"8.9.9", false},
		{"9.0.0", true},
		{"9.5.2", true},
		{"10.0.0", false},
		{"not-a-version", false},
	} {
		if got := range_.Contains(test.version); got != test.want {
			t.Errorf("Contains(%q) = %v, want %v", test.version, got, test.want)
		}
	}
}

func TestDefinitionRejectsUnsafePackagePaths(t *testing.T) {
	definition := Definition{
		SchemaVersion: 1, ID: "image-tool", Version: "1.2.3", Name: "Image Tool", Platform: "linux-amd64",
		Executables: map[string]string{"tool": "../../tmp/tool"},
		Artifact:    &Artifact{URL: "https://example.test/tool.tar.gz", SHA256: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Format: "tar.gz"},
	}
	if err := definition.Validate(); err == nil {
		t.Fatal("expected traversal path to be rejected")
	}
}
