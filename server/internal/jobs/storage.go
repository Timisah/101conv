package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/timisah/101conv/server/internal/acceleration"
)

// Storage values derived from database size columns are estimates. A file may
// have been removed outside 101conv; filesystem capacity is measured in the
// Core container's mount namespace and cannot reveal host mount paths.
type StorageAmount struct {
	Count int64 `json:"count"`
	Bytes int64 `json:"bytes"`
}
type StorageCapacity struct {
	Name       string `json:"name"`
	Path       string `json:"path,omitempty"`
	TotalBytes uint64 `json:"totalBytes,omitempty"`
	FreeBytes  uint64 `json:"freeBytes,omitempty"`
	Error      string `json:"error,omitempty"`
}
type OwnerStorage struct {
	OwnerID     string `json:"ownerId"`
	Count       int64  `json:"count"`
	Bytes       int64  `json:"bytes"`
	ResultCount int64  `json:"resultCount,omitempty"`
	ResultBytes int64  `json:"resultBytes,omitempty"`
}
type StorageOverview struct {
	DatabaseBytes      int64             `json:"databaseBytes"`
	ReservedInputBytes int64             `json:"reservedInputBytes"`
	Originals          StorageAmount     `json:"originals"`
	Results            StorageAmount     `json:"results"`
	AbandonedUploads   StorageAmount     `json:"abandonedUploads"`
	FailedOriginals    StorageAmount     `json:"failedOriginals"`
	CanceledOriginals  StorageAmount     `json:"canceledOriginals"`
	Jobs               map[string]int64  `json:"jobs"`
	Owners             []OwnerStorage    `json:"owners"`
	Capacity           []StorageCapacity `json:"capacity"`
	PendingCleanup     int64             `json:"pendingCleanup"`
	WorkerTempLimit    int64             `json:"workerTempLimitBytes"`
	MeasurementDetails map[string]string `json:"measurementDetails"`
}
type RetentionPolicy struct {
	AbandonedUploadDays *int          `json:"abandonedUploadDays"` // compatibility field; nil means manual cleanup
	Unsubmitted         RetentionRule `json:"unsubmittedUploads"`
	SubmittedOriginals  RetentionRule `json:"submittedOriginals"`
	SuccessfulResults   RetentionRule `json:"successfulResults"`
	FailedOriginals     RetentionRule `json:"failedOriginals"`
	CanceledOriginals   RetentionRule `json:"canceledOriginals"`
}
type RetentionRule struct {
	Mode string `json:"mode"`
	Days *int   `json:"days,omitempty"`
}
type CleanupReport struct {
	Candidates     int64 `json:"candidates"`
	EstimatedBytes int64 `json:"estimatedBytes"`
	Deleted        int64 `json:"deleted"`
	ReclaimedBytes int64 `json:"reclaimedBytes"`
	Interrupted    int64 `json:"interrupted"`
}
type CleanupEvent struct {
	Category       string    `json:"category"`
	OwnerID        string    `json:"ownerId"`
	FileID         string    `json:"fileId"`
	ReclaimedBytes int64     `json:"reclaimedBytes"`
	CreatedAt      time.Time `json:"createdAt"`
}

type WorkerStatus struct {
	ID                    string              `json:"id"`
	LastSeen              time.Time           `json:"lastSeen"`
	LastActivity          *time.Time          `json:"lastActivity,omitempty"`
	Online                bool                `json:"online"`
	ActiveJobs            int                 `json:"activeJobs"`
	ConfiguredConcurrency int                 `json:"configuredConcurrency"`
	BatchConcurrency      int                 `json:"batchConcurrency"`
	Acceleration          acceleration.Report `json:"acceleration"`
}
type OperationsSnapshot struct {
	DatabaseConnected bool             `json:"databaseConnected"`
	Workers           []WorkerStatus   `json:"workers"`
	JobStates         map[string]int64 `json:"jobStates"`
	ExpiredLeases     int64            `json:"expiredLeases"`
	RecentCleanup     []CleanupEvent   `json:"recentCleanup"`
}

