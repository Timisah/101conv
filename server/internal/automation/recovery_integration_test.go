package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/jobs"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
)

type automationFixture struct {
	t                          *testing.T
	ctx                        context.Context
	db                         *sql.DB
	svc                        *Service
	manager                    *management.Service
	jobs                       *jobs.Service
	root, input, output, owner string
	in, out                    Drive
	a                          Automation
}

func newAutomationFixture(t *testing.T, existing bool) *automationFixture {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL for PostgreSQL automation recovery tests")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("automation_recovery_%d", time.Now().UnixNano())
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); admin.Close() })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(10)
	t.Cleanup(func() { db.Close() })
	f := &automationFixture{t: t, ctx: ctx, db: db, root: t.TempDir()}
	f.input = filepath.Join(f.root, "input")
	f.output = filepath.Join(f.root, "output")
	imports := filepath.Join(f.root, "imports")
	for _, p := range []string{f.input, f.output, imports} {
		if err = os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(imports, "tool"), []byte("converter fixture"), 0700); err != nil {
		t.Fatal(err)
	}
	f.manager, err = management.OpenWithPolicy(filepath.Join(f.root, "state"), []string{imports})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.manager.Close() })
	_, err = f.manager.InstallEngine(ctx, engine.Definition{SchemaVersion: 1, ID: "test-engine", Version: "1.0.0", Name: "Test engine", Platform: runtime.GOOS + "-" + runtime.GOARCH, Executables: map[string]string{"tool": "tool"}}, imports)
	if err != nil {
		t.Fatal(err)
	}
	f.installProfile("1.0.0")
	f.jobs, err = jobs.NewService(db, f.manager, filepath.Join(f.root, "uploads"), filepath.Join(f.root, "results"))
	if err != nil {
		t.Fatal(err)
	}
	if err = f.jobs.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	accounts, err := auth.NewService(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = accounts.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	principal, err := accounts.CreateInitialAdmin(ctx, "automationadmin", "automation-test-password")
	if err != nil {
		t.Fatal(err)
	}
	f.owner = principal.ID
	f.svc, err = NewService(db, []string{f.root}, f.jobs, f.manager, accounts)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if drives, e := f.svc.ListDrives(ctx); e != nil || drives == nil || len(drives) != 0 {
		t.Fatalf("empty drives must be an array: %v %v", drives, e)
	}
	if automations, e := f.svc.ListAutomations(ctx, f.owner); e != nil || automations == nil || len(automations) != 0 {
		t.Fatalf("empty automations must be an array: %v %v", automations, e)
	}
	f.in, err = f.svc.CreateDrive(ctx, "Input", f.input, "read")
	if err != nil {
		t.Fatal(err)
	}
	f.out, err = f.svc.CreateDrive(ctx, "Output", f.output, "read_write")
	if err != nil {
		t.Fatal(err)
	}
	def := Definition{Trigger: Trigger{Type: "folder.scan", InputDriveID: f.in.ID, IntervalSeconds: 30, StabilitySeconds: 5, MaxConcurrent: 2, ProcessExistingOnNext: existing}, Filter: FileFilter{Extensions: []string{"ppm"}}, Action: FileAction{Mode: "convert", ProfileID: "test-profile", TargetFormat: "pgm", OutputDriveID: f.out.ID, OutputBehavior: "unique"}}
	f.a, err = f.svc.CreateAutomation(ctx, f.owner, "Test automation", def)
	if err != nil {
		t.Fatal(err)
	}
	if history, e := f.svc.History(ctx, f.a.ID, f.owner, 50); e != nil || history == nil || len(history) != 0 {
		t.Fatalf("empty history must be an array: %v %v", history, e)
	}
	return f
}
func (f *automationFixture) installProfile(version string) {
	f.t.Helper()
	install := f.manager.InstallProfile
	if version != "1.0.0" {
		install = f.manager.UpdateProfile
	}
	_, err := install(f.ctx, profile.Definition{SchemaVersion: 1, Type: profile.TypeConversion, ID: "test-profile", Version: version, Name: "Test profile", Engines: []profile.EngineRequirement{{EngineID: "test-engine", Versions: engine.VersionRange{Minimum: "1.0.0"}}}, Rules: []profile.ConversionRule{{EngineID: "test-engine", Executable: "tool", Sources: []string{"ppm"}, Targets: []string{"pgm"}, Arguments: []string{"{source}", "{target}"}}}}, "")
	if err != nil {
		f.t.Fatal(err)
	}
}
func (f *automationFixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.ctx, q, args...); err != nil {
		f.t.Fatal(err)
	}
}
func (f *automationFixture) count(q string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.db.QueryRowContext(f.ctx, q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}
func (f *automationFixture) write(name string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.input, name), []byte("P3\n2 1\n255\n255 0 0 0 255 0\n"), 0600); err != nil {
		f.t.Fatal(err)
	}
}
func (f *automationFixture) scan() {
	f.t.Helper()
	if err := f.svc.scanAutomation(f.ctx, f.a, true); err != nil {
		f.t.Fatal(err)
	}
}
func (f *automationFixture) stable() {
	f.exec(`UPDATE conv_automation_runs SET first_seen=now()-interval '10 seconds' WHERE state='observing'`)
}
func (f *automationFixture) restart() {
	f.t.Helper()
	s, err := NewService(f.db, []string{f.root}, f.jobs, f.manager, f.svc.accounts)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = s.Migrate(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	f.svc = s
}
func (f *automationFixture) queued() Run {
	f.t.Helper()
	runs, err := f.svc.History(f.ctx, f.a.ID, f.owner, 200)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, r := range runs {
		if r.State == "queued" {
			return r
		}
	}
	var revision int
	var definition []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT a.current_revision,r.definition FROM conv_automations a JOIN conv_automation_revisions r ON r.automation_id=a.id AND r.revision=a.current_revision WHERE a.id=$1`, f.a.ID).Scan(&revision, &definition); err != nil {
		f.t.Fatalf("no queued run: %+v; automation query: %v", runs, err)
	}
	var generation, requestRevision int64
	var processExisting, baselineProcessExisting, inventoryReady bool
	if err := f.db.QueryRowContext(f.ctx, `SELECT generation,request_revision,process_existing,baseline_process_existing,inventory_ready FROM conv_automation_scans WHERE automation_id=$1`, f.a.ID).Scan(&generation, &requestRevision, &processExisting, &baselineProcessExisting, &inventoryReady); err != nil {
		f.t.Fatalf("no queued run: %+v; revision=%d definition=%s; scan query: %v", runs, revision, definition, err)
	}
	f.t.Fatalf("no queued run: %+v; revision=%d definition=%s; scan={generation:%d requestRevision:%d processExisting:%t baselineProcessExisting:%t inventoryReady:%t}", runs, revision, definition, generation, requestRevision, processExisting, baselineProcessExisting, inventoryReady)
	return Run{}
}
func (f *automationFixture) complete(r Run) {
	f.t.Helper()
	body := []byte("P2\n2 1\n255\n76 150\n")
	key := r.JobID + "-1.bin"
	if err := os.WriteFile(filepath.Join(f.jobs.ResultRoot(), key), body, 0600); err != nil {
		f.t.Fatal(err)
	}
	f.exec(`UPDATE conv_jobs SET state='succeeded',result_key=$2,result_size=$3 WHERE id=$1`, r.JobID, key, len(body))
}
func (f *automationFixture) publish() {
	f.t.Helper()
	if err := f.svc.publishCompleted(f.ctx); err != nil {
		f.t.Fatal(err)
	}
}
func (f *automationFixture) runState(id string) string {
	f.t.Helper()
	var state string
	if err := f.db.QueryRowContext(f.ctx, `SELECT state FROM conv_automation_runs WHERE id=$1`, id).Scan(&state); err != nil {
		f.t.Fatal(err)
	}
	return state
}

func TestAutomationInitialBaselineResumesPartialDirectory(t *testing.T) {
	f := newAutomationFixture(t, false)
	for i := 0; i < 300; i++ {
		f.write(fmt.Sprintf("%03d.ppm", i))
		if i == 299 {
			future := time.Now().Add(24 * time.Hour)
			if err := os.Chtimes(filepath.Join(f.input, "299.ppm"), future, future); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Interrupt after several committed entries, before the page cursor/last_scan.
	f.exec(`CREATE FUNCTION interrupt_scan() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.source_name='010.ppm' THEN RAISE EXCEPTION 'simulated interruption'; END IF; RETURN NEW; END $$; CREATE TRIGGER interrupt_scan BEFORE INSERT ON conv_automation_runs FOR EACH ROW EXECUTE FUNCTION interrupt_scan()`)
	if err := f.svc.scanAutomation(f.ctx, f.a, true); err == nil {
		t.Fatal("expected interruption")
	}
	if n := f.count(`SELECT count(*) FROM conv_automation_runs WHERE state='skipped_existing'`); n != 10 {
		t.Fatalf("partial baseline=%d", n)
	}
	if n := f.count(`SELECT count(*) FROM conv_automation_scans WHERE baseline_complete`); n != 0 {
		t.Fatal("partial initial scan marked complete")
	}
	if n := f.count(`SELECT count(*) FROM conv_automations WHERE last_scan IS NOT NULL`); n != 0 {
		t.Fatal("interrupted scan advanced last_scan")
	}
	f.exec(`DROP TRIGGER interrupt_scan ON conv_automation_runs`)
	f.restart()
	f.scan()
	if n := f.count(`SELECT count(*) FROM conv_automation_scans WHERE baseline_complete`); n != 0 {
		t.Fatal("truncated baseline marked complete")
	}
	f.restart()
	f.scan()
	f.stable()
	f.scan()
	f.scan()
	if n := f.count(`SELECT count(*) FROM conv_automation_runs WHERE state='skipped_existing'`); n != 300 {
		t.Fatalf("baseline rows=%d", n)
	}
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 0 {
		t.Fatalf("preexisting jobs=%d", n)
	}
	if n := f.count(`SELECT count(*) FROM conv_automation_scans WHERE baseline_complete`); n != 1 {
		t.Fatal("baseline did not finish")
	}
	// Cancellation must also leave the established baseline intact.
	ctx, cancel := context.WithCancel(f.ctx)
	cancel()
	_ = f.svc.scanAutomation(ctx, f.a, true)
	f.restart()
	f.scan()
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 0 {
		t.Fatal("restart submitted baseline files")
	}
}
func TestAutomationExplicitExistingRequestAndInputChange(t *testing.T) {
	f := newAutomationFixture(t, false)
	f.write("existing.ppm")
	f.scan()
	f.stable()
	f.scan()
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 0 {
		t.Fatal("default baseline submitted")
	}
	def := f.a.Definition
	def.Trigger.ProcessExistingOnNext = true
	var err error
	f.a, err = f.svc.UpdateAutomation(f.ctx, f.a.ID, f.owner, f.a.Name, def)
	if err != nil {
		t.Fatal(err)
	}
	f.scan()
	f.stable()
	f.restart()
	f.scan()
	r := f.queued()
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 1 {
		t.Fatalf("explicit request jobs=%d", n)
	}
	f.complete(r)
	f.publish()
	f.scan()
	f.scan()
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 1 {
		t.Fatal("unchanged file submitted again")
	}
	// A drive registration changing its path cannot reuse a consumed opt-in.
	other := filepath.Join(f.root, "other")
	if err = os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(other, "old.ppm"), []byte("old input"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.UpdateDrive(f.ctx, f.in.ID, "Input", other, "read", true)
	if err != nil {
		t.Fatal(err)
	}
	f.scan()
	f.stable()
	f.scan()
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 1 {
		t.Fatal("changed input silently submitted existing files")
	}
	// An edited automation with a different input also establishes a baseline.
	third := filepath.Join(f.root, "third")
	if err = os.Mkdir(third, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(third, "third.ppm"), []byte("third input"), 0600); err != nil {
		t.Fatal(err)
	}
	drive, err := f.svc.CreateDrive(f.ctx, "Third", third, "read")
	if err != nil {
		t.Fatal(err)
	}
	def = f.a.Definition
	def.Trigger.InputDriveID = drive.ID
	def.Trigger.ProcessExistingOnNext = false
	f.a, err = f.svc.UpdateAutomation(f.ctx, f.a.ID, f.owner, f.a.Name, def)
	if err != nil {
		t.Fatal(err)
	}
	f.scan()
	f.stable()
	f.scan()
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 1 {
		t.Fatal("edited input submitted existing files")
	}
}
func TestAutomationProfilePinningAndLegacyPolicy(t *testing.T) {
	f := newAutomationFixture(t, true)
	if f.a.Definition.Action.ProfileVersion != "1.0.0" {
		t.Fatal("omitted version was not pinned")
	}
	f.write("pinned.ppm")
	f.scan()
	f.stable()
	f.scan()
	r := f.queued()
	f.installProfile("2.0.0")
	f.restart()
	var version string
	if err := f.db.QueryRow(`SELECT profile_version FROM conv_jobs WHERE id=$1`, r.JobID).Scan(&version); err != nil || version != "1.0.0" {
		t.Fatalf("job version=%q %v", version, err)
	}
	if err := f.svc.scanAutomation(f.ctx, f.a, true); err == nil {
		t.Fatal("silently drifted profile version")
	}
	def := f.a.Definition
	def.Action.ProfileVersion = ""
	def.Trigger.ProcessExistingOnNext = false
	updated, err := f.svc.UpdateAutomation(f.ctx, f.a.ID, f.owner, f.a.Name, def)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Definition.Action.ProfileVersion != "2.0.0" {
		t.Fatal("explicit update did not pin current version")
	}
	var raw []byte
	if err = f.db.QueryRow(`SELECT definition FROM conv_automation_revisions WHERE automation_id=$1 AND revision=1`, f.a.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var old Definition
	json.Unmarshal(raw, &old)
	if old.Action.ProfileVersion != "1.0.0" {
		t.Fatal("historical revision mutated")
	}
	// Simulate an upgraded legacy record, preserving its original JSON/history.
	f.exec(`UPDATE conv_automation_revisions SET definition=definition #- '{action,profileVersion}' WHERE automation_id=$1 AND revision=$2`, updated.ID, updated.Revision)
	f.restart()
	legacy, err := f.svc.GetAutomation(f.ctx, updated.ID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Status != "blocked" || !strings.Contains(legacy.Diagnostic, "not pinned") || legacy.Definition.Action.ProfileVersion != "" {
		t.Fatalf("legacy policy: %+v", legacy)
	}
	if _, err = f.svc.SetEnabled(f.ctx, legacy.ID, f.owner, true); err == nil {
		t.Fatal("legacy version drift allowed")
	}
	if f.count(`SELECT count(*) FROM conv_automation_runs`) == 0 {
		t.Fatal("history lost")
	}
}

func TestAutomationOutputAuthorizationInFlight(t *testing.T) {
	for _, change := range []string{"read_only", "disabled", "path", "removed", "symlink", "unwritable"} {
		t.Run(change, func(t *testing.T) {
			f := newAutomationFixture(t, true)
			f.write("result.ppm")
			f.scan()
			f.stable()
			f.scan()
			r := f.queued()
			other := filepath.Join(f.root, "other-output")
			if err := os.Mkdir(other, 0700); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "read_only":
				_, err := f.svc.UpdateDrive(f.ctx, f.out.ID, "Output", f.output, "read", true)
				if err != nil {
					t.Fatal(err)
				}
			case "disabled":
				_, err := f.svc.UpdateDrive(f.ctx, f.out.ID, "Output", f.output, "read_write", false)
				if err != nil {
					t.Fatal(err)
				}
			case "path":
				_, err := f.svc.UpdateDrive(f.ctx, f.out.ID, "Output", other, "read_write", true)
				if err != nil {
					t.Fatal(err)
				}
			case "removed":
				f.exec(`DELETE FROM conv_mounted_drives WHERE id=$1`, f.out.ID)
			case "unwritable":
				if os.Geteuid() == 0 {
					t.Skip("mode-bit revocation needs an unprivileged test process")
				}
				if err := os.Chmod(f.output, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(f.output, 0700) })
			case "symlink":
				if err := os.Rename(f.output, f.output+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, f.output); err != nil {
					t.Fatal(err)
				}
			}
			f.complete(r)
			f.restart()
			f.publish()
			if state := f.runState(r.ID); state != "blocked" {
				t.Fatalf("state=%s", state)
			}
			if _, err := os.Lstat(filepath.Join(other, "result.pgm")); !os.IsNotExist(err) {
				t.Fatal("published to changed destination")
			}
			if _, err := os.Lstat(filepath.Join(f.output, "result.pgm")); !os.IsNotExist(err) {
				t.Fatal("published after revocation")
			}
		})
	}
}
func TestAutomationPublicationRecoveryAllPolicies(t *testing.T) {
	for _, behavior := range []string{"create", "skip_existing", "unique"} {
		t.Run(behavior, func(t *testing.T) {
			f := newAutomationFixture(t, true)
			def := f.a.Definition
			def.Action.OutputBehavior = behavior
			var err error
			f.a, err = f.svc.UpdateAutomation(f.ctx, f.a.ID, f.owner, f.a.Name, def)
			if err != nil {
				t.Fatal(err)
			}
			f.write("recover.ppm")
			f.scan()
			f.stable()
			f.scan()
			r := f.queued()
			f.complete(r)
			// Crash at the actual DB acknowledgement after durable output publication.
			f.exec(`CREATE FUNCTION interrupt_publication() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='succeeded' THEN RAISE EXCEPTION 'simulated lost completion'; END IF; RETURN NEW; END $$; CREATE TRIGGER interrupt_publication BEFORE UPDATE ON conv_automation_runs FOR EACH ROW EXECUTE FUNCTION interrupt_publication()`)
			if err = f.svc.publishCompleted(f.ctx); err == nil {
				t.Fatal("expected lost acknowledgement")
			}
			if state := f.runState(r.ID); state != "queued" {
				t.Fatalf("premature terminal state %s", state)
			}
			expected := "P2\n2 1\n255\n76 150\n"
			if body, err := os.ReadFile(filepath.Join(f.output, "recover.pgm")); err != nil || string(body) != expected {
				t.Fatalf("output=%q %v", body, err)
			}
			f.exec(`DROP TRIGGER interrupt_publication ON conv_automation_runs`)
			// A committed output is recovered even if access was revoked afterwards.
			f.exec(`UPDATE conv_mounted_drives SET access_mode='read' WHERE id=$1`, f.out.ID)
			f.restart()
			f.publish()
			f.publish()
			if f.runState(r.ID) != "succeeded" {
				t.Fatal("published output did not recover as succeeded")
			}
			entries, err := os.ReadDir(f.output)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("duplicate output or leaked receipt: %v", entries)
			}
			if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 1 {
				t.Fatalf("duplicate jobs=%d", n)
			}
		})
	}
}
func TestAutomationSubmissionRecoveryKeepsUploadAndJob(t *testing.T) {
	f := newAutomationFixture(t, true)
	f.write("submit.ppm")
	f.scan()
	f.stable()
	// Simulate SubmitFor committing while the run's job_id acknowledgement fails.
	f.exec(`CREATE FUNCTION interrupt_submission() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.state='queued' THEN RAISE EXCEPTION 'lost job acknowledgement'; END IF; RETURN NEW; END $$; CREATE TRIGGER interrupt_submission BEFORE UPDATE ON conv_automation_runs FOR EACH ROW EXECUTE FUNCTION interrupt_submission()`)
	f.scan()
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 1 {
		t.Fatalf("committed jobs=%d", n)
	}
	if n := f.count(`SELECT count(*) FROM conv_uploads`); n != 1 {
		t.Fatalf("uploads=%d", n)
	}
	f.exec(`DROP TRIGGER interrupt_submission ON conv_automation_runs; UPDATE conv_automation_runs SET next_attempt=now()`)
	// The installed profile changes before recovery: recover the existing job,
	// without re-resolving or rebuilding the execution spec.
	f.installProfile("2.0.0")
	f.restart()
	if err := f.svc.recoverSubmissions(f.ctx); err != nil {
		t.Fatal(err)
	}
	r := f.queued()
	if r.Revision != 1 {
		t.Fatal("run revision drift")
	}
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 1 {
		t.Fatal("duplicate job after crash")
	}
	if n := f.count(`SELECT count(*) FROM conv_uploads`); n != 1 {
		t.Fatal("duplicate upload after crash")
	}
	f.complete(r)
	f.publish()
	if f.runState(r.ID) != "succeeded" {
		t.Fatal("recovered submission did not publish")
	}
}

