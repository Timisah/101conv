package automation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/jobs"
)

const (
	maxEntriesPerDriveScan = 250
	maxFilesPerAutomation  = 20
	maxScanBytes           = int64(512 << 20)
)

type PreviewFile struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	Matches bool   `json:"matches"`
	Reason  string `json:"reason,omitempty"`
}
type Preview struct {
	AutomationID string        `json:"automationId"`
	Revision     int           `json:"revision"`
	Files        []PreviewFile `json:"files"`
	Truncated    bool          `json:"truncated"`
}

type executionSnapshot struct {
	Definition Definition `json:"definition"`
	OutputPath string     `json:"outputPath"`
}

// Run starts the bounded polling scheduler. Core is the only process with
// access to registered mounts; the converter Worker receives only private job
// uploads and its existing read-only engine mount.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	_ = s.runTick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.runTick(ctx); err != nil { /* individual errors are persisted on the affected automation */
			}
		}
	}
}

func (s *Service) DryRun(ctx context.Context, id, owner string) (Preview, error) {
	a, err := s.GetAutomation(ctx, id, owner)
	if err != nil {
		return Preview{}, err
	}
	input, err := s.CheckDrive(ctx, a.Definition.Trigger.InputDriveID)
	if err != nil {
		return Preview{}, err
	}
	if !input.Accessible {
		return Preview{}, errors.New("input drive is inaccessible")
	}
	entries, err := os.ReadDir(input.Path)
	if err != nil {
		return Preview{}, err
	}
	preview := Preview{AutomationID: a.ID, Revision: a.Revision}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for i, e := range entries {
		if i >= maxEntriesPerDriveScan {
			preview.Truncated = true
			break
		}
		if e.Type()&os.ModeSymlink != 0 || !e.Type().IsRegular() {
			continue
		}
		info, x := e.Info()
		if x != nil {
			continue
		}
		ok, reason := matchesDefinition(a.Definition, e.Name(), info.Size())
		preview.Files = append(preview.Files, PreviewFile{Name: e.Name(), Size: info.Size(), Matches: ok, Reason: reason})
		if len(preview.Files) >= maxFilesPerAutomation {
			preview.Truncated = true
			break
		}
	}
	return preview, nil
}

func (s *Service) RunNow(ctx context.Context, id, owner string) ([]Run, error) {
	a, err := s.GetAutomation(ctx, id, owner)
	if err != nil {
		return nil, err
	}
	if err = s.scanAutomation(ctx, a, true); err != nil {
		return nil, err
	}
	return s.History(ctx, id, owner, 50)
}

func (s *Service) runTick(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id FROM conv_automations WHERE enabled AND status IN ('ready','running') AND (last_scan IS NULL OR last_scan+make_interval(secs=>COALESCE((SELECT (definition->'trigger'->>'intervalSeconds')::int FROM conv_automation_revisions WHERE automation_id=id AND revision=current_revision),60))<=now()) ORDER BY COALESCE(last_scan,'epoch'::timestamptz),id LIMIT 50`)
	if err != nil {
		return err
	}
	type item struct{ id, owner string }
	var items []item
	for rows.Next() {
		var i item
		if err = rows.Scan(&i.id, &i.owner); err != nil {
			rows.Close()
			return err
		}
		items = append(items, i)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, i := range items {
		a, e := s.GetAutomation(ctx, i.id, i.owner)
		if e != nil {
			continue
		}
		if e = s.scanAutomation(ctx, a, false); e != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automations SET status='blocked',diagnostic=$2,enabled=false,updated_at=now() WHERE id=$1`, i.id, shortError(e))
		}
	}
	return s.publishCompleted(ctx)
}