func (s *Service) Operations(ctx context.Context) (OperationsSnapshot, error) {
	out := OperationsSnapshot{Workers: []WorkerStatus{}, JobStates: map[string]int64{}}
	if err := s.db.PingContext(ctx); err != nil {
		return out, err
	}
	out.DatabaseConnected = true
	rows, err := s.db.QueryContext(ctx, `SELECT state,count(*) FROM conv_jobs GROUP BY state`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var state string
		var count int64
		if err = rows.Scan(&state, &count); err != nil {
			rows.Close()
			return out, err
		}
		out.JobStates[state] = count
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	if err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM conv_jobs WHERE state='running' AND lease_until<now()`).Scan(&out.ExpiredLeases); err != nil {
		return out, err
	}
	rows, err = s.db.QueryContext(ctx, `SELECT worker_id,last_seen,last_activity,active_jobs,configured_concurrency,batch_concurrency,acceleration FROM conv_worker_heartbeats ORDER BY last_seen DESC LIMIT 10`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var worker WorkerStatus
		var activity sql.NullTime
		var rawAcceleration []byte
		if err = rows.Scan(&worker.ID, &worker.LastSeen, &activity, &worker.ActiveJobs, &worker.ConfiguredConcurrency, &worker.BatchConcurrency, &rawAcceleration); err != nil {
			rows.Close()
			return out, err
		}
		if len(rawAcceleration) > 0 && string(rawAcceleration) != "null" {
			if err = json.Unmarshal(rawAcceleration, &worker.Acceleration); err != nil {
				rows.Close()
				return out, err
			}
		}
		if activity.Valid {
			worker.LastActivity = &activity.Time
		}
		worker.Online = time.Since(worker.LastSeen) < 30*time.Second
		out.Workers = append(out.Workers, worker)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	out.RecentCleanup, err = s.RecentCleanup(ctx)
	return out, err
}

func (s *Service) StorageOverview(ctx context.Context) (StorageOverview, error) {
	out := StorageOverview{Jobs: map[string]int64{}, Owners: []OwnerStorage{}, Capacity: []StorageCapacity{}, WorkerTempLimit: 1 << 30,
		MeasurementDetails: map[string]string{"database": "Measured by PostgreSQL for the current database, including table and index storage.", "originals": "Database-recorded sizes; files changed outside 101conv are not reflected.", "results": "Database-recorded sizes for registered results.", "capacity": "Measured inside each Core container mount; shared host filesystems may appear more than once.", "reservedInputs": "Sum of original input sizes for queued and running jobs; this is not a prediction of output or scratch requirements.", "workerTemp": "Worker /work is a 1 GiB tmpfs; live use is unavailable to Core.", "engines": "Measured by the administrator-triggered filesystem scan; capacity is measured inside the managed engine mount."}}
	if err := s.db.QueryRowContext(ctx, `SELECT pg_database_size(current_database())`).Scan(&out.DatabaseBytes); err != nil {
		return out, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(sum(u.size_bytes),0) FROM conv_jobs j JOIN conv_uploads u ON u.id=j.upload_id WHERE j.state IN ('queued','running')`).Scan(&out.ReservedInputBytes); err != nil {
		return out, err
	}
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE original_deleted_at IS NULL AND original_missing_at IS NULL),COALESCE(sum(size_bytes) FILTER (WHERE original_deleted_at IS NULL AND original_missing_at IS NULL),0),count(*) FILTER (WHERE NOT consumed AND original_deleted_at IS NULL AND original_missing_at IS NULL),COALESCE(sum(size_bytes) FILTER (WHERE NOT consumed AND original_deleted_at IS NULL AND original_missing_at IS NULL),0),count(*) FILTER (WHERE cleanup_pending) FROM conv_uploads`).Scan(&out.Originals.Count, &out.Originals.Bytes, &out.AbandonedUploads.Count, &out.AbandonedUploads.Bytes, &out.PendingCleanup)
	if err != nil {
		return out, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE result_key IS NOT NULL AND result_deleted_at IS NULL AND result_missing_at IS NULL),COALESCE(sum(result_size) FILTER (WHERE result_key IS NOT NULL AND result_deleted_at IS NULL AND result_missing_at IS NULL),0) FROM conv_jobs`).Scan(&out.Results.Count, &out.Results.Bytes)
	if err != nil {
		return out, err
	}
	for _, pair := range []struct {
		state  string
		amount *StorageAmount
	}{{"failed", &out.FailedOriginals}, {"cancelled", &out.CanceledOriginals}} {
		err = s.db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(u.size_bytes),0) FROM conv_uploads u WHERE u.original_deleted_at IS NULL AND u.original_missing_at IS NULL AND EXISTS(SELECT 1 FROM conv_jobs j WHERE j.upload_id=u.id AND j.state=$1)`, pair.state).Scan(&pair.amount.Count, &pair.amount.Bytes)
		if err != nil {
			return out, err
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT state,count(*) FROM conv_jobs GROUP BY state`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var state string
		var count int64
		if err = rows.Scan(&state, &count); err != nil {
			rows.Close()
			return out, err
		}
		out.Jobs[state] = count
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = s.db.QueryContext(ctx, `SELECT owner_id,count(*),COALESCE(sum(size_bytes),0) FROM conv_uploads WHERE original_deleted_at IS NULL AND original_missing_at IS NULL GROUP BY owner_id ORDER BY sum(size_bytes) DESC LIMIT 25`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var owner OwnerStorage
		if err = rows.Scan(&owner.OwnerID, &owner.Count, &owner.Bytes); err != nil {
			rows.Close()
			return out, err
		}
		out.Owners = append(out.Owners, owner)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	for _, item := range []StorageCapacity{{Name: "Original uploads", Path: s.uploadRoot}, {Name: "Results", Path: s.resultRoot}, {Name: "Managed engines", Path: filepath.Join(s.management.RootPath(), "engines")}, {Name: "PostgreSQL", Error: "Filesystem capacity is in the PostgreSQL container and is not exposed to Core"}} {
		if item.Path == "" {
			out.Capacity = append(out.Capacity, item)
			continue
		}
		var fs syscall.Statfs_t
		if err = syscall.Statfs(item.Path, &fs); err != nil {
			item.Error = "Capacity unavailable inside Core"
		} else {
			item.TotalBytes = fs.Blocks * uint64(fs.Bsize)
			item.FreeBytes = fs.Bavail * uint64(fs.Bsize)
		}
		out.Capacity = append(out.Capacity, item)
	}
	return out, nil
}

