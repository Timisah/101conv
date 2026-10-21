package jobs

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// History entry kinds. A batch is one grouped entry; its member jobs are only
// reachable through the batch, never as separate top-level entries.
const (
	HistoryJob   = "job"
	HistoryBatch = "batch"
)

// HistoryFilter selects one page of the unified Jobs & history stream.
// ScopeOwnerID restricts results to one owner (empty means every owner and is
// only ever set for administrators); OwnerID is the optional administrator
// filter on top of that scope.
type HistoryFilter struct {
	Mode         string
	Kind         string
	Status       string
	Query        string
	ScopeOwnerID string
	OwnerID      string
	From         *time.Time
	To           *time.Time
	Limit        int
	Offset       int
	// IncludeOwners adds the owner's display name to each entry. Only set it
	// for administrators.
	IncludeOwners bool
}

// HistoryTotals are the summed sizes of a batch's succeeded items.
type HistoryTotals struct {
	InputBytes  int64 `json:"inputBytes"`
	OutputBytes int64 `json:"outputBytes"`
	SavedBytes  int64 `json:"savedBytes,omitempty"`
}

// HistoryEntry is either a standalone job or a batch parent.
type HistoryEntry struct {
	Type      string         `json:"type"`
	CreatedAt time.Time      `json:"createdAt"`
	FileName  string         `json:"fileName,omitempty"`
	OwnerID   string         `json:"ownerId,omitempty"`
	OwnerName string         `json:"ownerName,omitempty"`
	Job       *Job           `json:"job,omitempty"`
	Batch     *Batch         `json:"batch,omitempty"`
	Totals    *HistoryTotals `json:"totals,omitempty"`
}

// historyCTE unions standalone jobs with batch parents. Batch member jobs
// (batch_id IS NOT NULL) are intentionally excluded.
const historyCTE = `WITH entries AS (
 SELECT 'job' AS kind, j.id, j.created_at, j.owner_id, j.mode, j.state FROM conv_jobs j WHERE j.batch_id IS NULL
 UNION ALL
 SELECT 'batch', b.id, b.created_at, b.owner_id, b.mode, b.state FROM conv_batches b
)`

// historyWhere is shared by the count and page queries so they cannot drift.
// Parameters: $1 scope owner, $2 owner filter, $3 mode, $4 kind, $5 status,
// $6 from, $7 to, $8 search pattern (empty for none).
const historyWhere = ` WHERE ($1='' OR e.owner_id=$1) AND ($2='' OR e.owner_id=$2) AND ($3='' OR e.mode=$3) AND ($4='' OR e.kind=$4) AND ($5='' OR e.state=$5)
 AND ($6::timestamptz IS NULL OR e.created_at>=$6) AND ($7::timestamptz IS NULL OR e.created_at<$7)
 AND ($8='' OR e.id ILIKE $8 ESCAPE E'\\'
  OR (e.kind='job' AND EXISTS(SELECT 1 FROM conv_jobs sj JOIN conv_uploads su ON su.id=sj.upload_id WHERE sj.id=e.id AND (sj.profile_id ILIKE $8 ESCAPE E'\\' OR sj.source_format ILIKE $8 ESCAPE E'\\' OR sj.target_format ILIKE $8 ESCAPE E'\\' OR su.file_name ILIKE $8 ESCAPE E'\\')))
  OR (e.kind='batch' AND EXISTS(SELECT 1 FROM conv_batch_items bi JOIN conv_uploads bu ON bu.id=bi.upload_id LEFT JOIN conv_jobs bj ON bj.id=bi.current_job_id WHERE bi.batch_id=e.id AND (bu.file_name ILIKE $8 ESCAPE E'\\' OR bj.id ILIKE $8 ESCAPE E'\\' OR bj.profile_id ILIKE $8 ESCAPE E'\\' OR bj.source_format ILIKE $8 ESCAPE E'\\' OR bj.target_format ILIKE $8 ESCAPE E'\\'))))`

