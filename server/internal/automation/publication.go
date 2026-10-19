package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/timisah/101conv/server/internal/jobs"
)

// A retained hard-link receipt identifies a publication by inode, not merely by
// equal contents. It survives a crash between linking the output and committing
// the terminal run state. Every candidate name is journaled before linking.
func (s *Service) publishRun(ctx context.Context, id, automation, owner, name, jobID string, raw []byte, job jobs.Job, legacy bool) error {
	var snapshot executionSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.OutputPath == "" {
		return s.blockRun(ctx, id, "Execution snapshot is unavailable; inspect this run before retrying")
	}
	path, err := s.ResolveDrivePath(snapshot.OutputPath)
	if err != nil {
		return s.blockRun(ctx, id, "Output path is unavailable or outside approved roots; restore the registered destination")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return s.blockRun(ctx, id, "Output directory cannot be opened")
	}
	defer root.Close()
	if legacy {
		// C.1 runs have no publication journal. An existing candidate could be an
		// unacknowledged old publication; neither adopt it nor create another copy.
		base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)) + "." + snapshot.Definition.Action.TargetFormat
		limit := 1
		if snapshot.Definition.Action.OutputBehavior == "unique" {
			limit = 100
		}
		for i := 1; i <= limit; i++ {
			candidate := base
			if i > 1 {
				ext := filepath.Ext(base)
				candidate = strings.TrimSuffix(base, ext) + fmt.Sprintf(" (%d)", i) + ext
			}
			if _, e := root.Lstat(candidate); e == nil {
				return s.blockRun(ctx, id, "Legacy run has no publication receipt and a destination candidate exists; inspect existing outputs before any retry")
			} else if !errors.Is(e, os.ErrNotExist) {
				return s.blockRun(ctx, id, "Legacy destination cannot be inspected; review existing outputs")
			}
		}
	}
	stage := ".101conv-receipt-" + id
	var output, digest string
	if err = s.db.QueryRowContext(ctx, `SELECT output_name,publication_digest FROM conv_automation_runs WHERE id=$1`, id).Scan(&output, &digest); err != nil {
		return err
	}
	// Completion recovery precedes authorization: this publishes no new
	// output to the mounted folder and must record a publication that already happened.
	if output != "" && digest != "" && samePublication(root, stage, output, digest) {
		return s.finishPublication(ctx, root, id, automation, snapshot.Definition.Action.OutputDriveID, stage, output, digest)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var registeredPath, access string
	var enabled bool
	err = tx.QueryRowContext(ctx, `SELECT path,access_mode,enabled FROM conv_mounted_drives WHERE id=$1 FOR SHARE`, snapshot.Definition.Action.OutputDriveID).Scan(&registeredPath, &access, &enabled)
	if err != nil || !enabled || access != "read_write" || registeredPath != path {
		return s.blockRun(ctx, id, "Output drive was removed, disabled, changed, or is no longer registered read_write; review the destination")
	}
	// Anchor all file operations to the approved directory descriptor. Reject a
	// replaced path rather than following an administrator-visible symlink change.
	approved, err := s.ResolveDrivePath(registeredPath)
	actual, statErr := root.Stat(".")
	current, pathErr := os.Stat(registeredPath)
	if err != nil || approved != path || statErr != nil || pathErr != nil || !os.SameFile(actual, current) {
		return s.blockRun(ctx, id, "Output directory changed during execution; review the registered path")
	}
	probe := ".101conv-probe-" + mustID()
	f, err := root.OpenFile(probe, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return s.blockRun(ctx, id, "Output drive is not writable by Core; restore directory permissions")
	}
	closeErr := f.Close()
	removeErr := root.Remove(probe)
	if closeErr != nil || removeErr != nil {
		return s.blockRun(ctx, id, "Output permission probe failed; inspect destination storage")
	}
	if digest == "" {
		result, _, _, err := s.jobs.OpenResultFor(ctx, jobID, owner)
		if err != nil {
			return s.blockRun(ctx, id, "Conversion result is unavailable; inspect result retention before retrying")
		}
		digest, err = stagePublication(root, stage, result, job.OutputSize)
		closeErr := result.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return s.blockRun(ctx, id, shortError(err))
		}
	} else {
		got, err := rootDigest(root, stage)
		if err != nil || got != digest {
			return s.blockRun(ctx, id, "Publication receipt is missing or changed; inspect destination before retrying")
		}
	}
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name)) + "." + snapshot.Definition.Action.TargetFormat
	if strings.ContainsAny(base, "/\\\x00\r\n") {
		return s.blockRun(ctx, id, "Unsafe output filename")
	}
	behavior := snapshot.Definition.Action.OutputBehavior
	for index := 1; index <= 100; index++ {
		candidate := output
		if candidate == "" {
			candidate = base
			if behavior == "unique" && index > 1 {
				ext := filepath.Ext(base)
				candidate = strings.TrimSuffix(base, ext) + fmt.Sprintf(" (%d)", index) + ext
			}
		}
		if _, err = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET output_name=$2,publication_digest=$3 WHERE id=$1`, id, candidate, digest); err != nil {
			return err
		}
		if samePublication(root, stage, candidate, digest) {
			if err = tx.Commit(); err != nil {
				return err
			}
			return s.finishPublication(ctx, root, id, automation, snapshot.Definition.Action.OutputDriveID, stage, candidate, digest)
		}
		// Staging can take time. Recheck the descriptor against the currently
		// approved pathname immediately before the publication side effect.
		approved, pathErr := s.ResolveDrivePath(registeredPath)
		current, statErr := os.Stat(registeredPath)
		if pathErr != nil || statErr != nil || approved != path || !os.SameFile(actual, current) {
			return s.blockRun(ctx, id, "Output directory changed while staging; review the registered destination")
		}
		err = root.Link(stage, candidate)
		if err == nil {
			if err = syncRoot(root); err != nil {
				return err
			} // leave pending for receipt recovery
			if err = tx.Commit(); err != nil {
				return err
			}
			return s.finishPublication(ctx, root, id, automation, snapshot.Definition.Action.OutputDriveID, stage, candidate, digest)
		}
		if errors.Is(err, os.ErrExist) {
			if behavior == "unique" {
				output = ""
				continue
			}
			if behavior == "skip_existing" {
				_, err = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='skipped_existing_output',output_name='',finished_at=now() WHERE id=$1`, id)
				if err == nil {
					_ = root.Remove(stage)
				}
				return err
			}
			return s.blockRun(ctx, id, "Destination already exists; create never overwrites files")
		}
		return s.blockRun(ctx, id, "Atomic output publication failed: "+shortError(err))
	}
	return s.blockRun(ctx, id, "No unused output name within the 100-name limit")
}