func (s *Service) GetRetentionPolicy(ctx context.Context) (RetentionPolicy, error) {
	var p RetentionPolicy
	var days, submittedDays, resultDays, failedDays, canceledDays sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT abandoned_upload_days,unsubmitted_mode,submitted_original_mode,submitted_original_days,successful_result_mode,successful_result_days,failed_original_mode,failed_original_days,canceled_original_mode,canceled_original_days FROM conv_storage_settings WHERE id=1`).Scan(&days, &p.Unsubmitted.Mode, &p.SubmittedOriginals.Mode, &submittedDays, &p.SuccessfulResults.Mode, &resultDays, &p.FailedOriginals.Mode, &failedDays, &p.CanceledOriginals.Mode, &canceledDays)
	if err != nil {
		return RetentionPolicy{}, err
	}
	if days.Valid {
		v := int(days.Int64)
		p.AbandonedUploadDays = &v
		p.Unsubmitted.Days = &v
	}
	if !days.Valid && p.Unsubmitted.Mode == "after_days" {
		p.Unsubmitted.Mode = "manual"
	}
	for rule, value := range map[*RetentionRule]sql.NullInt64{&p.SubmittedOriginals: submittedDays, &p.SuccessfulResults: resultDays, &p.FailedOriginals: failedDays, &p.CanceledOriginals: canceledDays} {
		if value.Valid {
			v := int(value.Int64)
			rule.Days = &v
		}
	}
	return p, nil
}

func (s *Service) SetRetentionPolicy(ctx context.Context, policy RetentionPolicy) error {
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	if policy.AbandonedUploadDays != nil && (*policy.AbandonedUploadDays < 1 || *policy.AbandonedUploadDays > 3650) {
		return fmt.Errorf("%w: abandoned upload retention must be 1–3650 days or manual", ErrInvalidInput)
	}
	// Compatibility callers may only provide the original field. New callers
	// can independently configure each file category. A missing rule means keep.
	if policy.Unsubmitted.Mode == "" {
		if policy.AbandonedUploadDays == nil {
			policy.Unsubmitted.Mode = "manual"
		} else {
			policy.Unsubmitted = RetentionRule{Mode: "after_days", Days: policy.AbandonedUploadDays}
		}
	}
	if policy.SubmittedOriginals.Mode == "" {
		policy.SubmittedOriginals.Mode = "keep"
	}
	if policy.SuccessfulResults.Mode == "" {
		policy.SuccessfulResults.Mode = "keep"
	}
	if policy.FailedOriginals.Mode == "" {
		policy.FailedOriginals.Mode = "keep"
	}
	if policy.CanceledOriginals.Mode == "" {
		policy.CanceledOriginals.Mode = "keep"
	}
	for name, rule := range map[string]RetentionRule{"unsubmitted": policy.Unsubmitted, "submitted originals": policy.SubmittedOriginals, "successful results": policy.SuccessfulResults, "failed originals": policy.FailedOriginals, "canceled originals": policy.CanceledOriginals} {
		allowed := map[string]bool{"keep": true, "manual": true, "after_days": true}
		if name == "submitted originals" {
			allowed["after_success"] = true
		}
		if !allowed[rule.Mode] || (rule.Mode == "after_days" && (rule.Days == nil || *rule.Days < 1 || *rule.Days > 3650)) || (rule.Mode != "after_days" && rule.Days != nil) {
			return fmt.Errorf("%w: invalid %s retention policy", ErrInvalidInput, name)
		}
	}
	var abandoned *int
	if policy.Unsubmitted.Mode == "after_days" {
		abandoned = policy.Unsubmitted.Days
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE conv_storage_settings SET abandoned_upload_days=$1,unsubmitted_mode=$2,submitted_original_mode=$3,submitted_original_days=$4,successful_result_mode=$5,successful_result_days=$6,failed_original_mode=$7,failed_original_days=$8,canceled_original_mode=$9,canceled_original_days=$10 WHERE id=1`, abandoned, policy.Unsubmitted.Mode, policy.SubmittedOriginals.Mode, policy.SubmittedOriginals.Days, policy.SuccessfulResults.Mode, policy.SuccessfulResults.Days, policy.FailedOriginals.Mode, policy.FailedOriginals.Days, policy.CanceledOriginals.Mode, policy.CanceledOriginals.Days)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) PreviewAbandonedCleanup(ctx context.Context, days int) (CleanupReport, error) {
	return s.PreviewCleanupCategory(ctx, "unsubmitted", days)
}

