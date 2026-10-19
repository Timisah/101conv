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
	"log"
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
	Scope        string        `json:"scope"`
}

type executionSnapshot struct {
	Definition Definition `json:"definition"`
	OutputPath string     `json:"outputPath"`
	InputPath  string     `json:"inputPath"`
}

// Run starts the bounded polling scheduler. Core is the only process with
// access to registered mounts; the converter Worker receives only private job
// uploads and its existing read-only engine mount.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	if err := s.runTick(ctx); err != nil && ctx.Err() == nil {
		log.Printf("automation tick: %v", err)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.runTick(ctx); err != nil && ctx.Err() == nil {
				log.Printf("automation tick: %v", err)
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
	preview := Preview{AutomationID: a.ID, Revision: a.Revision, Scope: "current_file_matches_only"}
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
	if err := s.recoverSubmissions(ctx); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id FROM conv_automations WHERE enabled AND status IN ('ready','running') AND (SELECT definition->'trigger'->>'type' FROM conv_automation_revisions WHERE automation_id=id AND revision=current_revision)='folder.scan' AND (last_scan IS NULL OR last_scan+make_interval(secs=>COALESCE((SELECT (definition->'trigger'->>'intervalSeconds')::int FROM conv_automation_revisions WHERE automation_id=id AND revision=current_revision),60))<=now()) ORDER BY COALESCE(last_scan,'epoch'::timestamptz),id LIMIT 50`)
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
		if e = s.scanAutomation(ctx, a, false); e != nil && ctx.Err() == nil {
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
	// A queued scheduler item may have been edited or paused while waiting.
	a, err = s.GetAutomation(ctx, a.ID, a.OwnerID)
	if err != nil {
		return err
	}
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
	if err != nil {
		return err
	}
	if !input.Enabled || !input.Accessible {
		return errors.New("input drive is disabled or inaccessible")
	}
	scan, err := s.scanState(ctx, a, input)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(input.Path)
	if err != nil {
		return err
	}
	// ReadDir returns names in lexical order. Persist the last visited name so
	// large folders and interrupted baselines are not stuck on their first page.
	start := sort.Search(len(entries), func(i int) bool { return entries[i].Name() > scan.Cursor })
	end := min(start+maxEntriesPerDriveScan, len(entries))
	active, err := s.activeRunCount(ctx, a.ID)
	if err != nil {
		return err
	}
	maxConcurrent := max(1, a.Definition.Trigger.MaxConcurrent)
	var budget int64
	processed := 0
	cursor := scan.Cursor
	for _, entry := range entries[start:end] {
		if err = ctx.Err(); err != nil {
			return err
		}
		// Advancing this cursor is safe only after handling the entry successfully.
		if err = s.scanEntry(ctx, principal, a, input, scan, entry, &budget, &processed, active, maxConcurrent); err != nil {
			return err
		}
		cursor = entry.Name()
	}
	complete := end == len(entries)
	if complete {
		cursor = ""
	}
	_, err = s.db.ExecContext(ctx, `UPDATE conv_automation_scans SET cursor=$2,baseline_complete=baseline_complete OR $3,process_existing=CASE WHEN $3 THEN false ELSE process_existing END WHERE automation_id=$1`, a.ID, cursor, complete)
	if err != nil {
		return err
	}
	return s.markScan(ctx, a.ID)
}

type scanProgress struct {
	Generation      int64
	Cutoff          time.Time
	Cursor          string
	ProcessExisting bool
	InventoryReady  bool
}

func (s *Service) scanState(ctx context.Context, a Automation, input Drive) (scanProgress, error) {
	// Persist a generation before inventorying names, then commit its complete
	// baseline atomically before processing any page. File timestamps are not
	// an authority for whether a name existed at baseline capture.
	// A fresh explicit request is identified by its immutable revision number.
	var p scanProgress
	err := s.db.QueryRowContext(ctx, `INSERT INTO conv_automation_scans(automation_id,input_drive,input_path,process_existing,request_revision)
 VALUES($1,$2,$3,$4,$5) ON CONFLICT(automation_id) DO UPDATE SET
 generation=conv_automation_scans.generation+CASE WHEN conv_automation_scans.input_drive<>$2 OR conv_automation_scans.input_path<>$3 OR ($4 AND conv_automation_scans.request_revision<>$5) THEN 1 ELSE 0 END,
 cutoff=CASE WHEN conv_automation_scans.input_drive<>$2 OR conv_automation_scans.input_path<>$3 OR ($4 AND conv_automation_scans.request_revision<>$5) THEN now() ELSE conv_automation_scans.cutoff END,
 cursor=CASE WHEN conv_automation_scans.input_drive<>$2 OR conv_automation_scans.input_path<>$3 OR ($4 AND conv_automation_scans.request_revision<>$5) THEN '' ELSE conv_automation_scans.cursor END,
 inventory_ready=CASE WHEN conv_automation_scans.input_drive<>$2 OR conv_automation_scans.input_path<>$3 OR ($4 AND conv_automation_scans.request_revision<>$5) THEN false ELSE conv_automation_scans.inventory_ready END,
 baseline_complete=CASE WHEN conv_automation_scans.input_drive<>$2 OR conv_automation_scans.input_path<>$3 OR ($4 AND conv_automation_scans.request_revision<>$5) THEN false ELSE conv_automation_scans.baseline_complete END,
 process_existing=CASE WHEN conv_automation_scans.request_revision<>$5 AND $4 THEN true WHEN conv_automation_scans.input_drive<>$2 OR conv_automation_scans.input_path<>$3 THEN false ELSE conv_automation_scans.process_existing END,
 request_revision=$5,input_drive=$2,input_path=$3
 RETURNING generation,cutoff,cursor,process_existing,inventory_ready`, a.ID, input.ID, input.Path, a.Definition.Trigger.ProcessExistingOnNext, a.Revision).Scan(&p.Generation, &p.Cutoff, &p.Cursor, &p.ProcessExisting, &p.InventoryReady)
	if err != nil || p.InventoryReady {
		return p, err
	}
	entries, err := os.ReadDir(input.Path)
	if err != nil {
		return p, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	raw, err := json.Marshal(names)
	if err != nil {
		return p, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return p, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO conv_automation_baseline(automation_id,generation,source_name) SELECT $1,$2,value FROM jsonb_array_elements_text($3::jsonb) ON CONFLICT DO NOTHING`, a.ID, p.Generation, raw); err != nil {
		return p, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_automation_scans SET inventory_ready=true WHERE automation_id=$1 AND generation=$2`, a.ID, p.Generation); err != nil {
		return p, err
	}
	return p, tx.Commit()
}

func (s *Service) scanEntry(ctx context.Context, principal auth.Principal, a Automation, input Drive, scan scanProgress, entry os.DirEntry, budget *int64, processed *int, active, maxConcurrent int) error {
	if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".101conv-") {
		return nil
	}
	path := filepath.Join(input.Path, entry.Name())
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || filepath.Dir(resolved) != input.Path {
		return nil
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > jobs.MaxInputBytes {
		return nil
	}
	match, _ := matchesDefinition(a.Definition, entry.Name(), info.Size())
	if !match {
		return nil
	}
	var id, state string
	var size, mtime int64
	var firstSeen time.Time
	err = s.db.QueryRowContext(ctx, `SELECT id,state,observed_size,observed_mtime,first_seen FROM conv_automation_runs WHERE automation_id=$1 AND scan_generation=$3 AND source_path=$2 ORDER BY first_seen DESC,id DESC LIMIT 1`, a.ID, entry.Name(), scan.Generation).Scan(&id, &state, &size, &mtime, &firstSeen)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var baseline bool
	if !scan.ProcessExisting && (errors.Is(err, sql.ErrNoRows) || state == "skipped_existing") {
		if e := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_automation_baseline WHERE automation_id=$1 AND generation=$2 AND source_name=$3)`, a.ID, scan.Generation, entry.Name()).Scan(&baseline); e != nil {
			return e
		}
	}
	if baseline && (errors.Is(err, sql.ErrNoRows) || size == info.Size() && mtime == info.ModTime().UnixNano()) {
		if errors.Is(err, sql.ErrNoRows) {
			_, err = s.db.ExecContext(ctx, `INSERT INTO conv_automation_runs(id,automation_id,revision,fingerprint,source_name,source_path,state,observed_size,observed_mtime,scan_generation,finished_at) VALUES($1,$2,$3,$4,$5,$5,'skipped_existing',$6,$7,$8,now()) ON CONFLICT DO NOTHING`, mustID(), a.ID, a.Revision, fmt.Sprintf("baseline:%d:%s", scan.Generation, fileIdentityFingerprint(entry.Name(), fmt.Sprint(info.Size(), ":", info.ModTime().UnixNano()))), entry.Name(), info.Size(), info.ModTime().UnixNano(), scan.Generation)
			return err
		}
		return nil
	}
	if err == nil && size == info.Size() && mtime == info.ModTime().UnixNano() && state != "observing" {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) || size != info.Size() || mtime != info.ModTime().UnixNano() {
		id = mustID()
		output, err := s.CheckDrive(ctx, a.Definition.Action.OutputDriveID)
		if err != nil {
			return err
		}
		snapshot, _ := json.Marshal(executionSnapshot{Definition: a.Definition, OutputPath: output.Path, InputPath: input.Path})
		_, err = s.db.ExecContext(ctx, `INSERT INTO conv_automation_runs(id,automation_id,revision,fingerprint,source_name,source_path,state,observed_size,observed_mtime,scan_generation,execution_snapshot) VALUES($1,$2,$3,$4,$5,$5,'observing',$6,$7,$8,$9)`, id, a.ID, a.Revision, "observing:"+id, entry.Name(), info.Size(), info.ModTime().UnixNano(), scan.Generation, snapshot)
		return err
	}
	if time.Since(firstSeen) < time.Duration(a.Definition.Trigger.StabilitySeconds)*time.Second || *processed >= maxFilesPerAutomation || active+*processed >= maxConcurrent || info.Size() > maxScanBytes-*budget {
		return nil
	}
	*budget += info.Size()
	digest, err := fileFingerprint(path)
	if err != nil {
		return err
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(info, after) || info.Size() != after.Size() || info.ModTime() != after.ModTime() {
		return nil
	}
	fingerprint := fileIdentityFingerprint(input.ID+":"+entry.Name(), digest)
	var generated, duplicate bool
	if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_generated_files WHERE drive_id=$1 AND fingerprint=$2)`, input.ID, digest).Scan(&generated); err != nil {
		return err
	}
	if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_automation_runs WHERE automation_id=$1 AND (fingerprint=$2 OR fingerprint=$4) AND id<>$3)`, a.ID, fingerprint, id, fileIdentityFingerprint(entry.Name(), digest)).Scan(&duplicate); err != nil {
		return err
	}
	if generated || duplicate {
		state = "skipped_generated"
		if duplicate {
			state = "skipped_duplicate"
		}
		_, err = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state=$2,finished_at=now() WHERE id=$1`, id, state)
		return err
	}
	// Claim the fingerprint and freeze the request before any external side effect.
	_, err = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET fingerprint=$2,source_digest=$3,state='submitting',started_at=now() WHERE id=$1`, id, fingerprint, digest)
	if err != nil {
		return err
	}
	*processed++
	return s.resumeSubmission(ctx, id)
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

