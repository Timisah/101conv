package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"time"
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
	Path       string `json:"path"`
	TotalBytes uint64 `json:"totalBytes,omitempty"`
	FreeBytes  uint64 `json:"freeBytes,omitempty"`
	Error      string `json:"error,omitempty"`
}
type OwnerStorage struct {
	OwnerID string `json:"ownerId"`
	Count   int64  `json:"count"`
	Bytes   int64  `json:"bytes"`
}
type StorageOverview struct {
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
	AbandonedUploadDays *int `json:"abandonedUploadDays"`
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
	ID           string     `json:"id"`
	LastSeen     time.Time  `json:"lastSeen"`
	LastActivity *time.Time `json:"lastActivity,omitempty"`
	Online       bool       `json:"online"`
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
	rows, err = s.db.QueryContext(ctx, `SELECT worker_id,last_seen,last_activity FROM conv_worker_heartbeats ORDER BY last_seen DESC LIMIT 10`)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var worker WorkerStatus
		var activity sql.NullTime
		if err = rows.Scan(&worker.ID, &worker.LastSeen, &activity); err != nil {
			rows.Close()
			return out, err
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
		MeasurementDetails: map[string]string{"originals": "Database-recorded sizes; files changed outside 101conv are not reflected.", "results": "Database-recorded sizes for registered results.", "capacity": "Measured inside each Core container mount; shared host filesystems may appear more than once.", "workerTemp": "Worker /work is a 1 GiB tmpfs; live usage is not visible to Core.", "engines": "Installed engine disk usage is not measured in this view."}}
	err := s.db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(size_bytes),0),count(*) FILTER (WHERE NOT consumed),COALESCE(sum(size_bytes) FILTER (WHERE NOT consumed),0),count(*) FILTER (WHERE cleanup_pending) FROM conv_uploads`).Scan(&out.Originals.Count, &out.Originals.Bytes, &out.AbandonedUploads.Count, &out.AbandonedUploads.Bytes, &out.PendingCleanup)
	if err != nil {
		return out, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE result_key IS NOT NULL),COALESCE(sum(result_size) FILTER (WHERE result_key IS NOT NULL),0) FROM conv_jobs`).Scan(&out.Results.Count, &out.Results.Bytes)
	if err != nil {
		return out, err
	}
	for _, pair := range []struct {
		state  string
		amount *StorageAmount
	}{{"failed", &out.FailedOriginals}, {"cancelled", &out.CanceledOriginals}} {
		err = s.db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(u.size_bytes),0) FROM conv_uploads u WHERE EXISTS(SELECT 1 FROM conv_jobs j WHERE j.upload_id=u.id AND j.state=$1)`, pair.state).Scan(&pair.amount.Count, &pair.amount.Bytes)
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
	rows, err = s.db.QueryContext(ctx, `SELECT owner_id,count(*),COALESCE(sum(size_bytes),0) FROM conv_uploads GROUP BY owner_id ORDER BY sum(size_bytes) DESC LIMIT 25`)
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
	for _, item := range []StorageCapacity{{Name: "Original uploads", Path: s.uploadRoot}, {Name: "Results", Path: s.resultRoot}} {
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
	var days sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT abandoned_upload_days FROM conv_storage_settings WHERE id=1`).Scan(&days)
	if err != nil {
		return RetentionPolicy{}, err
	}
	if !days.Valid {
		return RetentionPolicy{}, nil
	}
	v := int(days.Int64)
	return RetentionPolicy{AbandonedUploadDays: &v}, nil
}