func (s *Service) scanAutomation(ctx context.Context, a Automation, manual bool) error {
	if s.jobs == nil {
		return errors.New("job processing is not configured")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "automation:"+a.ID).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "automation:"+a.ID)
	if !manual && !a.Enabled {
		return nil
	}
	principal, err := s.executionPrincipal(ctx, a.OwnerID, a.Definition.Action.Mode)
	if err != nil {
		return err
	}
	if err = s.ValidateDefinition(ctx, a.Definition); err != nil {
		return err
	}
	input, err := s.CheckDrive(ctx, a.Definition.Trigger.InputDriveID)
	if err != nil || !input.Accessible {
		if err != nil {
			return err
		}
		return errors.New("input drive is inaccessible")
	}
	if err = s.markScan(ctx, a.ID); err != nil {
		return err
	}
	firstScan := a.LastScan == nil
	entries, err := os.ReadDir(input.Path)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	if len(entries) > maxEntriesPerDriveScan {
		entries = entries[:maxEntriesPerDriveScan]
	}
	var budget int64
	processed := 0
	active, err := s.activeRunCount(ctx, a.ID)
	if err != nil {
		return err
	}
	maxConcurrent := a.Definition.Trigger.MaxConcurrent
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	if active >= maxConcurrent {
		return nil
	}
	for _, entry := range entries {
		if processed >= maxFilesPerAutomation || ctx.Err() != nil || active+processed >= maxConcurrent {
			break
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".101conv-") {
			continue
		}
		full := filepath.Join(input.Path, entry.Name())
		resolved, e := filepath.EvalSymlinks(full)
		if e != nil || filepath.Dir(resolved) != input.Path {
			continue
		}
		info, e := os.Stat(resolved)
		if e != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > jobs.MaxInputBytes {
			continue
		}
		match, _ := matchesDefinition(a.Definition, entry.Name(), info.Size())
		if !match {
			continue
		}
		if info.Size() > maxScanBytes-budget {
			break
		}
		budget += info.Size()
		var state string
		var seenSize, seenMtime int64
		var firstSeen time.Time
		var attempt int
		var nextAttempt sql.NullTime
		e = s.db.QueryRowContext(ctx, `SELECT state,observed_size,observed_mtime,first_seen,attempt_count,next_attempt FROM conv_automation_runs WHERE automation_id=$1 AND source_path=$2 ORDER BY first_seen DESC LIMIT 1`, a.ID, entry.Name()).Scan(&state, &seenSize, &seenMtime, &firstSeen, &attempt, &nextAttempt)
		if firstScan && !a.Definition.Trigger.ProcessExistingOnNext {
			if errors.Is(e, sql.ErrNoRows) {
				_, _ = s.db.ExecContext(ctx, `INSERT INTO conv_automation_runs(id,automation_id,revision,fingerprint,source_name,source_path,state,observed_size,observed_mtime) VALUES($1,$2,$3,$4,$5,$6,'skipped_existing',$7,$8) ON CONFLICT(automation_id,fingerprint) DO NOTHING`, mustID(), a.ID, a.Revision, "baseline:"+entry.Name()+":"+fmt.Sprint(info.Size(), ":", info.ModTime().UnixNano()), entry.Name(), entry.Name(), info.Size(), info.ModTime().UnixNano())
			}
			continue
		}
		if e == nil && state == "retry_wait" {
			if attempt >= 3 || nextAttempt.Valid && time.Now().Before(nextAttempt.Time) {
				continue
			}
			digest, hashErr := fileFingerprint(resolved)
			current, statErr := os.Stat(resolved)
			if hashErr != nil || statErr != nil || current.Size() != info.Size() || current.ModTime() != info.ModTime() {
				continue
			}
			var runID string
			if err = s.db.QueryRowContext(ctx, `SELECT id FROM conv_automation_runs WHERE automation_id=$1 AND source_path=$2 ORDER BY first_seen DESC LIMIT 1`, a.ID, entry.Name()).Scan(&runID); err != nil {
				return err
			}
			if err = s.submitFile(ctx, principal, a, runID, resolved, entry.Name(), digest, info.Size(), attempt); err != nil {
				_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state=CASE WHEN attempt_count<2 THEN 'retry_wait' ELSE 'failed' END,attempt_count=attempt_count+1,error=$2,next_attempt=CASE WHEN attempt_count<2 THEN now()+interval '30 seconds' ELSE NULL END,finished_at=CASE WHEN attempt_count<2 THEN NULL ELSE now() END WHERE id=$1`, runID, shortError(err))
				continue
			}
			processed++
			continue
		}
		if e == nil && seenSize == info.Size() && seenMtime == info.ModTime().UnixNano() && state != "observing" {
			continue
		}
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if errors.Is(e, sql.ErrNoRows) || seenSize != info.Size() || seenMtime != info.ModTime().UnixNano() {
			id := mustID()
			finger := "observing:" + id
			_, e = s.db.ExecContext(ctx, `INSERT INTO conv_automation_runs(id,automation_id,revision,fingerprint,source_name,source_path,state,observed_size,observed_mtime) VALUES($1,$2,$3,$4,$5,$6,'observing',$7,$8)`, id, a.ID, a.Revision, finger, entry.Name(), entry.Name(), info.Size(), info.ModTime().UnixNano())
			if e != nil {
				return e
			}
			continue
		}
		if time.Since(firstSeen) < time.Duration(a.Definition.Trigger.StabilitySeconds)*time.Second {
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET last_seen=now() WHERE automation_id=$1 AND source_path=$2 AND state='observing'`, a.ID, entry.Name())
			continue
		}
		file, openErr := os.Open(resolved)
		if openErr != nil {
			continue
		}
		h := sha256.New()
		n, copyErr := io.Copy(h, io.LimitReader(file, jobs.MaxInputBytes+1))
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || n != info.Size() {
			continue
		}
		after, e := os.Stat(resolved)
		if e != nil || after.Size() != info.Size() || after.ModTime() != info.ModTime() {
			continue
		}
		digest := hex.EncodeToString(h.Sum(nil))
		runFingerprint := fileIdentityFingerprint(entry.Name(), digest)
		var generated bool
		_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_generated_files WHERE drive_id=$1 AND fingerprint=$2)`, input.ID, digest).Scan(&generated)
		if generated {
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='skipped_generated',fingerprint=$3,finished_at=now() WHERE automation_id=$1 AND source_path=$2 AND state='observing'`, a.ID, entry.Name(), digest)
			continue
		}
		var duplicate bool
		_ = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_automation_runs WHERE automation_id=$1 AND fingerprint=$2 AND state<>'observing')`, a.ID, runFingerprint).Scan(&duplicate)
		if duplicate {
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='skipped_duplicate',fingerprint=$3,finished_at=now() WHERE automation_id=$1 AND source_path=$2 AND state='observing'`, a.ID, entry.Name(), runFingerprint)
			continue
		}
		var runID string
		_ = s.db.QueryRowContext(ctx, `SELECT id FROM conv_automation_runs WHERE automation_id=$1 AND source_path=$2 AND state='observing' ORDER BY first_seen DESC LIMIT 1`, a.ID, entry.Name()).Scan(&runID)
		if runID == "" {
			continue
		}
		if err = s.submitFile(ctx, principal, a, runID, resolved, entry.Name(), digest, info.Size(), 0); err != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state=CASE WHEN attempt_count<2 THEN 'retry_wait' ELSE 'failed' END,attempt_count=attempt_count+1,fingerprint=$2,error=$3,next_attempt=CASE WHEN attempt_count<2 THEN now()+interval '30 seconds' ELSE NULL END,finished_at=CASE WHEN attempt_count<2 THEN NULL ELSE now() END WHERE id=$1`, runID, runFingerprint, shortError(err))
			continue
		}
		processed++
	}
	return nil
}

func (s *Service) executionPrincipal(ctx context.Context, ownerID, mode string) (auth.Principal, error) {
	if s.accounts == nil {
		return auth.Principal{}, errors.New("automation account authorization is not configured")
	}
	p, err := s.accounts.Principal(ctx, ownerID)
	if err != nil {
		return auth.Principal{}, fmt.Errorf("automation owner is unavailable: %w", err)
	}
	required := auth.PermissionConversion
	if mode == "compress" {
		required = auth.PermissionCompression
	}
	if !p.Permissions[auth.PermissionAutomationsExecute] || !p.Permissions[required] {
		return auth.Principal{}, errors.New("automation owner no longer has permission to execute this workflow")
	}
	return p, nil
}

func (s *Service) submitFile(ctx context.Context, principal auth.Principal, a Automation, runID, path, name, digest string, size int64, attempt int) error {
	outputDrive, err := s.CheckDrive(ctx, a.Definition.Action.OutputDriveID)
	if err != nil || !outputDrive.Enabled || !outputDrive.Writable {
		return errors.New("configured output drive is unavailable or not writable")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return errors.New("input changed or is no longer a regular file")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), path)
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		_ = file.Close()
		return errors.New("input changed while it was being opened")
	}
	maxFileBytes := principal.MaxFileBytes
	if maxFileBytes <= 0 || maxFileBytes > jobs.MaxInputBytes {
		maxFileBytes = jobs.MaxInputBytes
	}
	var monthlyBytes *int64
	if principal.Quotas.MonthlyBytes.Mode == "limited" {
		monthlyBytes = principal.Quotas.MonthlyBytes.Value
	}
	upload, err := s.jobs.CreateUploadFor(ctx, principal.ID, maxFileBytes, monthlyBytes, name, file)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if upload.SHA256 != digest {
		return errors.New("input file changed after the stability check; it will be observed again")
	}
	key := "automation:" + a.ID + ":r" + fmt.Sprint(a.Revision) + ":" + runID + ":a" + fmt.Sprint(attempt)
	var monthlyJobs *int64
	if principal.Quotas.MonthlyJobs.Mode == "limited" {
		monthlyJobs = principal.Quotas.MonthlyJobs.Value
	}
	job, _, err := s.jobs.SubmitFor(ctx, jobs.SubmitRequest{UploadID: upload.ID, ProfileID: a.Definition.Action.ProfileID, ProfileVersion: a.Definition.Action.ProfileVersion, TargetFormat: a.Definition.Action.TargetFormat, Mode: a.Definition.Action.Mode, IdempotencyKey: key}, principal.ID, jobs.Limits{MonthlyJobs: monthlyJobs})
	if err != nil {
		return err
	}
	snapshot, _ := json.Marshal(executionSnapshot{Definition: a.Definition, OutputPath: outputDrive.Path})
	_, err = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='queued',fingerprint=$2,job_id=$3,upload_id=$4,execution_snapshot=$5,started_at=now(),last_seen=now(),next_attempt=NULL WHERE id=$1`, runID, fileIdentityFingerprint(name, digest), job.ID, upload.ID, snapshot)
	if err != nil {
		return err
	}
	_, _ = s.db.ExecContext(ctx, `UPDATE conv_automations SET status='ready',diagnostic='',last_execution=now(),updated_at=now() WHERE id=$1`, a.ID)
	return nil
}

