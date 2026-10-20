package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/timisah/101conv/server/internal/worker"
)

type WorkerConfig struct {
	ID               string
	Lease            time.Duration
	PollInterval     time.Duration
	Concurrency      int
	BatchConcurrency int
}

type Worker struct {
	db     *sql.DB
	runner worker.Runner
	config WorkerConfig
	active atomic.Int32
}

type claimedJob struct {
	ID          string
	UploadID    string
	InputSHA256 string
	InputSize   int64
	Spec        ExecutionSpec
	Attempt     int
	MaxAttempts int
}

func NewWorker(db *sql.DB, runner worker.Runner, config WorkerConfig) (*Worker, error) {
	if db == nil || runner == nil {
		return nil, fmt.Errorf("worker requires PostgreSQL and a process runner")
	}
	if config.ID == "" {
		id, err := randomID()
		if err != nil {
			return nil, err
		}
		config.ID = id
	}
	if config.Lease <= 0 {
		config.Lease = 45 * time.Second
	} else if config.Lease < 3*time.Second {
		return nil, fmt.Errorf("worker lease must be at least 3 seconds")
	}
	if config.PollInterval <= 0 {
		config.PollInterval = time.Second
	}
	if config.Concurrency <= 0 {
		config.Concurrency = 1
	}
	if config.Concurrency > 8 {
		return nil, fmt.Errorf("worker concurrency cannot exceed 8")
	}
	if config.BatchConcurrency <= 0 {
		config.BatchConcurrency = 2
	}
	if config.BatchConcurrency > 20 {
		return nil, fmt.Errorf("batch concurrency cannot exceed 20")
	}
	return &Worker{db: db, runner: runner, config: config}, nil
}

func (w *Worker) Run(ctx context.Context) error {
	if err := w.recordWorkerHeartbeat(ctx, false); err != nil {
		return err
	}
	heartbeatCtx, stopHeartbeat := context.WithCancel(ctx)
	defer stopHeartbeat()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				_ = w.recordWorkerHeartbeat(heartbeatCtx, false)
			}
		}
	}()
	workerCtx, stop := context.WithCancel(ctx)
	defer stop()
	errCh := make(chan error, w.config.Concurrency)
	var wg sync.WaitGroup
	for slot := 0; slot < w.config.Concurrency; slot++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for workerCtx.Err() == nil {
				claim, err := w.claim(workerCtx)
				if err != nil {
					if workerCtx.Err() == nil {
						select {
						case errCh <- err:
						default:
						}
					}
					stop()
					return
				}
				if claim == nil {
					select {
					case <-workerCtx.Done():
						return
					case <-time.After(w.config.PollInterval):
						continue
					}
				}
				w.active.Add(1)
				_ = w.recordWorkerHeartbeat(workerCtx, true)
				w.runClaim(workerCtx, *claim)
				w.active.Add(-1)
				_ = w.recordWorkerHeartbeat(workerCtx, true)
			}
		}()
	}
	wg.Wait()
	select {
	case err := <-errCh:
		return err
	default:
	}
	return ctx.Err()
}

func (w *Worker) recordWorkerHeartbeat(ctx context.Context, activity bool) error {
	_, err := w.db.ExecContext(ctx, `INSERT INTO conv_worker_heartbeats(worker_id,last_seen,last_activity,active_jobs,configured_concurrency,batch_concurrency) VALUES($1,now(),CASE WHEN $2 THEN now() ELSE NULL END,$3,$4,$5)
	ON CONFLICT(worker_id) DO UPDATE SET last_seen=now(),last_activity=CASE WHEN $2 THEN now() ELSE conv_worker_heartbeats.last_activity END,active_jobs=$3,configured_concurrency=$4,batch_concurrency=$5`, w.config.ID, activity, w.active.Load(), w.config.Concurrency, w.config.BatchConcurrency)
	return err
}

