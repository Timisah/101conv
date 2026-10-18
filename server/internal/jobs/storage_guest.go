package jobs

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// GuestRetentionPolicy contains independent lifetimes for anonymous files and
// metadata. The zero value is deliberately a no-op (keep data); callers pass
// persisted administrator settings explicitly.
type GuestRetentionPolicy struct {
	UploadHours, ResultHours, FailedHours, MetadataHours int
}

// LockGuestRetention serializes policy changes with the scheduled cleanup pass,
// so a newly lengthened retention interval cannot race an older deletion run.
func (s *Service) LockGuestRetention() func() {
	s.retentionMu.Lock()
	return s.retentionMu.Unlock
}

func (s *Service) CleanupGuestExpired(ctx context.Context, policy GuestRetentionPolicy, limit int) (CleanupReport, error) {
	if limit < 1 || limit > 100 || policy.UploadHours < 1 || policy.ResultHours < 1 || policy.FailedHours < 1 || policy.MetadataHours < 1 {
		return CleanupReport{}, ErrInvalidInput
	}
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	return s.cleanupGuestExpiredLocked(ctx, policy, limit)
}

func (s *Service) cleanupGuestExpiredLocked(ctx context.Context, policy GuestRetentionPolicy, limit int) (CleanupReport, error) {
	var report CleanupReport
	if _, err := s.db.ExecContext(ctx, `WITH expired AS (SELECT id FROM conv_guest_upload_reservations WHERE expires_at<now() ORDER BY expires_at LIMIT 500) DELETE FROM conv_guest_upload_reservations r USING expired e WHERE r.id=e.id`); err != nil {
		return report, err
	}
	for _, item := range []struct {
		fn    func(context.Context, int, int) (CleanupReport, error)
		hours int
	}{{s.cleanupGuestResults, policy.ResultHours}, {s.cleanupGuestAbandoned, policy.UploadHours}} {
		part, err := item.fn(ctx, item.hours, limit)
		report.Candidates += part.Candidates
		report.Deleted += part.Deleted
		report.Interrupted += part.Interrupted
		report.ReclaimedBytes += part.ReclaimedBytes
		if err != nil {
			return report, err
		}
	}
	part, err := s.cleanupGuestOriginals(ctx, policy.ResultHours, policy.FailedHours, limit)
	report.Candidates += part.Candidates
	report.Deleted += part.Deleted
	report.Interrupted += part.Interrupted
	report.ReclaimedBytes += part.ReclaimedBytes
	if err != nil {
		return report, err
	}
	return report, s.cleanupGuestMetadata(ctx, policy.MetadataHours, limit)
}

type guestFileCandidate struct {
	id, owner, key, category string
	observed                 sql.NullInt64
	size                     int64
}