func (s *Service) PreviewCleanupCategory(ctx context.Context, category string, days int) (CleanupReport, error) {
	if (days < 1 || days > 3650) && !(category == "successful_originals" && days == 0) {
		return CleanupReport{}, ErrInvalidInput
	}
	var out CleanupReport
	var query string
	switch category {
	case "unsubmitted":
		query = `SELECT count(*),COALESCE(sum(u.size_bytes),0) FROM conv_uploads u WHERE NOT u.consumed AND u.original_deleted_at IS NULL AND u.original_missing_at IS NULL AND (u.cleanup_pending OR u.created_at<now()-($1::int*interval '1 day')) AND NOT EXISTS(SELECT 1 FROM conv_jobs j WHERE j.upload_id=u.id) AND NOT EXISTS(SELECT 1 FROM conv_batch_items bi WHERE bi.upload_id=u.id AND bi.state IN ('pending','submitting'))`
	case "submitted_originals":
		query = `SELECT count(*),COALESCE(sum(u.size_bytes),0) FROM conv_uploads u JOIN conv_jobs j ON j.upload_id=u.id WHERE u.consumed AND u.original_deleted_at IS NULL AND u.original_missing_at IS NULL AND j.state IN ('succeeded','failed','cancelled') AND j.updated_at<now()-($1::int*interval '1 day') AND NOT EXISTS(SELECT 1 FROM conv_jobs newer WHERE newer.upload_id=u.id AND newer.created_at>j.created_at) AND NOT EXISTS(SELECT 1 FROM conv_jobs active WHERE active.upload_id=u.id AND active.state IN ('queued','running'))`
	case "successful_results":
		query = `SELECT count(*),COALESCE(sum(result_size),0) FROM conv_jobs WHERE state='succeeded' AND result_key IS NOT NULL AND result_deleted_at IS NULL AND result_missing_at IS NULL AND updated_at<now()-($1::int*interval '1 day')`
	case "successful_originals":
		query = `SELECT count(*),COALESCE(sum(u.size_bytes),0) FROM conv_uploads u JOIN conv_jobs j ON j.upload_id=u.id WHERE $1::int=0 AND u.consumed AND u.original_deleted_at IS NULL AND u.original_missing_at IS NULL AND j.state='succeeded' AND j.result_key IS NOT NULL AND j.result_deleted_at IS NULL AND j.result_missing_at IS NULL AND NOT EXISTS(SELECT 1 FROM conv_jobs newer WHERE newer.upload_id=u.id AND newer.created_at>j.created_at) AND NOT EXISTS(SELECT 1 FROM conv_jobs active WHERE active.upload_id=u.id AND active.state IN ('queued','running'))`
	case "failed_originals":
		query = `SELECT count(*),COALESCE(sum(u.size_bytes),0) FROM conv_uploads u JOIN conv_jobs j ON j.upload_id=u.id WHERE u.original_deleted_at IS NULL AND u.original_missing_at IS NULL AND j.state='failed' AND j.updated_at<now()-($1::int*interval '1 day') AND NOT EXISTS(SELECT 1 FROM conv_jobs newer WHERE newer.upload_id=u.id AND newer.created_at>j.created_at) AND NOT EXISTS(SELECT 1 FROM conv_jobs active WHERE active.upload_id=u.id AND active.state IN ('queued','running'))`
	case "canceled_originals":
		query = `SELECT count(*),COALESCE(sum(u.size_bytes),0) FROM conv_uploads u JOIN conv_jobs j ON j.upload_id=u.id WHERE u.original_deleted_at IS NULL AND u.original_missing_at IS NULL AND j.state='cancelled' AND j.updated_at<now()-($1::int*interval '1 day') AND NOT EXISTS(SELECT 1 FROM conv_jobs newer WHERE newer.upload_id=u.id AND newer.created_at>j.created_at) AND NOT EXISTS(SELECT 1 FROM conv_jobs active WHERE active.upload_id=u.id AND active.state IN ('queued','running'))`
	default:
		return CleanupReport{}, ErrInvalidInput
	}
	err := s.db.QueryRowContext(ctx, query, days).Scan(&out.Candidates, &out.EstimatedBytes)
	return out, err
}

