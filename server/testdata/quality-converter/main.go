package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// quality-converter is a deterministic fixture for the Worker quality search.
// It emits a valid JXL container header and a size proportional to --quality;
// no real image encoding is intended here.
func main() {
	quality := 0
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "--quality=") {
			quality, _ = strconv.Atoi(strings.TrimPrefix(arg, "--quality="))
		}
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "missing output")
		os.Exit(2)
	}
	output := os.Args[len(os.Args)-1]
	data := make([]byte, 20+quality*4)
	copy(data, []byte{0, 0, 0, 12, 'J', 'X', 'L', ' ', 0x0d, 0x0a, 0x87, 0x0a})
	if err := os.WriteFile(output, data, 0600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
