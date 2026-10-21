package automation

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/jobs"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/version"
)

var (
	ErrNotFound = errors.New("automation or drive not found")
	ErrConflict = errors.New("automation or drive is in use")
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
)

type Service struct {
	db          *sql.DB
	allowedRoot []string
	jobs        *jobs.Service
	management  *management.Service
	accounts    *auth.Service
}

func NewService(db *sql.DB, allowedRoots []string, jobService *jobs.Service, managementService *management.Service, accountService *auth.Service) (*Service, error) {
	if db == nil {
		return nil, errors.New("automation storage requires PostgreSQL")
	}
	roots := make([]string, 0, len(allowedRoots))
	for _, root := range allowedRoots {
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("automation mount root must be absolute")
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			return nil, fmt.Errorf("automation mount root %q is unavailable: %w", root, err)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("automation mount root must be a readable directory")
		}
		roots = append(roots, resolved)
	}
	return &Service{db: db, allowedRoot: roots, jobs: jobService, management: managementService, accounts: accountService}, nil
}

func (s *Service) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS conv_mounted_drives (
 id TEXT PRIMARY KEY, name TEXT NOT NULL, path TEXT NOT NULL UNIQUE, access_mode TEXT NOT NULL CHECK(access_mode IN ('read','read_write')),
 enabled BOOLEAN NOT NULL DEFAULT true, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS conv_automations (
 id TEXT PRIMARY KEY, owner_id TEXT NOT NULL, name TEXT NOT NULL, enabled BOOLEAN NOT NULL DEFAULT false,
 status TEXT NOT NULL DEFAULT 'paused', diagnostic TEXT NOT NULL DEFAULT '', current_revision INTEGER NOT NULL DEFAULT 1,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(), last_execution TIMESTAMPTZ, last_scan TIMESTAMPTZ
);
ALTER TABLE conv_automations ADD COLUMN IF NOT EXISTS last_scan TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS conv_automations_enabled_idx ON conv_automations(enabled,updated_at);
CREATE TABLE IF NOT EXISTS conv_automation_revisions (
 automation_id TEXT NOT NULL REFERENCES conv_automations(id) ON DELETE CASCADE, revision INTEGER NOT NULL,
 definition JSONB NOT NULL, block_versions JSONB NOT NULL DEFAULT '{}'::jsonb, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(automation_id,revision)
);
CREATE TABLE IF NOT EXISTS conv_automation_runs (
 id TEXT PRIMARY KEY, automation_id TEXT NOT NULL REFERENCES conv_automations(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL, fingerprint TEXT NOT NULL, source_name TEXT NOT NULL, state TEXT NOT NULL,
 job_id TEXT, output_name TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '', attempt_count INTEGER NOT NULL DEFAULT 0,
 observed_size BIGINT NOT NULL DEFAULT 0, observed_mtime BIGINT NOT NULL DEFAULT 0, source_path TEXT NOT NULL DEFAULT '', upload_id TEXT NOT NULL DEFAULT '', execution_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb,
 first_seen TIMESTAMPTZ NOT NULL DEFAULT now(), last_seen TIMESTAMPTZ NOT NULL DEFAULT now(),
 next_attempt TIMESTAMPTZ, started_at TIMESTAMPTZ, finished_at TIMESTAMPTZ,
 UNIQUE(automation_id,fingerprint)
);
CREATE INDEX IF NOT EXISTS conv_automation_runs_history_idx ON conv_automation_runs(automation_id,first_seen DESC);
CREATE INDEX IF NOT EXISTS conv_automation_runs_pending_idx ON conv_automation_runs(state,next_attempt);
ALTER TABLE conv_automation_runs ADD COLUMN IF NOT EXISTS source_path TEXT NOT NULL DEFAULT '';
ALTER TABLE conv_automation_runs ADD COLUMN IF NOT EXISTS upload_id TEXT NOT NULL DEFAULT '';
ALTER TABLE conv_automation_runs ADD COLUMN IF NOT EXISTS execution_snapshot JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE conv_automation_runs ADD COLUMN IF NOT EXISTS scan_generation BIGINT NOT NULL DEFAULT 0;
ALTER TABLE conv_automation_runs ADD COLUMN IF NOT EXISTS source_digest TEXT NOT NULL DEFAULT '';
ALTER TABLE conv_automation_runs ADD COLUMN IF NOT EXISTS publication_digest TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS conv_automation_runs_source_idx ON conv_automation_runs(automation_id,scan_generation,source_path,first_seen DESC);
CREATE TABLE IF NOT EXISTS conv_automation_scans (
 automation_id TEXT PRIMARY KEY REFERENCES conv_automations(id) ON DELETE CASCADE,
 input_drive TEXT NOT NULL, input_path TEXT NOT NULL, generation BIGINT NOT NULL DEFAULT 1,
 cutoff TIMESTAMPTZ NOT NULL DEFAULT now(), baseline_complete BOOLEAN NOT NULL DEFAULT false,
 cursor TEXT NOT NULL DEFAULT '', process_existing BOOLEAN NOT NULL DEFAULT false,
 baseline_process_existing BOOLEAN NOT NULL DEFAULT false, request_revision INTEGER NOT NULL
);
ALTER TABLE conv_automation_scans ADD COLUMN IF NOT EXISTS inventory_ready BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE conv_automation_scans ADD COLUMN IF NOT EXISTS baseline_process_existing BOOLEAN NOT NULL DEFAULT false;
CREATE TABLE IF NOT EXISTS conv_automation_baseline (
 automation_id TEXT NOT NULL REFERENCES conv_automations(id) ON DELETE CASCADE,
 generation BIGINT NOT NULL, source_name TEXT NOT NULL,
 PRIMARY KEY(automation_id,generation,source_name)
);
-- A legacy last_scan cannot prove its first scan finished. Rebaseline safely,
-- without replaying a possibly consumed process-existing request.
INSERT INTO conv_automation_scans(automation_id,input_drive,input_path,request_revision)
 SELECT a.id,d.id,d.path,a.current_revision FROM conv_automations a
 JOIN conv_automation_revisions r ON r.automation_id=a.id AND r.revision=a.current_revision
 JOIN conv_mounted_drives d ON d.id=r.definition->'trigger'->>'inputDriveId'
 WHERE a.last_scan IS NOT NULL ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS conv_generated_files (
 drive_id TEXT NOT NULL REFERENCES conv_mounted_drives(id) ON DELETE CASCADE, fingerprint TEXT NOT NULL,
 automation_id TEXT NOT NULL REFERENCES conv_automations(id) ON DELETE CASCADE, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(drive_id,fingerprint)
);
`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE conv_automations a SET enabled=false,status='blocked',diagnostic='Profile version is not pinned. Edit and save this automation to pin the installed version before executing.' FROM conv_automation_revisions r WHERE r.automation_id=a.id AND r.revision=a.current_revision AND COALESCE(r.definition->'action'->>'profileVersion','')=''`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE conv_automation_runs SET state='blocked',error='Legacy retry requires review; its historical request was not durable. Save an explicitly pinned revision for future files.',finished_at=now() WHERE state='retry_wait'`)
	return err
}

func (s *Service) ResolveDrivePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("drive path must be absolute inside the Core container")
	}
	clean := filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("drive path is unavailable: %w", err)
	}
	info, err := os.Lstat(clean)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("drive path must be an existing directory, not a symlink")
	}
	for _, root := range s.allowedRoot {
		rel, e := filepath.Rel(root, resolved)
		if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return resolved, nil
		}
	}
	return "", errors.New("drive path is outside the administrator-configured mounted roots")
}

func (s *Service) CreateDrive(ctx context.Context, name, path, access string) (Drive, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 100 || (access != "read" && access != "read_write") {
		return Drive{}, errors.New("drive name and read/read_write access are required")
	}
	resolved, err := s.ResolveDrivePath(path)
	if err != nil {
		return Drive{}, err
	}
	id, err := randomID()
	if err != nil {
		return Drive{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO conv_mounted_drives(id,name,path,access_mode) VALUES($1,$2,$3,$4)`, id, name, resolved, access)
	if err != nil {
		return Drive{}, err
	}
	return s.CheckDrive(ctx, id)
}

