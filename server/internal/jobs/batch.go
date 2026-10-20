package jobs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const MaxBatchItems = 100

type BatchRequest struct {
	Mode                 string             `json:"mode"`
	IdempotencyKey       string             `json:"idempotencyKey"`
	CombinedTargetSizeMB int64              `json:"combinedTargetSizeMb,omitempty"`
	Items                []BatchItemRequest `json:"items"`
}

type BatchItemRequest struct {
	UploadID     string            `json:"uploadId"`
	ProfileID    string            `json:"profileId,omitempty"`
	TargetFormat string            `json:"targetFormat"`
	Options      map[string]string `json:"options,omitempty"`
}

type Batch struct {
	ID          string      `json:"id"`
	Mode        string      `json:"mode"`
	State       string      `json:"state"`
	Total       int         `json:"total"`
	Queued      int         `json:"queued"`
	Running     int         `json:"running"`
	Succeeded   int         `json:"succeeded"`
	Failed      int         `json:"failed"`
	Cancelled   int         `json:"cancelled"`
	CreatedAt   time.Time   `json:"createdAt"`
	CompletedAt *time.Time  `json:"completedAt,omitempty"`
	Items       []BatchItem `json:"items"`
}

type BatchItem struct {
	Index     int            `json:"index"`
	UploadID  string         `json:"uploadId"`
	FileName  string         `json:"fileName"`
	JobID     string         `json:"jobId,omitempty"`
	State     string         `json:"state"`
	Error     string         `json:"error,omitempty"`
	ErrorCode string         `json:"errorCode,omitempty"`
	Job       *Job           `json:"job,omitempty"`
	Attempts  []BatchAttempt `json:"attempts"`
}

