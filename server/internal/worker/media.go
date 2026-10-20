package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var pixelBitDepthRE = regexp.MustCompile(`p([0-9]{2})(?:le|be)?$`)

type mediaProbe struct {
	Duration float64
	Streams  []mediaStream
}

type mediaStream struct {
	CodecType        string          `json:"codec_type"`
	Width            int             `json:"width"`
	Height           int             `json:"height"`
	ColorPrimaries   string          `json:"color_primaries"`
	ColorTransfer    string          `json:"color_transfer"`
	ColorSpace       string          `json:"color_space"`
	ColorRange       string          `json:"color_range"`
	BitsPerRawSample string          `json:"bits_per_raw_sample"`
	PixelFormat      string          `json:"pix_fmt"`
	SideData         []mediaSideData `json:"side_data_list"`
}

type mediaSideData struct {
	Type string `json:"side_data_type"`
}

type mediaProbeDocument struct {
	Format struct {
		Duration json.RawMessage `json:"duration"`
	} `json:"format"`
	Streams []mediaStream `json:"streams"`
}

func probeMedia(parent context.Context, engineRoot, work, inputPath string, in Invocation) (*mediaProbe, error) {
	executable := filepath.Join(engineRoot, in.EngineID, in.EngineVersion, filepath.FromSlash(in.ProbeExecutableRelativePath))
	if err := rejectSymlinkPath(engineRoot, executable); err != nil {
		return nil, fmt.Errorf("resolve media probe: %w", err)
	}
	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0111 == 0 {
		return nil, fmt.Errorf("compression probe executable is unavailable")
	}
	probeCtx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, executable, "-v", "error", "-show_streams", "-show_format", "-of", "json", inputPath)
	cmd.Dir = work
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work, "LANG=C.UTF-8"}
	stdout, stderr := &boundedBuffer{}, &boundedBuffer{}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err = cmd.Run(); err != nil {
		if probeCtx.Err() != nil {
			return nil, fmt.Errorf("media probe timed out")
		}
		return nil, fmt.Errorf("media probe failed")
	}
	var document mediaProbeDocument
	if err = json.Unmarshal([]byte(stdout.String()), &document); err != nil {
		return nil, fmt.Errorf("media probe returned invalid metadata")
	}
	duration, err := parseProbeNumber(document.Format.Duration)
	if err != nil {
		// Duration is required only by the target-size bitrate path. Fidelity
		// checks can still validate a stream when a container omits it.
		duration = 0
	}
	return &mediaProbe{Duration: duration, Streams: document.Streams}, nil
}

func parseProbeNumber(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var text string
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, err
		}
	} else {
		text = string(raw)
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
		return 0, fmt.Errorf("invalid probe number")
	}
	return number, nil
}

func (s mediaStream) bitDepth() int {
	if value, err := strconv.Atoi(strings.TrimSpace(s.BitsPerRawSample)); err == nil && value > 0 {
		return value
	}
	match := pixelBitDepthRE.FindStringSubmatch(strings.ToLower(strings.TrimSpace(s.PixelFormat)))
	if len(match) == 2 {
		value, _ := strconv.Atoi(match[1])
		return value
	}
	return 0
}

func (s mediaStream) sideDataContains(needle string) bool {
	needle = strings.ToLower(needle)
	for _, data := range s.SideData {
		if strings.Contains(strings.ToLower(data.Type), needle) {
			return true
		}
	}
	return false
}

func (s mediaStream) hdr() (isHDR, dynamic bool) {
	transfer := strings.ToLower(strings.TrimSpace(s.ColorTransfer))
	primaries := strings.ToLower(strings.TrimSpace(s.ColorPrimaries))
	bitDepth := s.bitDepth()
	isHDR = transfer == "smpte2084" || transfer == "arib-std-b67" || s.sideDataContains("mastering display metadata") || s.sideDataContains("content light level metadata") || primaries == "bt2020" && bitDepth >= 10
	dynamic = s.sideDataContains("dynamic") || s.sideDataContains("hdr10+") || s.sideDataContains("dolby vision") || s.sideDataContains("dovi")
	return isHDR, dynamic
}

func firstVideoStream(probe mediaProbe) (mediaStream, bool) {
	for _, stream := range probe.Streams {
		if strings.EqualFold(stream.CodecType, "video") {
			return stream, true
		}
	}
	return mediaStream{}, false
}

func validateMediaFidelity(input, output mediaProbe, in Invocation) error {
	source, hasVideo := firstVideoStream(input)
	if !hasVideo {
		return nil
	}
	target, hasOutputVideo := firstVideoStream(output)
	if !hasOutputVideo {
		return fmt.Errorf("output lost the source video stream")
	}
	if in.PreserveResolution && (source.Width < 1 || source.Height < 1 || target.Width != source.Width || target.Height != source.Height) {
		return fmt.Errorf("output changed the source video resolution")
	}
	if !in.PreserveHDR {
		return nil
	}
	sourceHDR, sourceDynamic := source.hdr()
	if !sourceHDR {
		return nil
	}
	targetHDR, targetDynamic := target.hdr()
	if !targetHDR {
		return fmt.Errorf("output lost source HDR signaling")
	}
	properties := []struct {
		label       string
		sourceValue string
		targetValue string
	}{
		{label: "color primaries", sourceValue: source.ColorPrimaries, targetValue: target.ColorPrimaries},
		{label: "color transfer", sourceValue: source.ColorTransfer, targetValue: target.ColorTransfer},
		{label: "color matrix", sourceValue: source.ColorSpace, targetValue: target.ColorSpace},
		{label: "color range", sourceValue: source.ColorRange, targetValue: target.ColorRange},
	}
	for _, property := range properties {
		label, sourceValue := property.label, property.sourceValue
		if sourceValue != "" && !strings.EqualFold(sourceValue, property.targetValue) {
			return fmt.Errorf("output changed source %s", label)
		}
	}
	if sourceDepth := source.bitDepth(); sourceDepth > 0 && target.bitDepth() < sourceDepth {
		return fmt.Errorf("output reduced source video bit depth")
	}
	for _, label := range []string{"mastering display metadata", "content light level metadata"} {
		if source.sideDataContains(label) && !target.sideDataContains(label) {
			return fmt.Errorf("output lost source %s", label)
		}
	}
	if sourceDynamic && !targetDynamic {
		return fmt.Errorf("output lost source dynamic HDR metadata")
	}
	return nil
}