func (s *Service) blockRun(ctx context.Context, id, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='blocked',error=$2,finished_at=now() WHERE id=$1`, id, message)
	return err
}
func (s *Service) finishPublication(ctx context.Context, root *os.Root, id, automation, drive, stage, output, digest string) error {
	if err := syncRoot(root); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Generated-file bookkeeping and run completion commit together. A drive may
	// have been removed after the link; retain the run even in that case.
	_, err = tx.ExecContext(ctx, `INSERT INTO conv_generated_files(drive_id,fingerprint,automation_id) SELECT id,$2,$3 FROM conv_mounted_drives WHERE id=$1 ON CONFLICT DO NOTHING`, drive, digest, automation)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE conv_automation_runs SET state='succeeded',output_name=$2,error='',finished_at=now() WHERE id=$1`, id, output)
	if err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	_ = root.Remove(stage)
	return nil
}
func samePublication(root *os.Root, stage, target, digest string) bool {
	a, err := root.Lstat(stage)
	if err != nil || !a.Mode().IsRegular() {
		return false
	}
	b, err := root.Lstat(target)
	if err != nil || !b.Mode().IsRegular() || !os.SameFile(a, b) {
		return false
	}
	got, err := rootDigest(root, stage)
	return err == nil && got == digest
}
func rootDigest(root *os.Root, name string) (string, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("publication is not a regular file")
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, jobs.MaxOutputBytes+1))
	if err != nil {
		return "", err
	}
	if n > jobs.MaxOutputBytes {
		return "", errors.New("publication exceeds output limit")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func syncRoot(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func stagePublication(root *os.Root, name string, source io.Reader, size int64) (string, error) {
	if size < 0 || size > jobs.MaxOutputBytes {
		return "", errors.New("invalid output size")
	}
	temp := ".101conv-stage-" + mustID()
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer root.Remove(temp)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(source, jobs.MaxOutputBytes+1))
	if err == nil && (n != size || n > jobs.MaxOutputBytes) {
		err = errors.New("conversion output size changed during publication")
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	digest := hex.EncodeToString(h.Sum(nil))
	if err = root.Link(temp, name); errors.Is(err, os.ErrExist) {
		got, e := rootDigest(root, name)
		if e != nil || got != digest {
			return "", errors.New("publication receipt changed")
		}
	} else if err != nil {
		return "", err
	}
	return digest, syncRoot(root)
}