func (s *Service) cleanupGuestResults(ctx context.Context, hours, limit int) (CleanupReport, error) {
	var out CleanupReport
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,owner_id,result_key,result_cleanup_observed_bytes FROM conv_jobs WHERE owner_id LIKE 'guest:%' AND state='succeeded' AND result_key IS NOT NULL AND result_deleted_at IS NULL AND result_missing_at IS NULL AND ((NOT result_cleanup_pending AND updated_at<now()-($1::integer*interval '1 hour')) OR (result_cleanup_pending AND (result_cleanup_claim_until IS NULL OR result_cleanup_claim_until<now()))) ORDER BY updated_at,id LIMIT $2 FOR UPDATE SKIP LOCKED`, hours, limit)
	if err != nil {
		return out, err
	}
	cs := []guestFileCandidate{}
	for rows.Next() {
		var c guestFileCandidate
		if err = rows.Scan(&c.id, &c.owner, &c.key, &c.observed); err != nil {
			rows.Close()
			return out, err
		}
		cs = append(cs, c)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	for _, c := range cs {
		if _, err = tx.ExecContext(ctx, `UPDATE conv_jobs SET result_cleanup_pending=true,result_cleanup_claim_until=now()+interval '5 minutes' WHERE id=$1`, c.id); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Candidates = int64(len(cs))
	valid := regexp.MustCompile(`^[a-f0-9-]{36}-[0-9]{1,6}\.bin$`)
	for _, c := range cs {
		if !valid.MatchString(c.key) {
			out.Interrupted++
			continue
		}
		s.fileMu.Lock()
		path := filepath.Join(s.resultRoot, c.key)
		info, e := os.Lstat(path)
		size := int64(0)
		missing := false
		if e == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			s.fileMu.Unlock()
			out.Interrupted++
			continue
		}
		if e == nil {
			size = info.Size()
			if _, err = s.db.ExecContext(ctx, `UPDATE conv_jobs SET result_cleanup_observed_bytes=$2 WHERE id=$1 AND result_cleanup_pending`, c.id, size); err == nil {
				err = os.Remove(path)
			}
		} else if errors.Is(e, os.ErrNotExist) && c.observed.Valid {
			size = c.observed.Int64
		} else if errors.Is(e, os.ErrNotExist) {
			_, err = s.db.ExecContext(ctx, `UPDATE conv_jobs SET result_missing_at=COALESCE(result_missing_at,now()),result_cleanup_pending=false,result_cleanup_claim_until=NULL WHERE id=$1 AND result_cleanup_pending`, c.id)
			missing = true
		} else {
			err = e
		}
		if err == nil && !missing {
			_, err = s.db.ExecContext(ctx, `UPDATE conv_jobs SET result_deleted_at=COALESCE(result_deleted_at,now()),result_cleanup_pending=false,result_cleanup_claim_until=NULL,result_cleanup_observed_bytes=NULL WHERE id=$1 AND result_cleanup_pending`, c.id)
		}
		if err == nil && !missing {
			_, err = s.db.ExecContext(ctx, `INSERT INTO conv_cleanup_events(category,owner_id,file_id,bytes_reclaimed) VALUES('guest_result',$1,$2,$3)`, c.owner, c.id, size)
		}
		s.fileMu.Unlock()
		if err != nil {
			out.Interrupted++
			return out, err
		}
		if missing {
			out.Interrupted++
			continue
		}
		out.Deleted++
		out.ReclaimedBytes += size
	}
	return out, nil
}

func (s *Service) cleanupGuestOriginals(ctx context.Context, resultHours, failedHours, limit int) (CleanupReport, error) {
	var out CleanupReport
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT u.id,u.owner_id,u.cleanup_observed_bytes,j.result_key,j.result_size,CASE WHEN j.state='succeeded' THEN 'guest_success_original' ELSE 'guest_terminal_original' END
 FROM conv_uploads u JOIN conv_jobs j ON j.upload_id=u.id
 WHERE u.owner_id LIKE 'guest:%' AND u.consumed AND u.original_deleted_at IS NULL AND u.original_missing_at IS NULL
 AND NOT EXISTS(SELECT 1 FROM conv_jobs newer WHERE newer.upload_id=u.id AND newer.created_at>j.created_at)
 AND NOT EXISTS(SELECT 1 FROM conv_jobs active WHERE active.upload_id=u.id AND active.state IN ('queued','running'))
 AND NOT EXISTS(SELECT 1 FROM conv_batch_items bi WHERE bi.upload_id=u.id AND bi.state IN ('pending','submitting'))
 AND ((j.state='succeeded' AND j.result_key IS NOT NULL AND ((j.result_deleted_at IS NOT NULL) OR (j.result_deleted_at IS NULL AND j.result_missing_at IS NULL AND j.updated_at<now()-($1::integer*interval '1 hour'))) )
 OR (j.state IN ('failed','cancelled') AND j.updated_at<now()-($2::integer*interval '1 hour')))
 AND (NOT u.cleanup_pending OR u.cleanup_claim_until IS NULL OR u.cleanup_claim_until<now())
 ORDER BY j.updated_at,u.id LIMIT $3 FOR UPDATE OF u SKIP LOCKED`, resultHours, failedHours, limit)
	if err != nil {
		return out, err
	}
	cs := []guestFileCandidate{}
	for rows.Next() {
		var c guestFileCandidate
		if err = rows.Scan(&c.id, &c.owner, &c.observed, &c.key, &c.size, &c.category); err != nil {
			rows.Close()
			return out, err
		}
		cs = append(cs, c)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	for _, c := range cs {
		if _, err = tx.ExecContext(ctx, `UPDATE conv_uploads SET cleanup_pending=true,cleanup_claim_until=now()+interval '5 minutes' WHERE id=$1`, c.id); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Candidates = int64(len(cs))
	validResult := regexp.MustCompile(`^[a-f0-9-]{36}-[0-9]{1,6}\.bin$`)
	for _, c := range cs {
		s.fileMu.Lock()
		// A successful original remains eligible only after a durable output
		// registration has been checked. A result tombstone records that the
		// previously accepted result has since expired.
		var state string
		var deleted sql.NullTime
		var resultKey sql.NullString
		var resultSize int64
		checkErr := s.db.QueryRowContext(ctx, `SELECT state,result_deleted_at,result_key,result_size FROM conv_jobs WHERE upload_id=$1 ORDER BY created_at DESC LIMIT 1`, c.id).Scan(&state, &deleted, &resultKey, &resultSize)
		if checkErr != nil || state == "succeeded" && (!resultKey.Valid || !validResult.MatchString(resultKey.String)) {
			_, clearErr := s.db.ExecContext(ctx, `UPDATE conv_uploads SET cleanup_pending=false,cleanup_claim_until=NULL WHERE id=$1`, c.id)
			s.fileMu.Unlock()
			if clearErr != nil {
				return out, clearErr
			}
			out.Interrupted++
			continue
		}
		if state == "succeeded" && !deleted.Valid {
			info, e := os.Lstat(filepath.Join(s.resultRoot, resultKey.String))
			if e != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != resultSize {
				_, clearErr := s.db.ExecContext(ctx, `UPDATE conv_uploads SET cleanup_pending=false,cleanup_claim_until=NULL WHERE id=$1`, c.id)
				s.fileMu.Unlock()
				if clearErr != nil {
					return out, clearErr
				}
				out.Interrupted++
				continue
			}
		}
		path := filepath.Join(s.uploadRoot, c.id)
		info, e := os.Lstat(path)
		size := int64(0)
		missing := false
		if e == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			s.fileMu.Unlock()
			out.Interrupted++
			continue
		}
		if e == nil {
			size = info.Size()
			_, err = s.db.ExecContext(ctx, `UPDATE conv_uploads SET cleanup_observed_bytes=$2 WHERE id=$1 AND cleanup_pending`, c.id, size)
			if err == nil {
				err = os.Remove(path)
			}
		} else if errors.Is(e, os.ErrNotExist) && c.observed.Valid {
			size = c.observed.Int64
		} else if errors.Is(e, os.ErrNotExist) {
			_, err = s.db.ExecContext(ctx, `UPDATE conv_uploads SET original_missing_at=COALESCE(original_missing_at,now()),cleanup_pending=false,cleanup_claim_until=NULL WHERE id=$1 AND cleanup_pending`, c.id)
			missing = true
		} else {
			err = e
		}
		if err == nil && !missing {
			_, err = s.db.ExecContext(ctx, `UPDATE conv_uploads SET original_deleted_at=COALESCE(original_deleted_at,now()),cleanup_pending=false,cleanup_claim_until=NULL,cleanup_observed_bytes=NULL WHERE id=$1 AND cleanup_pending`, c.id)
		}
		if err == nil && !missing {
			_, err = s.db.ExecContext(ctx, `INSERT INTO conv_cleanup_events(category,owner_id,file_id,bytes_reclaimed) VALUES($1,$2,$3,$4)`, c.category, c.owner, c.id, size)
		}
		s.fileMu.Unlock()
		if err != nil {
			return out, err
		}
		if missing {
			out.Interrupted++
			continue
		}
		out.Deleted++
		out.ReclaimedBytes += size
	}
	return out, nil
}

