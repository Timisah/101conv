// Package analytics persists durable, retention-independent lifetime processing
// statistics per user and across the server. It records one idempotent ledger
// entry per terminal job (never double-counting retries, batch parents, or
// downloads) and stores no per-file identifiable metadata.
package analytics

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) (*Service, error) {
	if db == nil {
		return nil, fmt.Errorf("analytics require PostgreSQL")
	}
	return &Service{db: db}, nil
}

type Overview struct {
	TrackingSince        time.Time `json:"trackingSince"`
	BackfilledJobs       int64     `json:"backfilledJobs"`
	ProcessedInputBytes  int64     `json:"processedInputBytes"`
	ProcessedOutputBytes int64     `json:"processedOutputBytes"`
	SavedBytes           int64     `json:"savedBytes"`
	SavingsPercent       float64   `json:"savingsPercent"`
	TotalItems           int64     `json:"totalItems"`
	SucceededItems       int64     `json:"succeededItems"`
	FailedItems          int64     `json:"failedItems"`
	CancelledItems       int64     `json:"cancelledItems"`
	ConvertItems         int64     `json:"convertItems"`
	CompressItems        int64     `json:"compressItems"`
	StandaloneJobs       int64     `json:"standaloneJobs"`
	BatchItems           int64     `json:"batchItems"`
	BatchExecutions      int64     `json:"batchExecutions"`
	Last30DaysInputBytes int64     `json:"last30DaysInputBytes"`
	Last30DaysSucceeded  int64     `json:"last30DaysSucceeded"`
}

type UserStats struct {
	OwnerID              string  `json:"ownerId"`
	DisplayName          string  `json:"displayName"`
	Category             string  `json:"category"` // "user", "guest", or "deleted"
	ProcessedInputBytes  int64   `json:"processedInputBytes"`
	ProcessedOutputBytes int64   `json:"processedOutputBytes"`
	SavedBytes           int64   `json:"savedBytes"`
	SavingsPercent       float64 `json:"savingsPercent"`
	TotalItems           int64   `json:"totalItems"`
	SucceededItems       int64   `json:"succeededItems"`
	FailedItems          int64   `json:"failedItems"`
	CancelledItems       int64   `json:"cancelledItems"`
	ConvertItems         int64   `json:"convertItems"`
	CompressItems        int64   `json:"compressItems"`
	StandaloneJobs       int64   `json:"standaloneJobs"`
	BatchItems           int64   `json:"batchItems"`
	BatchExecutions      int64   `json:"batchExecutions"`
}