func (s *Service) SetRetentionPolicy(ctx context.Context, policy RetentionPolicy) error {
	if policy.AbandonedUploadDays != nil && (*policy.AbandonedUploadDays < 1 || *policy.AbandonedUploadDays > 3650) {
		return fmt.Errorf("%w: abandoned upload retention must be 1–3650 days or manual", ErrInvalidInput)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE conv_storage_settings SET abandoned_upload_days=$1 WHERE id=1`, policy.AbandonedUploadDays)
	return err
}

func (s *Service) PreviewAbandonedCleanup(ctx context.Context, days int) (CleanupReport, error) {
	if days < 1 || days > 3650 {
		return CleanupReport{}, ErrInvalidInput
	}
	var out CleanupReport
	err := s.db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(u.size_bytes),0) FROM conv_uploads u WHERE NOT u.consumed AND (u.cleanup_pending OR u.created_at < now()-($1::integer * interval '1 day')) AND NOT EXISTS (SELECT 1 FROM conv_jobs j WHERE j.upload_id=u.id)`, days).Scan(&out.Candidates, &out.EstimatedBytes)
	return out, err
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
		err = s.db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(size_bytes),0) FROM conv_uploads WHERE cleanup_pending AND NOT consumed`).Scan(&report.Candidates, &report.EstimatedBytes)
	} else {
		report, err = s.PreviewAbandonedCleanup(ctx, days)
	}
	if err != nil {
		return report, err
	}
	type candidate struct{ id, owner string }
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT u.id,u.owner_id FROM conv_uploads u WHERE NOT u.consumed AND ((NOT u.cleanup_pending AND $1::integer>0 AND u.created_at < now()-($1::integer * interval '1 day')) OR (u.cleanup_pending AND (u.cleanup_claim_until IS NULL OR u.cleanup_claim_until<now()))) AND NOT EXISTS(SELECT 1 FROM conv_jobs j WHERE j.upload_id=u.id) ORDER BY u.created_at,u.id LIMIT $2 FOR UPDATE OF u SKIP LOCKED`, days, limit)
	if err != nil {
		return report, err
	}
	selected := []candidate{}
	for rows.Next() {
		var c candidate
		if err = rows.Scan(&c.id, &c.owner); err != nil {
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
		path := filepath.Join(s.uploadRoot, c.id)
		info, statErr := os.Lstat(path)
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return report, statErr
		}
		var reclaimed int64
		if statErr == nil {
			if !info.Mode().IsRegular() {
				return report, fmt.Errorf("cleanup candidate is not a regular file")
			}
			reclaimed = info.Size()
			if err = os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return report, err
			}
		}
		finalTx, beginErr := s.db.BeginTx(ctx, nil)
		if beginErr != nil {
			return report, beginErr
		}
		var deletedID string
		deleteErr := finalTx.QueryRowContext(ctx, `DELETE FROM conv_uploads u WHERE u.id=$1 AND u.cleanup_pending AND NOT EXISTS(SELECT 1 FROM conv_jobs j WHERE j.upload_id=u.id) RETURNING id`, c.id).Scan(&deletedID)
		if errors.Is(deleteErr, sql.ErrNoRows) {
			finalTx.Rollback()
			continue
		}
		if deleteErr != nil {
			finalTx.Rollback()
			return report, deleteErr
		}
		if _, err = finalTx.ExecContext(ctx, `INSERT INTO conv_cleanup_events(category,owner_id,file_id,bytes_reclaimed) VALUES('abandoned_upload',$1,$2,$3)`, c.owner, c.id, reclaimed); err != nil {
			finalTx.Rollback()
			return report, err
		}
		if err = finalTx.Commit(); err != nil {
			return report, err
		}
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

func (s *Service) RunRetention(ctx context.Context) {
	run := func() {
		policy, err := s.GetRetentionPolicy(ctx)
		if err != nil {
			log.Printf("retention: could not read policy: %v", err)
			return
		}
		days := 0
		if policy.AbandonedUploadDays != nil {
			days = *policy.AbandonedUploadDays
		}
		for i := 0; i < 5; i++ {
			report, err := s.CleanupAbandoned(ctx, days, 100)
			if err != nil {
				log.Printf("retention: cleanup interrupted after %d deletions: %v", report.Deleted, err)
				return
			}
			if report.Deleted < 100 {
				return
			}
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