func (s *Service) cleanupGuestAbandoned(ctx context.Context, hours, limit int) (CleanupReport, error) {
	var out CleanupReport
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT u.id,u.owner_id,u.cleanup_observed_bytes FROM conv_uploads u WHERE u.owner_id LIKE 'guest:%' AND NOT u.consumed AND u.original_deleted_at IS NULL AND u.original_missing_at IS NULL AND ((NOT u.cleanup_pending AND u.created_at<now()-($1::integer*interval '1 hour')) OR (u.cleanup_pending AND (u.cleanup_claim_until IS NULL OR u.cleanup_claim_until<now()))) AND NOT EXISTS(SELECT 1 FROM conv_jobs j WHERE j.upload_id=u.id) AND NOT EXISTS(SELECT 1 FROM conv_batch_items bi WHERE bi.upload_id=u.id AND bi.state IN ('pending','submitting')) ORDER BY u.created_at,u.id LIMIT $2 FOR UPDATE OF u SKIP LOCKED`, hours, limit)
	if err != nil {
		return out, err
	}
	cs := []guestFileCandidate{}
	for rows.Next() {
		var c guestFileCandidate
		if err = rows.Scan(&c.id, &c.owner, &c.observed); err != nil {
			rows.Close()
			return out, err
		}
		cs = append(cs, c)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	for _, c := range cs {
		if _, err = tx.ExecContext(ctx, `UPDATE conv_uploads SET cleanup_pending=true,cleanup_claim_until=now()+interval '5 minutes' WHERE id=$1`, c.id); err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	out.Candidates = int64(len(cs))
	for _, c := range cs {
		s.fileMu.Lock()
		path := filepath.Join(s.uploadRoot, c.id)
		info, e := os.Lstat(path)
		size := int64(0)
		if e == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
			s.fileMu.Unlock()
			out.Interrupted++
			continue
		}
		missing := false
		if e == nil {
			size = info.Size()
			_, err = s.db.ExecContext(ctx, `UPDATE conv_uploads SET cleanup_observed_bytes=$2 WHERE id=$1 AND cleanup_pending`, c.id, size)
			if err == nil {
				err = os.Remove(path)
			}
		} else if errors.Is(e, os.ErrNotExist) && c.observed.Valid {
			size = c.observed.Int64
		} else if errors.Is(e, os.ErrNotExist) {
			_, err = s.db.ExecContext(ctx, `UPDATE conv_uploads SET original_missing_at=COALESCE(original_missing_at,now()),cleanup_pending=false,cleanup_claim_until=NULL WHERE id=$1 AND cleanup_pending`, c.id)
			missing = true
		} else {
			err = e
		}
		if err == nil && !missing {
			_, err = s.db.ExecContext(ctx, `UPDATE conv_uploads SET original_deleted_at=COALESCE(original_deleted_at,now()),cleanup_pending=false,cleanup_claim_until=NULL,cleanup_observed_bytes=NULL WHERE id=$1 AND cleanup_pending`, c.id)
		}
		if err == nil && !missing {
			_, err = s.db.ExecContext(ctx, `INSERT INTO conv_cleanup_events(category,owner_id,file_id,bytes_reclaimed) VALUES('guest_unsubmitted_upload',$1,$2,$3)`, c.owner, c.id, size)
		}
		s.fileMu.Unlock()
		if err != nil {
			return out, err
		}
		if missing {
			out.Interrupted++
			continue
		}
		out.Deleted++
		out.ReclaimedBytes += size
	}
	return out, nil
}

func (s *Service) cleanupGuestMetadata(ctx context.Context, hours, limit int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM conv_batches b WHERE b.id IN (SELECT b.id FROM conv_batches b WHERE b.owner_id LIKE 'guest:%' AND b.completed_at<now()-($1::integer*interval '1 hour') AND NOT EXISTS(SELECT 1 FROM conv_batch_items i WHERE i.batch_id=b.id AND i.state IN ('pending','submitting','queued','running')) AND NOT EXISTS(SELECT 1 FROM conv_batch_items i JOIN conv_uploads u ON u.id=i.upload_id WHERE i.batch_id=b.id AND (u.original_deleted_at IS NULL AND u.original_missing_at IS NULL OR u.cleanup_pending)) ORDER BY b.completed_at LIMIT $2)`, hours, limit); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM conv_jobs j WHERE j.id IN (SELECT j.id FROM conv_jobs j WHERE j.owner_id LIKE 'guest:%' AND j.state IN ('succeeded','failed','cancelled') AND j.updated_at<now()-($1::integer*interval '1 hour') AND (j.result_key IS NULL OR j.result_deleted_at IS NOT NULL OR j.result_missing_at IS NOT NULL) AND NOT j.result_cleanup_pending AND EXISTS(SELECT 1 FROM conv_uploads u WHERE u.id=j.upload_id AND (u.original_deleted_at IS NOT NULL OR u.original_missing_at IS NOT NULL) AND NOT u.cleanup_pending) AND NOT EXISTS(SELECT 1 FROM conv_batch_items i WHERE i.current_job_id=j.id) ORDER BY j.updated_at LIMIT $2)`, hours, limit); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM conv_uploads u WHERE u.id IN (SELECT u.id FROM conv_uploads u WHERE u.owner_id LIKE 'guest:%' AND u.created_at<now()-($1::integer*interval '1 hour') AND (u.original_deleted_at IS NOT NULL OR u.original_missing_at IS NOT NULL) AND NOT u.cleanup_pending AND NOT EXISTS(SELECT 1 FROM conv_jobs j WHERE j.upload_id=u.id) AND NOT EXISTS(SELECT 1 FROM conv_batch_items i WHERE i.upload_id=u.id) ORDER BY u.created_at LIMIT $2)`, hours, limit); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) RunGuestRetention(ctx context.Context, load func(context.Context) (GuestRetentionPolicy, error)) {
	run := func() {
		unlock := s.LockGuestRetention()
		defer unlock()
		policy, err := load(ctx)
		if err != nil {
			log.Printf("guest retention: settings unavailable: %v", err)
			return
		}
		for i := 0; i < 5; i++ {
			report, e := s.cleanupGuestExpiredLocked(ctx, policy, 100)
			if e != nil {
				log.Printf("guest retention: cleanup incomplete after %d files: %v", report.Deleted, e)
				return
			}
			if report.Deleted < 300 {
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