func fileIdentityFingerprint(name, digest string) string {
	h := sha256.Sum256([]byte(name + "\x00" + digest))
	return hex.EncodeToString(h[:])
}

func (s *Service) publishCompleted(ctx context.Context) error {
	if s.jobs == nil {
		return nil
	}
	// Manual scans and the periodic scheduler can both observe completed jobs.
	// Serialize output publication across Core instances so two pollers cannot
	// publish the same result under different "unique" filenames.
	lockConn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer lockConn.Close()
	var locked bool
	if err = lockConn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "automation:publish").Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer lockConn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "automation:publish")
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.automation_id,r.revision,r.source_name,r.job_id,a.owner_id,r.execution_snapshot FROM conv_automation_runs r JOIN conv_automations a ON a.id=r.automation_id WHERE r.state IN ('queued','running') AND r.job_id IS NOT NULL ORDER BY r.started_at LIMIT 100`)
	if err != nil {
		return err
	}
	type pending struct {
		id, automation, owner, name, job string
		revision                         int
		snapshot                         []byte
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.id, &p.automation, &p.revision, &p.name, &p.job, &p.owner, &p.snapshot); err != nil {
			rows.Close()
			return err
		}
		list = append(list, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, p := range list {
		job, e := s.jobs.GetFor(ctx, p.job, p.owner)
		if e != nil {
			continue
		}
		switch job.State {
		case jobs.Queued, jobs.Running:
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state=$2 WHERE id=$1`, p.id, string(job.State))
			continue
		case jobs.Failed, jobs.Cancelled:
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET attempt_count=attempt_count+1,state=CASE WHEN $2='failed' AND attempt_count<2 THEN 'retry_wait' ELSE $2 END,next_attempt=CASE WHEN $2='failed' AND attempt_count<2 THEN now()+make_interval(secs=>CASE WHEN attempt_count=0 THEN 30 WHEN attempt_count=1 THEN 60 ELSE 120 END) ELSE NULL END,error=$3,finished_at=CASE WHEN $2='failed' AND attempt_count<2 THEN NULL ELSE now() END WHERE id=$1`, p.id, string(job.State), shortError(errors.New(job.Error)))
			continue
		case jobs.Succeeded:
		default:
			continue
		}
		var snapshot executionSnapshot
		if json.Unmarshal(p.snapshot, &snapshot) != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='blocked',error='execution snapshot is unavailable',finished_at=now() WHERE id=$1`, p.id)
			continue
		}
		path, e := s.ResolveDrivePath(snapshot.OutputPath)
		if e != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='blocked',error='configured output drive is unavailable',finished_at=now() WHERE id=$1`, p.id)
			continue
		}
		var currentEnabled bool
		var currentPath string
		if e = s.db.QueryRowContext(ctx, `SELECT enabled,path FROM conv_mounted_drives WHERE id=$1`, snapshot.Definition.Action.OutputDriveID).Scan(&currentEnabled, &currentPath); e != nil || !currentEnabled || currentPath != path {
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='blocked',error='configured output drive changed or was disabled during this execution',finished_at=now() WHERE id=$1`, p.id)
			continue
		}
		result, _, _, e := s.jobs.OpenResultFor(ctx, p.job, p.owner)
		if e != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET attempt_count=attempt_count+1,state=CASE WHEN attempt_count<2 THEN 'retry_wait' ELSE 'failed' END,next_attempt=CASE WHEN attempt_count<2 THEN now()+make_interval(secs=>CASE WHEN attempt_count=0 THEN 30 WHEN attempt_count=1 THEN 60 ELSE 120 END) ELSE NULL END,error=$2,finished_at=CASE WHEN attempt_count<2 THEN NULL ELSE now() END WHERE id=$1`, p.id, shortError(e))
			continue
		}
		outName := strings.TrimSuffix(filepath.Base(p.name), filepath.Ext(p.name)) + "." + snapshot.Definition.Action.TargetFormat
		if snapshot.Definition.Action.OutputBehavior == "skip_existing" {
			if _, statErr := os.Lstat(filepath.Join(path, outName)); statErr == nil {
				_ = result.Close()
				_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='skipped_existing_output',finished_at=now() WHERE id=$1`, p.id)
				continue
			} else if !errors.Is(statErr, os.ErrNotExist) {
				_ = result.Close()
				continue
			}
		}
		published, e := publishFile(path, outName, snapshot.Definition.Action.OutputBehavior, result, job.OutputSize)
		closeErr := result.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET attempt_count=attempt_count+1,state=CASE WHEN attempt_count<2 THEN 'retry_wait' ELSE 'failed' END,next_attempt=CASE WHEN attempt_count<2 THEN now()+make_interval(secs=>CASE WHEN attempt_count=0 THEN 30 WHEN attempt_count=1 THEN 60 ELSE 120 END) ELSE NULL END,error=$2,finished_at=CASE WHEN attempt_count<2 THEN NULL ELSE now() END WHERE id=$1`, p.id, shortError(e))
			continue
		}
		finger, e := fileFingerprint(filepath.Join(path, published))
		if e == nil {
			_, _ = s.db.ExecContext(ctx, `INSERT INTO conv_generated_files(drive_id,fingerprint,automation_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, snapshot.Definition.Action.OutputDriveID, finger, p.automation)
		}
		_, _ = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='succeeded',output_name=$2,finished_at=now() WHERE id=$1`, p.id, published)
	}
	return nil
}

func (s *Service) markScan(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conv_automations SET last_scan=now() WHERE id=$1`, id)
	return err
}