type BatchAttempt struct {
	Number     int        `json:"number"`
	State      string     `json:"state"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
}

func (s *Service) RetryBatchItems(ctx context.Context, id, ownerID string, indexes []int, limits Limits) (Batch, error) {
	if !jobIDRE.MatchString(id) || len(indexes) > MaxBatchItems {
		return Batch{}, ErrInvalidInput
	}
	var batchOwner, mode string
	if err := s.db.QueryRowContext(ctx, `SELECT owner_id,mode FROM conv_batches WHERE id=$1 AND ($2='' OR owner_id=$2)`, id, ownerID).Scan(&batchOwner, &mode); errors.Is(err, sql.ErrNoRows) {
		return Batch{}, ErrNotFound
	} else if err != nil {
		return Batch{}, err
	}
	if len(indexes) == 0 {
		rows, err := s.db.QueryContext(ctx, `SELECT item_index FROM conv_batch_items WHERE batch_id=$1 AND state='failed' ORDER BY item_index`, id)
		if err != nil {
			return Batch{}, err
		}
		for rows.Next() {
			var i int
			if err = rows.Scan(&i); err != nil {
				rows.Close()
				return Batch{}, err
			}
			indexes = append(indexes, i)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return Batch{}, err
		}
		rows.Close()
	}
	seen := map[int]bool{}
	submitPending := false
	for _, index := range indexes {
		if index < 0 || index >= MaxBatchItems || seen[index] {
			return Batch{}, ErrInvalidInput
		}
		seen[index] = true
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return Batch{}, err
		}
		if limits.Guest {
			if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('101conv-guest-storage',0))`); err != nil {
				tx.Rollback()
				return Batch{}, err
			}
			if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('101conv-guest-jobs',0))`); err != nil {
				tx.Rollback()
				return Batch{}, err
			}
		}
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, batchOwner); err != nil {
			tx.Rollback()
			return Batch{}, err
		}
		var batchCancelled bool
		if err = tx.QueryRowContext(ctx, `SELECT cancel_requested FROM conv_batches WHERE id=$1 AND owner_id=$2 FOR UPDATE`, id, batchOwner).Scan(&batchCancelled); errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			return Batch{}, ErrNotFound
		} else if err != nil {
			tx.Rollback()
			return Batch{}, err
		}
		if batchCancelled {
			tx.Rollback()
			return Batch{}, fmt.Errorf("%w: canceled batch items cannot be retried", ErrConflict)
		}
		if limits.Guest {
			var ownerOutstanding, globalOutstanding int
			q := `SELECT (SELECT count(*) FROM conv_jobs j WHERE j.owner_id=$1 AND j.state IN ('queued','running'))+(SELECT count(*) FROM conv_batch_items i JOIN conv_batches b ON b.id=i.batch_id WHERE b.owner_id=$1 AND i.state IN ('pending','submitting'))`
			if err = tx.QueryRowContext(ctx, q, batchOwner).Scan(&ownerOutstanding); err != nil {
				tx.Rollback()
				return Batch{}, err
			}
			q = `SELECT (SELECT count(*) FROM conv_jobs j WHERE j.owner_id LIKE 'guest:%' AND j.state IN ('queued','running'))+(SELECT count(*) FROM conv_batch_items i JOIN conv_batches b ON b.id=i.batch_id WHERE b.owner_id LIKE 'guest:%' AND i.state IN ('pending','submitting'))`
			if err = tx.QueryRowContext(ctx, q).Scan(&globalOutstanding); err != nil {
				tx.Rollback()
				return Batch{}, err
			}
			if ownerOutstanding+1 > limits.MaxGuestActiveJobs || globalOutstanding+1 > limits.MaxGlobalGuestJobs {
				tx.Rollback()
				return Batch{}, ErrGuestCapacity
			}
			if limits.MaxGlobalGuestStorageBytes < 1 {
				tx.Rollback()
				return Batch{}, ErrGuestCapacity
			}
			used, usageErr := guestStorageUsage(ctx, tx)
			if usageErr != nil {
				tx.Rollback()
				return Batch{}, usageErr
			}
			if used+MaxOutputBytes > limits.MaxGlobalGuestStorageBytes {
				tx.Rollback()
				return Batch{}, ErrGuestCapacity
			}
		}
		reset, resetErr := tx.ExecContext(ctx, `UPDATE conv_batch_items SET state='pending',error='',error_code='',claim_until=now(),updated_at=now() WHERE batch_id=$1 AND item_index=$2 AND current_job_id IS NULL AND state='failed'`, id, index)
		if resetErr != nil {
			tx.Rollback()
			return Batch{}, resetErr
		}
		if n, _ := reset.RowsAffected(); n > 0 {
			if err = tx.Commit(); err != nil {
				return Batch{}, err
			}
			submitPending = true
			continue
		}
		var oldJob, uploadID string
		var state string
		var execution []byte
		var profileID, profileVersion, source, target, jobMode, oldHash string
		err = tx.QueryRowContext(ctx, `SELECT i.current_job_id,i.upload_id,j.state,j.execution,j.profile_id,j.profile_version,j.source_format,j.target_format,j.mode,j.request_hash FROM conv_batch_items i JOIN conv_jobs j ON j.id=i.current_job_id JOIN conv_batches b ON b.id=i.batch_id WHERE i.batch_id=$1 AND i.item_index=$2 AND ($3='' OR b.owner_id=$3) FOR UPDATE OF i,j`, id, index, ownerID).Scan(&oldJob, &uploadID, &state, &execution, &profileID, &profileVersion, &source, &target, &jobMode, &oldHash)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			return Batch{}, ErrNotFound
		}
		if err != nil {
			tx.Rollback()
			return Batch{}, err
		}
		if state != "failed" {
			tx.Rollback()
			return Batch{}, fmt.Errorf("%w: only failed batch items can be retried", ErrConflict)
		}
		var uploadOwner string
		var expired, missing, pending bool
		if err = tx.QueryRowContext(ctx, `SELECT owner_id,original_deleted_at IS NOT NULL,original_missing_at IS NOT NULL,cleanup_pending FROM conv_uploads WHERE id=$1 FOR UPDATE`, uploadID).Scan(&uploadOwner, &expired, &missing, &pending); err != nil {
			tx.Rollback()
			return Batch{}, err
		}
		if expired {
			tx.Rollback()
			return Batch{}, ErrFileExpired
		}
		if missing {
			tx.Rollback()
			return Batch{}, ErrFileMissing
		}
		if pending {
			tx.Rollback()
			return Batch{}, fmt.Errorf("%w: original is being cleaned up", ErrConflict)
		}
		if limits.MonthlyJobs != nil {
			var count int64
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM conv_jobs WHERE owner_id=$1 AND created_at>=date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, batchOwner).Scan(&count); err != nil {
				tx.Rollback()
				return Batch{}, err
			}
			if count >= *limits.MonthlyJobs {
				tx.Rollback()
				return Batch{}, ErrQuotaExceeded
			}
		}
		var spec ExecutionSpec
		if err = json.Unmarshal(execution, &spec); err != nil {
			tx.Rollback()
			return Batch{}, fmt.Errorf("stored execution specification is invalid: %w", err)
		}
		profiles, err := s.management.ListProfiles(ctx)
		if err != nil {
			tx.Rollback()
			return Batch{}, err
		}
		profileEnabled := false
		for _, p := range profiles {
			if p.Definition.ID == profileID && p.Definition.Version == profileVersion && p.State.Enabled {
				profileEnabled = true
				break
			}
		}
		if !profileEnabled {
			tx.Rollback()
			return Batch{}, fmt.Errorf("%w: the original profile version is no longer enabled", ErrConflict)
		}
		engines, err := s.management.ListEngines(ctx)
		if err != nil {
			tx.Rollback()
			return Batch{}, err
		}
		engineAvailable := false
		for _, e := range engines {
			if e.Installation.EngineID == spec.EngineID && e.Installation.Version == spec.EngineVersion {
				engineAvailable = true
				break
			}
		}
		if !engineAvailable {
			tx.Rollback()
			return Batch{}, fmt.Errorf("%w: the original engine version is no longer installed", ErrConflict)
		}
		if batchCancelled {
			tx.Rollback()
			return Batch{}, fmt.Errorf("%w: canceled batch items cannot be retried", ErrConflict)
		}
		newID, err := randomID()
		if err != nil {
			tx.Rollback()
			return Batch{}, err
		}
		key := "retry:" + newID
		if _, err = tx.ExecContext(ctx, `INSERT INTO conv_jobs(id,upload_id,profile_id,profile_version,source_format,target_format,mode,execution,idempotency_key,request_hash,state,max_attempts,owner_id,batch_id,batch_item_index) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'queued',$11,$12,$13,$14)`, newID, uploadID, profileID, profileVersion, source, target, jobMode, execution, key, oldHash, MaxAttempts, batchOwner, id, index); err != nil {
			tx.Rollback()
			return Batch{}, err
		}
		var seq int
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM conv_batch_item_jobs WHERE batch_id=$1 AND item_index=$2`, id, index).Scan(&seq); err != nil {
			tx.Rollback()
			return Batch{}, err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO conv_batch_item_jobs(batch_id,item_index,job_id,sequence) VALUES($1,$2,$3,$4)`, id, index, newID, seq); err != nil {
			tx.Rollback()
			return Batch{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE conv_batch_items SET current_job_id=$3,state='queued',error='',updated_at=now() WHERE batch_id=$1 AND item_index=$2`, id, index, newID); err != nil {
			tx.Rollback()
			return Batch{}, err
		}
		if err = tx.Commit(); err != nil {
			return Batch{}, err
		}
	}
	if submitPending {
		if err := s.resumeBatch(ctx, id, batchOwner, limits); err != nil {
			return Batch{}, err
		}
	}
	return s.GetBatch(ctx, id, ownerID, limits)
}