// Migrate creates the durable analytics ledger, metadata baseline table, and
// transactional trigger on conv_jobs, then backfills verifiable retained
// terminal jobs idempotently.
func (s *Service) Migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS conv_analytics_meta (
 id SMALLINT PRIMARY KEY CHECK (id = 1),
 tracking_since TIMESTAMPTZ NOT NULL,
 backfilled_jobs BIGINT NOT NULL DEFAULT 0,
 backfilled_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS conv_processing_ledger (
 job_id TEXT PRIMARY KEY,
 owner_id TEXT NOT NULL,
 mode TEXT NOT NULL CHECK (mode IN ('convert', 'compress')),
 is_batch_item BOOLEAN NOT NULL DEFAULT false,
 batch_id TEXT NOT NULL DEFAULT '',
 terminal_state TEXT NOT NULL CHECK (terminal_state IN ('succeeded', 'failed', 'cancelled')),
 input_bytes BIGINT NOT NULL DEFAULT 0,
 output_bytes BIGINT NOT NULL DEFAULT 0,
 saved_bytes BIGINT NOT NULL DEFAULT 0,
 recorded_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS conv_processing_ledger_owner_idx ON conv_processing_ledger(owner_id, recorded_at DESC);
CREATE INDEX IF NOT EXISTS conv_processing_ledger_recorded_idx ON conv_processing_ledger(recorded_at DESC);

CREATE OR REPLACE FUNCTION conv_record_processing_ledger() RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
AS $$
DECLARE
 v_input BIGINT := 0;
 v_output BIGINT := 0;
 v_saved BIGINT := 0;
 v_mode TEXT := CASE WHEN NEW.mode = 'compress' THEN 'compress' ELSE 'convert' END;
BEGIN
 IF NEW.state NOT IN ('succeeded', 'failed', 'cancelled') THEN
  RETURN NEW;
 END IF;
 IF NEW.state = 'succeeded' THEN
  SELECT COALESCE(u.size_bytes, 0) INTO v_input FROM conv_uploads u WHERE u.id = NEW.upload_id;
  v_output := GREATEST(0, COALESCE(NEW.result_size, 0));
  IF v_input > v_output AND v_output > 0 THEN
   v_saved := v_input - v_output;
  END IF;
 END IF;
 INSERT INTO conv_processing_ledger(
  job_id, owner_id, mode, is_batch_item, batch_id, terminal_state,
  input_bytes, output_bytes, saved_bytes, recorded_at
 ) VALUES (
  NEW.id,
  COALESCE(NULLIF(NEW.owner_id, ''), 'unknown'),
  v_mode,
  NEW.batch_id IS NOT NULL AND NEW.batch_id <> '',
  COALESCE(NEW.batch_id, ''),
  NEW.state,
  v_input,
  v_output,
  v_saved,
  COALESCE(NEW.updated_at, now())
 )
 ON CONFLICT (job_id) DO NOTHING;
 RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS conv_jobs_processing_ledger_trg ON conv_jobs;
CREATE TRIGGER conv_jobs_processing_ledger_trg
 AFTER INSERT OR UPDATE OF state ON conv_jobs
 FOR EACH ROW
 WHEN (NEW.state IN ('succeeded', 'failed', 'cancelled'))
 EXECUTE FUNCTION conv_record_processing_ledger();
`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return err
	}

	// Idempotent one-time backfill from verifiable retained terminal jobs.
	res, err := s.db.ExecContext(ctx, `
INSERT INTO conv_processing_ledger(
 job_id, owner_id, mode, is_batch_item, batch_id, terminal_state,
 input_bytes, output_bytes, saved_bytes, recorded_at
)
SELECT
 j.id,
 COALESCE(NULLIF(j.owner_id, ''), 'unknown'),
 CASE WHEN j.mode = 'compress' THEN 'compress' ELSE 'convert' END,
 j.batch_id IS NOT NULL AND j.batch_id <> '',
 COALESCE(j.batch_id, ''),
 j.state,
 CASE WHEN j.state = 'succeeded' THEN COALESCE(u.size_bytes, 0) ELSE 0 END,
 CASE WHEN j.state = 'succeeded' THEN GREATEST(0, COALESCE(j.result_size, 0)) ELSE 0 END,
 CASE WHEN j.state = 'succeeded' AND COALESCE(u.size_bytes, 0) > GREATEST(0, COALESCE(j.result_size, 0)) AND GREATEST(0, COALESCE(j.result_size, 0)) > 0
      THEN COALESCE(u.size_bytes, 0) - GREATEST(0, COALESCE(j.result_size, 0))
      ELSE 0 END,
 COALESCE(j.updated_at, j.created_at, now())
FROM conv_jobs j
LEFT JOIN conv_uploads u ON u.id = j.upload_id
WHERE j.state IN ('succeeded', 'failed', 'cancelled')
ON CONFLICT (job_id) DO NOTHING
`)
	if err != nil {
		return err
	}
	backfilled, _ := res.RowsAffected()

	_, err = s.db.ExecContext(ctx, `
INSERT INTO conv_analytics_meta(id, tracking_since, backfilled_jobs, backfilled_at)
VALUES (
 1,
 COALESCE((SELECT MIN(recorded_at) FROM conv_processing_ledger), now()),
 $1,
 now()
)
ON CONFLICT (id) DO NOTHING
`, backfilled)
	return err
}

func (s *Service) Overview(ctx context.Context) (Overview, error) {
	var out Overview
	err := s.db.QueryRowContext(ctx, `SELECT tracking_since, backfilled_jobs FROM conv_analytics_meta WHERE id = 1`).Scan(&out.TrackingSince, &out.BackfilledJobs)
	if err != nil && err != sql.ErrNoRows {
		return Overview{}, err
	}
	if out.TrackingSince.IsZero() {
		out.TrackingSince = time.Now().UTC()
	}

	err = s.db.QueryRowContext(ctx, `
SELECT
 COALESCE(SUM(input_bytes), 0),
 COALESCE(SUM(output_bytes), 0),
 COALESCE(SUM(saved_bytes), 0),
 COUNT(*),
 COUNT(*) FILTER (WHERE terminal_state = 'succeeded'),
 COUNT(*) FILTER (WHERE terminal_state = 'failed'),
 COUNT(*) FILTER (WHERE terminal_state = 'cancelled'),
 COUNT(*) FILTER (WHERE mode = 'convert' AND terminal_state = 'succeeded'),
 COUNT(*) FILTER (WHERE mode = 'compress' AND terminal_state = 'succeeded'),
 COUNT(*) FILTER (WHERE NOT is_batch_item),
 COUNT(*) FILTER (WHERE is_batch_item),
 COUNT(DISTINCT NULLIF(batch_id, '')),
 COALESCE(SUM(input_bytes) FILTER (WHERE recorded_at >= now() - interval '30 days'), 0),
 COUNT(*) FILTER (WHERE terminal_state = 'succeeded' AND recorded_at >= now() - interval '30 days')
FROM conv_processing_ledger
`).Scan(
		&out.ProcessedInputBytes,
		&out.ProcessedOutputBytes,
		&out.SavedBytes,
		&out.TotalItems,
		&out.SucceededItems,
		&out.FailedItems,
		&out.CancelledItems,
		&out.ConvertItems,
		&out.CompressItems,
		&out.StandaloneJobs,
		&out.BatchItems,
		&out.BatchExecutions,
		&out.Last30DaysInputBytes,
		&out.Last30DaysSucceeded,
	)
	if err != nil {
		return Overview{}, err
	}
	if out.ProcessedInputBytes > 0 && out.SavedBytes > 0 {
		out.SavingsPercent = float64(out.SavedBytes) * 100 / float64(out.ProcessedInputBytes)
	}
	return out, nil
}

func (s *Service) ByUser(ctx context.Context, limit, offset int) ([]UserStats, int, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(DISTINCT CASE WHEN owner_id LIKE 'guest:%' THEN 'guest' ELSE owner_id END)
FROM conv_processing_ledger
`).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	rows, err := s.db.QueryContext(ctx, `
WITH grouped AS (
 SELECT
  CASE WHEN owner_id LIKE 'guest:%' THEN 'guest' ELSE owner_id END AS owner_key,
  COALESCE(SUM(input_bytes), 0) AS input_bytes,
  COALESCE(SUM(output_bytes), 0) AS output_bytes,
  COALESCE(SUM(saved_bytes), 0) AS saved_bytes,
  COUNT(*) AS total_items,
  COUNT(*) FILTER (WHERE terminal_state = 'succeeded') AS succeeded_items,
  COUNT(*) FILTER (WHERE terminal_state = 'failed') AS failed_items,
  COUNT(*) FILTER (WHERE terminal_state = 'cancelled') AS cancelled_items,
  COUNT(*) FILTER (WHERE mode = 'convert' AND terminal_state = 'succeeded') AS convert_items,
  COUNT(*) FILTER (WHERE mode = 'compress' AND terminal_state = 'succeeded') AS compress_items,
  COUNT(*) FILTER (WHERE NOT is_batch_item) AS standalone_jobs,
  COUNT(*) FILTER (WHERE is_batch_item) AS batch_items,
  COUNT(DISTINCT NULLIF(batch_id, '')) AS batch_executions
 FROM conv_processing_ledger
 GROUP BY 1
)
SELECT
 g.owner_key,
 COALESCE(u.username, ''),
 g.input_bytes, g.output_bytes, g.saved_bytes,
 g.total_items, g.succeeded_items, g.failed_items, g.cancelled_items,
 g.convert_items, g.compress_items, g.standalone_jobs, g.batch_items, g.batch_executions
FROM grouped g
LEFT JOIN conv_users u ON u.id = g.owner_key
ORDER BY g.input_bytes DESC, g.succeeded_items DESC, g.owner_key ASC
LIMIT $1 OFFSET $2
`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]UserStats, 0, limit)
	for rows.Next() {
		var u UserStats
		var username string
		if err = rows.Scan(
			&u.OwnerID,
			&username,
			&u.ProcessedInputBytes,
			&u.ProcessedOutputBytes,
			&u.SavedBytes,
			&u.TotalItems,
			&u.SucceededItems,
			&u.FailedItems,
			&u.CancelledItems,
			&u.ConvertItems,
			&u.CompressItems,
			&u.StandaloneJobs,
			&u.BatchItems,
			&u.BatchExecutions,
		); err != nil {
			return nil, 0, err
		}
		switch {
		case u.OwnerID == "guest":
			u.DisplayName = "Guest sessions"
			u.Category = "guest"
		case strings.TrimSpace(username) != "":
			u.DisplayName = username
			u.Category = "user"
		case u.OwnerID == "admin":
			u.DisplayName = "Administrator"
			u.Category = "user"
		default:
			u.DisplayName = "Deleted user"
			u.Category = "deleted"
		}
		if u.ProcessedInputBytes > 0 && u.SavedBytes > 0 {
			u.SavingsPercent = float64(u.SavedBytes) * 100 / float64(u.ProcessedInputBytes)
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}
