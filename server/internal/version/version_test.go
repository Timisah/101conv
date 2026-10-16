package version

import "testing"

func TestCompareMatchesExistingNumericReleaseLineBehavior(t *testing.T) {
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"1.2.3-dev", "1.2.3", 0},
		{"1.2.2", "1.2.3", -1},
		{"2.0.0+build.1", "1.99.99", 1},
	} {
		got, err := Compare(test.left, test.right)
		if err != nil || got != test.want {
			t.Fatalf("Compare(%q, %q) = %d, %v; want %d", test.left, test.right, got, err, test.want)
		}
	}
}

func TestParseRejectsMalformedVersions(t *testing.T) {
	for _, value := range []string{"", "1.2", "01.2.3", "1.x.3"} {
		if _, err := Parse(value); err == nil {
			t.Errorf("Parse(%q) unexpectedly succeeded", value)
		}
	}
}