// CreateBatch persists the full request before queuing any item. A restart can
// resume pending items, and deterministic item idempotency keys make that safe.
func (s *Service) CreateBatch(ctx context.Context, req BatchRequest, ownerID string, limits Limits) (Batch, bool, error) {
	req.Mode = strings.TrimSpace(req.Mode)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if req.Mode != "convert" && req.Mode != "compress" || !idempotencyRE.MatchString(req.IdempotencyKey) || len(req.Items) < 1 || len(req.Items) > MaxBatchItems {
		return Batch{}, false, fmt.Errorf("%w: batch mode, idempotency key, or item count is invalid", ErrInvalidInput)
	}
	if req.CombinedTargetSizeMB < 0 || req.CombinedTargetSizeMB > maxCombinedTargetSizeMB || (req.CombinedTargetSizeMB > 0 && req.Mode != "compress") {
		return Batch{}, false, fmt.Errorf("%w: combinedTargetSizeMb must be between 1 and %d for compression batches", ErrInvalidInput, maxCombinedTargetSizeMB)
	}
	if limits.Guest && limits.MaxBatchFiles > 0 && len(req.Items) > limits.MaxBatchFiles {
		return Batch{}, false, ErrGuestCapacity
	}
	for i := range req.Items {
		if !fileIDRE.MatchString(req.Items[i].UploadID) || !formatRE.MatchString(strings.ToLower(req.Items[i].TargetFormat)) {
			return Batch{}, false, fmt.Errorf("%w: item %d has an invalid upload or target format", ErrInvalidInput, i+1)
		}
		req.Items[i].TargetFormat = strings.ToLower(req.Items[i].TargetFormat)
	}
	seenUploads := make(map[string]struct{}, len(req.Items))
	for i, item := range req.Items {
		if _, exists := seenUploads[item.UploadID]; exists {
			return Batch{}, false, fmt.Errorf("%w: upload %q is listed more than once (item %d)", ErrInvalidInput, item.UploadID, i+1)
		}
		seenUploads[item.UploadID] = struct{}{}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Batch{}, false, err
	}
	digest := sha256.Sum256(body)
	hash := fmt.Sprintf("%x", digest[:])
	var existingID, existingHash string
	err = s.db.QueryRowContext(ctx, `SELECT id,request_hash FROM conv_batches WHERE owner_id=$1 AND idempotency_key=$2`, ownerID, req.IdempotencyKey).Scan(&existingID, &existingHash)
	if err == nil {
		if existingHash != hash {
			return Batch{}, false, fmt.Errorf("%w: idempotency key was already used for a different batch", ErrConflict)
		}
		if err = s.resumeBatch(ctx, existingID, ownerID, limits); err != nil {
			return Batch{}, true, err
		}
		batch, getErr := s.GetBatch(ctx, existingID, ownerID, limits)
		return batch, true, getErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Batch{}, false, err
	}
	if req.CombinedTargetSizeMB > 0 {
		if err := s.applyCombinedCompressionBudget(ctx, &req, ownerID); err != nil {
			return Batch{}, false, err
		}
	}
	id, err := randomID()
	if err != nil {
		return Batch{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, false, err
	}
	defer tx.Rollback()
	if limits.Guest {
		if limits.MaxGuestActiveJobs < 1 || limits.MaxGlobalGuestJobs < 1 {
			return Batch{}, false, ErrGuestCapacity
		}
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('101conv-guest-storage',0))`); err != nil {
			return Batch{}, false, err
		}
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('101conv-guest-jobs',0))`); err != nil {
			return Batch{}, false, err
		}
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, ownerID); err != nil {
		return Batch{}, false, err
	}
	err = tx.QueryRowContext(ctx, `SELECT id,request_hash FROM conv_batches WHERE owner_id=$1 AND idempotency_key=$2`, ownerID, req.IdempotencyKey).Scan(&existingID, &existingHash)
	if err == nil {
		if existingHash != hash {
			return Batch{}, false, fmt.Errorf("%w: idempotency key was already used for a different batch", ErrConflict)
		}
		if err = tx.Commit(); err != nil {
			return Batch{}, false, err
		}
		if err = s.resumeBatch(ctx, existingID, ownerID, limits); err != nil {
			return Batch{}, true, err
		}
		batch, getErr := s.GetBatch(ctx, existingID, ownerID, limits)
		return batch, true, getErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Batch{}, false, err
	}
	if limits.Guest {
		var ownerOutstanding, globalOutstanding int
		countQuery := `SELECT (SELECT count(*) FROM conv_jobs j WHERE j.owner_id=$1 AND j.state IN ('queued','running')) + (SELECT count(*) FROM conv_batch_items bi JOIN conv_batches b ON b.id=bi.batch_id WHERE b.owner_id=$1 AND bi.state IN ('pending','submitting'))`
		if err = tx.QueryRowContext(ctx, countQuery, ownerID).Scan(&ownerOutstanding); err != nil {
			return Batch{}, false, err
		}
		globalQuery := `SELECT (SELECT count(*) FROM conv_jobs j WHERE j.owner_id LIKE 'guest:%' AND j.state IN ('queued','running')) + (SELECT count(*) FROM conv_batch_items bi JOIN conv_batches b ON b.id=bi.batch_id WHERE b.owner_id LIKE 'guest:%' AND bi.state IN ('pending','submitting'))`
		if err = tx.QueryRowContext(ctx, globalQuery).Scan(&globalOutstanding); err != nil {
			return Batch{}, false, err
		}
		if ownerOutstanding+len(req.Items) > limits.MaxGuestActiveJobs || globalOutstanding+len(req.Items) > limits.MaxGlobalGuestJobs {
			return Batch{}, false, ErrGuestCapacity
		}
		if limits.MaxGlobalGuestStorageBytes < 1 {
			return Batch{}, false, ErrGuestCapacity
		}
		used, usageErr := guestStorageUsage(ctx, tx)
		if usageErr != nil {
			return Batch{}, false, usageErr
		}
		if used+int64(len(req.Items))*MaxOutputBytes > limits.MaxGlobalGuestStorageBytes {
			return Batch{}, false, ErrGuestCapacity
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO conv_batches(id,owner_id,mode,idempotency_key,request_hash,total) VALUES($1,$2,$3,$4,$5,$6)`, id, ownerID, req.Mode, req.IdempotencyKey, hash, len(req.Items)); err != nil {
		return Batch{}, false, err
	}
	for i, item := range req.Items {
		payload, marshalErr := json.Marshal(SubmitRequest{UploadID: item.UploadID, ProfileID: item.ProfileID, TargetFormat: item.TargetFormat, Mode: req.Mode, Options: item.Options, IdempotencyKey: batchItemKey(id, i)})
		if marshalErr != nil {
			return Batch{}, false, marshalErr
		}
		var storedOwner string
		var expired, missing, consumed, cleanupPending bool
		if err = tx.QueryRowContext(ctx, `SELECT owner_id,original_deleted_at IS NOT NULL,original_missing_at IS NOT NULL,consumed,cleanup_pending FROM conv_uploads WHERE id=$1 FOR UPDATE`, item.UploadID).Scan(&storedOwner, &expired, &missing, &consumed, &cleanupPending); errors.Is(err, sql.ErrNoRows) || storedOwner != ownerID {
			return Batch{}, false, fmt.Errorf("%w: item %d upload is unavailable", ErrNotFound, i+1)
		} else if err != nil {
			return Batch{}, false, err
		}
		if consumed || cleanupPending {
			return Batch{}, false, fmt.Errorf("%w: item %d upload was already submitted or is being cleaned up", ErrConflict, i+1)
		}
		if expired || missing {
			return Batch{}, false, fmt.Errorf("%w: item %d original is unavailable", ErrFileExpired, i+1)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO conv_batch_items(batch_id,item_index,upload_id,request) VALUES($1,$2,$3,$4)`, id, i, item.UploadID, payload); err != nil {
			return Batch{}, false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return Batch{}, false, err
	}
	if err = s.resumeBatch(ctx, id, ownerID, limits); err != nil {
		return Batch{}, false, err
	}
	batch, err := s.GetBatch(ctx, id, ownerID, limits)
	return batch, false, err
}