func (w *Worker) claim(ctx context.Context) (*claimedJob, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Lease expiry is the crash-recovery boundary. An abandoned attempt is
	// recorded before the job is made claimable again.
	_, err = tx.ExecContext(ctx, `UPDATE conv_job_attempts a SET state=CASE WHEN j.cancel_requested THEN 'cancelled' ELSE 'lease_expired' END,finished_at=now(),error=CASE WHEN j.cancel_requested THEN 'job cancelled' ELSE 'worker lease expired' END FROM conv_jobs j WHERE a.job_id=j.id AND a.attempt_number=j.attempt_count AND a.state='running' AND j.state='running' AND j.lease_until<=now()`)
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE conv_jobs SET state=CASE WHEN cancel_requested THEN 'cancelled' WHEN attempt_count>=max_attempts THEN 'failed' ELSE 'queued' END,error=CASE WHEN cancel_requested THEN 'job cancelled' WHEN attempt_count>=max_attempts THEN 'worker lease expired; retry limit reached' ELSE error END,progress=CASE WHEN cancel_requested THEN 0 ELSE progress END,lease_owner=NULL,lease_until=NULL,updated_at=now() WHERE state='running' AND lease_until<=now()`)
	if err != nil {
		return nil, err
	}
	// Complete cancellations whose Core stopped after setting the durable batch
	// fence but before transitioning every queued job.
	_, err = tx.ExecContext(ctx, `UPDATE conv_jobs j SET state='cancelled',error='batch cancelled',updated_at=now()
	 FROM conv_batches b WHERE j.batch_id=b.id AND b.cancel_requested AND j.state='queued' AND NOT j.batch_submission_pending`)
	if err != nil {
		return nil, err
	}
	var c claimedJob
	var raw []byte
	var lastOwner string
	if err = tx.QueryRowContext(ctx, `SELECT last_owner FROM conv_worker_scheduler WHERE id=1 FOR UPDATE`).Scan(&lastOwner); err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `WITH eligible AS (
	 SELECT j.id,j.owner_id,j.created_at FROM conv_jobs j
		WHERE j.state='queued' AND j.attempt_count<j.max_attempts AND NOT j.cancel_requested AND NOT j.batch_submission_pending
		AND (j.batch_id IS NULL OR (NOT EXISTS (SELECT 1 FROM conv_batches b WHERE b.id=j.batch_id AND b.cancel_requested)
		 AND (SELECT count(*) FROM conv_jobs active WHERE active.batch_id=j.batch_id AND active.state='running')<$2))
	), next_owner AS (
	 SELECT owner_id FROM eligible GROUP BY owner_id ORDER BY (owner_id>$1) DESC,owner_id LIMIT 1
	)
	SELECT j.id,j.upload_id,j.execution,j.attempt_count+1,j.max_attempts,u.sha256,u.size_bytes
	FROM conv_jobs j JOIN conv_uploads u ON u.id=j.upload_id JOIN eligible e ON e.id=j.id
	WHERE j.owner_id=(SELECT owner_id FROM next_owner)
	ORDER BY j.created_at,j.id LIMIT 1 FOR UPDATE OF j SKIP LOCKED`, lastOwner, w.config.BatchConcurrency).Scan(&c.ID, &c.UploadID, &raw, &c.Attempt, &c.MaxAttempts, &c.InputSHA256, &c.InputSize)
	if errors.Is(err, sql.ErrNoRows) {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &c.Spec); err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE conv_jobs SET state='running',attempt_count=$2,lease_owner=$3,lease_until=now()+($4 * interval '1 millisecond'),progress=0,error=NULL,updated_at=now() WHERE id=$1`, c.ID, c.Attempt, w.config.ID, w.config.Lease.Milliseconds())
	if err != nil {
		return nil, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO conv_job_attempts(job_id,attempt_number,state,lease_owner) VALUES($1,$2,'running',$3)`, c.ID, c.Attempt, w.config.ID)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_worker_scheduler SET last_owner=(SELECT owner_id FROM conv_jobs WHERE id=$1) WHERE id=1`, c.ID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (w *Worker) runClaim(parent context.Context, c claimedJob) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	heartbeatErr := make(chan error, 1)
	go func() {
		defer close(done)
		heartbeatInterval := w.config.Lease / 3
		if heartbeatInterval > time.Second {
			heartbeatInterval = time.Second
		}
		if heartbeatInterval <= 0 {
			heartbeatInterval = time.Millisecond
		}
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cancelled, err := w.heartbeat(ctx, c.ID)
				if err != nil {
					select {
					case heartbeatErr <- err:
					default:
					}
					cancel()
					return
				}
				if cancelled {
					cancel()
					return
				}
			}
		}
	}()
	inv := worker.Invocation{JobID: c.ID, Attempt: c.Attempt, InputID: c.UploadID, EngineID: c.Spec.EngineID, EngineVersion: c.Spec.EngineVersion, ExecutableRelativePath: c.Spec.ExecutableRelativePath, Arguments: c.Spec.Arguments, CompressionArguments: c.Spec.CompressionArguments, TargetSizeBytes: c.Spec.TargetSizeBytes, ProbeExecutableRelativePath: c.Spec.ProbeExecutableRelativePath, AudioBitrateKbps: c.Spec.AudioBitrateKbps, MinimumVideoKbps: c.Spec.MinimumVideoKbps, RequireSmaller: c.Spec.RequireSmaller, CompressionQualityMin: c.Spec.CompressionQualityMin, CompressionQualityMax: c.Spec.CompressionQualityMax, CompressionQualityStep: c.Spec.CompressionQualityStep, CompressionQualityHigherBetter: c.Spec.CompressionQualityHigherBetter, CompressionQualityAttempts: c.Spec.CompressionQualityAttempts, PreserveResolution: c.Spec.PreserveResolution, PreserveHDR: c.Spec.PreserveHDR, TargetExtension: c.Spec.TargetExtension, SourceExtension: c.Spec.SourceExtension, ExpectedInputSHA256: c.InputSHA256, ExpectedInputSize: c.InputSize}
	result, runErr := w.runner.Run(ctx, inv)
	cancel()
	<-done
	select {
	case hbErr := <-heartbeatErr:
		if runErr == nil {
			runErr = hbErr
		}
	default:
	}
	if errors.Is(parent.Err(), context.Canceled) {
		w.release(context.Background(), c, "worker stopping")
		return
	}
	if err := w.finish(context.Background(), c, result, runErr); err != nil {
		log.Printf("finish job %s attempt %d: %v", c.ID, c.Attempt, err)
	}
}

