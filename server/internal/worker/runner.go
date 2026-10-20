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
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/timisah/101conv/server/internal/limits"
)

const (
	MaxCapturedOutput = 32 << 10
	DefaultMaxOutput  = limits.MaxOutputBytes
)

var safeSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

type Invocation struct {
	JobID                          string
	Attempt                        int
	InputID                        string
	EngineID                       string
	EngineVersion                  string
	ExecutableRelativePath         string
	Arguments                      []string
	CompressionArguments           []string
	TargetSizeBytes                int64
	ProbeExecutableRelativePath    string
	AudioBitrateKbps               int
	MinimumVideoKbps               int
	RequireSmaller                 bool
	CompressionQualityMin          float64
	CompressionQualityMax          float64
	CompressionQualityStep         float64
	CompressionQualityHigherBetter bool
	CompressionQualityAttempts     int
	TargetExtension                string
	SourceExtension                string
	ExpectedInputSHA256            string
	ExpectedInputSize              int64
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
	if !safeExtension(in.TargetExtension) || !safeRelative(in.ExecutableRelativePath) || len(in.Arguments) == 0 || len(in.Arguments) > 128 || len(in.CompressionArguments) > 64 {
		return Result{}, fmt.Errorf("invalid worker invocation definition")
	}
	adaptiveQuality := in.TargetSizeBytes > 0 && in.CompressionQualityStep > 0
	if in.TargetSizeBytes > 0 && !adaptiveQuality && (!safeRelative(in.ProbeExecutableRelativePath) || in.AudioBitrateKbps < 1 || in.MinimumVideoKbps < 1) {
		return Result{}, fmt.Errorf("invalid compression target-size settings")
	}
	if adaptiveQuality && (math.IsNaN(in.CompressionQualityMin) || math.IsNaN(in.CompressionQualityMax) || math.IsNaN(in.CompressionQualityStep) || math.IsInf(in.CompressionQualityMin, 0) || math.IsInf(in.CompressionQualityMax, 0) || math.IsInf(in.CompressionQualityStep, 0) || in.CompressionQualityMin > in.CompressionQualityMax || in.CompressionQualityStep <= 0) {
		return Result{}, fmt.Errorf("invalid adaptive compression quality settings")
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
	if inputInfo.Size() > limits.MaxInputBytes || inputInfo.Size() != in.ExpectedInputSize || len(in.ExpectedInputSHA256) != 64 {
		return Result{}, fmt.Errorf("job input failed size or metadata validation")
	}
	inputFile, err := os.Open(inputPath)
	if err != nil {
		return Result{}, err
	}
	inputHash := sha256.New()
	_, hashErr := io.Copy(inputHash, io.LimitReader(inputFile, limits.MaxInputBytes+1))
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
	if in.EngineID == "libreoffice" {
		if err = validateOfficeInput(inputPath, in.SourceExtension); err != nil {
			return Result{}, err
		}
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
	compressionArgs, err := resolveCompressionArguments(parent, engineRoot, work, inputPath, in)
	if err != nil {
		return Result{}, err
	}
	maxOutput := r.MaxOutput
	if maxOutput <= 0 {
		maxOutput = DefaultMaxOutput
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	type candidate struct {
		path           string
		size           int64
		exitCode       int
		stdout, stderr string
	}
	buildArguments := func(outputPath, quality string) ([]string, error) {
		args := make([]string, 0, len(in.Arguments)+len(compressionArgs))
		seenSource, seenTarget, seenCompressionSlot := false, false, false
		appendArgument := func(arg string) error {
			if strings.Contains(arg, "{compression.quality}") {
				if quality == "" {
					return fmt.Errorf("adaptive compression quality was not resolved")
				}
				arg = strings.ReplaceAll(arg, "{compression.quality}", quality)
			}
			if strings.ContainsAny(arg, "{}") || strings.ContainsRune(arg, '\x00') {
				return fmt.Errorf("unsafe converter argument")
			}
			args = append(args, arg)
			return nil
		}
		for _, arg := range in.Arguments {
			switch arg {
			case "{source}":
				args = append(args, inputPath)
				seenSource = true
			case "{target}":
				args = append(args, outputPath)
				seenTarget = true
			case "{compression.args}":
				for _, compressionArg := range compressionArgs {
					if err := appendArgument(compressionArg); err != nil {
						return nil, err
					}
				}
				seenCompressionSlot = true
			default:
				if err := appendArgument(arg); err != nil {
					return nil, err
				}
			}
		}
		if !seenSource || !seenTarget {
			return nil, fmt.Errorf("converter arguments must include source and target placeholders")
		}
		if len(in.CompressionArguments) > 0 && !seenCompressionSlot {
			return nil, fmt.Errorf("compression arguments require a {compression.args} marker")
		}
		return args, nil
	}
	runConverter := func(outputPath, quality string) (candidate, error) {
		args, buildErr := buildArguments(outputPath, quality)
		if buildErr != nil {
			return candidate{}, buildErr
		}
		cmd := exec.CommandContext(ctx, executable, args...)
		cmd.Dir = work
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work, "LANG=C.UTF-8"}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			killErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if errors.Is(killErr, syscall.ESRCH) {
				return nil
			}
			return killErr
		}
		cmd.WaitDelay = 2 * time.Second
		stdout, stderr := &boundedBuffer{}, &boundedBuffer{}
		cmd.Stdout, cmd.Stderr = stdout, stderr
		if runErr := cmd.Run(); runErr != nil {
			if ctx.Err() != nil {
				return candidate{stdout: stdout.String(), stderr: stderr.String()}, ctx.Err()
			}
			var exit *exec.ExitError
			if errors.As(runErr, &exit) {
				return candidate{exitCode: exit.ExitCode(), stdout: stdout.String(), stderr: stderr.String()}, fmt.Errorf("converter exited with code %d: %w", exit.ExitCode(), runErr)
			}
			return candidate{stdout: stdout.String(), stderr: stderr.String()}, runErr
		}
		outputInfo, statErr := os.Lstat(outputPath)
		if statErr != nil {
			return candidate{stdout: stdout.String(), stderr: stderr.String()}, fmt.Errorf("converter produced no output: %w", statErr)
		}
		if !outputInfo.Mode().IsRegular() || outputInfo.Mode()&os.ModeSymlink != 0 || outputInfo.Size() <= 0 || outputInfo.Size() > maxOutput {
			return candidate{stdout: stdout.String(), stderr: stderr.String()}, fmt.Errorf("converter output failed type or size validation")
		}
		if signatureErr := validateImageSignature(outputPath, in.TargetExtension); signatureErr != nil {
			return candidate{exitCode: 0, stdout: stdout.String(), stderr: stderr.String()}, signatureErr
		}
		return candidate{path: outputPath, size: outputInfo.Size(), stdout: stdout.String(), stderr: stderr.String()}, nil
	}

	selectedTarget := in.TargetSizeBytes
	if in.RequireSmaller && selectedTarget > inputInfo.Size() {
		selectedTarget = inputInfo.Size()
	}
	selected := candidate{}
	if adaptiveQuality {
		difference := in.CompressionQualityMax - in.CompressionQualityMin
		qualityCount := int(math.Floor(difference/in.CompressionQualityStep+1e-9)) + 1
		if math.Abs(float64(qualityCount-1)*in.CompressionQualityStep-difference) > 1e-9 {
			qualityCount++
		}
		if qualityCount < 1 || qualityCount > 100000 {
			return Result{}, fmt.Errorf("adaptive compression quality range is too large")
		}
		qualityAt := func(index int) float64 {
			if index >= qualityCount-1 {
				if in.CompressionQualityHigherBetter {
					return in.CompressionQualityMin
				}
				return in.CompressionQualityMax
			}
			if in.CompressionQualityHigherBetter {
				return in.CompressionQualityMax - float64(index)*in.CompressionQualityStep
			}
			return in.CompressionQualityMin + float64(index)*in.CompressionQualityStep
		}
		tryQuality := func(index, attempt int) (candidate, error) {
			path := filepath.Join(work, fmt.Sprintf("candidate-%02d.%s", attempt, in.TargetExtension))
			return runConverter(path, strconv.FormatFloat(qualityAt(index), 'f', -1, 64))
		}
		attemptLimit := in.CompressionQualityAttempts
		if attemptLimit <= 0 {
			attemptLimit = 8
		}
		if attemptLimit < 2 {
			attemptLimit = 2
		}
		if attemptLimit > 12 {
			attemptLimit = 12
		}
		attempts := 0
		selected, err = tryQuality(0, attempts)
		attempts++
		if err != nil {
			return Result{Stdout: selected.stdout, Stderr: selected.stderr}, err
		}
		if selected.size > selectedTarget {
			worstIndex := qualityCount - 1
			if worstIndex == 0 {
				return Result{Stdout: selected.stdout, Stderr: selected.stderr}, fmt.Errorf("no quality setting produced an output within the requested target size")
			}
			worst, worstErr := tryQuality(worstIndex, attempts)
			attempts++
			if worstErr != nil {
				return Result{Stdout: worst.stdout, Stderr: worst.stderr}, worstErr
			}
			if worst.size > selectedTarget {
				return Result{Stdout: worst.stdout, Stderr: worst.stderr}, fmt.Errorf("no quality setting produced an output within the requested target size")
			}
			selected = worst
			low, high := 1, worstIndex-1
			for low <= high && attempts < attemptLimit {
				middle := low + (high-low)/2
				trial, trialErr := tryQuality(middle, attempts)
				attempts++
				if trialErr != nil {
					return Result{Stdout: trial.stdout, Stderr: trial.stderr}, trialErr
				}
				if trial.size <= selectedTarget {
					selected = trial
					high = middle - 1
				} else {
					low = middle + 1
				}
			}
		}
	} else {
		selected, err = runConverter(filepath.Join(work, "output."+in.TargetExtension), "")
		if err != nil {
			return Result{Stdout: selected.stdout, Stderr: selected.stderr}, err
		}
		if in.TargetSizeBytes > 0 && selected.size > selectedTarget {
			return Result{ExitCode: selected.exitCode, Stdout: selected.stdout, Stderr: selected.stderr}, fmt.Errorf("converter output exceeded the requested target size")
		}
	}
	if in.RequireSmaller && selected.size >= inputInfo.Size() {
		return Result{ExitCode: selected.exitCode, Stdout: selected.stdout, Stderr: selected.stderr}, fmt.Errorf("compression did not reduce the input size")
	}
	resultKey := fmt.Sprintf("%s-%d.bin", in.JobID, in.Attempt)
	resultPath := filepath.Join(r.ResultRoot, resultKey)
	if err = os.MkdirAll(r.ResultRoot, 0700); err != nil {
		return Result{}, err
	}
	if err = copyAtomic(selected.path, resultPath, maxOutput); err != nil {
		return Result{}, err
	}
	return Result{ResultKey: resultKey, Size: selected.size, ExitCode: selected.exitCode, Stdout: selected.stdout, Stderr: selected.stderr}, nil
}

// validateImageSignature catches a converter that exits successfully after
// writing a text file, an empty container, or an output with the wrong format.
// It intentionally applies only to the image extensions understood by the
// built-in image profiles, plus a PDF publication guard. Other formats are
// governed by their engine adapters.
func validateImageSignature(path, extension string) error {
	if extension == "pdf" {
		return validatePDFSignature(path)
	}
	known := map[string]bool{"jpg": true, "jpeg": true, "png": true, "webp": true, "tif": true, "tiff": true, "bmp": true, "gif": true, "avif": true, "jxl": true}
	if !known[extension] {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open image output for validation: %w", err)
	}
	defer file.Close()
	header := make([]byte, 12)
	if _, err = io.ReadFull(file, header); err != nil {
		return fmt.Errorf("image output failed signature validation: %w", err)
	}
	valid := false
	switch extension {
	case "jpg", "jpeg":
		valid = header[0] == 0xff && header[1] == 0xd8 && header[2] == 0xff
	case "png":
		valid = string(header[:8]) == "\x89PNG\r\n\x1a\n"
	case "webp":
		valid = string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP"
	case "tif", "tiff":
		valid = (string(header[:4]) == "II*\x00" || string(header[:4]) == "MM\x00*" || string(header[:4]) == "II+\x00" || string(header[:4]) == "MM\x00+")
	case "bmp":
		valid = string(header[:2]) == "BM"
	case "gif":
		valid = string(header[:6]) == "GIF87a" || string(header[:6]) == "GIF89a"
	case "avif":
		valid = string(header[4:8]) == "ftyp" && (string(header[8:12]) == "avif" || string(header[8:12]) == "avis")
	case "jxl":
		valid = (header[0] == 0xff && header[1] == 0x0a) || (string(header) == "\x00\x00\x00\x0cJXL \r\n\x87\n")
	}
	if !valid {
		return fmt.Errorf("converter output failed %s image signature validation", extension)
	}
	return nil
}

// This is a cheap publication guard, not a complete PDF parser. The document
// adapter also runs pdfinfo before returning a result to the Worker.
func validatePDFSignature(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	header := make([]byte, 8)
	if _, err = io.ReadFull(f, header); err != nil ||
		!(string(header[:7]) == "%PDF-1." && header[7] >= '0' && header[7] <= '7' || string(header) == "%PDF-2.0") {
		return fmt.Errorf("converter output failed PDF header validation")
	}
	info, err := f.Stat()
	if err != nil {
		return err
	}
	start := info.Size() - 1024
	if start < 0 {
		start = 0
	}
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	tail, err := io.ReadAll(io.LimitReader(f, 1024))
	if err != nil || !strings.HasSuffix(strings.TrimSpace(string(tail)), "%%EOF") {
		return fmt.Errorf("converter output failed PDF trailer validation")
	}
	return nil
}

func resolveCompressionArguments(parent context.Context, engineRoot, work, inputPath string, in Invocation) ([]string, error) {
	if len(in.CompressionArguments) == 0 {
		return nil, nil
	}
	values := map[string]string{}
	if in.TargetSizeBytes > 0 && in.CompressionQualityStep <= 0 {
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
		cmd := exec.CommandContext(probeCtx, executable, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", inputPath)
		cmd.Dir = work
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "TMPDIR=" + work, "LANG=C.UTF-8"}
		out, stderr := &boundedBuffer{}, &boundedBuffer{}
		cmd.Stdout, cmd.Stderr = out, stderr
		if err = cmd.Run(); err != nil {
			if probeCtx.Err() != nil {
				return nil, fmt.Errorf("media probe timed out")
			}
			return nil, fmt.Errorf("could not inspect media duration: %s", strings.TrimSpace(stderr.String()))
		}
		duration, err := strconv.ParseFloat(strings.TrimSpace(out.String()), 64)
		if err != nil || duration <= 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			return nil, fmt.Errorf("media probe returned an invalid duration")
		}
		// Reserve 10%% of the requested budget for container metadata and
		// muxing overhead; the final artifact is still checked against the cap.
		budget := in.TargetSizeBytes
		if in.RequireSmaller && budget > in.ExpectedInputSize {
			budget = in.ExpectedInputSize
		}
		totalKbps := int64(math.Floor(float64(budget) * 8 / duration / 1000 * 0.90))
		usesVideo, usesAudio := false, false
		for _, arg := range in.CompressionArguments {
			usesVideo = usesVideo || strings.Contains(arg, "{compression.videoKbps}") || strings.Contains(arg, "{compression.bufferKbps}")
			usesAudio = usesAudio || strings.Contains(arg, "{compression.audioKbps}")
		}
		if usesVideo {
			audioKbps := int64(in.AudioBitrateKbps)
			videoKbps := totalKbps - audioKbps
			if videoKbps < int64(in.MinimumVideoKbps) {
				return nil, fmt.Errorf("target size is too small for this media duration")
			}
			values["{compression.videoKbps}"] = strconv.FormatInt(videoKbps, 10)
			values["{compression.bufferKbps}"] = strconv.FormatInt(videoKbps*2, 10)
			if usesAudio {
				values["{compression.audioKbps}"] = strconv.FormatInt(audioKbps, 10)
			}
		} else if usesAudio {
			audioKbps := totalKbps
			if audioKbps > 320 {
				audioKbps = 320
			}
			if audioKbps < 32 {
				return nil, fmt.Errorf("target size is too small for this media duration")
			}
			values["{compression.audioKbps}"] = strconv.FormatInt(audioKbps, 10)
		}
	}
	result := make([]string, len(in.CompressionArguments))
	for i, arg := range in.CompressionArguments {
		if strings.ContainsRune(arg, '\x00') {
			return nil, fmt.Errorf("unsafe compression argument")
		}
		for token, value := range values {
			if strings.Contains(arg, token) {
				arg = strings.ReplaceAll(arg, token, value)
			}
		}
		if strings.ContainsAny(strings.ReplaceAll(arg, "{compression.quality}", "0"), "{}") {
			return nil, fmt.Errorf("compression argument requires a target-size setting")
		}
		result[i] = arg
	}
	return result, nil
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
