package acceleration

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// softwareCodecMap maps validated FFmpeg software video encoder names to
// canonical codec identifiers used by hardware capability matching.
var softwareCodecMap = map[string]string{
	"libx264":    "h264",
	"libx265":    "hevc",
	"libvpx-vp9": "vp9",
	"libaom-av1": "av1",
	"libsvtav1":  "av1",
}

// hardwareEncoderMap maps (api, canonicalCodec) -> allowlisted FFmpeg encoder name.
var hardwareEncoderMap = map[string]map[string]string{
	"vaapi": {
		"h264": "h264_vaapi",
		"hevc": "hevc_vaapi",
		"vp9":  "vp9_vaapi",
		"av1":  "av1_vaapi",
	},
	"qsv": {
		"h264": "h264_qsv",
		"hevc": "hevc_qsv",
		"vp9":  "vp9_qsv",
		"av1":  "av1_qsv",
	},
	"nvenc-nvdec": {
		"h264": "h264_nvenc",
		"hevc": "hevc_nvenc",
		"av1":  "av1_nvenc",
	},
}

// ProbeFFmpegCapabilities inspects an installed FFmpeg binary's supported
// hardware accelerations (-hwaccels) and encoders (-encoders) and annotates
// each available hardware accelerator in report. It never weakens HDR policy
// or enables devices that failed sysfs/driver verification.
func ProbeFFmpegCapabilities(ctx context.Context, ffmpegPath string, report *Report, probe Probe) error {
	if report == nil || strings.TrimSpace(ffmpegPath) == "" {
		return nil
	}
	if probe.Run == nil {
		return fmt.Errorf("accelerator probe runner is nil")
	}
	hwOut, hwErr := probe.Run(ctx, ffmpegPath, "-hide_banner", "-hwaccels")
	encOut, encErr := probe.Run(ctx, ffmpegPath, "-hide_banner", "-encoders")
	if hwErr != nil && encErr != nil {
		return fmt.Errorf("probe ffmpeg capabilities: hwaccels=%v encoders=%v", hwErr, encErr)
	}
	hwaccels := parseFFmpegHWAccels(string(hwOut))
	encoders := parseFFmpegEncoders(string(encOut))

	for i := range report.Accelerators {
		acc := &report.Accelerators[i]
		if acc.Vendor == VendorCPU || !acc.Available {
			continue
		}
		var matchedAccels []string
		switch acc.API {
		case "vaapi":
			if hwaccels["vaapi"] {
				matchedAccels = append(matchedAccels, "vaapi")
			}
		case "qsv":
			if hwaccels["qsv"] {
				matchedAccels = append(matchedAccels, "qsv")
			}
		case "nvenc-nvdec":
			if hwaccels["cuda"] {
				matchedAccels = append(matchedAccels, "cuda")
			}
		}
		var matchedCodecs []string
		if encMap, ok := hardwareEncoderMap[acc.API]; ok {
			for canonical, ffmpegEnc := range encMap {
				if encoders[ffmpegEnc] {
					matchedCodecs = append(matchedCodecs, canonical)
				}
			}
		}
		sort.Strings(matchedCodecs)
		acc.HWAccels = matchedAccels
		acc.Codecs = matchedCodecs
		acc.Decode = len(matchedAccels) > 0
		acc.Encode = len(matchedCodecs) > 0
		acc.Verification = "device-driver-ffmpeg"
		if len(matchedCodecs) == 0 {
			acc.Reason = fmt.Sprintf("device verified, but installed FFmpeg build does not expose %s hardware encoders", acc.API)
		} else {
			acc.Reason = fmt.Sprintf("verified %s device and FFmpeg encoders (%s); HDR preservation remains CPU-only", acc.API, strings.Join(matchedCodecs, ", "))
		}
	}
	return nil
}

func parseFFmpegHWAccels(output string) map[string]bool {
	result := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasSuffix(trimmed, ":") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 1 {
			result[strings.ToLower(fields[0])] = true
		}
	}
	return result
}

func parseFFmpegEncoders(output string) map[string]bool {
	result := make(map[string]bool)
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			continue
		}
		flags := fields[0]
		if len(flags) == 6 && strings.HasPrefix(flags, "V") {
			result[strings.ToLower(fields[1])] = true
		}
	}
	return result
}

// DetectVideoCodecFromArgs inspects validated FFmpeg arguments and returns the
// canonical video codec if a translatable software video encoder is present.
// Audio-only, image, or stream-copy (-c:v copy) jobs return ("", false).
func DetectVideoCodecFromArgs(args []string) (string, bool) {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-c:v" || args[i] == "-vcodec" {
			enc := strings.ToLower(strings.TrimSpace(args[i+1]))
			if codec, ok := softwareCodecMap[enc]; ok {
				return codec, true
			}
			return "", false
		}
	}
	return "", false
}