func TestAutomationPublicationNeverAdoptsOrOverwritesExistingFile(t *testing.T) {
	for _, behavior := range []string{"create", "skip_existing", "unique"} {
		t.Run(behavior, func(t *testing.T) {
			f := newAutomationFixture(t, true)
			def := f.a.Definition
			def.Action.OutputBehavior = behavior
			var err error
			f.a, err = f.svc.UpdateAutomation(f.ctx, f.a.ID, f.owner, f.a.Name, def)
			if err != nil {
				t.Fatal(err)
			}
			f.write("same.ppm")
			f.scan()
			f.stable()
			f.scan()
			r := f.queued()
			f.complete(r)
			body := []byte("P2\n2 1\n255\n76 150\n")
			if err = os.WriteFile(filepath.Join(f.output, "same.pgm"), body, 0600); err != nil {
				t.Fatal(err)
			}
			f.publish()
			f.restart()
			f.publish()
			expected := map[string]string{"create": "blocked", "skip_existing": "skipped_existing_output", "unique": "succeeded"}[behavior]
			if state := f.runState(r.ID); state != expected {
				t.Fatalf("state=%s want %s", state, expected)
			}
			if got, err := os.ReadFile(filepath.Join(f.output, "same.pgm")); err != nil || string(got) != string(body) {
				t.Fatal("existing contents changed")
			}
			if behavior == "unique" {
				if got, err := os.ReadFile(filepath.Join(f.output, "same (2).pgm")); err != nil || string(got) != string(body) {
					t.Fatalf("unique result=%q %v", got, err)
				}
			}
		})
	}
}
func TestAutomationPauseResumeAndLegacyBaseline(t *testing.T) {
	f := newAutomationFixture(t, false)
	f.write("baseline.ppm")
	f.scan()
	if _, err := f.svc.SetEnabled(f.ctx, f.a.ID, f.owner, false); err != nil {
		t.Fatal(err)
	}
	f.write("during-pause.ppm")
	if _, err := f.svc.SetEnabled(f.ctx, f.a.ID, f.owner, true); err != nil {
		t.Fatal(err)
	}
	f.scan()
	f.stable()
	f.restart()
	f.scan()
	r := f.queued()
	if r.FileName != "during-pause.ppm" {
		t.Fatalf("resume submitted %s", r.FileName)
	}
	// Reproduce an upgraded C.1 record whose process-existing request was already
	// consumed, and whose last_scan may have preceded an incomplete first scan.
	f.exec(`DELETE FROM conv_automation_scans; DELETE FROM conv_automation_baseline; UPDATE conv_automation_revisions SET definition=jsonb_set(definition,'{trigger,processExistingOnNextScan}','true')`)
	f.write("legacy-preexisting.ppm")
	f.restart()
	f.scan()
	f.stable()
	f.scan()
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 1 {
		t.Fatal("legacy upgrade replayed existing contents")
	}
	if n := f.count(`SELECT count(*) FROM conv_automation_runs WHERE source_name='legacy-preexisting.ppm' AND state='skipped_existing'`); n != 1 {
		t.Fatal("legacy baseline was not recorded")
	}
}