// resumeSubmission reuses one upload and one idempotency key. A lost response
// after SubmitFor commits is recovered by key before consulting mutable profiles.
func (s *Service) resumeSubmission(ctx context.Context, runID string) error {
	var automationID, owner, name, uploadID, digest string
	var raw []byte
	var revision, attempt int
	var next sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT r.automation_id,a.owner_id,r.source_name,r.upload_id,r.source_digest,r.execution_snapshot,r.revision,r.attempt_count,r.next_attempt FROM conv_automation_runs r JOIN conv_automations a ON a.id=r.automation_id WHERE r.id=$1 AND r.state='submitting'`, runID).Scan(&automationID, &owner, &name, &uploadID, &digest, &raw, &revision, &attempt, &next)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if next.Valid && time.Now().Before(next.Time) {
		return nil
	}
	key := "automation:" + runID
	var jobID string
	err = s.db.QueryRowContext(ctx, `SELECT id FROM conv_jobs WHERE idempotency_key=$1 AND owner_id=$2`, key, owner).Scan(&jobID)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='queued',job_id=$2,error='',next_attempt=NULL WHERE id=$1`, runID, jobID)
		return err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var snapshot executionSnapshot
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	submit := func() error {
		if snapshot.Definition.Action.ProfileVersion == "" {
			return errors.New("run has no pinned profile version; edit and save the automation for future runs")
		}
		principal, err := s.executionPrincipal(ctx, owner, snapshot.Definition.Action.Mode)
		if err != nil {
			return err
		}
		output, err := s.CheckDrive(ctx, snapshot.Definition.Action.OutputDriveID)
		if err != nil || !output.Enabled || output.Access != "read_write" || !output.Writable || output.Path != snapshot.OutputPath {
			return errors.New("output drive changed, was disabled, or is no longer registered read_write")
		}
		if uploadID == "" {
			input, err := s.CheckDrive(ctx, snapshot.Definition.Trigger.InputDriveID)
			if err != nil || !input.Enabled || !input.Accessible || input.Path != snapshot.InputPath {
				return errors.New("input drive changed or is unavailable")
			}
			inputRoot, err := os.OpenRoot(input.Path)
			if err != nil {
				return err
			}
			defer inputRoot.Close()
			file, err := inputRoot.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
			if err != nil {
				return err
			}
			defer file.Close()
			info, err := file.Stat()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return errors.New("source is no longer a regular file")
			}
			maxBytes := principal.MaxFileBytes
			if maxBytes <= 0 || maxBytes > jobs.MaxInputBytes {
				maxBytes = jobs.MaxInputBytes
			}
			var monthlyBytes *int64
			if principal.Quotas.MonthlyBytes.Mode == "limited" {
				monthlyBytes = principal.Quotas.MonthlyBytes.Value
			}
			upload, err := s.jobs.CreateUploadFor(ctx, owner, maxBytes, monthlyBytes, name, file)
			if err != nil {
				return err
			}
			if upload.SHA256 != digest {
				return errors.New("source changed after observation; this run will not submit changed contents")
			}
			uploadID = upload.ID
			if _, err = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET upload_id=$2 WHERE id=$1`, runID, uploadID); err != nil {
				return err
			}
		}
		var monthlyJobs *int64
		if principal.Quotas.MonthlyJobs.Mode == "limited" {
			monthlyJobs = principal.Quotas.MonthlyJobs.Value
		}
		action := snapshot.Definition.Action
		job, _, err := s.jobs.SubmitFor(ctx, jobs.SubmitRequest{UploadID: uploadID, ProfileID: action.ProfileID, ProfileVersion: action.ProfileVersion, TargetFormat: action.TargetFormat, Mode: action.Mode, IdempotencyKey: key}, owner, jobs.Limits{MonthlyJobs: monthlyJobs})
		if err != nil {
			return err
		}
		_, err = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='queued',job_id=$2,error='',next_attempt=NULL WHERE id=$1`, runID, job.ID)
		return err
	}
	err = submit()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Never change the key or execution request, even when a DB acknowledgement
		// is lost. Worker handles conversion retries with its frozen execution spec.
		_, updateErr := s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET attempt_count=attempt_count+1,error=$2,next_attempt=now()+interval '30 seconds',state=CASE WHEN attempt_count>=2 THEN 'blocked' ELSE 'submitting' END,finished_at=CASE WHEN attempt_count>=2 THEN now() ELSE NULL END WHERE id=$1`, runID, shortError(err))
		return updateErr
	}
	_, err = s.db.ExecContext(ctx, `UPDATE conv_automations SET last_execution=now() WHERE id=$1`, automationID)
	return err
}

func (s *Service) recoverSubmissions(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT automation_id FROM conv_automation_runs WHERE state='submitting' AND (next_attempt IS NULL OR next_attempt<=now())`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return err
		}
		var locked bool
		err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, "automation:"+id).Scan(&locked)
		if err != nil {
			conn.Close()
			return err
		}
		if !locked {
			conn.Close()
			continue
		}
		err = s.resumeAutomationSubmissions(ctx, id)
		_, unlockErr := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "automation:"+id)
		conn.Close()
		if err != nil {
			return err
		}
		if unlockErr != nil {
			return unlockErr
		}
	}
	return nil
}
func (s *Service) resumeAutomationSubmissions(ctx context.Context, id string) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM conv_automation_runs WHERE automation_id=$1 AND state='submitting' ORDER BY first_seen LIMIT 20`, id)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = s.resumeSubmission(ctx, id); err != nil {
			return err
		}
	}
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
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.automation_id,r.revision,r.source_name,r.job_id,a.owner_id,r.execution_snapshot,r.scan_generation=0 FROM conv_automation_runs r JOIN conv_automations a ON a.id=r.automation_id WHERE r.state IN ('queued','running') AND r.job_id IS NOT NULL ORDER BY r.started_at LIMIT 100`)
	if err != nil {
		return err
	}
	type pending struct {
		id, automation, owner, name, job string
		revision                         int
		snapshot                         []byte
		legacy                           bool
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err = rows.Scan(&p.id, &p.automation, &p.revision, &p.name, &p.job, &p.owner, &p.snapshot, &p.legacy); err != nil {
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
			if _, e = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state=$2,error=$3,finished_at=now() WHERE id=$1`, p.id, string(job.State), shortError(errors.New(job.Error))); e != nil {
				return e
			}
			continue
		case jobs.Succeeded:
		default:
			continue
		}
		if e = s.publishRun(ctx, p.id, p.automation, p.owner, p.name, p.job, p.snapshot, job, p.legacy); e != nil {
			return e
		}

	}
	return nil
}

func (s *Service) markScan(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conv_automations SET last_scan=now() WHERE id=$1`, id)
	return err
}

func (s *Service) activeRunCount(ctx context.Context, automationID string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM conv_automation_runs r WHERE r.automation_id=$1 AND r.state IN ('submitting','queued','running')`, automationID).Scan(&count)
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

func fileFingerprint(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
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