// BuildFFmpegArgs translates validated software FFmpeg arguments into
// allowlisted hardware-accelerated arguments for the selected accelerator.
// Returns (translatedArgs, usedHardware, error). When the selection is CPU or
// the command has no translatable video encoder, returns (args, false, nil).
func BuildFFmpegArgs(args []string, sel Selection, codec string) ([]string, bool, error) {
	if sel.Accelerator.Vendor == VendorCPU || !sel.Accelerator.Available {
		return args, false, nil
	}
	canonical := strings.ToLower(strings.TrimSpace(codec))
	if canonical == "" {
		detected, ok := DetectVideoCodecFromArgs(args)
		if !ok {
			return args, false, nil
		}
		canonical = detected
	}
	apiEncoders, ok := hardwareEncoderMap[sel.Accelerator.API]
	if !ok {
		return args, false, nil
	}
	hwEncoder, ok := apiEncoders[canonical]
	if !ok {
		return args, false, nil
	}

	var prefix []string
	var vfAppend string
	qualityFlag := "-qp"

	switch sel.Accelerator.API {
	case "vaapi":
		devicePath := sel.Accelerator.DevicePath
		if devicePath == "" && sel.Accelerator.Device != "" {
			devicePath = filepath.Join("/dev/dri", sel.Accelerator.Device)
		}
		if !validDevicePath(devicePath, "/dev/dri", renderNodeName) {
			return nil, false, fmt.Errorf("vaapi accelerator device path %q is not allowlisted", devicePath)
		}
		prefix = []string{"-vaapi_device", devicePath}
		vfAppend = "format=nv12,hwupload"
		qualityFlag = "-qp"
	case "qsv":
		devicePath := sel.Accelerator.DevicePath
		if devicePath == "" && sel.Accelerator.Device != "" {
			devicePath = filepath.Join("/dev/dri", sel.Accelerator.Device)
		}
		if !validDevicePath(devicePath, "/dev/dri", renderNodeName) {
			return nil, false, fmt.Errorf("qsv accelerator device path %q is not allowlisted", devicePath)
		}
		prefix = []string{"-init_hw_device", "qsv=hw:" + devicePath, "-filter_hw_device", "hw"}
		vfAppend = "hwupload=extra_hw_frames=64,format=qsv"
		qualityFlag = "-global_quality"
	case "nvenc-nvdec":
		prefix = []string{"-hwaccel", "cuda"}
		qualityFlag = "-cq"
	default:
		return args, false, nil
	}

	out := make([]string, 0, len(args)+len(prefix)+4)
	out = append(out, prefix...)
	hasVF := false
	replacedCodec := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "-c:v" || arg == "-vcodec") && i+1 < len(args) {
			enc := strings.ToLower(strings.TrimSpace(args[i+1]))
			if _, translatable := softwareCodecMap[enc]; translatable {
				out = append(out, "-c:v", hwEncoder)
				replacedCodec = true
				i++
				continue
			}
		}
		// Replace software-only -crf with the hardware encoder's quality flag.
		if arg == "-crf" && i+1 < len(args) {
			out = append(out, qualityFlag, args[i+1])
			i++
			continue
		}
		// Strip libvpx-vp9-specific -b:v 0 when switching to hardware quality mode.
		if arg == "-deadline" && i+1 < len(args) {
			i++
			continue
		}
		if (arg == "-vf" || arg == "-filter:v") && i+1 < len(args) && vfAppend != "" {
			hasVF = true
			out = append(out, arg, args[i+1]+","+vfAppend)
			i++
			continue
		}
		out = append(out, arg)
	}
	if !replacedCodec {
		return args, false, nil
	}
	if vfAppend != "" && !hasVF && len(out) >= 1 {
		// Insert -vf before the final output path argument.
		target := out[len(out)-1]
		out = append(out[:len(out)-1], "-vf", vfAppend, target)
	}
	return out, true, nil
}

// IsHardwareDeviceError inspects FFmpeg stderr to distinguish GPU/driver
// initialization failures from corrupt input files.
func IsHardwareDeviceError(stderr string) bool {
	lower := strings.ToLower(stderr)
	patterns := []string{
		"failed to initialise vaapi connection",
		"no va display found",
		"vaapi device creation failed",
		"device creation failed",
		"cannot load libcuda",
		"no capable devices found",
		"nvenc",
		"opencl",
		"error initializing an mfx session",
		"qsv",
		"cannot open /dev/dri/",
		"hwaccel",
		"driver version is insufficient",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}