func TestAutomationUploadRecoveryIsBoundedAndKeepsPinnedRequest(t *testing.T) {
	f := newAutomationFixture(t, true)
	f.write("upload.ppm")
	f.scan()
	f.stable()
	// The upload is committed and saved on the run before job creation fails.
	f.exec(`CREATE FUNCTION interrupt_job() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'job service unavailable'; END $$; CREATE TRIGGER interrupt_job BEFORE INSERT ON conv_jobs FOR EACH ROW EXECUTE FUNCTION interrupt_job()`)
	f.scan()
	if n := f.count(`SELECT count(*) FROM conv_uploads`); n != 1 {
		t.Fatalf("uploads=%d", n)
	}
	if n := f.count(`SELECT count(*) FROM conv_automation_runs WHERE state='submitting' AND upload_id<>''`); n != 1 {
		t.Fatal("upload not durable before submission")
	}
	f.exec(`DROP TRIGGER interrupt_job ON conv_jobs`)
	f.installProfile("2.0.0")
	f.restart()
	for i := 0; i < 4; i++ {
		f.exec(`UPDATE conv_automation_runs SET next_attempt=now()`)
		if err := f.svc.recoverSubmissions(f.ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 0 {
		t.Fatal("recovery silently used a newer profile")
	}
	if n := f.count(`SELECT count(*) FROM conv_uploads`); n != 1 {
		t.Fatal("retry reuploaded the source")
	}
	if n := f.count(`SELECT count(*) FROM conv_automation_runs WHERE state='blocked' AND attempt_count=3 AND execution_snapshot->'definition'->'action'->>'profileVersion'='1.0.0'`); n != 1 {
		t.Fatal("retry was not bounded or changed pinned request")
	}
}

func TestAutomationInputRoundTripEditsInvalidateBeforeNextScan(t *testing.T) {
	f := newAutomationFixture(t, false)
	f.scan()
	f.write("arrived-before-path-edit.ppm")
	other := filepath.Join(f.root, "other")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{other, f.input} {
		if _, err := f.svc.UpdateDrive(f.ctx, f.in.ID, "Input", path, "read", true); err != nil {
			t.Fatal(err)
		}
	}
	f.restart()
	f.scan()
	f.stable()
	f.scan()
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 0 {
		t.Fatal("path round trip reused an old baseline")
	}
	drive, err := f.svc.CreateDrive(f.ctx, "Other input", other, "read")
	if err != nil {
		t.Fatal(err)
	}
	f.write("arrived-before-input-edit.ppm")
	for _, inputID := range []string{drive.ID, f.in.ID} {
		def := f.a.Definition
		def.Trigger.InputDriveID = inputID
		f.a, err = f.svc.UpdateAutomation(f.ctx, f.a.ID, f.owner, f.a.Name, def)
		if err != nil {
			t.Fatal(err)
		}
	}
	f.restart()
	f.scan()
	f.stable()
	f.scan()
	if n := f.count(`SELECT count(*) FROM conv_jobs`); n != 0 {
		t.Fatal("input round trip reused an old baseline")
	}
	if n := f.count(`SELECT count(*) FROM conv_automation_runs WHERE state='skipped_existing' AND source_name='arrived-before-input-edit.ppm'`); n != 1 {
		t.Fatal("new baseline not recorded")
	}
}

func TestAutomationLegacyPublicationDoesNotDuplicateAmbiguousOutput(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			f := newAutomationFixture(t, true)
			f.write("legacy.ppm")
			f.scan()
			f.stable()
			f.scan()
			r := f.queued()
			f.complete(r)
			f.exec(`UPDATE conv_automation_runs SET scan_generation=0 WHERE id=$1`, r.ID)
			if existing {
				if err := os.WriteFile(filepath.Join(f.output, "legacy (2).pgm"), []byte("possibly an old publication"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			f.restart()
			f.publish()
			f.publish()
			want := "succeeded"
			if existing {
				want = "blocked"
			}
			if state := f.runState(r.ID); state != want {
				t.Fatalf("legacy state=%s want %s", state, want)
			}
			entries, err := os.ReadDir(f.output)
			if err != nil || len(entries) != 1 {
				t.Fatalf("legacy outputs: %v %v", entries, err)
			}
		})
	}
}

func TestAutomationDeletionSerializesWithScan(t *testing.T) {
	f := newAutomationFixture(t, false)
	conn, err := f.db.Conn(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.ExecContext(f.ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, "automation:"+f.a.ID); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(f.ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "automation:"+f.a.ID)
	ctx, cancel := context.WithTimeout(f.ctx, 100*time.Millisecond)
	defer cancel()
	if err = f.svc.DeleteAutomation(ctx, f.a.ID, f.owner); err == nil {
		t.Fatal("deletion raced an active scan")
	}
	if _, err = f.svc.GetAutomation(f.ctx, f.a.ID, f.owner); err != nil {
		t.Fatal("scan lost its automation")
	}
	if _, err = conn.ExecContext(f.ctx, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, "automation:"+f.a.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.DeleteAutomation(f.ctx, f.a.ID, f.owner); err != nil {
		t.Fatal(err)
	}
}