func (s *Service) UpdateDrive(ctx context.Context, id, name, path, access string, enabled bool) (Drive, error) {
	if !idPattern.MatchString(id) || strings.TrimSpace(name) == "" || len(name) > 100 || (access != "read" && access != "read_write") {
		return Drive{}, errors.New("invalid drive update")
	}
	resolved, err := s.ResolveDrivePath(path)
	if err != nil {
		return Drive{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Drive{}, err
	}
	defer tx.Rollback()
	var previousPath string
	if err = tx.QueryRowContext(ctx, `SELECT path FROM conv_mounted_drives WHERE id=$1 FOR UPDATE`, id).Scan(&previousPath); errors.Is(err, sql.ErrNoRows) {
		return Drive{}, ErrNotFound
	} else if err != nil {
		return Drive{}, err
	}
	if previousPath != resolved {
		// Invalidate at edit time, including A->B->A edits between scheduler ticks.
		// Serialize with scans before touching their mutable progress rows.
		rows, e := tx.QueryContext(ctx, `SELECT automation_id FROM conv_automation_scans WHERE input_drive=$1 ORDER BY automation_id`, id)
		if e != nil {
			return Drive{}, e
		}
		var ids []string
		for rows.Next() {
			var a string
			if e = rows.Scan(&a); e != nil {
				rows.Close()
				return Drive{}, e
			}
			ids = append(ids, a)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return Drive{}, e
		}
		for _, a := range ids {
			if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "automation:"+a); err != nil {
				return Drive{}, err
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE conv_automation_scans s SET generation=generation+1,input_path=$2,cutoff=now(),cursor='',inventory_ready=false,baseline_complete=false,process_existing=false,baseline_process_existing=false,request_revision=a.current_revision FROM conv_automations a WHERE s.automation_id=a.id AND s.input_drive=$1`, id, resolved); err != nil {
			return Drive{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_mounted_drives SET name=$2,path=$3,access_mode=$4,enabled=$5,updated_at=now() WHERE id=$1`, id, strings.TrimSpace(name), resolved, access, enabled); err != nil {
		return Drive{}, err
	}
	if err = tx.Commit(); err != nil {
		return Drive{}, err
	}
	return s.CheckDrive(ctx, id)
}

func (s *Service) ListDrives(ctx context.Context) ([]Drive, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,d.name,d.path,d.access_mode,d.enabled,d.created_at,d.updated_at,(SELECT count(*) FROM conv_automations a JOIN conv_automation_revisions r ON r.automation_id=a.id AND r.revision=a.current_revision WHERE r.definition->'trigger'->>'inputDriveId'=d.id OR r.definition->'action'->>'outputDriveId'=d.id) FROM conv_mounted_drives d ORDER BY d.name,d.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Drive, 0)
	for rows.Next() {
		var d Drive
		if err = rows.Scan(&d.ID, &d.Name, &d.Path, &d.Access, &d.Enabled, &d.CreatedAt, &d.UpdatedAt, &d.AutomationCount); err != nil {
			return nil, err
		}
		if checked, e := s.CheckDrive(ctx, d.ID); e == nil {
			d = checked
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Service) CheckDrive(ctx context.Context, id string) (Drive, error) {
	var d Drive
	err := s.db.QueryRowContext(ctx, `SELECT d.id,d.name,d.path,d.access_mode,d.enabled,d.created_at,d.updated_at,(SELECT count(*) FROM conv_automations a JOIN conv_automation_revisions r ON r.automation_id=a.id AND r.revision=a.current_revision WHERE r.definition->'trigger'->>'inputDriveId'=d.id OR r.definition->'action'->>'outputDriveId'=d.id) FROM conv_mounted_drives d WHERE d.id=$1`, id).Scan(&d.ID, &d.Name, &d.Path, &d.Access, &d.Enabled, &d.CreatedAt, &d.UpdatedAt, &d.AutomationCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Drive{}, ErrNotFound
	}
	if err != nil {
		return Drive{}, err
	}
	checked, e := s.ResolveDrivePath(d.Path)
	if e != nil {
		d.HealthError = e.Error()
		return d, nil
	}
	d.Accessible = true
	var fs syscall.Statfs_t
	if e = syscall.Statfs(checked, &fs); e == nil {
		d.FreeBytes = fs.Bavail * uint64(fs.Bsize)
	}
	if d.Access == "read_write" {
		var probe [12]byte
		if _, e = rand.Read(probe[:]); e == nil {
			f, createErr := os.OpenFile(filepath.Join(checked, ".101conv-health-"+hex.EncodeToString(probe[:])), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if createErr != nil {
				d.HealthError = "not writable by the Core container"
			} else {
				d.Writable = true
				if closeErr := f.Close(); closeErr != nil {
					d.HealthError = closeErr.Error()
				}
				if removeErr := os.Remove(f.Name()); removeErr != nil {
					d.HealthError = "permission probe could not be removed"
				}
			}
		}
	}
	return d, nil
}

func (s *Service) RemoveDrive(ctx context.Context, id string) error {
	var refs int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM conv_automations a JOIN conv_automation_revisions r ON r.automation_id=a.id AND r.revision=a.current_revision WHERE r.definition->'trigger'->>'inputDriveId'=$1 OR r.definition->'action'->>'outputDriveId'=$1`, id).Scan(&refs); err != nil {
		return err
	}
	if refs > 0 {
		return ErrConflict
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM conv_mounted_drives WHERE id=$1`, id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) CreateAutomation(ctx context.Context, owner, name string, def Definition) (Automation, error) {
	if owner == "" || strings.TrimSpace(name) == "" || len(name) > 120 {
		return Automation{}, errors.New("owner and automation name are required")
	}
	if err := s.validateOwner(ctx, owner, def.Action.Mode); err != nil {
		return Automation{}, err
	}
	if err := s.pinProfile(ctx, &def); err != nil {
		return Automation{}, err
	}
	if err := s.ValidateDefinition(ctx, def); err != nil {
		return Automation{}, err
	}
	id, err := randomID()
	if err != nil {
		return Automation{}, err
	}
	raw, _ := json.Marshal(def)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Automation{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO conv_automations(id,owner_id,name,status) VALUES($1,$2,$3,'paused')`, id, owner, strings.TrimSpace(name)); err != nil {
		return Automation{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO conv_automation_revisions(automation_id,revision,definition,block_versions) VALUES($1,1,$2,$3)`, id, raw, blockVersions()); err != nil {
		return Automation{}, err
	}
	if err = tx.Commit(); err != nil {
		return Automation{}, err
	}
	return s.GetAutomation(ctx, id, owner)
}

func (s *Service) UpdateAutomation(ctx context.Context, id, owner, name string, def Definition) (Automation, error) {
	if strings.TrimSpace(name) == "" || len(name) > 120 {
		return Automation{}, errors.New("automation name is required and must be at most 120 characters")
	}
	if err := s.validateOwner(ctx, owner, def.Action.Mode); err != nil {
		return Automation{}, err
	}
	if err := s.pinProfile(ctx, &def); err != nil {
		return Automation{}, err
	}
	if err := s.ValidateDefinition(ctx, def); err != nil {
		return Automation{}, err
	}
	raw, _ := json.Marshal(def)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Automation{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "automation:"+id); err != nil {
		return Automation{}, err
	}
	var revision int
	if err = tx.QueryRowContext(ctx, `SELECT current_revision FROM conv_automations WHERE id=$1 AND ($2='' OR owner_id=$2) FOR UPDATE`, id, owner).Scan(&revision); errors.Is(err, sql.ErrNoRows) {
		return Automation{}, ErrNotFound
	} else if err != nil {
		return Automation{}, err
	}
	var previousInput string
	if err = tx.QueryRowContext(ctx, `SELECT definition->'trigger'->>'inputDriveId' FROM conv_automation_revisions WHERE automation_id=$1 AND revision=$2`, id, revision).Scan(&previousInput); err != nil {
		return Automation{}, err
	}
	revision++
	if previousInput != def.Trigger.InputDriveID {
		if _, err = tx.ExecContext(ctx, `UPDATE conv_automation_scans SET generation=generation+1,input_drive=$2,input_path=(SELECT path FROM conv_mounted_drives WHERE id=$2),cutoff=now(),cursor='',inventory_ready=false,baseline_complete=false,process_existing=$3,baseline_process_existing=$3,request_revision=$4 WHERE automation_id=$1`, id, def.Trigger.InputDriveID, def.Trigger.ProcessExistingOnNext, revision); err != nil {
			return Automation{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO conv_automation_revisions(automation_id,revision,definition,block_versions) VALUES($1,$2,$3,$4)`, id, revision, raw, blockVersions()); err != nil {
		return Automation{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_automations SET name=$3,current_revision=$4,status=CASE WHEN enabled THEN 'ready' ELSE 'paused' END,diagnostic='',updated_at=now() WHERE id=$1 AND ($2='' OR owner_id=$2)`, id, owner, strings.TrimSpace(name), revision); err != nil {
		return Automation{}, err
	}
	if err = tx.Commit(); err != nil {
		return Automation{}, err
	}
	return s.GetAutomation(ctx, id, owner)
}

func (s *Service) GetAutomation(ctx context.Context, id, owner string) (Automation, error) {
	var a Automation
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT a.id,a.owner_id,a.name,a.enabled,a.status,a.diagnostic,a.current_revision,r.definition,a.created_at,a.updated_at,a.last_execution,a.last_scan FROM conv_automations a JOIN conv_automation_revisions r ON r.automation_id=a.id AND r.revision=a.current_revision WHERE a.id=$1 AND ($2='' OR a.owner_id=$2)`, id, owner).Scan(&a.ID, &a.OwnerID, &a.Name, &a.Enabled, &a.Status, &a.Diagnostic, &a.Revision, &raw, &a.CreatedAt, &a.UpdatedAt, &a.LastExecution, &a.LastScan)
	if errors.Is(err, sql.ErrNoRows) {
		return Automation{}, ErrNotFound
	}
	if err != nil {
		return Automation{}, err
	}
	if err = json.Unmarshal(raw, &a.Definition); err != nil {
		return Automation{}, err
	}
	return a, nil
}

func (s *Service) ListAutomations(ctx context.Context, owner string) ([]Automation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id,a.owner_id,a.name,a.enabled,a.status,a.diagnostic,a.current_revision,r.definition,a.created_at,a.updated_at,a.last_execution,a.last_scan FROM conv_automations a JOIN conv_automation_revisions r ON r.automation_id=a.id AND r.revision=a.current_revision WHERE ($1='' OR a.owner_id=$1) ORDER BY a.updated_at DESC`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Automation, 0)
	for rows.Next() {
		var a Automation
		var raw []byte
		if err = rows.Scan(&a.ID, &a.OwnerID, &a.Name, &a.Enabled, &a.Status, &a.Diagnostic, &a.Revision, &raw, &a.CreatedAt, &a.UpdatedAt, &a.LastExecution, &a.LastScan); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &a.Definition); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) SetEnabled(ctx context.Context, id, owner string, enabled bool) (Automation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Automation{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "automation:"+id); err != nil {
		return Automation{}, err
	}

	if enabled {
		a, e := s.GetAutomation(ctx, id, owner)
		if e != nil {
			return Automation{}, e
		}
		if e = s.validateOwner(ctx, owner, a.Definition.Action.Mode); e != nil {
			return Automation{}, e
		}
		if e = s.ValidateDefinition(ctx, a.Definition); e != nil {
			_, _ = s.db.ExecContext(ctx, `UPDATE conv_automations SET enabled=false,status='blocked',diagnostic=$3,updated_at=now() WHERE id=$1 AND owner_id=$2`, id, owner, e.Error())
			return Automation{}, e
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE conv_automations SET enabled=$3,status=CASE WHEN $3 THEN 'ready' ELSE 'paused' END,diagnostic='',updated_at=now() WHERE id=$1 AND ($2='' OR owner_id=$2)`, id, owner, enabled)
	if err != nil {
		return Automation{}, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return Automation{}, ErrNotFound
	}
	if err = tx.Commit(); err != nil {
		return Automation{}, err
	}
	return s.GetAutomation(ctx, id, owner)
}

func (s *Service) validateOwner(ctx context.Context, ownerID, mode string) error {
	// An empty owner is reserved for administrator-authorized API calls. The
	// HTTP layer checks automations.manage before allowing those mutations;
	// non-admin callers always pass their immutable account ID.
	if ownerID == "" {
		return nil
	}
	if s.accounts == nil {
		return errors.New("automation account authorization is not configured")
	}
	p, err := s.accounts.Principal(ctx, ownerID)
	if err != nil {
		return fmt.Errorf("automation owner is unavailable: %w", err)
	}
	if p.IsAdmin() {
		return nil
	}
	modePermission := auth.PermissionConversion
	if mode == "compress" {
		modePermission = auth.PermissionCompression
	}
	if !p.Permissions[auth.PermissionAutomationsManage] || !p.Permissions[auth.PermissionAutomationsExecute] || !p.Permissions[modePermission] {
		return errors.New("automation owner needs workflow management, execution, and matching conversion permissions")
	}
	return nil
}

func (s *Service) DeleteAutomation(ctx context.Context, id, owner string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "automation:"+id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM conv_automations WHERE id=$1 AND ($2='' OR owner_id=$2) AND NOT EXISTS(SELECT 1 FROM conv_automation_runs r WHERE r.automation_id=$1 AND r.state IN ('submitting','queued','running'))`, id, owner)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	return tx.Commit()
}

func (s *Service) History(ctx context.Context, id, owner string, limit int) ([]Run, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_automations WHERE id=$1 AND ($2='' OR owner_id=$2))`, id, owner).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.id,r.automation_id,r.revision,r.source_name,r.state,COALESCE(r.job_id,''),r.output_name,r.error,COALESCE(r.started_at,r.first_seen),r.finished_at FROM conv_automation_runs r WHERE r.automation_id=$1 ORDER BY r.first_seen DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Run, 0)
	for rows.Next() {
		var v Run
		if err = rows.Scan(&v.ID, &v.AutomationID, &v.Revision, &v.FileName, &v.State, &v.JobID, &v.OutputName, &v.Error, &v.StartedAt, &v.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) pinProfile(ctx context.Context, d *Definition) error {
	if d.Action.ProfileVersion != "" {
		return nil
	}
	if s.management == nil {
		return errors.New("profile management is unavailable")
	}
	profiles, err := s.management.ListProfiles(ctx)
	if err != nil {
		return err
	}
	for _, installed := range profiles {
		if installed.Definition.ID == d.Action.ProfileID {
			d.Action.ProfileVersion = installed.Definition.Version
			return nil
		}
	}
	return errors.New("selected profile is not installed")
}

func (s *Service) ValidateDefinition(ctx context.Context, d Definition) error {
	if d.Action.ProfileVersion == "" {
		return errors.New("profile version is not pinned; edit and save this automation before executing")
	}

	if d.Trigger.Type != "folder.scan" && d.Trigger.Type != "manual" {
		return errors.New("trigger must be folder.scan or manual")
	}
	if d.Trigger.Type == "folder.scan" && (d.Trigger.IntervalSeconds < 30 || d.Trigger.IntervalSeconds > 86400) {
		return errors.New("scan interval must be between 30 seconds and 24 hours")
	}
	if d.Trigger.MaxConcurrent == 0 {
		d.Trigger.MaxConcurrent = 1
	}
	if d.Trigger.MaxConcurrent < 1 || d.Trigger.MaxConcurrent > 8 {
		return errors.New("automation concurrency must be between 1 and 8")
	}
	if d.Trigger.StabilitySeconds < 5 || d.Trigger.StabilitySeconds > 3600 {
		return errors.New("file stability window must be between 5 seconds and 1 hour")
	}
	input, err := s.CheckDrive(ctx, d.Trigger.InputDriveID)
	if err != nil {
		return err
	}
	if !input.Enabled || input.Access != "read" && input.Access != "read_write" || !input.Accessible {
		return errors.New("input drive is disabled or inaccessible")
	}
	output, err := s.CheckDrive(ctx, d.Action.OutputDriveID)
	if err != nil {
		return err
	}
	if !output.Enabled || output.Access != "read_write" || !output.Writable {
		return errors.New("output drive must be enabled and writable by Core")
	}
	if d.Action.OutputBehavior != "create" && d.Action.OutputBehavior != "skip_existing" && d.Action.OutputBehavior != "unique" {
		return errors.New("output behavior must be create, skip_existing, or unique; replacement is not supported")
	}
	if d.Action.Mode != "convert" && d.Action.Mode != "compress" || d.Action.ProfileID == "" || !idPattern.MatchString(d.Action.ProfileID) {
		return errors.New("a conversion or compression profile must be selected")
	}
	d.Action.TargetFormat = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d.Action.TargetFormat)), ".")
	if !regexp.MustCompile(`^[a-z0-9]{1,12}$`).MatchString(d.Action.TargetFormat) {
		return errors.New("a valid target extension is required")
	}
	if d.Filter.MinBytes < 0 || d.Filter.MaxBytes < 0 || d.Filter.MaxBytes > 0 && d.Filter.MinBytes > d.Filter.MaxBytes {
		return errors.New("file size filter is invalid")
	}
	for _, ext := range d.Filter.Extensions {
		if !regexp.MustCompile(`^[a-zA-Z0-9]{1,12}$`).MatchString(strings.TrimPrefix(ext, ".")) {
			return errors.New("file extension filter is invalid")
		}
	}
	if d.Filter.NameGlob != "" {
		if _, err = filepath.Match(d.Filter.NameGlob, "sample"); err != nil {
			return fmt.Errorf("filename pattern is invalid: %w", err)
		}
	}
	if d.Condition != nil {
		if err = validateCondition(*d.Condition, 0, new(int)); err != nil {
			return err
		}
	}
	conditionFormats := []string{}
	if d.Condition != nil {
		collectConditionFormats(*d.Condition, &conditionFormats)
	}
	if s.management != nil {
		profiles, profileErr := s.management.ListProfiles(ctx)
		if profileErr != nil {
			return profileErr
		}
		found := false
		for _, installed := range profiles {
			p := installed.Definition
			if p.ID != d.Action.ProfileID {
				continue
			}
			if !installed.State.Enabled {
				return errors.New("selected conversion profile is disabled")
			}
			if d.Action.ProfileVersion != "" && p.Version != d.Action.ProfileVersion {
				return errors.New("selected profile version is no longer installed; edit this automation to review a compatible version")
			}
			if !p.SupportsMode(d.Action.Mode) {
				return errors.New("selected profile is not compatible with this automation mode")
			}
			for _, requirement := range p.Engines {
				if installed.State.ResolvedEngines[requirement.EngineID] == "" {
					return fmt.Errorf("profile engine %q is not currently installed and compatible", requirement.EngineID)
				}
			}
			if d.Action.Mode == "compress" && p.Compression == nil {
				return errors.New("selected profile does not provide compression")
			}
			formats := append(append([]string(nil), d.Filter.Extensions...), conditionFormats...)
			if len(formats) == 0 {
				hasTarget := false
				for _, rule := range p.Rules {
					for _, target := range rule.Targets {
						if strings.TrimPrefix(strings.ToLower(target), ".") == d.Action.TargetFormat && (d.Action.Mode != "compress" || len(rule.CompressionArguments) > 0) {
							hasTarget = true
						}
					}
				}
				if !hasTarget {
					return fmt.Errorf("selected profile has no compatible rule for the .%s output", d.Action.TargetFormat)
				}
			}
			if len(formats) > 0 {
				for _, ext := range formats {
					compatible := false
					for _, rule := range p.Rules {
						for _, source := range rule.Sources {
							for _, target := range rule.Targets {
								if strings.TrimPrefix(strings.ToLower(ext), ".") == strings.TrimPrefix(strings.ToLower(source), ".") && strings.TrimPrefix(strings.ToLower(d.Action.TargetFormat), ".") == strings.TrimPrefix(strings.ToLower(target), ".") {
									compatible = true
								}
							}
						}
					}
					if !compatible {
						return fmt.Errorf("selected profile has no compatible rule for .%s to .%s", ext, d.Action.TargetFormat)
					}
				}
			}
			found = true
		}
		if !found {
			return errors.New("selected profile is not installed")
		}
	}
	return nil
}

func validateCondition(c Condition, depth int, count *int) error {
	*count++
	if depth > 8 || *count > 32 {
		return errors.New("condition tree exceeds the supported depth or block count")
	}
	switch c.Operator {
	case "and", "or":
		if len(c.Children) < 2 {
			return fmt.Errorf("%s conditions require at least two child conditions", strings.ToUpper(c.Operator))
		}
		if c.Value != "" || c.Bytes != 0 {
			return errors.New("group conditions cannot include leaf values")
		}
	case "not":
		if len(c.Children) != 1 || c.Value != "" || c.Bytes != 0 {
			return errors.New("NOT conditions require exactly one child")
		}
	case "extension":
		if len(c.Children) != 0 || !regexp.MustCompile(`^\.?[a-zA-Z0-9]{1,12}$`).MatchString(c.Value) || c.Bytes != 0 {
			return errors.New("extension condition requires a file extension")
		}
	case "name_glob":
		if len(c.Children) != 0 || c.Value == "" || c.Bytes != 0 {
			return errors.New("filename condition requires a glob pattern")
		}
		if _, err := filepath.Match(c.Value, "sample"); err != nil {
			return fmt.Errorf("filename condition pattern is invalid: %w", err)
		}
	case "min_size", "max_size":
		if len(c.Children) != 0 || c.Value != "" || c.Bytes < 0 {
			return errors.New("size condition requires a non-negative byte limit")
		}
	default:
		return fmt.Errorf("unsupported condition block %q", c.Operator)
	}
	for _, child := range c.Children {
		if err := validateCondition(child, depth+1, count); err != nil {
			return err
		}
	}
	return nil
}

func collectConditionFormats(c Condition, formats *[]string) {
	if c.Operator == "extension" {
		*formats = append(*formats, strings.TrimPrefix(c.Value, "."))
	}
	for _, child := range c.Children {
		collectConditionFormats(child, formats)
	}
}

func (s *Service) TestPath(path string, write bool) error {
	resolved, err := s.ResolveDrivePath(path)
	if err != nil {
		return err
	}
	if write {
		var b [8]byte
		if _, err = rand.Read(b[:]); err != nil {
			return err
		}
		name := filepath.Join(resolved, ".101conv-probe-"+hex.EncodeToString(b[:]))
		f, e := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		if e = f.Close(); e != nil {
			_ = os.Remove(name)
			return e
		}
		return os.Remove(name)
	}
	f, err := os.OpenRoot(resolved)
	if err != nil {
		return err
	}
	return f.Close()
}

func (s *Service) DeleteDriveIfUnused(ctx context.Context, id string) error {
	return s.RemoveDrive(ctx, id)
}
func randomID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func blockVersions() []byte {
	b, _ := json.Marshal(map[string]string{"trigger.folder-scan": "1.0.0", "trigger.manual": "1.0.0", "condition.file-filter": "1.0.0", "condition.and": "1.0.0", "condition.or": "1.0.0", "condition.not": "1.0.0", "action.convert": "1.0.0", "action.compress": "1.0.0", "action.skip": "1.0.0", "action.stop": "1.0.0", "action.file-output": "1.0.0"})
	return b
}

var Blocks = []Block{
	{ID: "trigger.folder-scan", Version: "1.0.0", Kind: "trigger", Name: "Watch a folder", Description: "Periodically check a mounted folder for stable new files.", ConfigSchema: map[string]any{"inputDriveId": "drive-id", "intervalSeconds": "integer", "stabilitySeconds": "integer", "maxConcurrent": "integer", "processExistingOnNextScan": "boolean"}, RequiredPermission: "automations.execute"},
	{ID: "trigger.manual", Version: "1.0.0", Kind: "trigger", Name: "Run manually", Description: "Scan the configured input folder when requested.", ConfigSchema: map[string]any{"inputDriveId": "drive-id"}, RequiredPermission: "automations.execute"},
	{ID: "condition.file-filter", Version: "1.0.0", Kind: "condition", Name: "Match files", Description: "Filter by extension, filename pattern, and size.", ConfigSchema: map[string]any{"extensions": "string[]", "nameGlob": "glob", "minBytes": "integer", "maxBytes": "integer"}, RequiredPermission: "automations.execute"},
	{ID: "condition.and", Version: "1.0.0", Kind: "condition", Name: "All conditions", Description: "Match only when every child condition is true.", ConfigSchema: map[string]any{"children": "condition[]"}, RequiredPermission: "automations.execute"},
	{ID: "condition.or", Version: "1.0.0", Kind: "condition", Name: "Any condition", Description: "Match when at least one child condition is true.", ConfigSchema: map[string]any{"children": "condition[]"}, RequiredPermission: "automations.execute"},
	{ID: "condition.not", Version: "1.0.0", Kind: "condition", Name: "Exclude condition", Description: "Invert one child condition.", ConfigSchema: map[string]any{"child": "condition"}, RequiredPermission: "automations.execute"},
	{ID: "action.convert", Version: "1.0.0", Kind: "action", Name: "Convert file", Description: "Submit a conversion to an installed compatible profile.", ConfigSchema: map[string]any{"profileId": "profile-id", "targetFormat": "extension"}, RequiredPermission: "conversion.use"},
	{ID: "action.compress", Version: "1.0.0", Kind: "action", Name: "Compress file", Description: "Submit compression to an installed compatible profile.", ConfigSchema: map[string]any{"profileId": "profile-id", "targetFormat": "extension"}, RequiredPermission: "compression.use"},
	{ID: "action.skip", Version: "1.0.0", Kind: "action", Name: "Skip file", Description: "Record a matched file as intentionally skipped.", ConfigSchema: map[string]any{}, RequiredPermission: "automations.execute"},
	{ID: "action.stop", Version: "1.0.0", Kind: "action", Name: "Stop workflow", Description: "Stop the current workflow run without changing files.", ConfigSchema: map[string]any{}, RequiredPermission: "automations.execute"},
	{ID: "action.file-output", Version: "1.0.0", Kind: "action", Name: "Write result", Description: "Publish a validated result into a registered writable destination.", ConfigSchema: map[string]any{"outputDriveId": "drive-id", "behavior": "create|skip_existing|unique"}, RequiredPermission: "drives.manage"},
}

func BuiltinBlueprints() []Blueprint {
	return []Blueprint{
		{ID: "automatic-folder-conversion", Version: "1.0.0", Name: "Automatic Folder Conversion", Description: "Convert stable new files from one mounted folder into another.", Mode: "convert", Definition: Definition{Trigger: Trigger{Type: "folder.scan", IntervalSeconds: 60, StabilitySeconds: 30}, Action: FileAction{Mode: "convert", OutputBehavior: "unique"}}},
		{ID: "automatic-compression", Version: "1.0.0", Name: "Automatic Compression", Description: "Compress files above a size threshold and save the result separately.", Mode: "compress", Definition: Definition{Trigger: Trigger{Type: "folder.scan", IntervalSeconds: 60, StabilitySeconds: 30}, Filter: FileFilter{MinBytes: 10 << 20}, Action: FileAction{Mode: "compress", OutputBehavior: "unique"}}},
	}
}

// ExtensionCompatible implements the C.1 declarative contract check. No package
// code is loaded; an extension must declare an exact API major and range.
func ExtensionCompatible(manifest ExtensionManifest, apiVersion string) bool {
	if manifest.ID == "" || manifest.Publisher == "" || manifest.Version == "" {
		return false
	}
	if manifest.ExtensionAPIRange.Minimum != "" || manifest.ExtensionAPIRange.MaximumExclusive != "" {
		if manifest.ExtensionAPIRange.Minimum != "" {
			cmp, err := version.Compare(apiVersion, manifest.ExtensionAPIRange.Minimum)
			if err != nil || cmp < 0 {
				return false
			}
		}
		if manifest.ExtensionAPIRange.MaximumExclusive != "" {
			cmp, err := version.Compare(apiVersion, manifest.ExtensionAPIRange.MaximumExclusive)
			if err != nil || cmp >= 0 {
				return false
			}
		}
		return true
	}
	// Existing manifests that declared one exact API version remain readable.
	return manifest.ExtensionAPI == apiVersion
}

// SelectCompatibleExtension chooses the newest compatible release while
// retaining older compatible releases as candidates. Callers can therefore
// resolve by a stable extension API contract instead of hiding a usable older
// package behind a newer, incompatible manifest.
func SelectCompatibleExtension(manifests []ExtensionManifest, apiVersion string) (ExtensionManifest, bool) {
	candidates := make([]ExtensionManifest, 0, len(manifests))
	for _, manifest := range manifests {
		if ExtensionCompatible(manifest, apiVersion) {
			candidates = append(candidates, manifest)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		cmp, err := version.Compare(candidates[i].Version, candidates[j].Version)
		if err != nil || cmp == 0 {
			return candidates[i].ID < candidates[j].ID
		}
		return cmp > 0
	})
	if len(candidates) == 0 {
		return ExtensionManifest{}, false
	}
	return candidates[0], true
}
