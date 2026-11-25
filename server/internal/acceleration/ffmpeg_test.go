package acceleration

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestValidatePolicy(t *testing.T) {
	cases := map[string]string{
		"":                 PolicyAuto,
		"auto":             PolicyAuto,
		"CPU":              PolicyCPU,
		"prefer_hardware":  PolicyPreferHardware,
		"require_hardware": PolicyRequireHardware,
	}
	for input, want := range cases {
		got, err := ValidatePolicy(input)
		if err != nil || got != want {
			t.Fatalf("ValidatePolicy(%q) = (%q, %v), want (%q, nil)", input, got, err, want)
		}
	}
	if _, err := ValidatePolicy("unsafe_flag"); err == nil {
		t.Fatal("expected invalid policy to be rejected")
	}
}

func TestProbeFFmpegCapabilitiesAndCodecResolve(t *testing.T) {
	report := Report{
		SchemaVersion:      1,
		HardwareConfigured: true,
		CPUFallback:        true,
		Accelerators: []Accelerator{
			{ID: "cpu", Vendor: VendorCPU, API: "software", Available: true, Encode: true, Decode: true, HDRPreserving: true},
			{ID: "intel-vaapi/renderD128", Vendor: VendorIntel, API: "vaapi", Device: "renderD128", DevicePath: "/dev/dri/renderD128", Available: true, Encode: true, Decode: true},
			{ID: "intel-qsv/renderD128", Vendor: VendorIntel, API: "qsv", Device: "renderD128", DevicePath: "/dev/dri/renderD128", Available: true, Encode: true, Decode: true},
			{ID: "nvidia-nvenc/nvidia0", Vendor: VendorNVIDIA, API: "nvenc-nvdec", Device: "nvidia0", DevicePath: "/dev/nvidia0", Available: true, Encode: true, Decode: true},
		},
	}
	probe := Probe{
		Run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if len(args) >= 2 && args[1] == "-hwaccels" {
				return []byte("Hardware acceleration methods:\nvaapi\ncuda\n"), nil
			}
			return []byte("Encoders:\n V..... h264_vaapi           H.264/AVC (VAAPI)\n V..... vp9_vaapi            VP9 (VAAPI)\n V..... h264_nvenc           NVIDIA NVENC H.264 encoder\n"), nil
		},
	}
	if err := ProbeFFmpegCapabilities(context.Background(), "/usr/bin/ffmpeg", &report, probe); err != nil {
		t.Fatalf("ProbeFFmpegCapabilities: %v", err)
	}

	// Verify Intel VAAPI got h264 + vp9, QSV got disabled for encode (no qsv encoders), NVIDIA got h264.
	byID := map[string]Accelerator{}
	for _, a := range report.Accelerators {
		byID[a.ID] = a
	}
	if got := strings.Join(byID["intel-vaapi/renderD128"].Codecs, ","); got != "h264,vp9" {
		t.Fatalf("intel-vaapi codecs = %q, want h264,vp9", got)
	}
	if byID["intel-qsv/renderD128"].Encode {
		t.Fatal("expected intel-qsv encode to be false when FFmpeg lacks qsv encoders")
	}
	if got := strings.Join(byID["nvidia-nvenc/nvidia0"].Codecs, ","); got != "h264" {
		t.Fatalf("nvidia codecs = %q, want h264", got)
	}

	// VP9 non-HDR resolves to intel-vaapi.
	sel, err := report.Resolve(Request{Encode: true, Codec: "vp9", Policy: PolicyAuto})
	if err != nil || sel.Accelerator.ID != "intel-vaapi/renderD128" {
		t.Fatalf("expected intel-vaapi for vp9, got %+v err=%v", sel, err)
	}

	// AV1 is not in any hardware accelerator's verified codecs -> CPU fallback.
	sel, err = report.Resolve(Request{Encode: true, Codec: "av1", Policy: PolicyPreferHardware})
	if err != nil || sel.Accelerator.Vendor != VendorCPU || !strings.Contains(sel.Strategy, "av1") {
		t.Fatalf("expected CPU fallback for av1, got %+v err=%v", sel, err)
	}

	// RequireHardware for AV1 must fail explicitly.
	if _, err := report.Resolve(Request{Encode: true, Codec: "av1", Policy: PolicyRequireHardware}); err == nil {
		t.Fatal("expected require_hardware for unsupported codec av1 to fail")
	}

	// PreserveHDR must fall back to CPU even when hardware supports h264.
	sel, err = report.Resolve(Request{Encode: true, Codec: "h264", PreserveHDR: true, Policy: PolicyPreferHardware})
	if err != nil || sel.Accelerator.Vendor != VendorCPU || !strings.Contains(sel.Strategy, "HDR") {
		t.Fatalf("expected CPU fallback for HDR, got %+v err=%v", sel, err)
	}

	// RequireHardware + PreserveHDR must fail explicitly.
	if _, err := report.Resolve(Request{Encode: true, Codec: "h264", PreserveHDR: true, Policy: PolicyRequireHardware}); err == nil {
		t.Fatal("expected require_hardware + PreserveHDR to fail")
	}

	// PolicyCPU must always pick CPU.
	sel, err = report.Resolve(Request{Encode: true, Codec: "h264", Policy: PolicyCPU})
	if err != nil || sel.Accelerator.Vendor != VendorCPU {
		t.Fatalf("expected CPU when PolicyCPU selected, got %+v err=%v", sel, err)
	}
}

