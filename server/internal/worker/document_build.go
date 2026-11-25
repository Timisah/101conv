package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/timisah/101conv/server/internal/document"
	"github.com/timisah/101conv/server/internal/limits"
)

func (r *ProcessRunner) runDocumentBuild(parent context.Context, in Invocation) (ret Result, retErr error) {
	if !safeSegment.MatchString(in.JobID) || in.Attempt < 1 || in.Attempt > 1000000 || !safeExtension(in.TargetExtension) {
		return Result{}, fmt.Errorf("invalid document build invocation identity")
	}
	spec := in.DocumentBuild
	if spec == nil {
		return Result{}, fmt.Errorf("missing document build specification")
	}
	if err := spec.Manifest.Validate(); err != nil {
		return Result{}, fmt.Errorf("invalid document build manifest: %w", err)
	}
	if len(spec.Sources) == 0 {
		return Result{}, fmt.Errorf("document build requires at least one verified source")
	}

	// Verify every source upload exists in UploadRoot, is a regular non-symlink
	// file, and matches the frozen size and SHA-256 digest recorded by Core.
	sourcePaths := make(map[string]document.SourceFileInput, len(spec.Sources))
	for _, src := range spec.Sources {
		if !safeSegment.MatchString(src.UploadID) || len(src.SHA256) != 64 || src.Size <= 0 || src.Size > limits.MaxInputBytes {
			return Result{}, fmt.Errorf("invalid document build source metadata for %q", src.UploadID)
		}
		inputPath := filepath.Join(r.UploadRoot, src.UploadID)
		info, err := os.Lstat(inputPath)
		if err != nil {
			return Result{}, fmt.Errorf("open document source %q: %w", src.UploadID, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != src.Size {
			return Result{}, fmt.Errorf("document source %q failed size or file-type validation", src.UploadID)
		}
		f, err := os.Open(inputPath)
		if err != nil {
			return Result{}, err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, io.LimitReader(f, limits.MaxInputBytes+1))
		closeErr := f.Close()
		if copyErr != nil {
			return Result{}, copyErr
		}
		if closeErr != nil {
			return Result{}, closeErr
		}
		if hex.EncodeToString(h.Sum(nil)) != src.SHA256 {
			return Result{}, fmt.Errorf("document source %q digest mismatch", src.UploadID)
		}
		sourcePaths[src.UploadID] = document.SourceFileInput{
			Path:      inputPath,
			FileName:  src.FileName,
			Extension: src.Extension,
		}
	}

	var libreOfficePath, pdftoppmPath string
	if r.EngineRoot != "" {
		if engineRoot, err := filepath.EvalSymlinks(r.EngineRoot); err == nil {
			if spec.LibreOfficeEngineID != "" && spec.LibreOfficeEngineVersion != "" && spec.LibreOfficeRelativePath != "" &&
				safeSegment.MatchString(spec.LibreOfficeEngineID) && safeSegment.MatchString(spec.LibreOfficeEngineVersion) && safeRelative(spec.LibreOfficeRelativePath) {
				candidate := filepath.Join(engineRoot, spec.LibreOfficeEngineID, spec.LibreOfficeEngineVersion, filepath.FromSlash(spec.LibreOfficeRelativePath))
				if rejectSymlinkPath(engineRoot, candidate) == nil {
					if st, err := os.Lstat(candidate); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
						libreOfficePath = candidate
					}
				}
			}
			if spec.PopplerEngineID != "" && spec.PopplerEngineVersion != "" && spec.PopplerRelativePath != "" &&
				safeSegment.MatchString(spec.PopplerEngineID) && safeSegment.MatchString(spec.PopplerEngineVersion) && safeRelative(spec.PopplerRelativePath) {
				candidate := filepath.Join(engineRoot, spec.PopplerEngineID, spec.PopplerEngineVersion, filepath.FromSlash(spec.PopplerRelativePath))
				if rejectSymlinkPath(engineRoot, candidate) == nil {
					if st, err := os.Lstat(candidate); err == nil && st.Mode().IsRegular() && st.Mode().Perm()&0111 != 0 {
						pdftoppmPath = candidate
					}
				}
			}
		}
	}

	if err := os.MkdirAll(r.WorkRoot, 0700); err != nil {
		return Result{}, err
	}
	work, err := os.MkdirTemp(r.WorkRoot, "docbuild-")
	if err != nil {
		return Result{}, err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(work); cleanupErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("remove document build workspace: %w", cleanupErr))
		}
	}()

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

	outPath := filepath.Join(work, "assembled."+in.TargetExtension)
	assembled, err := document.Assemble(ctx, document.AssembleRequest{
		Manifest:        spec.Manifest,
		SourcePaths:     sourcePaths,
		WorkDir:         work,
		OutputPath:      outPath,
		MaxOutputBytes:  maxOutput,
		LibreOfficePath: libreOfficePath,
		PDFToPPMPath:    pdftoppmPath,
	})
	if err != nil {
		return Result{}, err
	}

	resultKey := fmt.Sprintf("%s-%d.bin", in.JobID, in.Attempt)
	resultPath := filepath.Join(r.ResultRoot, resultKey)
	if err := os.MkdirAll(r.ResultRoot, 0700); err != nil {
		return Result{}, err
	}
	if err := copyAtomic(assembled.OutputPath, resultPath, maxOutput); err != nil {
		return Result{}, err
	}
	return Result{
		ResultKey: resultKey,
		Size:      assembled.Size,
		ExitCode:  0,
	}, nil
}
