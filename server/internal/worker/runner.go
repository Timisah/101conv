// Package worker is the only process-execution boundary in the Go service.
// Production runs it in a separate container with a read-only engine mount,
// no public network, and a size-limited tmpfs workspace.
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const (
	MaxCapturedOutput = 32 << 10
	DefaultMaxOutput  = 200 << 20
)

var safeSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

type Invocation struct {
	JobID                  string
	Attempt                int
	InputID                string
	EngineID               string
	EngineVersion          string
	ExecutableRelativePath string
	Arguments              []string
	TargetExtension        string
	ExpectedInputSHA256    string
	ExpectedInputSize      int64
}

type Result struct {
	ResultKey string
	Size      int64
	ExitCode  int
	Stdout    string
	Stderr    string
}

type Runner interface {
	Run(context.Context, Invocation) (Result, error)
}

type ProcessRunner struct {
	EngineRoot string
	UploadRoot string
	ResultRoot string
	WorkRoot   string
	Timeout    time.Duration
	MaxOutput  int64
}

func (r *ProcessRunner) Run(parent context.Context, in Invocation) (ret Result, retErr error) {
	if !safeSegment.MatchString(in.JobID) || !safeSegment.MatchString(in.InputID) || !safeSegment.MatchString(in.EngineID) || !safeSegment.MatchString(in.EngineVersion) || in.Attempt < 1 || in.Attempt > 1000000 {
		return Result{}, fmt.Errorf("invalid worker invocation identity")
	}
	if !safeExtension(in.TargetExtension) || !safeRelative(in.ExecutableRelativePath) || len(in.Arguments) == 0 || len(in.Arguments) > 128 {
		return Result{}, fmt.Errorf("invalid worker invocation definition")
	}
	engineRoot, err := filepath.EvalSymlinks(r.EngineRoot)
	if err != nil {
		return Result{}, fmt.Errorf("resolve engine root: %w", err)
	}
	executable := filepath.Join(engineRoot, in.EngineID, in.EngineVersion, filepath.FromSlash(in.ExecutableRelativePath))
	if err = rejectSymlinkPath(engineRoot, executable); err != nil {
		return Result{}, err
	}
	info, err := os.Lstat(executable)
	if err != nil {
		return Result{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0111 == 0 {
		return Result{}, fmt.Errorf("engine executable is not a regular executable")
	}
	inputPath := filepath.Join(r.UploadRoot, in.InputID)
	inputInfo, err := os.Lstat(inputPath)
	if err != nil {
		return Result{}, err
	}
	if !inputInfo.Mode().IsRegular() || inputInfo.Mode()&os.ModeSymlink != 0 || inputInfo.Size() <= 0 {
		return Result{}, fmt.Errorf("job input is not a regular file")
	}
	if inputInfo.Size() > 100<<20 || inputInfo.Size() != in.ExpectedInputSize || len(in.ExpectedInputSHA256) != 64 {
		return Result{}, fmt.Errorf("job input failed size or metadata validation")
	}
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return Result{}, err
	}
	inputHash := sha256.New()
	_, hashErr := io.Copy(inputHash, io.LimitReader(inputFile, (100<<20)+1))
	closeErr := inputFile.Close()
	if hashErr != nil {
		return Result{}, hashErr
	}
	if closeErr != nil {
		return Result{}, closeErr
	}
	if hex.EncodeToString(inputHash.Sum(nil)) != in.ExpectedInputSHA256 {
		return Result{}, fmt.Errorf("job input digest does not match uploaded content")
	}
	if err = os.MkdirAll(r.WorkRoot, 0700); err != nil {
		return Result{}, err
	}
	work, err := os.MkdirTemp(r.WorkRoot, "attempt-")
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(work); cleanupErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("remove attempt workspace: %w", cleanupErr))
		}
	}()
	outputPath := filepath.Join(work, "output."+in.TargetExtension)
	args := make([]string, len(in.Arguments))
	seenSource, seenTarget := false, false
	for i, arg := range in.Arguments {
		switch arg {
		case "{source}":
			args[i] = inputPath
			seenSource = true
		case "{target}":
			args[i] = outputPath
			seenTarget = true
		default:
			if strings.ContainsAny(arg, "{}") || strings.ContainsRune(arg, '\x00') {
				return Result{}, fmt.Errorf("unsafe converter argument")
			}
			args[i] = arg
		}
	}
	if !seenSource || !seenTarget {
		return Result{}, fmt.Errorf("converter arguments must include source and target placeholders")
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = work
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work, "LANG=C.UTF-8"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	stdout, stderr := &boundedBuffer{}, &boundedBuffer{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		if ctx.Err() != nil {
			return Result{Stdout: stdout.String(), Stderr: stderr.String()}, ctx.Err()
		}
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			exitCode = exit.ExitCode()
		} else {
			return Result{Stdout: stdout.String(), Stderr: stderr.String()}, runErr
		}
		return Result{ExitCode: exitCode, Stdout: stdout.String(), Stderr: stderr.String()}, fmt.Errorf("converter exited with code %d: %w", exitCode, runErr)
	}
	outputInfo, err := os.Lstat(outputPath)
	if err != nil {
		return Result{ExitCode: exitCode, Stdout: stdout.String(), Stderr: stderr.String()}, fmt.Errorf("converter produced no output: %w", err)
	}
	maxOutput := r.MaxOutput
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutput
	}
	if !outputInfo.Mode().IsRegular() || outputInfo.Mode()&os.ModeSymlink != 0 || outputInfo.Size() <= 0 || outputInfo.Size() > maxOutput {
		return Result{ExitCode: exitCode, Stdout: stdout.String(), Stderr: stderr.String()}, fmt.Errorf("converter output failed type or size validation")
	}
	resultKey := fmt.Sprintf("%s-%d.bin", in.JobID, in.Attempt)
	resultPath := filepath.Join(r.ResultRoot, resultKey)
	if err = os.MkdirAll(r.ResultRoot, 0700); err != nil {
		return Result{}, err
	}
	if err = copyAtomic(outputPath, resultPath, maxOutput); err != nil {
		return Result{}, err
	}
	return Result{ResultKey: resultKey, Size: outputInfo.Size(), ExitCode: exitCode, Stdout: stdout.String(), Stderr: stderr.String()}, nil
}