func (s *Service) activeRunCount(ctx context.Context, automationID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM conv_automation_runs r JOIN conv_jobs j ON j.id=r.job_id WHERE r.automation_id=$1 AND j.state IN ('queued','running')`, automationID).Scan(&count)
	return count, err
}

func matches(filter FileFilter, name string, size int64) (bool, string) {
	if filter.MinBytes > 0 && size < filter.MinBytes {
		return false, "Below minimum file size"
	}
	if filter.MaxBytes > 0 && size > filter.MaxBytes {
		return false, "Above maximum file size"
	}
	if filter.NameGlob != "" {
		ok, e := filepath.Match(filter.NameGlob, name)
		if e != nil || !ok {
			return false, "Filename does not match"
		}
	}
	if len(filter.Extensions) > 0 {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
		for _, candidate := range filter.Extensions {
			if ext == strings.TrimPrefix(strings.ToLower(candidate), ".") {
				return true, ""
			}
		}
		return false, "File extension is not selected"
	}
	return true, ""
}

func matchesDefinition(def Definition, name string, size int64) (bool, string) {
	if matched, reason := matches(def.Filter, name, size); !matched {
		return false, reason
	}
	if def.Condition == nil {
		return true, ""
	}
	matched, reason := evaluateCondition(*def.Condition, name, size)
	if !matched && reason == "" {
		reason = "File does not satisfy the configured conditions"
	}
	return matched, reason
}

func evaluateCondition(c Condition, name string, size int64) (bool, string) {
	switch c.Operator {
	case "and":
		for _, child := range c.Children {
			if ok, reason := evaluateCondition(child, name, size); !ok {
				return false, reason
			}
		}
		return true, ""
	case "or":
		for _, child := range c.Children {
			if ok, _ := evaluateCondition(child, name, size); ok {
				return true, ""
			}
		}
		return false, "No alternative condition matched"
	case "not":
		ok, _ := evaluateCondition(c.Children[0], name, size)
		if ok {
			return false, "Excluded by a NOT condition"
		}
		return true, ""
	case "extension":
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
		return ext == strings.TrimPrefix(strings.ToLower(c.Value), "."), "File extension condition did not match"
	case "name_glob":
		ok, err := filepath.Match(c.Value, name)
		return err == nil && ok, "Filename condition did not match"
	case "min_size":
		return size >= c.Bytes, "File is below the configured size condition"
	case "max_size":
		return size <= c.Bytes, "File exceeds the configured size condition"
	default:
		return false, "Unknown condition block"
	}
}

func publishFile(root, name, behavior string, source io.Reader, size int64) (string, error) {
	if size < 0 || size > jobs.MaxOutputBytes {
		return "", errors.New("conversion output exceeded the supported file size")
	}
	if strings.ContainsAny(name, "/\\\x00\r\n") || name == "" {
		return "", errors.New("output filename is unsafe")
	}
	stage, err := os.CreateTemp(root, ".101conv-stage-*")
	if err != nil {
		return "", err
	}
	stagePath := stage.Name()
	defer os.Remove(stagePath)
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(stage, hasher), io.LimitReader(source, jobs.MaxOutputBytes+1))
	if err == nil && written > jobs.MaxOutputBytes {
		err = errors.New("conversion output exceeded the supported file size")
	}
	if err == nil && size > 0 && written != size {
		err = errors.New("conversion output size changed during publication")
	}
	if err == nil {
		err = stage.Sync()
	}
	if closeErr := stage.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	stageDigest := hex.EncodeToString(hasher.Sum(nil))
	for index := 1; index <= 100; index++ {
		candidate := name
		if behavior == "unique" && index > 1 {
			ext := filepath.Ext(name)
			candidate = strings.TrimSuffix(name, ext) + fmt.Sprintf(" (%d)", index) + ext
		}
		target := filepath.Join(root, candidate)
		if behavior == "unique" {
			if existing, e := fileFingerprint(target); e == nil {
				if existing == stageDigest {
					return candidate, nil
				}
			} else if !errors.Is(e, os.ErrNotExist) {
				return "", e
			}
		}
		if behavior == "skip_existing" {
			if _, e := os.Lstat(target); e == nil {
				return "", fmt.Errorf("destination %q already exists", candidate)
			} else if !errors.Is(e, os.ErrNotExist) {
				return "", e
			}
		}
		if behavior != "unique" && behavior != "skip_existing" && behavior != "create" {
			return "", errors.New("unsupported output behavior")
		}
		if e := os.Link(stagePath, target); e == nil {
			return candidate, nil
		} else if errors.Is(e, os.ErrExist) {
			if behavior == "unique" {
				continue
			}
			return "", fmt.Errorf("destination %q already exists", candidate)
		} else {
			return "", fmt.Errorf("atomic publication is unavailable on this destination filesystem: %w", e)
		}
	}
	return "", errors.New("could not find an unused destination filename")
}

func fileFingerprint(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, io.LimitReader(f, jobs.MaxOutputBytes+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func shortError(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}
func mustID() string {
	id, err := randomID()
	if err != nil {
		return "run-id-unavailable"
	}
	return id
}
