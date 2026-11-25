package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timisah/101conv/server/internal/acceleration"
)

func TestProcessRunnerHardwareAccelerationAndFallback(t *testing.T) {
	root := t.TempDir()
	engineDir := filepath.Join(root, "engines", "ffmpeg", "1.0.0", "bin")
	uploadDir := filepath.Join(root, "uploads")
	resultDir := filepath.Join(root, "results")
	workDir := filepath.Join(root, "work")
	for _, d := range []string{engineDir, uploadDir, resultDir, workDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	// Fake ffmpeg that succeeds on hardware VAAPI when mode=ok, fails with a
	// VAAPI init error when mode=fail_hw, and always succeeds on software libx264.
	script := `#!/bin/sh
out=""
has_vaapi=0
for arg in "$@"; do
  if [ "$arg" = "-vaapi_device" ]; then
    has_vaapi=1
  fi
  out="$arg"
done
if [ "$has_vaapi" = "1" ] && [ -f "` + filepath.Join(root, "fail_hw") + `" ]; then
  echo "AVHWDeviceContext: Failed to initialise VAAPI connection: -1 (operation failed)" >&2
  exit 1
fi
printf 'fake-mp4-output-%s' "$has_vaapi" > "$out"
`
	ffmpegBin := filepath.Join(engineDir, "ffmpeg")
	if err := os.WriteFile(ffmpegBin, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}

	inputBytes := []byte("fake-input-video")
	inputSum := sha256.Sum256(inputBytes)
	inputID := "in-video-1"
	if err := os.WriteFile(filepath.Join(uploadDir, inputID), inputBytes, 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	report := acceleration.Report{
		SchemaVersion:      1,
		HardwareConfigured: true,
		CPUFallback:        true,
		Accelerators: []acceleration.Accelerator{
			{ID: "cpu", Vendor: acceleration.VendorCPU, API: "software", Available: true, Encode: true, Decode: true, HDRPreserving: true},
			{ID: "intel-vaapi/renderD128", Vendor: acceleration.VendorIntel, API: "vaapi", Device: "renderD128", DevicePath: "/dev/dri/renderD128", Available: true, Encode: true, Decode: true, Codecs: []string{"h264"}},
		},
	}

	runner := &ProcessRunner{EngineRoot: filepath.Join(root, "engines"), UploadRoot: uploadDir, ResultRoot: resultDir, WorkRoot: workDir}
	baseInv := Invocation{
		JobID:                  "job-hw-1",
		Attempt:                1,
		InputID:                inputID,
		EngineID:               "ffmpeg",
		EngineVersion:          "1.0.0",
		ExecutableRelativePath: "bin/ffmpeg",
		Arguments:              []string{"-y", "-i", "{source}", "-c:v", "libx264", "-crf", "23", "{target}"},
		SourceExtension:        "mp4",
		TargetExtension:        "mp4",
		ExpectedInputSHA256:    hex.EncodeToString(inputSum[:]),
		ExpectedInputSize:      int64(len(inputBytes)),
		WorkerAcceleration:     report,
	}

	// 1. Hardware succeeds when available.
	res, err := runner.Run(context.Background(), baseInv)
	if err != nil {
		t.Fatalf("expected hardware run to succeed: %v", err)
	}
	if res.Acceleration == nil || res.Acceleration.AcceleratorID != "intel-vaapi/renderD128" || res.Acceleration.Fallback {
		t.Fatalf("unexpected acceleration summary on hardware success: %+v", res.Acceleration)
	}
	outBytes, _ := os.ReadFile(filepath.Join(resultDir, res.ResultKey))
	if string(outBytes) != "fake-mp4-output-1" {
		t.Fatalf("expected hardware output marker, got %q", string(outBytes))
	}

	// 2. Hardware device error transparently falls back to CPU under auto/prefer_hardware.
	if err := os.WriteFile(filepath.Join(root, "fail_hw"), []byte("1"), 0o644); err != nil {
		t.Fatalf("write fail_hw: %v", err)
	}
	fallbackInv := baseInv
	fallbackInv.JobID = "job-hw-2"
	fallbackInv.HardwareAcceleration = acceleration.PolicyPreferHardware
	res, err = runner.Run(context.Background(), fallbackInv)
	if err != nil {
		t.Fatalf("expected transparent CPU fallback to succeed: %v", err)
	}
	if res.Acceleration == nil || res.Acceleration.Vendor != acceleration.VendorCPU || !res.Acceleration.Fallback || res.Acceleration.DeviceError == "" {
		t.Fatalf("expected fallback acceleration summary with deviceError, got %+v", res.Acceleration)
	}
	outBytes, _ = os.ReadFile(filepath.Join(resultDir, res.ResultKey))
	if string(outBytes) != "fake-mp4-output-0" {
		t.Fatalf("expected CPU fallback output marker, got %q", string(outBytes))
	}

	// 3. require_hardware must NOT fall back on device error.
	reqInv := baseInv
	reqInv.JobID = "job-hw-3"
	reqInv.HardwareAcceleration = acceleration.PolicyRequireHardware
	res, err = runner.Run(context.Background(), reqInv)
	if err == nil {
		t.Fatal("expected require_hardware to fail on hardware device error")
	}
	if res.Acceleration == nil || !strings.Contains(res.Acceleration.DeviceError, "exited with code 1") {
		t.Fatalf("expected device error recorded on failed require_hardware job, got %+v", res.Acceleration)
	}
}