type boundedBuffer struct{ data []byte }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	room := MaxCapturedOutput - len(b.data)
	if room > 0 {
		if room > n {
			room = n
		}
		b.data = append(b.data, p[:room]...)
	}
	return n, nil
}
func (b *boundedBuffer) String() string { return string(b.data) }

func copyAtomic(source, dest string, max int64) (retErr error) {
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("result already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".result-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			if closeErr := tmp.Close(); closeErr != nil {
				retErr = errors.Join(retErr, fmt.Errorf("close temporary result: %w", closeErr))
			}
		}
		if _, statErr := os.Lstat(tmpName); statErr == nil {
			if cleanupErr := os.Remove(tmpName); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("remove temporary result: %w", cleanupErr))
			}
		}
	}()
	n, copyErr := io.Copy(tmp, io.LimitReader(in, max+1))
	if copyErr != nil {
		tmp.Close()
		return copyErr
	}
	if n > max {
		tmp.Close()
		return fmt.Errorf("converter output exceeded size limit")
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	closed = true
	if err = os.Rename(tmpName, dest); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(dest))
	if err != nil {
		if removeErr := os.Remove(dest); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return errors.Join(err, fmt.Errorf("remove result after directory open failure: %w", removeErr))
		}
		return err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		if removeErr := os.Remove(dest); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return errors.Join(err, fmt.Errorf("remove uncommitted result: %w", removeErr))
		}
		return err
	}
	return nil
}

func safeRelative(p string) bool {
	if p == "" || filepath.IsAbs(p) || strings.Contains(p, `\`) || filepath.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return false
	}
	for _, part := range strings.Split(p, string(filepath.Separator)) {
		if part == "." || part == ".." || part == "" {
			return false
		}
	}
	return true
}
func safeExtension(ext string) bool {
	if ext == "" || len(ext) > 12 {
		return false
	}
	for _, r := range ext {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
func rejectSymlinkPath(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("executable escapes engine root")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, e := os.Lstat(current)
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in engine executable path")
		}
	}
	return nil
}
