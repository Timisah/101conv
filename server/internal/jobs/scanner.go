package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// StorageScan is an exact measurement of regular files under the configured
// upload, result, and managed-engine roots. It is asynchronous and read-only.
type StorageScan struct {
	Status      string                         `json:"status"`
	StartedAt   *time.Time                     `json:"startedAt,omitempty"`
	UpdatedAt   time.Time                      `json:"updatedAt"`
	CompletedAt *time.Time                     `json:"completedAt,omitempty"`
	Error       string                         `json:"error,omitempty"`
	Categories  map[string]StorageScanCategory `json:"categories"`
	Owners      []OwnerStorage                 `json:"owners"`
}
type StorageScanCategory struct {
	Files               int64                    `json:"files"`
	Bytes               int64                    `json:"bytes"`
	AgeBuckets          map[string]StorageAmount `json:"ageBuckets"`
	MissingMetadata     int64                    `json:"missingMetadata"`
	UnreferencedFiles   int64                    `json:"unreferencedFiles"`
	UnreferencedBytes   int64                    `json:"unreferencedBytes"`
	DatabaseFiles       int64                    `json:"databaseFiles"`
	DatabaseBytes       int64                    `json:"databaseBytes"`
	MissingSamples      []string                 `json:"missingSamples,omitempty"`
	UnreferencedSamples []string                 `json:"unreferencedSamples,omitempty"`
}
type scanProgress struct {
	Categories map[string]StorageScanCategory `json:"categories"`
	Cursors    map[string]string              `json:"cursors"`
	Done       map[string]bool                `json:"done"`
	Owners     []OwnerStorage                 `json:"owners"`
}
type scanFile struct {
	category, path string
	size           int64
	modified       time.Time
}

var storageScanMu sync.Mutex