func batchItemKey(batchID string, index int) string {
	return "batch:" + batchID + ":" + fmt.Sprint(index)
}

func (s *Service) resumeBatch(ctx context.Context, batchID, ownerID string, limits Limits) error {
	for {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if limits.Guest {
			if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('101conv-guest-storage',0))`); err != nil {
				tx.Rollback()
				return err
			}
			if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('101conv-guest-jobs',0))`); err != nil {
				tx.Rollback()
				return err
			}
			if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, ownerID); err != nil {
				tx.Rollback()
				return err
			}
			var ownerOutstanding, globalOutstanding int
			q := `SELECT (SELECT count(*) FROM conv_jobs j WHERE j.owner_id=$1 AND j.state IN ('queued','running'))+(SELECT count(*) FROM conv_batch_items i JOIN conv_batches b ON b.id=i.batch_id WHERE b.owner_id=$1 AND i.state IN ('pending','submitting'))`
			if err = tx.QueryRowContext(ctx, q, ownerID).Scan(&ownerOutstanding); err != nil {
				tx.Rollback()
				return err
			}
			q = `SELECT (SELECT count(*) FROM conv_jobs j WHERE j.owner_id LIKE 'guest:%' AND j.state IN ('queued','running'))+(SELECT count(*) FROM conv_batch_items i JOIN conv_batches b ON b.id=i.batch_id WHERE b.owner_id LIKE 'guest:%' AND i.state IN ('pending','submitting'))`
			if err = tx.QueryRowContext(ctx, q).Scan(&globalOutstanding); err != nil {
				tx.Rollback()
				return err
			}
			if ownerOutstanding > limits.MaxGuestActiveJobs || globalOutstanding > limits.MaxGlobalGuestJobs {
				tx.Rollback()
				return ErrGuestCapacity
			}
			if limits.MaxGlobalGuestStorageBytes < 1 {
				tx.Rollback()
				return ErrGuestCapacity
			}
			used, usageErr := guestStorageUsage(ctx, tx)
			if usageErr != nil {
				tx.Rollback()
				return usageErr
			}
			if used > limits.MaxGlobalGuestStorageBytes {
				tx.Rollback()
				return ErrGuestCapacity
			}
		}
		var cancelled bool
		if err = tx.QueryRowContext(ctx, `SELECT cancel_requested FROM conv_batches WHERE id=$1`, batchID).Scan(&cancelled); err != nil {
			tx.Rollback()
			return err
		}
		if cancelled {
			tx.Rollback()
			return nil
		}
		var index int
		var raw []byte
		err = tx.QueryRowContext(ctx, `SELECT item_index,request FROM conv_batch_items WHERE batch_id=$1 AND (state='pending' OR (state='submitting' AND claim_until<now())) ORDER BY item_index LIMIT 1 FOR UPDATE SKIP LOCKED`, batchID).Scan(&index, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			tx.Rollback()
			return nil
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE conv_batch_items SET state='submitting',claim_until=now()+interval '5 minutes',updated_at=now() WHERE batch_id=$1 AND item_index=$2`, batchID, index); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		var req SubmitRequest
		if err = json.Unmarshal(raw, &req); err != nil {
			return s.failBatchItem(ctx, batchID, index, "Saved item request could not be read", "invalid_request")
		}
		job, _, submitErr := s.submitBatchItem(ctx, req, ownerID, limits, batchID, index)
		if submitErr != nil {
			if errors.Is(submitErr, ErrCanceled) {
				continue
			}
			if updateErr := s.failBatchItem(ctx, batchID, index, batchError(submitErr), batchErrorCode(submitErr)); updateErr != nil {
				return updateErr
			}
			continue
		}
		if err = s.attachBatchJob(ctx, batchID, index, job); err != nil {
			return err
		}
	}
}

func batchError(err error) string {
	switch {
	case errors.Is(err, ErrUnsupported):
		return "No compatible enabled profile is available for this file and output format."
	case errors.Is(err, ErrQuotaExceeded):
		return "Your monthly job quota has been reached."
	case errors.Is(err, ErrFileExpired):
		return "The original file has expired and cannot be submitted."
	case errors.Is(err, ErrFileMissing):
		return "The original file is missing from storage."
	case errors.Is(err, ErrConflict):
		return "This file is already submitted or is being cleaned up."
	case errors.Is(err, ErrInvalidInput):
		return "The selected profile or options are not valid for this file."
	default:
		return "This file could not be queued. An administrator can inspect the job diagnostics."
	}
}

func batchErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrUnsupported):
		return "unsupported_or_incompatible_profile"
	case errors.Is(err, ErrQuotaExceeded):
		return "quota_exceeded"
	case errors.Is(err, ErrFileExpired):
		return "original_expired"
	case errors.Is(err, ErrFileMissing):
		return "original_missing"
	case errors.Is(err, ErrConflict):
		return "upload_conflict"
	case errors.Is(err, ErrInvalidInput):
		return "invalid_request"
	default:
		return "queue_failed"
	}
}

func (s *Service) failBatchItem(ctx context.Context, batchID string, index int, message, code string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conv_batch_items SET state='failed',error=$3,error_code=$4,updated_at=now() WHERE batch_id=$1 AND item_index=$2 AND current_job_id IS NULL AND state IN ('pending','submitting')`, batchID, index, message, code)
	return err
}

