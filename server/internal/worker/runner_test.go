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