func validHistoryStatus(kind, status string) bool {
	switch State(status) {
	case "", Queued, Running, Succeeded, Failed, Cancelled:
		return true
	}
	// "partial" (some items succeeded, some did not) exists only for batches.
	return status == "partial" && kind != HistoryJob
}

func likePattern(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	if len(query) > 120 {
		query = query[:120]
	}
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query) + "%"
}

// History returns one page of the unified history plus the total number of
// matching entries. Ownership is enforced in SQL via ScopeOwnerID.
func (s *Service) History(ctx context.Context, filter HistoryFilter) ([]HistoryEntry, int, error) {
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 25
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Mode != "" && filter.Mode != "convert" && filter.Mode != "compress" {
		return nil, 0, fmt.Errorf("%w: mode must be convert or compress", ErrInvalidInput)
	}
	if filter.Kind != "" && filter.Kind != HistoryJob && filter.Kind != HistoryBatch {
		return nil, 0, fmt.Errorf("%w: type must be job or batch", ErrInvalidInput)
	}
	if !validHistoryStatus(filter.Kind, filter.Status) {
		return nil, 0, fmt.Errorf("%w: unsupported status", ErrInvalidInput)
	}
	if filter.From != nil && filter.To != nil && !filter.From.Before(*filter.To) {
		return nil, 0, fmt.Errorf("%w: from must be before to", ErrInvalidInput)
	}
	args := []any{filter.ScopeOwnerID, filter.OwnerID, filter.Mode, filter.Kind, filter.Status, filter.From, filter.To, likePattern(filter.Query)}
	var total int
	if err := s.db.QueryRowContext(ctx, historyCTE+` SELECT count(*) FROM entries e`+historyWhere, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, historyCTE+` SELECT e.kind, e.id, e.created_at, e.owner_id FROM entries e`+historyWhere+` ORDER BY e.created_at DESC, e.id DESC LIMIT $9 OFFSET $10`,
		append(args, filter.Limit, filter.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	entries := make([]HistoryEntry, 0, filter.Limit)
	var jobIDs, batchIDs []string
	for rows.Next() {
		var entry HistoryEntry
		var id string
		if err = rows.Scan(&entry.Type, &id, &entry.CreatedAt, &entry.OwnerID); err != nil {
			return nil, 0, err
		}
		if entry.Type == HistoryJob {
			entry.Job = &Job{ID: id}
			jobIDs = append(jobIDs, id)
		} else {
			entry.Batch = &Batch{ID: id}
			batchIDs = append(batchIDs, id)
		}
		entries = append(entries, entry)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, err
	}
	rows.Close()
	if err = s.hydrateHistoryJobs(ctx, entries, jobIDs); err != nil {
		return nil, 0, err
	}
	if err = s.hydrateHistoryBatches(ctx, entries, batchIDs); err != nil {
		return nil, 0, err
	}
	if filter.IncludeOwners {
		s.attachOwnerNames(ctx, entries)
	} else {
		for i := range entries {
			entries[i].OwnerID = ""
		}
	}
	return entries, total, nil
}

func (s *Service) hydrateHistoryJobs(ctx context.Context, entries []HistoryEntry, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+`,COALESCE((SELECT file_name FROM conv_uploads u WHERE u.id=conv_jobs.upload_id),'') FROM conv_jobs WHERE id=ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := make(map[string]Job, len(ids))
	names := make(map[string]string, len(ids))
	for rows.Next() {
		var job Job
		var name string
		if err = scanJob(rows, &job, &name); err != nil {
			return err
		}
		byID[job.ID] = job
		names[job.ID] = name
	}
	if err = rows.Err(); err != nil {
		return err
	}
	for i := range entries {
		if entries[i].Job == nil {
			continue
		}
		id := entries[i].Job.ID
		job, ok := byID[id]
		if !ok {
			return sql.ErrNoRows
		}
		entries[i].Job = &job
		entries[i].FileName = names[id]
	}
	return nil
}

func (s *Service) hydrateHistoryBatches(ctx context.Context, entries []HistoryEntry, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,mode,state,total,queued,running,succeeded,failed,cancelled,created_at,completed_at FROM conv_batches WHERE id=ANY($1)`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	batches := make(map[string]Batch, len(ids))
	for rows.Next() {
		var b Batch
		if err = rows.Scan(&b.ID, &b.Mode, &b.State, &b.Total, &b.Queued, &b.Running, &b.Succeeded, &b.Failed, &b.Cancelled, &b.CreatedAt, &b.CompletedAt); err != nil {
			return err
		}
		b.Items = []BatchItem{}
		batches[b.ID] = b
	}
	if err = rows.Err(); err != nil {
		return err
	}
	rows.Close()
	firstNames := make(map[string]string, len(ids))
	nameRows, err := s.db.QueryContext(ctx, `SELECT bi.batch_id, u.file_name FROM conv_batch_items bi JOIN conv_uploads u ON u.id=bi.upload_id WHERE bi.batch_id=ANY($1) AND bi.item_index=0`, ids)
	if err != nil {
		return err
	}
	defer nameRows.Close()
	for nameRows.Next() {
		var id, name string
		if err = nameRows.Scan(&id, &name); err != nil {
			return err
		}
		firstNames[id] = name
	}
	if err = nameRows.Err(); err != nil {
		return err
	}
	nameRows.Close()
	totals := make(map[string]HistoryTotals, len(ids))
	sizeRows, err := s.db.QueryContext(ctx, `SELECT bi.batch_id, COALESCE(sum(u.size_bytes),0), COALESCE(sum(j.result_size),0)
	 FROM conv_batch_items bi JOIN conv_jobs j ON j.id=bi.current_job_id AND j.state='succeeded' JOIN conv_uploads u ON u.id=j.upload_id
	 WHERE bi.batch_id=ANY($1) GROUP BY bi.batch_id`, ids)
	if err != nil {
		return err
	}
	defer sizeRows.Close()
	for sizeRows.Next() {
		var id string
		var t HistoryTotals
		if err = sizeRows.Scan(&id, &t.InputBytes, &t.OutputBytes); err != nil {
			return err
		}
		if t.InputBytes > t.OutputBytes && t.OutputBytes > 0 {
			t.SavedBytes = t.InputBytes - t.OutputBytes
		}
		totals[id] = t
	}
	if err = sizeRows.Err(); err != nil {
		return err
	}
	for i := range entries {
		if entries[i].Batch == nil {
			continue
		}
		id := entries[i].Batch.ID
		batch, ok := batches[id]
		if !ok {
			return sql.ErrNoRows
		}
		entries[i].Batch = &batch
		entries[i].FileName = firstNames[id]
		if t, ok := totals[id]; ok {
			t := t
			entries[i].Totals = &t
		}
	}
	return nil
}

// attachOwnerNames is best-effort: a missing users table or an owner without
// an account (guests, removed users) falls back to a generic label.
func (s *Service) attachOwnerNames(ctx context.Context, entries []HistoryEntry) {
	seen := map[string]bool{}
	var owners []string
	for _, entry := range entries {
		if !seen[entry.OwnerID] {
			seen[entry.OwnerID] = true
			owners = append(owners, entry.OwnerID)
		}
	}
	names := map[string]string{}
	if rows, err := s.db.QueryContext(ctx, `SELECT id,username FROM conv_users WHERE id=ANY($1)`, owners); err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, name string
			if rows.Scan(&id, &name) == nil {
				names[id] = name
			}
		}
	}
	for i := range entries {
		owner := entries[i].OwnerID
		switch name := names[owner]; {
		case name != "":
			entries[i].OwnerName = name
		case strings.HasPrefix(owner, "guest:"):
			entries[i].OwnerName = "Guest"
		default:
			entries[i].OwnerName = owner
		}
	}
}