func (s *Service) attachBatchJob(ctx context.Context, batchID string, index int, job Job) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner, itemState string
	var batchCancelled bool
	if err = tx.QueryRowContext(ctx, `SELECT owner_id,cancel_requested FROM conv_batches WHERE id=$1 FOR UPDATE`, batchID).Scan(&owner, &batchCancelled); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT state FROM conv_batch_items WHERE batch_id=$1 AND item_index=$2 FOR UPDATE`, batchID, index).Scan(&itemState); err != nil {
		return err
	}
	var sequence int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(sequence),0)+1 FROM conv_batch_item_jobs WHERE batch_id=$1 AND item_index=$2`, batchID, index).Scan(&sequence); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_jobs SET batch_id=$2,batch_item_index=$3,batch_submission_pending=false WHERE id=$1 AND owner_id=$4`, job.ID, batchID, index, owner); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO conv_batch_item_jobs(batch_id,item_index,job_id,sequence) VALUES($1,$2,$3,$4) ON CONFLICT(job_id) DO NOTHING`, batchID, index, job.ID, sequence); err != nil {
		return err
	}
	finalState, finalError := string(job.State), ""
	if batchCancelled || itemState == "cancelled" {
		finalState, finalError = "cancelled", "Cancelled before submission completed"
		if _, err = tx.ExecContext(ctx, `UPDATE conv_jobs SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE state END,cancel_requested=CASE WHEN state='running' THEN true ELSE cancel_requested END,updated_at=now() WHERE id=$1 AND state IN ('queued','running')`, job.ID); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_batch_items SET current_job_id=$3,state=$4,error=$5,error_code='',claim_until=now(),updated_at=now() WHERE batch_id=$1 AND item_index=$2`, batchID, index, job.ID, finalState, finalError); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) GetBatch(ctx context.Context, id, ownerID string, limits Limits) (Batch, error) {
	return s.getBatch(ctx, id, ownerID, limits, true)
}

func (s *Service) getBatch(ctx context.Context, id, ownerID string, limits Limits, includeItems bool) (Batch, error) {
	if !jobIDRE.MatchString(id) {
		return Batch{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Batch{}, err
	}
	defer tx.Rollback()
	var b Batch
	// Status is a read-only snapshot. Current job state is authoritative while an
	// item has a job; pending/submitting items use their durable item state.
	err = tx.QueryRowContext(ctx, `SELECT id,mode,state,total,queued,running,succeeded,failed,cancelled,created_at,completed_at
	 FROM conv_batches WHERE id=$1 AND ($2='' OR owner_id=$2)`, id, ownerID).Scan(&b.ID, &b.Mode, &b.State, &b.Total, &b.Queued, &b.Running, &b.Succeeded, &b.Failed, &b.Cancelled, &b.CreatedAt, &b.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Batch{}, ErrNotFound
	}
	if err != nil {
		return Batch{}, err
	}
	if !includeItems {
		b.Items = []BatchItem{}
		return b, tx.Commit()
	}
	rows, err := tx.QueryContext(ctx, `SELECT i.item_index,i.upload_id,u.file_name,COALESCE(j.id,''),COALESCE(j.state,i.state),COALESCE(j.error,i.error),CASE WHEN j.id IS NULL THEN i.error_code ELSE '' END,
	 j.profile_id,j.profile_version,j.source_format,j.target_format,j.mode,j.progress,j.attempt_count,j.max_attempts,j.result_size,j.created_at,j.updated_at,
	 CASE WHEN u.original_deleted_at IS NOT NULL THEN 'expired' WHEN u.original_missing_at IS NOT NULL THEN 'missing' WHEN u.cleanup_pending THEN 'scheduled' ELSE 'available' END,
	 CASE WHEN j.result_deleted_at IS NOT NULL THEN 'expired' WHEN j.result_missing_at IS NOT NULL THEN 'missing' WHEN j.result_cleanup_pending THEN 'scheduled' WHEN j.result_key IS NOT NULL THEN 'available' WHEN j.id IS NOT NULL THEN 'not_ready' ELSE NULL END
	 FROM conv_batch_items i JOIN conv_uploads u ON u.id=i.upload_id LEFT JOIN conv_jobs j ON j.id=i.current_job_id
	 WHERE i.batch_id=$1 ORDER BY i.item_index`, id)
	if err != nil {
		return Batch{}, err
	}
	defer rows.Close()
	b.Items = []BatchItem{}
	jobIDs := []string{}
	for rows.Next() {
		var item BatchItem
		var profileID, profileVersion, source, target, mode sql.NullString
		var progress, attempt, maxAttempts, resultSize sql.NullInt64
		var created, updated sql.NullTime
		var originalState, resultState sql.NullString
		if err = rows.Scan(&item.Index, &item.UploadID, &item.FileName, &item.JobID, &item.State, &item.Error, &item.ErrorCode,
			&profileID, &profileVersion, &source, &target, &mode, &progress, &attempt, &maxAttempts, &resultSize, &created, &updated, &originalState, &resultState); err != nil {
			return Batch{}, err
		}
		if item.JobID != "" {
			item.Job = &Job{ID: item.JobID, ProfileID: profileID.String, ProfileVer: profileVersion.String, SourceFormat: source.String, TargetFormat: target.String, Mode: mode.String, State: State(item.State), Progress: int(progress.Int64), Attempt: int(attempt.Int64), MaxAttempts: int(maxAttempts.Int64), Error: item.Error, OutputSize: resultSize.Int64, CreatedAt: created.Time, UpdatedAt: updated.Time, OriginalState: originalState.String, ResultState: resultState.String}
			item.Attempts = []BatchAttempt{}
			jobIDs = append(jobIDs, item.JobID)
		}
		b.Items = append(b.Items, item)
	}
	_ = rows.Close()
	if err = rows.Err(); err != nil {
		return Batch{}, err
	}
	if len(jobIDs) > 0 {
		attemptRows, e := tx.QueryContext(ctx, `SELECT job_id,attempt_number,state,started_at,finished_at,COALESCE(error,'') FROM conv_job_attempts WHERE job_id=ANY($1) ORDER BY job_id,attempt_number`, jobIDs)
		if e != nil {
			return Batch{}, e
		}
		byJob := make(map[string][]BatchAttempt, len(jobIDs))
		for attemptRows.Next() {
			var jid string
			var item BatchAttempt
			var finished sql.NullTime
			if e = attemptRows.Scan(&jid, &item.Number, &item.State, &item.StartedAt, &finished, &item.Error); e != nil {
				return Batch{}, e
			}
			if finished.Valid {
				item.FinishedAt = &finished.Time
			}
			byJob[jid] = append(byJob[jid], item)
		}
		_ = attemptRows.Close()
		if e = attemptRows.Err(); e != nil {
			return Batch{}, e
		}
		for i := range b.Items {
			if b.Items[i].JobID != "" {
				b.Items[i].Attempts = byJob[b.Items[i].JobID]
			}
		}
	}
	return b, tx.Commit()
}

func (s *Service) ResumeBatch(ctx context.Context, id, ownerID string, limits Limits) (Batch, error) {
	if !jobIDRE.MatchString(id) {
		return Batch{}, ErrNotFound
	}
	var visible bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_batches WHERE id=$1 AND ($2='' OR owner_id=$2))`, id, ownerID).Scan(&visible); err != nil {
		return Batch{}, err
	}
	if !visible {
		return Batch{}, ErrNotFound
	}
	submitOwner := ownerID
	if submitOwner == "" {
		if err := s.db.QueryRowContext(ctx, `SELECT owner_id FROM conv_batches WHERE id=$1`, id).Scan(&submitOwner); err != nil {
			return Batch{}, err
		}
	}
	if err := s.resumeBatch(ctx, id, submitOwner, limits); err != nil {
		return Batch{}, err
	}
	return s.GetBatch(ctx, id, ownerID, limits)
}

