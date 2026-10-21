package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// quality-converter is a deterministic fixture for the Worker quality search.
// It emits a valid JXL container header and, by default, a size proportional
// to --quality. The optional non-monotonic mode exercises unusual encoder
// size curves; no real image encoding is intended here.
func main() {
	quality := 0
	nonMonotonic := false
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "--quality=") {
			quality, _ = strconv.Atoi(strings.TrimPrefix(arg, "--quality="))
		}
		if arg == "--non-monotonic" {
			nonMonotonic = true
		}
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "missing output")
		os.Exit(2)
	}
	output := os.Args[len(os.Args)-1]
	size := 20 + quality*4
	if nonMonotonic {
		// Deliberately violate the usual quality/size ordering so worker
		// tests can verify that a bounded search does not infer infeasibility
		// from a single failed endpoint or binary-search assumption.
		sizes := map[int]int{1: 180, 2: 220, 3: 320, 4: 340, 5: 380, 6: 200, 7: 360, 8: 400}
		if nonMonotonicSize, ok := sizes[quality]; ok {
			size = nonMonotonicSize
		}
	}
	data := make([]byte, size)
	copy(data, []byte{0, 0, 0, 12, 'J', 'X', 'L', ' ', 0x0d, 0x0a, 0x87, 0x0a})
	if err := os.WriteFile(output, data, 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
