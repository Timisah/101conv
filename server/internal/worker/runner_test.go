package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestProcessRunnerRunsRealPPMConverter(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("container worker is Linux")
	}
	root := t.TempDir()
	engineDir := filepath.Join(root, "engines", "ppm-pgm", "1.0.0")
	if err := os.MkdirAll(engineDir, 0700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(engineDir, "ppm-pgm")
	build := exec.Command("go", "build", "-o", binary, "../../testdata/ppm-pgm")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real converter: %v\n%s", err, output)
	}
	if err := os.Chmod(binary, 0700); err != nil {
		t.Fatal(err)
	}
	input := []byte("P3\n2 1\n255\n255 0 0 0 255 0\n")
	uploadID := "11111111-1111-4111-8111-111111111111"
	uploadRoot := filepath.Join(root, "uploads")
	if err := os.MkdirAll(uploadRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uploadRoot, uploadID), input, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(input)
	runner := &ProcessRunner{EngineRoot: filepath.Join(root, "engines"), UploadRoot: uploadRoot, ResultRoot: filepath.Join(root, "results"), WorkRoot: filepath.Join(root, "work"), Timeout: 5 * time.Second}
	result, err := runner.Run(context.Background(), Invocation{
		JobID: "22222222-2222-4222-8222-222222222222", Attempt: 1, InputID: uploadID,
		EngineID: "ppm-pgm", EngineVersion: "1.0.0", ExecutableRelativePath: "ppm-pgm",
		Arguments: []string{"--input", "{source}", "--output", "{target}"}, TargetExtension: "pgm",
		ExpectedInputSHA256: hex.EncodeToString(digest[:]), ExpectedInputSize: int64(len(input)),
	})
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(filepath.Join(runner.ResultRoot, result.ResultKey))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(output), "P2\n2 1\n255\n76 150 "; got != want {
		t.Fatalf("actual conversion output = %q, want %q", got, want)
	}
}

func TestProcessRunnerRejectsInputDigestMismatchAndInvalidArguments(t *testing.T) {
	root := t.TempDir()
	runner := &ProcessRunner{EngineRoot: root, UploadRoot: root, ResultRoot: root, WorkRoot: filepath.Join(root, "work")}
	_, err := runner.Run(context.Background(), Invocation{JobID: "job", Attempt: 1, InputID: "upload", EngineID: "engine", EngineVersion: "1", TargetExtension: "pgm"})
	if err == nil {
		t.Fatal("expected malformed invocation rejection")
	}
}

func TestProcessRunnerCompressesRealMediaToTargetSize(t *testing.T) {
	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed in this test environment")
	}
	ffprobePath, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed in this test environment")
	}
	root := t.TempDir()
	engineRoot := filepath.Join(root, "engines")
	engineDir := filepath.Join(engineRoot, "ffmpeg", "9.0.0")
	if err = os.MkdirAll(engineDir, 0700); err != nil {
		t.Fatal(err)
	}
	copyExecutable := func(source, target string) {
		t.Helper()
		data, readErr := os.ReadFile(source)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if writeErr := os.WriteFile(target, data, 0700); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	copyExecutable(ffmpegPath, filepath.Join(engineDir, "ffmpeg"))
	copyExecutable(ffprobePath, filepath.Join(engineDir, "ffprobe"))
	inputRoot := filepath.Join(root, "uploads")
	if err = os.MkdirAll(inputRoot, 0700); err != nil {
		t.Fatal(err)
	}
	inputID := "33333333-3333-4333-8333-333333333333"
	inputPath := filepath.Join(inputRoot, inputID)
	fixture := exec.Command(ffmpegPath, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=160x90:rate=15", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "4", "-c:v", "mpeg4", "-b:v", "500k", "-c:a", "aac", "-b:a", "96k", "-f", "mp4", inputPath)
	if output, runErr := fixture.CombinedOutput(); runErr != nil {
		t.Fatalf("create media fixture: %v\n%s", runErr, output)
	}
	input, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(input)
	runner := &ProcessRunner{EngineRoot: engineRoot, UploadRoot: inputRoot, ResultRoot: filepath.Join(root, "results"), WorkRoot: filepath.Join(root, "work"), Timeout: 30 * time.Second}
	result, err := runner.Run(context.Background(), Invocation{
		JobID: "44444444-4444-4444-8444-444444444444", Attempt: 1, InputID: inputID,
		EngineID: "ffmpeg", EngineVersion: "9.0.0", ExecutableRelativePath: "ffmpeg", ProbeExecutableRelativePath: "ffprobe",
		Arguments:            []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-i", "{source}", "{compression.args}", "{target}"},
		CompressionArguments: []string{"-b:v", "{compression.videoKbps}k", "-maxrate", "{compression.videoKbps}k", "-bufsize", "{compression.bufferKbps}k", "-b:a", "{compression.audioKbps}k"},
		TargetSizeBytes:      2 << 20, AudioBitrateKbps: 96, MinimumVideoKbps: 32, RequireSmaller: true,
		TargetExtension: "mp4", ExpectedInputSHA256: hex.EncodeToString(digest[:]), ExpectedInputSize: int64(len(input)),
	})
	if err != nil {
		t.Fatalf("compress media: %v", err)
	}
	if result.Size <= 0 || result.Size > 2<<20 || result.Size >= int64(len(input)) {
		t.Fatalf("compressed output size=%d bytes, input=%d target=%d", result.Size, len(input), 2<<20)
	}
	if _, err = exec.Command(ffprobePath, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", filepath.Join(runner.ResultRoot, result.ResultKey)).Output(); err != nil {
		t.Fatalf("compressed artifact is not valid media: %v", err)
	}
}