func (s *Service) CancelBatch(ctx context.Context, id, ownerID string) (Batch, error) {
	if !jobIDRE.MatchString(id) {
		return Batch{}, ErrNotFound
	}
	// Commit the cancellation fence separately. A worker claim checks this flag,
	// so queued jobs become ineligible immediately without holding a batch row
	// lock while job transitions (job -> item -> batch) are being canceled.
	res, err := s.db.ExecContext(ctx, `UPDATE conv_batches SET cancel_requested=true WHERE id=$1 AND ($2='' OR owner_id=$2)`, id, ownerID)
	if err != nil {
		return Batch{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Batch{}, ErrNotFound
	}
	// Update jobs before pending item rows: a running worker transitions job ->
	// item, so this lock order avoids deadlocks with the state-sync trigger.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE conv_jobs SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE state END,cancel_requested=CASE WHEN state='running' THEN true ELSE cancel_requested END,updated_at=now() WHERE batch_id=$1 AND state IN ('queued','running')`, id); err != nil {
		return Batch{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_batch_items SET state='cancelled',error='Cancelled before submission',error_code='cancelled',updated_at=now() WHERE batch_id=$1 AND current_job_id IS NULL AND state IN ('pending','submitting')`, id); err != nil {
		return Batch{}, err
	}
	if err = tx.Commit(); err != nil {
		return Batch{}, err
	}
	return s.GetBatch(ctx, id, ownerID, Limits{})
}

func (s *Service) CancelBatchItem(ctx context.Context, id string, index int, ownerID string) (Batch, error) {
	var jobID sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT i.current_job_id FROM conv_batch_items i JOIN conv_batches b ON b.id=i.batch_id WHERE i.batch_id=$1 AND i.item_index=$2 AND ($3='' OR b.owner_id=$3)`, id, index, ownerID).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return Batch{}, ErrNotFound
	}
	if err != nil {
		return Batch{}, err
	}
	if jobID.Valid {
		if _, err = s.db.ExecContext(ctx, `UPDATE conv_jobs SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE state END,cancel_requested=CASE WHEN state='running' THEN true ELSE cancel_requested END,updated_at=now() WHERE id=$1 AND state IN ('queued','running')`, jobID.String); err != nil {
			return Batch{}, err
		}
	} else {
		// Race safely with submission attachment. If attachment wins, cancel the
		// newly linked job in a second pass; if this update wins, attachment sees
		// the canceled item and cancels its job before it becomes claimable.
		if _, err = s.db.ExecContext(ctx, `UPDATE conv_batch_items i SET state='cancelled',error='Cancelled before submission',error_code='cancelled',updated_at=now() FROM conv_batches b WHERE i.batch_id=$1 AND i.item_index=$2 AND b.id=i.batch_id AND ($3='' OR b.owner_id=$3) AND i.current_job_id IS NULL AND i.state IN ('pending','submitting')`, id, index, ownerID); err != nil {
			return Batch{}, err
		}
		var attached sql.NullString
		if err = s.db.QueryRowContext(ctx, `SELECT current_job_id FROM conv_batch_items WHERE batch_id=$1 AND item_index=$2`, id, index).Scan(&attached); err != nil {
			return Batch{}, err
		}
		if attached.Valid {
			if _, err = s.db.ExecContext(ctx, `UPDATE conv_jobs SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE state END,cancel_requested=CASE WHEN state='running' THEN true ELSE cancel_requested END,updated_at=now() WHERE id=$1 AND state IN ('queued','running')`, attached.String); err != nil {
				return Batch{}, err
			}
		}
	}
	return s.GetBatch(ctx, id, ownerID, Limits{})
}

func (s *Service) ListBatches(ctx context.Context, ownerID string, limit, offset int) ([]Batch, int, error) {
	if limit < 1 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM conv_batches WHERE ($1='' OR owner_id=$1)`, ownerID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,mode,state,total,queued,running,succeeded,failed,cancelled,created_at,completed_at
	 FROM conv_batches WHERE ($1='' OR owner_id=$1) ORDER BY created_at DESC LIMIT $2 OFFSET $3`, ownerID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Batch, 0, limit)
	for rows.Next() {
		var b Batch
		if err = rows.Scan(&b.ID, &b.Mode, &b.State, &b.Total, &b.Queued, &b.Running, &b.Succeeded, &b.Failed, &b.Cancelled, &b.CreatedAt, &b.CompletedAt); err != nil {
			return nil, 0, err
		}
		b.Items = []BatchItem{}
		out = append(out, b)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}