func TestBuildFFmpegArgs(t *testing.T) {
	baseArgs := []string{"-y", "-i", "/tmp/in.mp4", "-c:v", "libx264", "-crf", "23", "-c:a", "aac", "/tmp/out.mp4"}

	vaapiSel := Selection{
		Accelerator: Accelerator{ID: "intel-vaapi/renderD128", Vendor: VendorIntel, API: "vaapi", Device: "renderD128", DevicePath: "/dev/dri/renderD128", Available: true},
	}
	got, used, err := BuildFFmpegArgs(baseArgs, vaapiSel, "h264")
	if err != nil || !used {
		t.Fatalf("vaapi BuildFFmpegArgs failed: used=%v err=%v", used, err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"-vaapi_device /dev/dri/renderD128", "-c:v h264_vaapi", "-qp 23", "-vf format=nv12,hwupload"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("vaapi args %q missing %q", joined, want)
		}
	}

	qsvSel := Selection{
		Accelerator: Accelerator{ID: "intel-qsv/renderD128", Vendor: VendorIntel, API: "qsv", Device: "renderD128", DevicePath: "/dev/dri/renderD128", Available: true},
	}
	got, used, err = BuildFFmpegArgs(baseArgs, qsvSel, "h264")
	if err != nil || !used {
		t.Fatalf("qsv BuildFFmpegArgs failed: used=%v err=%v", used, err)
	}
	joined = strings.Join(got, " ")
	for _, want := range []string{"-init_hw_device qsv=hw:/dev/dri/renderD128", "-c:v h264_qsv", "-global_quality 23"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("qsv args %q missing %q", joined, want)
		}
	}

	nvencSel := Selection{
		Accelerator: Accelerator{ID: "nvidia-nvenc/nvidia0", Vendor: VendorNVIDIA, API: "nvenc-nvdec", Device: "nvidia0", DevicePath: "/dev/nvidia0", Available: true},
	}
	got, used, err = BuildFFmpegArgs(baseArgs, nvencSel, "h264")
	if err != nil || !used {
		t.Fatalf("nvenc BuildFFmpegArgs failed: used=%v err=%v", used, err)
	}
	joined = strings.Join(got, " ")
	for _, want := range []string{"-hwaccel cuda", "-c:v h264_nvenc", "-cq 23"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("nvenc args %q missing %q", joined, want)
		}
	}

	// Audio-only or stream-copy must be a no-op.
	audioArgs := []string{"-y", "-i", "/tmp/in.wav", "-c:a", "libopus", "/tmp/out.opus"}
	if _, used, err := BuildFFmpegArgs(audioArgs, vaapiSel, ""); err != nil || used {
		t.Fatalf("expected audio-only to be no-op, got used=%v err=%v", used, err)
	}

	// Non-allowlisted device path must be rejected.
	badSel := Selection{
		Accelerator: Accelerator{ID: "bad", Vendor: VendorIntel, API: "vaapi", DevicePath: "/etc/passwd", Available: true},
	}
	if _, _, err := BuildFFmpegArgs(baseArgs, badSel, "h264"); err == nil {
		t.Fatal("expected non-allowlisted device path to be rejected")
	}
}

func TestIsHardwareDeviceError(t *testing.T) {
	if !IsHardwareDeviceError("AVHWDeviceContext: Failed to initialise VAAPI connection: -1 (operation failed)") {
		t.Fatal("expected VAAPI init failure to be recognized as hardware device error")
	}
	if !IsHardwareDeviceError("Cannot load libcuda.so.1") {
		t.Fatal("expected CUDA load failure to be recognized as hardware device error")
	}
	if IsHardwareDeviceError("Invalid data found when processing input") {
		t.Fatal("corrupt input must not be misclassified as hardware device error")
	}
}

// TestAccelerationBenchmarkHarness verifies deterministic timing/selection
// measurement across CPU and hardware policies without requiring a physical GPU.
func TestAccelerationBenchmarkHarness(t *testing.T) {
	report := Report{
		SchemaVersion:      1,
		HardwareConfigured: true,
		CPUFallback:        true,
		Accelerators: []Accelerator{
			{ID: "cpu", Vendor: VendorCPU, API: "software", Available: true, Encode: true, Decode: true, HDRPreserving: true},
			{ID: "amd-vaapi/renderD128", Vendor: VendorAMD, API: "vaapi", Device: "renderD128", DevicePath: "/dev/dri/renderD128", Available: true, Encode: true, Decode: true, Codecs: []string{"h264", "hevc"}},
		},
	}
	start := time.Now()
	for i := 0; i < 1000; i++ {
		sel, err := report.Resolve(Request{Encode: true, Codec: "h264", Policy: PolicyAuto})
		if err != nil || sel.Accelerator.Vendor != VendorAMD {
			t.Fatalf("unexpected resolve result: %+v err=%v", sel, err)
		}
		if _, used, err := BuildFFmpegArgs([]string{"-i", "in.mp4", "-c:v", "libx264", "-crf", "24", "out.mp4"}, sel, "h264"); err != nil || !used {
			t.Fatalf("unexpected translate result: used=%v err=%v", used, err)
		}
	}
	elapsed := time.Since(start)
	if elapsed > 2*time.Second {
		t.Fatalf("acceleration resolution/translation benchmark unexpectedly slow: %v", elapsed)
	}
}