func (w *Worker) heartbeat(ctx context.Context, id string) (bool, error) {
	var cancel bool
	err := w.db.QueryRowContext(ctx, `UPDATE conv_jobs SET lease_until=now()+($3 * interval '1 millisecond'),updated_at=now() WHERE id=$1 AND lease_owner=$2 AND state='running' AND cancel_requested=false RETURNING cancel_requested`, id, w.config.ID, w.config.Lease.Milliseconds()).Scan(&cancel)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var state string
	err = w.db.QueryRowContext(ctx, `SELECT state,cancel_requested FROM conv_jobs WHERE id=$1 AND lease_owner=$2`, id, w.config.ID).Scan(&state, &cancel)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrLeaseLost
	}
	if err != nil {
		return false, err
	}
	if cancel {
		return true, nil
	}
	return false, ErrLeaseLost
}

func (w *Worker) finish(ctx context.Context, c claimedJob, result worker.Result, runErr error) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cancel bool
	if err = tx.QueryRowContext(ctx, `SELECT cancel_requested FROM conv_jobs WHERE id=$1 AND lease_owner=$2 AND state='running' FOR UPDATE`, c.ID, w.config.ID).Scan(&cancel); errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	} else if err != nil {
		return err
	}
	state := string(Failed)
	attemptState := "failed"
	progress := 0
	var jobErr any
	var key any
	var size int64
	var exit any = result.ExitCode
	if cancel {
		state = string(Cancelled)
		attemptState = "cancelled"
		jobErr = ErrCanceled.Error()
		progress = 0
	} else if runErr == nil {
		state = string(Succeeded)
		attemptState = "succeeded"
		progress = 100
		key = result.ResultKey
		size = result.Size
	} else {
		jobErr = boundedError(runErr)
		if c.Attempt < c.MaxAttempts && retryableWorkerError(runErr) {
			state = string(Queued)
			attemptState = "interrupted"
			progress = 0
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_job_attempts SET state=$3,finished_at=now(),exit_code=$4,stdout=$5,stderr=$6,error=$7 WHERE job_id=$1 AND attempt_number=$2`, c.ID, c.Attempt, attemptState, exit, bound(result.Stdout), bound(result.Stderr), jobErr); err != nil {
		return err
	}
	if strings.ToLower(state) == string(Queued) {
		_, err = tx.ExecContext(ctx, `UPDATE conv_jobs SET state='queued',progress=0,lease_owner=NULL,lease_until=NULL,error=$2,updated_at=now() WHERE id=$1`, c.ID, jobErr)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE conv_jobs SET state=$2,progress=$3,error=$4,result_key=$5,result_size=$6,lease_owner=NULL,lease_until=NULL,updated_at=now() WHERE id=$1`, c.ID, state, progress, jobErr, key, size)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func retryableWorkerError(err error) bool {
	return errors.Is(err, ErrLeaseLost) || strings.Contains(strings.ToLower(err.Error()), "worker unavailable")
}
func (w *Worker) release(ctx context.Context, c claimedJob, reason string) {
	_, err := w.db.ExecContext(ctx, `UPDATE conv_job_attempts SET state='interrupted',finished_at=now(),error=$3 WHERE job_id=$1 AND attempt_number=$2 AND state='running'`, c.ID, c.Attempt, bound(reason))
	if err != nil {
		log.Printf("release attempt %s/%d: %v", c.ID, c.Attempt, err)
	}
	_, err = w.db.ExecContext(ctx, `UPDATE conv_jobs SET state=CASE WHEN attempt_count>=max_attempts THEN 'failed' ELSE 'queued' END,error=$2,lease_owner=NULL,lease_until=NULL,progress=0,updated_at=now() WHERE id=$1 AND lease_owner=$3 AND state='running'`, c.ID, bound(reason), w.config.ID)
	if err != nil {
		log.Printf("release job %s: %v", c.ID, err)
	}
}
func bound(s string) string {
	if len(s) > 32<<10 {
		return s[:32<<10]
	}
	return s
}
func boundedError(err error) string {
	if err == nil {
		return ""
	}
	return bound(err.Error())
}