func (s *Service) StorageScan(ctx context.Context) (StorageScan, error) {
	var out StorageScan
	var raw []byte
	var started, completed sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT status,started_at,updated_at,completed_at,error,snapshot FROM conv_storage_scans WHERE id=1`).Scan(&out.Status, &started, &out.UpdatedAt, &completed, &out.Error, &raw)
	if err != nil {
		return out, err
	}
	if started.Valid {
		out.StartedAt = &started.Time
	}
	if completed.Valid {
		out.CompletedAt = &completed.Time
	}
	var progress scanProgress
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &progress)
	}
	if progress.Categories == nil {
		progress.Categories = map[string]StorageScanCategory{}
	}
	out.Categories = progress.Categories
	out.Owners = progress.Owners
	if out.Owners == nil {
		out.Owners = []OwnerStorage{}
	}
	if out.Status == "running" {
		s.scanMu.Lock()
		running := s.scanRunning
		s.scanMu.Unlock()
		if !running {
			out.Status = "interrupted"
		}
	}
	return out, nil
}

// StartStorageScan starts a bounded batch scan or resumes an interrupted one.
// A database advisory lock prevents two Core processes from walking the same roots.
func (s *Service) StartStorageScan(ctx context.Context) (StorageScan, error) {
	storageScanMu.Lock()
	defer storageScanMu.Unlock()
	s.scanMu.Lock()
	if s.scanRunning {
		s.scanMu.Unlock()
		return s.StorageScan(ctx)
	}
	s.scanRunning = true
	s.scanMu.Unlock()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		s.setScannerRunning(false)
		return StorageScan{}, err
	}
	var locked bool
	if err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended('101conv-storage-scan',0))`).Scan(&locked); err != nil || !locked {
		conn.Close()
		s.setScannerRunning(false)
		if err != nil {
			return StorageScan{}, err
		}
		return s.StorageScan(ctx)
	}
	var status string
	if err = conn.QueryRowContext(ctx, `SELECT status FROM conv_storage_scans WHERE id=1`).Scan(&status); err != nil {
		conn.Close()
		s.setScannerRunning(false)
		return StorageScan{}, err
	}
	if status == "complete" || status == "not_started" {
		if _, err = conn.ExecContext(ctx, `DELETE FROM conv_storage_scan_files`); err != nil {
			conn.Close()
			s.setScannerRunning(false)
			return StorageScan{}, err
		}
		p := scanProgress{Categories: map[string]StorageScanCategory{}, Cursors: map[string]string{}, Done: map[string]bool{}}
		b, _ := json.Marshal(p)
		_, err = conn.ExecContext(ctx, `UPDATE conv_storage_scans SET status='running',started_at=now(),updated_at=now(),completed_at=NULL,error='',snapshot=$1 WHERE id=1`, b)
	} else {
		_, err = conn.ExecContext(ctx, `UPDATE conv_storage_scans SET status='running',updated_at=now(),error='' WHERE id=1`)
	}
	if err != nil {
		conn.Close()
		s.setScannerRunning(false)
		return StorageScan{}, err
	}
	go func() {
		defer conn.Close()
		defer func() {
			_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended('101conv-storage-scan',0))`)
			s.setScannerRunning(false)
		}()
		s.runStorageScan(conn)
	}()
	return s.StorageScan(ctx)
}
func (s *Service) setScannerRunning(value bool) {
	s.scanMu.Lock()
	s.scanRunning = value
	s.scanMu.Unlock()
}

func (s *Service) runStorageScan(conn *sql.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	roots := map[string]string{"originals": s.uploadRoot, "results": s.resultRoot, "engines": filepath.Join(s.management.RootPath(), "engines")}
	var raw []byte
	var progress scanProgress
	if err := conn.QueryRowContext(ctx, `SELECT snapshot FROM conv_storage_scans WHERE id=1`).Scan(&raw); err != nil {
		s.failStorageScan(conn, err)
		return
	}
	_ = json.Unmarshal(raw, &progress)
	if progress.Categories == nil {
		progress.Categories = map[string]StorageScanCategory{}
	}
	if progress.Cursors == nil {
		progress.Cursors = map[string]string{}
	}
	if progress.Done == nil {
		progress.Done = map[string]bool{}
	}
	limit := int64(250000)
	for _, category := range []string{"originals", "results", "engines"} {
		if progress.Done[category] {
			continue
		}
		root := roots[category]
		rootInfo, rootErr := os.Lstat(root)
		if errors.Is(rootErr, os.ErrNotExist) {
			_, _ = conn.ExecContext(ctx, `DELETE FROM conv_storage_scan_files WHERE category=$1`, category)
			progress.Categories[category] = StorageScanCategory{}
			progress.Done[category] = true
			continue
		} else if rootErr != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
			if rootErr == nil {
				rootErr = errors.New("configured scan root is not a real directory")
			}
			s.failStorageScan(conn, rootErr)
			return
		}
		cursor := progress.Cursors[category]
		batch := make([]scanFile, 0, 256)
		var seen int64
		stopped := false
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			tx, e := conn.BeginTx(ctx, nil)
			if e != nil {
				return e
			}
			for _, f := range batch {
				if _, e = tx.ExecContext(ctx, `INSERT INTO conv_storage_scan_files(category,relative_path,size_bytes,modified_at) VALUES($1,$2,$3,$4) ON CONFLICT(category,relative_path) DO UPDATE SET size_bytes=EXCLUDED.size_bytes,modified_at=EXCLUDED.modified_at`, f.category, f.path, f.size, f.modified); e != nil {
					tx.Rollback()
					return e
				}
			}
			b, _ := json.Marshal(progress)
			if _, e = tx.ExecContext(ctx, `UPDATE conv_storage_scans SET snapshot=$1,updated_at=now() WHERE id=1`, b); e != nil {
				tx.Rollback()
				return e
			}
			return tx.Commit()
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == root {
				return nil
			}
			rel, e := filepath.Rel(root, path)
			if e != nil {
				return e
			}
			rel = filepath.ToSlash(rel)
			if cursor != "" && rel <= cursor {
				if entry.IsDir() && !strings.HasPrefix(cursor, rel+"/") && rel+"/" <= cursor {
					return filepath.SkipDir
				}
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			info, e := entry.Info()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			progress.Cursors[category] = rel
			stat := progress.Categories[category]
			stat.Files++
			stat.Bytes += info.Size()
			progress.Categories[category] = stat
			batch = append(batch, scanFile{category: category, path: rel, size: info.Size(), modified: info.ModTime()})
			seen++
			if len(batch) >= 256 {
				if e = flush(); e != nil {
					return e
				}
				batch = batch[:0]
			}
			if seen >= limit {
				stopped = true
				return fs.SkipAll
			}
			return nil
		})
		if e := flush(); e != nil {
			s.failStorageScan(conn, e)
			return
		}
		if err != nil && !errors.Is(err, fs.SkipAll) {
			s.failStorageScan(conn, err)
			return
		}
		if stopped {
			s.finishStorageScan(conn, progress, "incomplete", "")
			return
		}
		delete(progress.Cursors, category)
		progress.Done[category] = true
		b, _ := json.Marshal(progress)
		if _, err = conn.ExecContext(ctx, `UPDATE conv_storage_scans SET snapshot=$1,updated_at=now() WHERE id=1`, b); err != nil {
			s.failStorageScan(conn, err)
			return
		}
	}
	if err := s.reconcileStorageScan(ctx, conn, &progress); err != nil {
		s.failStorageScan(conn, err)
		return
	}
	s.finishStorageScan(conn, progress, "complete", "")
}

func (s *Service) reconcileStorageScan(ctx context.Context, conn *sql.Conn, p *scanProgress) error {
	queries := []struct{ category, missing, orphan string }{{"originals", `SELECT count(*) FROM conv_uploads u WHERE u.original_deleted_at IS NULL AND u.original_missing_at IS NULL AND NOT EXISTS(SELECT 1 FROM conv_storage_scan_files f WHERE f.category='originals' AND f.relative_path=u.id)`, `SELECT count(*),COALESCE(sum(f.size_bytes),0) FROM conv_storage_scan_files f WHERE f.category='originals' AND NOT EXISTS(SELECT 1 FROM conv_uploads u WHERE u.id=f.relative_path AND u.original_deleted_at IS NULL)`}, {"results", `SELECT count(*) FROM conv_jobs j WHERE j.result_key IS NOT NULL AND j.result_deleted_at IS NULL AND j.result_missing_at IS NULL AND NOT EXISTS(SELECT 1 FROM conv_storage_scan_files f WHERE f.category='results' AND f.relative_path=j.result_key)`, `SELECT count(*),COALESCE(sum(f.size_bytes),0) FROM conv_storage_scan_files f WHERE f.category='results' AND NOT EXISTS(SELECT 1 FROM conv_jobs j WHERE j.result_key=f.relative_path AND j.result_deleted_at IS NULL)`}}
	for _, q := range queries {
		var missing, orphan, orphanBytes int64
		if err := conn.QueryRowContext(ctx, q.missing).Scan(&missing); err != nil {
			return err
		}
		if err := conn.QueryRowContext(ctx, q.orphan).Scan(&orphan, &orphanBytes); err != nil {
			return err
		}
		cat := p.Categories[q.category]
		if q.category == "originals" {
			if err := conn.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(size_bytes),0) FROM conv_uploads WHERE original_deleted_at IS NULL AND original_missing_at IS NULL`).Scan(&cat.DatabaseFiles, &cat.DatabaseBytes); err != nil {
				return err
			}
		}
		if q.category == "results" {
			if err := conn.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(result_size),0) FROM conv_jobs WHERE result_key IS NOT NULL AND result_deleted_at IS NULL AND result_missing_at IS NULL`).Scan(&cat.DatabaseFiles, &cat.DatabaseBytes); err != nil {
				return err
			}
		}
		cat.MissingMetadata = missing
		cat.UnreferencedFiles = orphan
		cat.UnreferencedBytes = orphanBytes
		missingSQL, orphanSQL := ``, ` `
		if q.category == "originals" {
			missingSQL = `SELECT u.id FROM conv_uploads u WHERE u.original_deleted_at IS NULL AND u.original_missing_at IS NULL AND NOT EXISTS(SELECT 1 FROM conv_storage_scan_files f WHERE f.category='originals' AND f.relative_path=u.id) ORDER BY u.id LIMIT 20`
			orphanSQL = `SELECT f.relative_path FROM conv_storage_scan_files f WHERE f.category='originals' AND NOT EXISTS(SELECT 1 FROM conv_uploads u WHERE u.id=f.relative_path AND u.original_deleted_at IS NULL) ORDER BY f.relative_path LIMIT 20`
		}
		if q.category == "results" {
			missingSQL = `SELECT j.result_key FROM conv_jobs j WHERE j.result_key IS NOT NULL AND j.result_deleted_at IS NULL AND j.result_missing_at IS NULL AND NOT EXISTS(SELECT 1 FROM conv_storage_scan_files f WHERE f.category='results' AND f.relative_path=j.result_key) ORDER BY j.result_key LIMIT 20`
			orphanSQL = `SELECT f.relative_path FROM conv_storage_scan_files f WHERE f.category='results' AND NOT EXISTS(SELECT 1 FROM conv_jobs j WHERE j.result_key=f.relative_path AND j.result_deleted_at IS NULL) ORDER BY f.relative_path LIMIT 20`
		}
		var err error
		cat.MissingSamples, err = scanSamples(ctx, conn, missingSQL)
		if err != nil {
			return err
		}
		cat.UnreferencedSamples, err = scanSamples(ctx, conn, orphanSQL)
		if err != nil {
			return err
		}
		cat.AgeBuckets, err = storageAgeBuckets(ctx, conn, q.category)
		if err != nil {
			return err
		}
		p.Categories[q.category] = cat
	}
	engineBuckets, err := storageAgeBuckets(ctx, conn, "engines")
	if err != nil {
		return err
	}
	engineStats := p.Categories["engines"]
	engineStats.AgeBuckets = engineBuckets
	p.Categories["engines"] = engineStats
	rows, err := conn.QueryContext(ctx, `SELECT u.owner_id,count(*),COALESCE(sum(f.size_bytes),0) FROM conv_storage_scan_files f JOIN conv_uploads u ON f.category='originals' AND f.relative_path=u.id WHERE u.original_deleted_at IS NULL GROUP BY u.owner_id ORDER BY sum(f.size_bytes) DESC LIMIT 100`)
	if err != nil {
		return err
	}
	defer rows.Close()
	owners := []OwnerStorage{}
	for rows.Next() {
		var o OwnerStorage
		if err = rows.Scan(&o.OwnerID, &o.Count, &o.Bytes); err != nil {
			return err
		}
		owners = append(owners, o)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	p.Owners = owners
	resultRows, err := conn.QueryContext(ctx, `SELECT j.owner_id,count(*),COALESCE(sum(f.size_bytes),0) FROM conv_storage_scan_files f JOIN conv_jobs j ON f.category='results' AND f.relative_path=j.result_key WHERE j.result_deleted_at IS NULL AND j.result_missing_at IS NULL GROUP BY j.owner_id`)
	if err != nil {
		return err
	}
	defer resultRows.Close()
	byOwner := map[string]int{}
	for i := range p.Owners {
		byOwner[p.Owners[i].OwnerID] = i
	}
	for resultRows.Next() {
		var o OwnerStorage
		if err = resultRows.Scan(&o.OwnerID, &o.ResultCount, &o.ResultBytes); err != nil {
			return err
		}
		if i, ok := byOwner[o.OwnerID]; ok {
			p.Owners[i].ResultCount = o.ResultCount
			p.Owners[i].ResultBytes = o.ResultBytes
		} else {
			p.Owners = append(p.Owners, o)
		}
	}
	if err = resultRows.Err(); err != nil {
		return err
	}
	return nil
}

// Filesystem modification time is the only portable age signal available in a
// mounted volume. These buckets are measured, not inferred from DB row age.
func storageAgeBuckets(ctx context.Context, conn *sql.Conn, category string) (map[string]StorageAmount, error) {
	rows, err := conn.QueryContext(ctx, `SELECT CASE WHEN modified_at >= now()-interval '7 days' THEN '0-7 days' WHEN modified_at >= now()-interval '30 days' THEN '8-30 days' WHEN modified_at >= now()-interval '90 days' THEN '31-90 days' ELSE 'over 90 days' END,count(*),COALESCE(sum(size_bytes),0) FROM conv_storage_scan_files WHERE category=$1 GROUP BY 1`, category)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	buckets := map[string]StorageAmount{}
	for rows.Next() {
		var name string
		var amount StorageAmount
		if err = rows.Scan(&name, &amount.Count, &amount.Bytes); err != nil {
			return nil, err
		}
		buckets[name] = amount
	}
	return buckets, rows.Err()
}

func scanSamples(ctx context.Context, conn *sql.Conn, query string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
func (s *Service) finishStorageScan(conn *sql.Conn, p scanProgress, status, message string) {
	b, _ := json.Marshal(p)
	if status == "complete" {
		tx, err := conn.BeginTx(context.Background(), nil)
		if err != nil {
			return
		}
		if _, err = tx.Exec(`DELETE FROM conv_storage_scan_files`); err != nil {
			tx.Rollback()
			return
		}
		if _, err = tx.Exec(`UPDATE conv_storage_scans SET status=$1,snapshot=$2,error=$3,updated_at=now(),completed_at=now() WHERE id=1`, status, b, message); err != nil {
			tx.Rollback()
			return
		}
		_ = tx.Commit()
		return
	}
	_, _ = conn.ExecContext(context.Background(), `UPDATE conv_storage_scans SET status=$1,snapshot=$2,error=$3,updated_at=now(),completed_at=NULL WHERE id=1`, status, b, message)
}
func (s *Service) failStorageScan(conn *sql.Conn, err error) {
	_, _ = conn.ExecContext(context.Background(), `UPDATE conv_storage_scans SET status='interrupted',error=$1,updated_at=now() WHERE id=1`, err.Error())
}