func (s *Service) CleanupCategory(ctx context.Context, category string, days, limit int) (CleanupReport, error) {
	if category == "unsubmitted" {
		return s.CleanupAbandoned(ctx, days, limit)
	}
	if days < 1 || days > 3650 || limit < 1 || limit > 100 {
		if !(category == "successful_originals" && days == 0 && limit >= 1 && limit <= 100) {
			return CleanupReport{}, ErrInvalidInput
		}
	}
	if category == "successful_originals" {
		return s.cleanupJobOriginals(ctx, "success", 0, limit)
	}
	switch category {
	case "submitted_originals":
		return s.cleanupJobOriginals(ctx, "submitted", days, limit)
	case "successful_results":
		return s.cleanupResults(ctx, days, limit)
	case "failed_originals":
		return s.cleanupJobOriginals(ctx, "failed", days, limit)
	case "canceled_originals":
		return s.cleanupJobOriginals(ctx, "cancelled", days, limit)
	default:
		return CleanupReport{}, ErrInvalidInput
	}
}

// CleanupAbandoned claims a bounded number of unsubmitted uploads. A durable
// pending flag and lease make deletion resumable after a Core crash. Submitted
// uploads and all files referenced by jobs are excluded.
func (s *Service) CleanupAbandoned(ctx context.Context, days, limit int) (CleanupReport, error) {
	if days < 0 || days > 3650 || limit < 1 || limit > 100 {
		return CleanupReport{}, ErrInvalidInput
	}
	var report CleanupReport
	var err error
	if days == 0 {
		err = s.db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(size_bytes),0) FROM conv_uploads WHERE cleanup_pending AND NOT consumed AND original_deleted_at IS NULL AND original_missing_at IS NULL`).Scan(&report.Candidates, &report.EstimatedBytes)
	} else {
		report, err = s.PreviewAbandonedCleanup(ctx, days)
	}
	if err != nil {
		return report, err
	}
	type candidate struct {
		id, owner string
		observed  sql.NullInt64
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT u.id,u.owner_id,u.cleanup_observed_bytes FROM conv_uploads u WHERE NOT u.consumed AND u.original_deleted_at IS NULL AND u.original_missing_at IS NULL AND ((NOT u.cleanup_pending AND $1::integer>0 AND u.created_at < now()-($1::integer * interval '1 day')) OR (u.cleanup_pending AND (u.cleanup_claim_until IS NULL OR u.cleanup_claim_until<now()))) AND NOT EXISTS(SELECT 1 FROM conv_jobs j WHERE j.upload_id=u.id) AND NOT EXISTS(SELECT 1 FROM conv_batch_items bi WHERE bi.upload_id=u.id AND bi.state IN ('pending','submitting')) ORDER BY u.created_at,u.id LIMIT $2 FOR UPDATE OF u SKIP LOCKED`, days, limit)
	if err != nil {
		return report, err
	}
	selected := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.owner, &c.observed); err != nil {
			rows.Close()
			return report, err
		}
		selected = append(selected, c)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return report, err
	}
	rows.Close()
	for _, c := range selected {
		if _, err = tx.ExecContext(ctx, `UPDATE conv_uploads SET cleanup_pending=true,cleanup_claim_until=now()+interval '5 minutes' WHERE id=$1`, c.id); err != nil {
			return report, err
		}
	}
	if err = tx.Commit(); err != nil {
		return report, err
	}
	for _, c := range selected {
		if !fileIDRE.MatchString(c.id) {
			return report, fmt.Errorf("invalid cleanup file id")
		}
		s.fileMu.Lock()
		path := filepath.Join(s.uploadRoot, c.id)
		info, statErr := os.Lstat(path)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			s.fileMu.Unlock()
			return report, statErr
		}
		var reclaimed int64
		if c.observed.Valid {
			reclaimed = c.observed.Int64
		}
		if errors.Is(statErr, os.ErrNotExist) && !c.observed.Valid {
			_, err = s.db.ExecContext(ctx, `UPDATE conv_uploads SET original_missing_at=COALESCE(original_missing_at,now()),cleanup_pending=false,cleanup_claim_until=NULL WHERE id=$1 AND cleanup_pending`, c.id)
			s.fileMu.Unlock()
			if err != nil {
				return report, err
			}
			report.Interrupted++
			continue
		}
		if statErr == nil {
			if !info.Mode().IsRegular() {
				s.fileMu.Unlock()
				return report, fmt.Errorf("cleanup candidate is not a regular file")
			}
			reclaimed = info.Size()
			// Persist the observed size before unlinking. If Core stops between
			// unlink and the final audit transaction, recovery can account for
			// the bytes that this cleanup removed.
			if _, err = s.db.ExecContext(ctx, `UPDATE conv_uploads SET cleanup_observed_bytes=$2 WHERE id=$1 AND cleanup_pending`, c.id, reclaimed); err != nil {
				s.fileMu.Unlock()
				return report, err
			}
			if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.fileMu.Unlock()
				return report, err
			}
		}
		// Keep the upload row as an explicit tombstone. Job history and API
		// references can now distinguish expired files from missing records.
		finalTx, beginErr := s.db.BeginTx(ctx, nil)
		if beginErr != nil {
			s.fileMu.Unlock()
			return report, beginErr
		}
		var deletedID string
		deleteErr := finalTx.QueryRowContext(ctx, `UPDATE conv_uploads u SET original_deleted_at=COALESCE(original_deleted_at,now()),cleanup_pending=false,cleanup_claim_until=NULL,cleanup_observed_bytes=NULL WHERE u.id=$1 AND u.cleanup_pending AND NOT EXISTS(SELECT 1 FROM conv_jobs j WHERE j.upload_id=u.id) AND NOT EXISTS(SELECT 1 FROM conv_batch_items bi WHERE bi.upload_id=u.id AND bi.state IN ('pending','submitting')) RETURNING id`, c.id).Scan(&deletedID)
		if errors.Is(deleteErr, sql.ErrNoRows) {
			finalTx.Rollback()
			s.fileMu.Unlock()
			continue
		}
		if deleteErr != nil {
			finalTx.Rollback()
			s.fileMu.Unlock()
			return report, deleteErr
		}
		if _, err = finalTx.ExecContext(ctx, `INSERT INTO conv_cleanup_events(category,owner_id,file_id,bytes_reclaimed) VALUES('unsubmitted_upload',$1,$2,$3)`, c.owner, c.id, reclaimed); err != nil {
			finalTx.Rollback()
			s.fileMu.Unlock()
			return report, err
		}
		if err = finalTx.Commit(); err != nil {
			s.fileMu.Unlock()
			return report, err
		}
		s.fileMu.Unlock()
		report.Deleted++
		report.ReclaimedBytes += reclaimed
	}
	report.Interrupted = int64(len(selected)) - report.Deleted
	return report, nil
}

