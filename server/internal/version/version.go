// Package version provides the numeric version comparison used by repository
// compatibility ranges. Suffixes are accepted and do not change the numeric
// release line, matching the existing catalog compatibility behavior.
package version

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var pattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

type Version struct {
	Major int
	Minor int
	Patch int
}

func Parse(value string) (Version, error) {
	match := pattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return Version{}, fmt.Errorf("expected major.minor.patch, got %q", value)
	}
	parts := [3]int{}
	for i := range parts {
		n, err := strconv.Atoi(match[i+1])
		if err != nil {
			return Version{}, err
		}
		parts[i] = n
	}
	return Version{Major: parts[0], Minor: parts[1], Patch: parts[2]}, nil
}

func Compare(left, right string) (int, error) {
	a, err := Parse(left)
	if err != nil {
		return 0, err
	}
	b, err := Parse(right)
	if err != nil {
		return 0, err
	}
	av := [...]int{a.Major, a.Minor, a.Patch}
	bv := [...]int{b.Major, b.Minor, b.Patch}
	for i := range av {
		if av[i] < bv[i] {
			return -1, nil
		}
		if av[i] > bv[i] {
			return 1, nil
		}
	}
	return 0, nil
}
