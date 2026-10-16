// ppm-pgm is a self-contained converter used by Compose integration tests. It
// translates ASCII PPM P3 pixels into PGM P2 grayscale. The optional delay and
// output-size flags exercise worker cancellation, crash recovery, and limits.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	var input, output string
	var delay time.Duration
	var outputBytes int
	for i := 1; i < len(os.Args); i++ {
		if i+1 >= len(os.Args) {
			fatal("missing argument value")
		}
		value := os.Args[i+1]
		switch os.Args[i] {
		case "--input":
			input = value
		case "--output":
			output = value
		case "--sleep-ms":
			ms, err := strconv.Atoi(value)
			if err != nil || ms < 0 || ms > 60000 {
				fatal("invalid sleep duration")
			}
			delay = time.Duration(ms) * time.Millisecond
		case "--output-bytes":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 100<<20 {
				fatal("invalid output byte count")
			}
			outputBytes = n
		default:
			fatal("unknown argument")
		}
		i++
	}
	if input == "" || output == "" {
		fatal("--input and --output are required")
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	if outputBytes > 0 {
		if err := os.WriteFile(output, make([]byte, outputBytes), 0600); err != nil {
			fatal(err.Error())
		}
		return
	}
	data, err := os.ReadFile(input)
	if err != nil {
		fatal(err.Error())
	}
	tokens := strings.FieldsFunc(string(data), func(r rune) bool { return r == ' ' || r == '\n' || r == '\r' || r == '\t' })
	clean := make([]string, 0, len(tokens))
	for _, token := range tokens {
		if !strings.HasPrefix(token, "#") {
			clean = append(clean, token)
		}
	}
	if len(clean) < 4 || clean[0] != "P3" {
		fatal("input must be an ASCII PPM P3 image")
	}
	w, err := strconv.Atoi(clean[1])
	if err != nil || w < 1 || w > 4096 {
		fatal("invalid PPM width")
	}
	h, err := strconv.Atoi(clean[2])
	if err != nil || h < 1 || h > 4096 {
		fatal("invalid PPM height")
	}
	max, err := strconv.Atoi(clean[3])
	if err != nil || max < 1 || max > 65535 {
		fatal("invalid PPM maximum")
	}
	if len(clean) != 4+w*h*3 {
		fatal("PPM pixel count does not match dimensions")
	}
	out, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		fatal(err.Error())
	}
	writer := bufio.NewWriter(out)
	if _, err = fmt.Fprintf(writer, "P2\n%d %d\n%d\n", w, h, max); err != nil {
		fatal(err.Error())
	}
	for i := 4; i < len(clean); i += 3 {
		r, e1 := strconv.Atoi(clean[i])
		g, e2 := strconv.Atoi(clean[i+1])
		b, e3 := strconv.Atoi(clean[i+2])
		if e1 != nil || e2 != nil || e3 != nil || r < 0 || g < 0 || b < 0 || r > max || g > max || b > max {
			fatal("invalid PPM pixel")
		}
		gray := (299*r + 587*g + 114*b + 500) / 1000
		if _, err = fmt.Fprintf(writer, "%d ", gray); err != nil {
			fatal(err.Error())
		}
	}
	if err = writer.Flush(); err != nil {
		fatal(err.Error())
	}
	if err = out.Sync(); err != nil {
		fatal(err.Error())
	}
	if err = out.Close(); err != nil {
		fatal(err.Error())
	}
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