func (s *Service) RecentCleanup(ctx context.Context) ([]CleanupEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT category,owner_id,file_id,bytes_reclaimed,created_at FROM conv_cleanup_events ORDER BY id DESC LIMIT 25`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CleanupEvent{}
	for rows.Next() {
		var event CleanupEvent
		if err = rows.Scan(&event.Category, &event.OwnerID, &event.FileID, &event.ReclaimedBytes, &event.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

// CleanupRetainedFiles applies only explicitly time-based policies. Originals
// are eligible after successful conversion only after the registered result
// has been verified on disk and its durable database reference is present.
// Rows remain as tombstones so job history can show expiration.
func (s *Service) CleanupRetainedFiles(ctx context.Context, limit int) (CleanupReport, error) {
	s.retentionMu.RLock()
	defer s.retentionMu.RUnlock()
	return s.cleanupRetainedFilesLocked(ctx, limit)
}

// The scheduler already holds retentionMu across its complete policy snapshot.
// Do not recursively acquire a read lock: a queued policy writer blocks it.
func (s *Service) cleanupRetainedFilesLocked(ctx context.Context, limit int) (CleanupReport, error) {
	if limit < 1 || limit > 100 {
		return CleanupReport{}, ErrInvalidInput
	}
	p, err := s.GetRetentionPolicy(ctx)
	if err != nil {
		return CleanupReport{}, err
	}
	var total CleanupReport
	// Original policies apply only to terminal jobs. `after_success` additionally
	// requires a verified, registered output; failures never qualify.
	if p.SubmittedOriginals.Mode == "after_days" {
		r, e := s.cleanupJobOriginals(ctx, "submitted", *p.SubmittedOriginals.Days, limit)
		total.add(r)
		if e != nil {
			return total, e
		}
	}
	if p.SubmittedOriginals.Mode == "after_success" {
		r, e := s.cleanupJobOriginals(ctx, "success", 0, limit)
		total.add(r)
		if e != nil {
			return total, e
		}
	}
	if p.FailedOriginals.Mode == "after_days" {
		r, e := s.cleanupJobOriginals(ctx, "failed", *p.FailedOriginals.Days, limit)
		total.add(r)
		if e != nil {
			return total, e
		}
	}
	if p.CanceledOriginals.Mode == "after_days" {
		r, e := s.cleanupJobOriginals(ctx, "cancelled", *p.CanceledOriginals.Days, limit)
		total.add(r)
		if e != nil {
			return total, e
		}
	}
	// Expire results after original-after-success rules have had a chance to
	// verify the registered output artifact on disk.
	if p.SuccessfulResults.Mode == "after_days" {
		r, e := s.cleanupResults(ctx, *p.SuccessfulResults.Days, limit)
		total.add(r)
		if e != nil {
			return total, e
		}
	}
	return total, nil
}

func (r *CleanupReport) add(other CleanupReport) {
	r.Candidates += other.Candidates
	r.EstimatedBytes += other.EstimatedBytes
	r.Deleted += other.Deleted
	r.ReclaimedBytes += other.ReclaimedBytes
	r.Interrupted += other.Interrupted
}

func (s *Service) cleanupResults(ctx context.Context, days, limit int) (CleanupReport, error) {
	var out CleanupReport
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,owner_id,result_key,result_cleanup_observed_bytes FROM conv_jobs WHERE state='succeeded' AND result_key IS NOT NULL AND result_deleted_at IS NULL AND result_missing_at IS NULL AND ((NOT result_cleanup_pending AND updated_at<now()-($1::int*interval '1 day')) OR (result_cleanup_pending AND (result_cleanup_claim_until IS NULL OR result_cleanup_claim_until<now()))) ORDER BY updated_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`, days, limit)
	if err != nil {
		return out, err
	}
	type c struct {
		id, owner, key string
		observed       sql.NullInt64
	}
	cs := []c{}
	for rows.Next() {
		var x c
		if err = rows.Scan(&x.id, &x.owner, &x.key, &x.observed); err != nil {
			rows.Close()
			return out, err
		}
		cs = append(cs, x)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	for _, x := range cs {
		if _, err = tx.ExecContext(ctx, `UPDATE conv_jobs SET result_cleanup_pending=true,result_cleanup_claim_until=now()+interval '5 minutes' WHERE id=$1`, x.id); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Candidates = int64(len(cs))
	for _, x := range cs {
		if !regexp.MustCompile(`^[a-f0-9-]{36}-[0-9]{1,6}\.bin$`).MatchString(x.key) {
			out.Interrupted++
			continue
		}
		s.fileMu.Lock()
		path := filepath.Join(s.resultRoot, x.key)
		info, e := os.Lstat(path)
		size := int64(0)
		if e == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				s.fileMu.Unlock()
				out.Interrupted++
				continue
			}
			size = info.Size()
			_, err = s.db.ExecContext(ctx, `UPDATE conv_jobs SET result_cleanup_observed_bytes=$2 WHERE id=$1 AND result_cleanup_pending`, x.id, size)
			if err == nil {
				err = os.Remove(path)
			}
		} else if errors.Is(e, os.ErrNotExist) && !x.observed.Valid {
			_, err = s.db.ExecContext(ctx, `UPDATE conv_jobs SET result_missing_at=COALESCE(result_missing_at,now()),result_cleanup_pending=false,result_cleanup_claim_until=NULL WHERE id=$1 AND result_cleanup_pending`, x.id)
			s.fileMu.Unlock()
			if err != nil {
				return out, err
			}
			out.Interrupted++
			continue
		} else if errors.Is(e, os.ErrNotExist) && x.observed.Valid {
			size = x.observed.Int64
		} else if e != nil {
			err = e
		}
		if err == nil {
			_, err = s.db.ExecContext(ctx, `UPDATE conv_jobs SET result_deleted_at=COALESCE(result_deleted_at,now()),result_cleanup_pending=false,result_cleanup_claim_until=NULL,result_cleanup_observed_bytes=NULL WHERE id=$1 AND result_cleanup_pending`, x.id)
			if err == nil {
				_, err = s.db.ExecContext(ctx, `INSERT INTO conv_cleanup_events(category,owner_id,file_id,bytes_reclaimed) VALUES('successful_result',$1,$2,$3)`, x.owner, x.id, size)
			}
		}
		s.fileMu.Unlock()
		if err != nil {
			out.Interrupted++
			return out, err
		}
		out.Deleted++
		out.ReclaimedBytes += size
	}
	return out, nil
}

func (s *Service) cleanupJobOriginals(ctx context.Context, category string, days, limit int) (CleanupReport, error) {
	var out CleanupReport
	stateClause := `j.state IN ('succeeded','failed','cancelled')`
	ageClause := `j.updated_at<now()-($1::int*interval '1 day')`
	if category == "success" {
		stateClause = `j.state='succeeded' AND j.result_key IS NOT NULL AND j.result_deleted_at IS NULL AND j.result_missing_at IS NULL`
		ageClause = `$1::int=0`
		days = 0
	}
	if category == "failed" {
		stateClause = `j.state='failed'`
	}
	if category == "cancelled" {
		stateClause = `j.state='cancelled'`
	}
	query := `SELECT u.id,u.owner_id,u.cleanup_observed_bytes,j.result_key,j.result_size FROM conv_uploads u JOIN conv_jobs j ON j.upload_id=u.id WHERE u.consumed AND u.original_deleted_at IS NULL AND u.original_missing_at IS NULL AND ` + stateClause + ` AND ` + ageClause + ` AND NOT EXISTS(SELECT 1 FROM conv_jobs newer WHERE newer.upload_id=u.id AND newer.created_at>j.created_at) AND NOT EXISTS(SELECT 1 FROM conv_jobs active WHERE active.upload_id=u.id AND active.state IN ('queued','running')) AND ((NOT u.cleanup_pending) OR u.cleanup_claim_until IS NULL OR u.cleanup_claim_until<now()) ORDER BY j.updated_at,u.id LIMIT $2 FOR UPDATE OF u SKIP LOCKED`
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, query, days, limit)
	if err != nil {
		return out, err
	}
	type c struct {
		id, owner  string
		observed   sql.NullInt64
		result     sql.NullString
		resultSize int64
	}
	cs := []c{}
	for rows.Next() {
		var x c
		if err = rows.Scan(&x.id, &x.owner, &x.observed, &x.result, &x.resultSize); err != nil {
			rows.Close()
			return out, err
		}
		cs = append(cs, x)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	// Retry/submit transactions lock the upload row too. Recheck active jobs in
	// a fresh READ COMMITTED statement while those row locks are still held, so
	// a retry committed during selection cannot lose its input to cleanup.
	eligible := cs[:0]
	for _, x := range cs {
		var active bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_jobs WHERE upload_id=$1 AND state IN ('queued','running'))`, x.id).Scan(&active); err != nil {
			return out, err
		}
		if !active {
			eligible = append(eligible, x)
		}
	}
	cs = eligible
	for _, x := range cs {
		if category == "success" {
			if !x.result.Valid || !regexp.MustCompile(`^[a-f0-9-]{36}-[0-9]{1,6}\.bin$`).MatchString(x.result.String) {
				continue
			}
			info, e := os.Lstat(filepath.Join(s.resultRoot, x.result.String))
			if e != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != x.resultSize {
				continue
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE conv_uploads SET cleanup_pending=true,cleanup_claim_until=now()+interval '5 minutes' WHERE id=$1`, x.id); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Candidates = int64(len(cs))
	for _, x := range cs {
		s.fileMu.Lock()
		if category == "success" {
			// A concurrent cleanup may expire the result after selection. Recheck
			// it under the same lock used by result cleanup before deleting its
			// original, so result and original policies cannot race destructively.
			resultInfo, resultErr := os.Lstat(filepath.Join(s.resultRoot, x.result.String))
			if resultErr != nil || !resultInfo.Mode().IsRegular() || resultInfo.Mode()&os.ModeSymlink != 0 || resultInfo.Size() != x.resultSize {
				_, clearErr := s.db.ExecContext(ctx, `UPDATE conv_uploads SET cleanup_pending=false,cleanup_claim_until=NULL WHERE id=$1 AND cleanup_pending`, x.id)
				s.fileMu.Unlock()
				if clearErr != nil {
					return out, clearErr
				}
				out.Interrupted++
				continue
			}
		}
		path := filepath.Join(s.uploadRoot, x.id)
		info, e := os.Lstat(path)
		size := int64(0)
		if e == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				s.fileMu.Unlock()
				out.Interrupted++
				continue
			}
			size = info.Size()
			_, err = s.db.ExecContext(ctx, `UPDATE conv_uploads SET cleanup_observed_bytes=$2 WHERE id=$1 AND cleanup_pending`, x.id, size)
			if err == nil {
				err = os.Remove(path)
			}
		} else if errors.Is(e, os.ErrNotExist) && !x.observed.Valid {
			_, err = s.db.ExecContext(ctx, `UPDATE conv_uploads SET original_missing_at=COALESCE(original_missing_at,now()),cleanup_pending=false,cleanup_claim_until=NULL WHERE id=$1 AND cleanup_pending`, x.id)
			s.fileMu.Unlock()
			if err != nil {
				return out, err
			}
			out.Interrupted++
			continue
		} else if errors.Is(e, os.ErrNotExist) && x.observed.Valid {
			size = x.observed.Int64
		} else if e != nil {
			err = e
		}
		if err == nil {
			_, err = s.db.ExecContext(ctx, `UPDATE conv_uploads SET original_deleted_at=COALESCE(original_deleted_at,now()),cleanup_pending=false,cleanup_claim_until=NULL,cleanup_observed_bytes=NULL WHERE id=$1 AND cleanup_pending`, x.id)
			if err == nil {
				_, err = s.db.ExecContext(ctx, `INSERT INTO conv_cleanup_events(category,owner_id,file_id,bytes_reclaimed) VALUES($1,$2,$3,$4)`, category+"_original", x.owner, x.id, size)
			}
		}
		s.fileMu.Unlock()
		if err != nil {
			out.Interrupted++
			return out, err
		}
		out.Deleted++
		out.ReclaimedBytes += size
	}
	return out, nil
}

func (s *Service) RunRetention(ctx context.Context) {
	run := func() {
		s.retentionMu.RLock()
		defer s.retentionMu.RUnlock()
		policy, err := s.GetRetentionPolicy(ctx)
		if err != nil {
			log.Printf("retention: could not read policy: %v", err)
			return
		}
		if policy.Unsubmitted.Mode == "after_days" && policy.Unsubmitted.Days != nil {
			days := *policy.Unsubmitted.Days
			for i := 0; i < 5; i++ {
				report, cleanupErr := s.CleanupAbandoned(ctx, days, 100)
				if cleanupErr != nil {
					log.Printf("retention: cleanup interrupted after %d deletions: %v", report.Deleted, cleanupErr)
					return
				}
				if report.Deleted < 100 {
					break
				}
			}
		}
		if _, err := s.cleanupRetainedFilesLocked(ctx, 100); err != nil {
			log.Printf("retention: scheduled cleanup incomplete: %v", err)
		}
	}
	run()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
