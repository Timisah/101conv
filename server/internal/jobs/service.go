package jobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/profile"
)

const (
	MaxInputBytes  = 100 << 20
	MaxOutputBytes = 200 << 20
	MaxAttempts    = 3
)

var (
	formatRE      = regexp.MustCompile(`^[a-z0-9]{1,12}$`)
	idempotencyRE = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,128}$`)
	fileIDRE      = regexp.MustCompile(`^[a-f0-9-]{36}$`)
	jobIDRE       = regexp.MustCompile(`^[a-f0-9-]{36}$`)
)

type Service struct {
	db         *sql.DB
	management *management.Service
	uploadRoot string
	resultRoot string
}

func (s *Service) UploadRoot() string { return s.uploadRoot }
func (s *Service) ResultRoot() string { return s.resultRoot }

// EngineHasActiveJobs prevents the management API from removing a managed
// binary version while queued or running jobs still hold that exact resolution.
func (s *Service) EngineHasActiveJobs(ctx context.Context, engineID, engineVersion string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_jobs WHERE state IN ('queued','running') AND execution->>'engineId'=$1 AND execution->>'engineVersion'=$2)`, engineID, engineVersion).Scan(&exists)
	return exists, err
}

func NewService(db *sql.DB, manager *management.Service, uploadRoot, resultRoot string) (*Service, error) {
	if db == nil || manager == nil || strings.TrimSpace(uploadRoot) == "" || strings.TrimSpace(resultRoot) == "" {
		return nil, fmt.Errorf("jobs require PostgreSQL, management services, and upload/result paths")
	}
	uploadRoot, err := filepath.Abs(uploadRoot)
	if err != nil {
		return nil, err
	}
	resultRoot, err = filepath.Abs(resultRoot)
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{uploadRoot, resultRoot} {
		if err = os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		info, e := os.Lstat(dir)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("job storage path must be a real directory: %s", dir)
		}
	}
	return &Service{db: db, management: manager, uploadRoot: uploadRoot, resultRoot: resultRoot}, nil
}

func (s *Service) Migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS conv_uploads (
 id TEXT PRIMARY KEY, file_name TEXT NOT NULL, extension TEXT NOT NULL,
 size_bytes BIGINT NOT NULL, sha256 TEXT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 consumed BOOLEAN NOT NULL DEFAULT false, owner_id TEXT NOT NULL DEFAULT 'admin'
);
CREATE TABLE IF NOT EXISTS conv_jobs (
 id TEXT PRIMARY KEY, upload_id TEXT NOT NULL REFERENCES conv_uploads(id),
 profile_id TEXT NOT NULL, profile_version TEXT NOT NULL,
 source_format TEXT NOT NULL, target_format TEXT NOT NULL,
 mode TEXT NOT NULL DEFAULT 'convert',
 execution JSONB NOT NULL, idempotency_key TEXT NOT NULL UNIQUE, request_hash TEXT NOT NULL,
 state TEXT NOT NULL, progress INTEGER NOT NULL DEFAULT 0,
 attempt_count INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 3,
 lease_owner TEXT, lease_until TIMESTAMPTZ, cancel_requested BOOLEAN NOT NULL DEFAULT false,
 error TEXT, result_key TEXT, result_size BIGINT NOT NULL DEFAULT 0,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 owner_id TEXT NOT NULL DEFAULT 'admin'
);
ALTER TABLE conv_uploads ADD COLUMN IF NOT EXISTS owner_id TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE conv_jobs ADD COLUMN IF NOT EXISTS owner_id TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE conv_jobs ADD COLUMN IF NOT EXISTS mode TEXT NOT NULL DEFAULT 'convert';
CREATE INDEX IF NOT EXISTS conv_jobs_owner_created_idx ON conv_jobs(owner_id, created_at DESC);
CREATE INDEX IF NOT EXISTS conv_jobs_claim_idx ON conv_jobs(state, lease_until, created_at);
CREATE TABLE IF NOT EXISTS conv_job_attempts (
 id BIGSERIAL PRIMARY KEY, job_id TEXT NOT NULL REFERENCES conv_jobs(id) ON DELETE CASCADE,
 attempt_number INTEGER NOT NULL, state TEXT NOT NULL, lease_owner TEXT NOT NULL,
 started_at TIMESTAMPTZ NOT NULL DEFAULT now(), finished_at TIMESTAMPTZ,
 exit_code INTEGER, stdout TEXT NOT NULL DEFAULT '', stderr TEXT NOT NULL DEFAULT '', error TEXT,
 UNIQUE(job_id, attempt_number)
);
CREATE TABLE IF NOT EXISTS conv_profile_preferences (
 scope TEXT NOT NULL CHECK (scope IN ('global','user')),
 owner_id TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL CHECK (mode IN ('convert','compress')),
 source_format TEXT NOT NULL, target_format TEXT NOT NULL, profile_id TEXT NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY(scope,owner_id,mode,source_format,target_format),
 CHECK ((scope='global' AND owner_id='') OR (scope='user' AND owner_id<>''))
);
`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func normalizePreference(mode, source, target string) (string, string, string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	source = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(source)), ".")
	target = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(target)), ".")
	if (mode != "convert" && mode != "compress") || !formatRE.MatchString(source) || !formatRE.MatchString(target) {
		return "", "", "", fmt.Errorf("%w: valid mode and source/target file formats are required", ErrInvalidInput)
	}
	return mode, source, target, nil
}

func (s *Service) storedPreference(ctx context.Context, scope, ownerID, mode, source, target string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT profile_id FROM conv_profile_preferences WHERE scope=$1 AND owner_id=$2 AND mode=$3 AND source_format=$4 AND target_format=$5`, scope, ownerID, mode, source, target).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Service) compatibleProfile(profileID, mode, source, target string) bool {
	d, _, _, _, err := s.management.ResolveConversion(profileID, source, target)
	return err == nil && d.SupportsMode(mode)
}

// ResolvePreferredProfile resolves user overrides, administrator defaults, and
// finally a stable profile-ID ordered fallback. Temporarily unavailable saved
// preferences remain stored and are reconsidered on every future resolution.
func (s *Service) ResolvePreferredProfile(ctx context.Context, ownerID, mode, source, target string) (string, string, error) {
	mode, source, target, err := normalizePreference(mode, source, target)
	if err != nil {
		return "", "", err
	}
	if ownerID != "" {
		if id, e := s.storedPreference(ctx, "user", ownerID, mode, source, target); e != nil {
			return "", "", e
		} else if id != "" && s.compatibleProfile(id, mode, source, target) {
			return id, "personal preference", nil
		}
	}
	if id, e := s.storedPreference(ctx, "global", "", mode, source, target); e != nil {
		return "", "", e
	} else if id != "" && s.compatibleProfile(id, mode, source, target) {
		return id, "administrator default", nil
	}

	records, err := s.management.ListProfiles(ctx)
	if err != nil {
		return "", "", err
	}
	ids := make([]string, 0, len(records))
	for _, rec := range records {
		if rec.Definition.SupportsMode(mode) && s.compatibleProfile(rec.Definition.ID, mode, source, target) {
			ids = append(ids, rec.Definition.ID)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return "", "", fmt.Errorf("%w: no enabled compatible %s profile is available for %s to %s", ErrUnsupported, mode, source, target)
	}
	return ids[0], "automatic fallback", nil
}

func (s *Service) SetProfilePreference(ctx context.Context, ownerID, mode, source, target, profileID string, global bool) error {
	mode, source, target, err := normalizePreference(mode, source, target)
	if err != nil {
		return err
	}
	profileID = strings.TrimSpace(profileID)
	if ownerID == "" || profileID == "" {
		return fmt.Errorf("%w: owner and profile are required", ErrInvalidInput)
	}
	if !s.compatibleProfile(profileID, mode, source, target) {
		return fmt.Errorf("%w: profile is disabled, incompatible, unavailable, or does not support this mapping", ErrUnsupported)
	}
	scope, keyOwner := "user", ownerID
	if global {
		scope, keyOwner = "global", ""
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO conv_profile_preferences(scope,owner_id,mode,source_format,target_format,profile_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(scope,owner_id,mode,source_format,target_format) DO UPDATE SET profile_id=EXCLUDED.profile_id,updated_at=now()`, scope, keyOwner, mode, source, target, profileID)
	return err
}

func (s *Service) DeleteProfilePreference(ctx context.Context, ownerID, mode, source, target string, global bool) error {
	mode, source, target, err := normalizePreference(mode, source, target)
	if err != nil {
		return err
	}
	scope, keyOwner := "user", ownerID
	if global {
		scope, keyOwner = "global", ""
	}
	if keyOwner == "" {
		return fmt.Errorf("%w: owner is required", ErrInvalidInput)
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM conv_profile_preferences WHERE scope=$1 AND owner_id=$2 AND mode=$3 AND source_format=$4 AND target_format=$5`, scope, keyOwner, mode, source, target)
	return err
}

func (s *Service) ListProfilePreferences(ctx context.Context, ownerID, mode string) ([]PreferenceMapping, error) {
	if mode != "" {
		mode = strings.ToLower(strings.TrimSpace(mode))
		if mode != "convert" && mode != "compress" {
			return nil, fmt.Errorf("%w: invalid preference mode", ErrInvalidInput)
		}
	}
	records, err := s.management.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	type mappingKey struct{ mode, source, target string }
	keys := map[mappingKey]bool{}
	for _, rec := range records {
		for _, rule := range rec.Definition.Rules {
			for _, src := range rule.Sources {
				for _, dst := range rule.Targets {
					for _, candidateMode := range []string{"convert", "compress"} {
						if (mode == "" || mode == candidateMode) && rec.Definition.SupportsMode(candidateMode) {
							keys[mappingKey{candidateMode, strings.TrimPrefix(strings.ToLower(src), "."), strings.TrimPrefix(strings.ToLower(dst), ".")}] = true
						}
					}
				}
			}
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT mode,source_format,target_format FROM conv_profile_preferences WHERE (scope='global' OR (scope='user' AND owner_id=$1)) AND ($2='' OR mode=$2)`, ownerID, mode)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k mappingKey
		if err = rows.Scan(&k.mode, &k.source, &k.target); err != nil {
			rows.Close()
			return nil, err
		}
		keys[k] = true
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	ordered := make([]mappingKey, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if a.mode != b.mode {
			return a.mode < b.mode
		}
		if a.source != b.source {
			return a.source < b.source
		}
		return a.target < b.target
	})
	out := make([]PreferenceMapping, 0, len(ordered))
	for _, k := range ordered {
		globalID, e := s.storedPreference(ctx, "global", "", k.mode, k.source, k.target)
		if e != nil {
			return nil, e
		}
		userID := ""
		if ownerID != "" {
			userID, e = s.storedPreference(ctx, "user", ownerID, k.mode, k.source, k.target)
			if e != nil {
				return nil, e
			}
		}
		m := PreferenceMapping{Mode: k.mode, SourceFormat: k.source, TargetFormat: k.target, GlobalProfileID: globalID, UserProfileID: userID, Alternatives: []ProfileChoice{}}
		for _, rec := range records {
			d := rec.Definition
			if !d.SupportsMode(k.mode) || !s.compatibleProfile(d.ID, k.mode, k.source, k.target) {
				continue
			}
			m.Alternatives = append(m.Alternatives, ProfileChoice{ID: d.ID, Name: d.Name, Version: d.Version})
		}
		sort.Slice(m.Alternatives, func(i, j int) bool { return m.Alternatives[i].ID < m.Alternatives[j].ID })
		m.GlobalAvailable = containsProfileChoice(m.Alternatives, globalID)
		m.UserAvailable = containsProfileChoice(m.Alternatives, userID)
		switch {
		case userID != "" && m.UserAvailable:
			m.EffectiveProfile = userID
			m.EffectiveSource = "personal preference"
		case globalID != "" && m.GlobalAvailable:
			m.EffectiveProfile = globalID
			m.EffectiveSource = "administrator default"
		case len(m.Alternatives) > 0:
			m.EffectiveProfile = m.Alternatives[0].ID
			m.EffectiveSource = "automatic fallback"
		default:
			m.EffectiveSource = "unavailable"
		}
		out = append(out, m)
	}
	return out, nil
}

func containsProfileChoice(choices []ProfileChoice, id string) bool {
	if id == "" {
		return false
	}
	for _, c := range choices {
		if c.ID == id {
			return true
		}
	}
	return false
}

func (s *Service) CreateUpload(ctx context.Context, fileName string, input io.Reader) (view Upload, retErr error) {
	return s.CreateUploadFor(ctx, "admin", MaxInputBytes, nil, fileName, input)
}

func (s *Service) CreateUploadFor(ctx context.Context, ownerID string, maxFileBytes int64, monthlyInputBytes *int64, fileName string, input io.Reader) (view Upload, retErr error) {
	if strings.TrimSpace(ownerID) == "" {
		return Upload{}, fmt.Errorf("upload owner is required")
	}
	if maxFileBytes <= 0 || maxFileBytes > MaxInputBytes {
		maxFileBytes = MaxInputBytes
	}
	name := filepath.Base(strings.ReplaceAll(strings.TrimSpace(fileName), `\`, "/"))
	if name == "" || name == "." || name == string(filepath.Separator) || strings.ContainsAny(name, "\r\n\x00") || len(name) > 255 {
		return Upload{}, fmt.Errorf("%w: invalid filename", ErrInvalidInput)
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(name)), ".")
	if !formatRE.MatchString(ext) {
		return Upload{}, fmt.Errorf("%w: file extension is required", ErrInvalidInput)
	}
	id, err := randomID()
	if err != nil {
		return Upload{}, err
	}
	tmp, err := os.CreateTemp(s.uploadRoot, ".upload-*.tmp")
	if err != nil {
		return Upload{}, err
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			if cleanupErr := os.Remove(tmpName); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("remove incomplete upload: %w", cleanupErr))
			}
		}
	}()
	hasher := sha256.New()
	limited := &io.LimitedReader{R: input, N: maxFileBytes + 1}
	size, copyErr := io.Copy(io.MultiWriter(tmp, hasher), limited)
	if copyErr != nil {
		tmp.Close()
		return Upload{}, copyErr
	}
	if size > maxFileBytes {
		tmp.Close()
		return Upload{}, ErrTooLarge
	}
	if size == 0 {
		tmp.Close()
		return Upload{}, fmt.Errorf("%w: empty upload", ErrInvalidInput)
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return Upload{}, err
	}
	if err = tmp.Close(); err != nil {
		return Upload{}, err
	}
	final := filepath.Join(s.uploadRoot, id)
	if err = os.Rename(tmpName, final); err != nil {
		return Upload{}, err
	}
	committed = true
	recorded := false
	defer func() {
		if !recorded {
			if cleanupErr := os.Remove(final); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				retErr = errors.Join(retErr, fmt.Errorf("remove unrecorded upload: %w", cleanupErr))
			}
		}
	}()
	view = Upload{ID: id, FileName: name, Extension: ext, Size: size, SHA256: hex.EncodeToString(hasher.Sum(nil)), CreatedAt: time.Now().UTC()}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Upload{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, ownerID); err != nil {
		return Upload{}, err
	}
	if monthlyInputBytes != nil {
		var used int64
		if err = tx.QueryRowContext(ctx, `SELECT COALESCE(sum(size_bytes),0) FROM conv_uploads WHERE owner_id=$1 AND created_at >= date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, ownerID).Scan(&used); err != nil {
			return Upload{}, err
		}
		if used+size > *monthlyInputBytes {
			return Upload{}, ErrQuotaExceeded
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO conv_uploads(id,file_name,extension,size_bytes,sha256,owner_id) VALUES($1,$2,$3,$4,$5,$6)`, view.ID, view.FileName, view.Extension, view.Size, view.SHA256, ownerID); err != nil {
		return Upload{}, err
	}
	if err = tx.Commit(); err != nil {
		return Upload{}, err
	}
	recorded = true
	return view, nil
}

func (s *Service) Submit(ctx context.Context, req SubmitRequest) (Job, bool, error) {
	return s.SubmitFor(ctx, req, "admin", Limits{})
}

type Limits struct {
	MonthlyJobs *int64
}

type MonthlyUsage struct {
	Uploads     int   `json:"uploads"`
	InputBytes  int64 `json:"inputBytes"`
	Conversions int   `json:"conversions"`
}

func (s *Service) MonthlyUsage(ctx context.Context, ownerID string) (MonthlyUsage, error) {
	var usage MonthlyUsage
	if err := s.db.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(size_bytes),0) FROM conv_uploads WHERE owner_id=$1 AND created_at>=date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, ownerID).Scan(&usage.Uploads, &usage.InputBytes); err != nil {
		return MonthlyUsage{}, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM conv_jobs WHERE owner_id=$1 AND created_at>=date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, ownerID).Scan(&usage.Conversions); err != nil {
		return MonthlyUsage{}, err
	}
	return usage, nil
}

func (s *Service) SubmitFor(ctx context.Context, req SubmitRequest, ownerID string, limits Limits) (Job, bool, error) {
	if strings.TrimSpace(ownerID) == "" {
		return Job{}, false, fmt.Errorf("job owner is required")
	}
	if !fileIDRE.MatchString(req.UploadID) || !idempotencyRE.MatchString(req.IdempotencyKey) {
		return Job{}, false, fmt.Errorf("%w: uploadId and an 8-128 character idempotencyKey are required", ErrInvalidInput)
	}
	target := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(req.TargetFormat)), ".")
	if !formatRE.MatchString(target) {
		return Job{}, false, fmt.Errorf("%w: targetFormat must be a file extension", ErrInvalidInput)
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "convert"
	}
	if mode != "convert" && mode != "compress" {
		return Job{}, false, fmt.Errorf("%w: mode must be convert or compress", ErrInvalidInput)
	}
	if mode == "convert" && len(req.Options) != 0 {
		return Job{}, false, fmt.Errorf("%w: options are only accepted for compression jobs", ErrInvalidInput)
	}
	var upload Upload
	err := s.db.QueryRowContext(ctx, `SELECT id,file_name,extension,size_bytes,sha256,created_at FROM conv_uploads WHERE id=$1 AND owner_id=$2`, req.UploadID, ownerID).Scan(&upload.ID, &upload.FileName, &upload.Extension, &upload.Size, &upload.SHA256, &upload.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, ErrNotFound
	}
	if err != nil {
		return Job{}, false, err
	}
	if req.ProfileID == "" {
		var err error
		req.ProfileID, _, err = s.ResolvePreferredProfile(ctx, ownerID, mode, upload.Extension, target)
		if err != nil {
			return Job{}, false, err
		}
	}
	profileDef, rule, engineDef, installation, err := s.management.ResolveConversion(req.ProfileID, upload.Extension, target)
	if err != nil {
		if strings.Contains(err.Error(), "does not support") {
			return Job{}, false, fmt.Errorf("%w: %v", ErrUnsupported, err)
		}
		return Job{}, false, err
	}
	if !profileDef.SupportsMode(mode) {
		return Job{}, false, fmt.Errorf("%w: %s profile %q cannot be used for %s jobs", ErrUnsupported, profileDef.Type, profileDef.ID, mode)
	}
	if installation.EngineID != rule.EngineID {
		return Job{}, false, fmt.Errorf("resolved engine does not match the selected profile rule")
	}
	var compressionArguments []string
	var targetSizeBytes int64
	var probeRelative string
	var audioBitrateKbps, minimumVideoKbps int
	if mode == "compress" {
		if profileDef.Compression == nil || len(rule.CompressionArguments) == 0 {
			return Job{}, false, fmt.Errorf("%w: profile does not provide compression for %s to %s", ErrUnsupported, upload.Extension, target)
		}
		var optionValues map[string]string
		optionValues, targetSizeBytes, err = validateCompressionOptions(*profileDef.Compression, req.Options)
		if err != nil {
			return Job{}, false, err
		}
		compressionArguments, err = expandCompressionOptions(rule.CompressionArguments, optionValues)
		if err != nil {
			return Job{}, false, err
		}
		if targetSizeBytes > 0 {
			probeRelative = engineDef.Executables[profileDef.Compression.ProbeExecutable]
			if !safeExecutableRelative(probeRelative) {
				return Job{}, false, fmt.Errorf("%w: compression probe executable is unavailable", ErrUnsupported)
			}
			audioBitrateKbps, minimumVideoKbps = profileDef.Compression.AudioBitrateKbps, profileDef.Compression.MinimumVideoKbps
		}
	}
	rel := engineDef.Executables[rule.Executable]
	if !safeExecutableRelative(rel) {
		return Job{}, false, fmt.Errorf("profile executable mapping is unsafe")
	}
	args, err := validateArguments(rule.Arguments)
	if err != nil {
		return Job{}, false, err
	}
	spec := ExecutionSpec{EngineID: installation.EngineID, EngineVersion: installation.Version, ExecutableName: rule.Executable, ExecutableRelativePath: rel, Arguments: args, SourceExtension: upload.Extension, TargetExtension: target, CompressionArguments: compressionArguments, TargetSizeBytes: targetSizeBytes, ProbeExecutableRelativePath: probeRelative, AudioBitrateKbps: audioBitrateKbps, MinimumVideoKbps: minimumVideoKbps, RequireSmaller: mode == "compress" && profileDef.Compression != nil && profileDef.Compression.RequireSmaller}
	specBytes, err := json.Marshal(spec)
	if err != nil {
		return Job{}, false, err
	}
	requestDigest, err := digestRequest(req.UploadID, req.ProfileID+"@"+profileDef.Version+"#"+mode, upload.Extension, target, specBytes)
	if err != nil {
		return Job{}, false, err
	}
	if existing, e := s.getByIdempotency(ctx, req.IdempotencyKey); e == nil {
		if existing.requestHash != requestDigest {
			return Job{}, false, fmt.Errorf("%w: idempotency key was used for a different request", ErrConflict)
		}
		return existing.job, true, nil
	} else if !errors.Is(e, ErrNotFound) {
		return Job{}, false, e
	}
	id, err := randomID()
	if err != nil {
		return Job{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, ownerID); err != nil {
		return Job{}, false, err
	}
	var consumed bool
	var storedOwner string
	if err = tx.QueryRowContext(ctx, `SELECT owner_id,consumed FROM conv_uploads WHERE id=$1 FOR UPDATE`, req.UploadID).Scan(&storedOwner, &consumed); errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, ErrNotFound
	} else if err != nil {
		return Job{}, false, err
	}
	if storedOwner != ownerID {
		return Job{}, false, ErrNotFound
	}
	if consumed {
		if existing, getErr := s.getByIdempotency(ctx, req.IdempotencyKey); getErr == nil && existing.requestHash == requestDigest {
			return existing.job, true, nil
		}
		return Job{}, false, fmt.Errorf("%w: upload has already been submitted", ErrConflict)
	}
	if limits.MonthlyJobs != nil {
		var jobCount int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM conv_jobs j WHERE j.owner_id=$1 AND j.created_at >= date_trunc('month',now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`, ownerID).Scan(&jobCount); err != nil {
			return Job{}, false, err
		}
		if int64(jobCount) >= *limits.MonthlyJobs {
			return Job{}, false, ErrQuotaExceeded
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO conv_jobs(id,upload_id,profile_id,profile_version,source_format,target_format,mode,execution,idempotency_key,request_hash,state,max_attempts,owner_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'queued',$11,$12)`, id, upload.ID, req.ProfileID, profileDef.Version, upload.Extension, target, mode, specBytes, req.IdempotencyKey, requestDigest, MaxAttempts, ownerID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if existing, getErr := s.getByIdempotency(ctx, req.IdempotencyKey); getErr == nil && existing.requestHash == requestDigest {
				return existing.job, true, nil
			}
			return Job{}, false, fmt.Errorf("%w: idempotency key already exists", ErrConflict)
		}
		return Job{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_uploads SET consumed=true WHERE id=$1`, upload.ID); err != nil {
		return Job{}, false, err
	}
	if err = tx.Commit(); err != nil {
		if existing, getErr := s.getByIdempotency(ctx, req.IdempotencyKey); getErr == nil && existing.requestHash == requestDigest {
			return existing.job, true, nil
		}
		return Job{}, false, err
	}
	job, err := s.GetFor(ctx, id, ownerID)
	return job, false, err
}

func validateArguments(arguments []string) ([]string, error) {
	if err := profile.ValidateWorkerArguments(arguments); err != nil {
		return nil, fmt.Errorf("%w: invalid profile arguments: %v", ErrInvalidInput, err)
	}
	return append([]string(nil), arguments...), nil
}

func safeExecutableRelative(rel string) bool {
	return rel != "" && !filepath.IsAbs(rel) && !strings.Contains(rel, `\`) && filepath.Clean(rel) == rel && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validateCompressionOptions(config profile.CompressionConfig, requested map[string]string) (map[string]string, int64, error) {
	definitions := make(map[string]profile.OptionDefinition, len(config.Options))
	for _, option := range config.Options {
		definitions[option.Key] = option
	}
	for key := range requested {
		if _, ok := definitions[key]; !ok {
			return nil, 0, fmt.Errorf("%w: unknown compression option %q", ErrInvalidInput, key)
		}
	}
	values := make(map[string]string, len(config.Options))
	var targetBytes int64
	for _, option := range config.Options {
		value := option.Default
		if supplied, ok := requested[option.Key]; ok {
			value = strings.TrimSpace(supplied)
		}
		switch option.Type {
		case "number":
			number, err := strconv.ParseFloat(value, 64)
			if option.Min == nil || option.Max == nil {
				return nil, 0, fmt.Errorf("%w: profile has invalid numeric option metadata", ErrInvalidInput)
			}
			if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number < *option.Min || number > *option.Max {
				return nil, 0, fmt.Errorf("%w: option %q must be between %v and %v", ErrInvalidInput, option.Key, *option.Min, *option.Max)
			}
			if option.Step != nil && math.Mod(number-*option.Min, *option.Step) > 1e-9 {
				return nil, 0, fmt.Errorf("%w: option %q does not match its allowed step", ErrInvalidInput, option.Key)
			}
			if option.TargetSizeMB {
				bytes := number * 1024 * 1024
				if bytes < 1 || bytes > float64(math.MaxInt64) {
					return nil, 0, fmt.Errorf("%w: invalid target size", ErrInvalidInput)
				}
				targetBytes = int64(math.Floor(bytes))
			}
			value = strconv.FormatFloat(number, 'f', -1, 64)
		case "select":
			found := false
			for _, choice := range option.Choices {
				if value == choice {
					found = true
					break
				}
			}
			if !found {
				return nil, 0, fmt.Errorf("%w: invalid value for option %q", ErrInvalidInput, option.Key)
			}
		default:
			return nil, 0, fmt.Errorf("%w: unsupported compression option type", ErrInvalidInput)
		}
		if strings.ContainsRune(value, '\x00') {
			return nil, 0, fmt.Errorf("%w: invalid option value", ErrInvalidInput)
		}
		values[option.Key] = value
	}
	return values, targetBytes, nil
}

func expandCompressionOptions(arguments []string, values map[string]string) ([]string, error) {
	result := make([]string, len(arguments))
	for i, arg := range arguments {
		if strings.HasPrefix(arg, "{option:") && strings.HasSuffix(arg, "}") {
			key := strings.TrimSuffix(strings.TrimPrefix(arg, "{option:"), "}")
			value, ok := values[key]
			if !ok {
				return nil, fmt.Errorf("%w: missing compression option %q", ErrInvalidInput, key)
			}
			result[i] = value
			continue
		}
		unwrapped := arg
		for _, token := range []string{"{compression.videoKbps}", "{compression.audioKbps}", "{compression.bufferKbps}"} {
			unwrapped = strings.ReplaceAll(unwrapped, token, "0")
		}
		if strings.ContainsAny(unwrapped, "{}") {
			return nil, fmt.Errorf("%w: unsupported compression argument placeholder", ErrInvalidInput)
		}
		result[i] = arg
	}
	return result, nil
}

type idemRecord struct {
	job         Job
	requestHash string
}

func (s *Service) getByIdempotency(ctx context.Context, key string) (idemRecord, error) {
	var r idemRecord
	err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+`,request_hash FROM conv_jobs WHERE idempotency_key=$1`, key), &r.job, &r.requestHash)
	if errors.Is(err, sql.ErrNoRows) {
		return idemRecord{}, ErrNotFound
	}
	return r, err
}

const jobColumns = `id,profile_id,profile_version,source_format,target_format,mode,state,progress,attempt_count,max_attempts,COALESCE(error,''),result_size,created_at,updated_at`

type rowScanner interface{ Scan(...any) error }

func scanJob(row rowScanner, job *Job, extra ...any) error {
	args := []any{&job.ID, &job.ProfileID, &job.ProfileVer, &job.SourceFormat, &job.TargetFormat, &job.Mode, &job.State, &job.Progress, &job.Attempt, &job.MaxAttempts, &job.Error, &job.OutputSize, &job.CreatedAt, &job.UpdatedAt}
	args = append(args, extra...)
	return row.Scan(args...)
}

func (s *Service) Get(ctx context.Context, id string) (Job, error) {
	return s.GetFor(ctx, id, "")
}

func (s *Service) GetFor(ctx context.Context, id, ownerID string) (Job, error) {
	if !jobIDRE.MatchString(id) {
		return Job{}, ErrNotFound
	}
	var job Job
	err := scanJob(s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM conv_jobs WHERE id=$1 AND ($2='' OR owner_id=$2)`, id, ownerID), &job)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return job, err
}

type ListFilter struct {
	Status  string
	Query   string
	Limit   int
	Offset  int
	OwnerID string
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Job, int, error) {
	if filter.Limit < 1 || filter.Limit > 100 {
		filter.Limit = 50
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	status := strings.TrimSpace(filter.Status)
	switch State(status) {
	case "", Queued, Running, Succeeded, Failed, Cancelled:
	default:
		return nil, 0, fmt.Errorf("%w: unsupported job status", ErrInvalidInput)
	}
	query := strings.TrimSpace(filter.Query)
	if len(query) > 120 {
		query = query[:120]
	}
	pattern := "%" + strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(query, `\`, `\\`), `%`, `\%`), `_`, `\_`) + "%"
	var total int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM conv_jobs WHERE ($1='' OR state=$1) AND ($2='' OR id ILIKE $3 ESCAPE E'\\' OR profile_id ILIKE $3 ESCAPE E'\\' OR source_format ILIKE $3 ESCAPE E'\\' OR target_format ILIKE $3 ESCAPE E'\\') AND ($4='' OR owner_id=$4)`, status, query, pattern, filter.OwnerID).Scan(&total)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM conv_jobs WHERE ($1='' OR state=$1) AND ($2='' OR id ILIKE $3 ESCAPE E'\\' OR profile_id ILIKE $3 ESCAPE E'\\' OR source_format ILIKE $3 ESCAPE E'\\' OR target_format ILIKE $3 ESCAPE E'\\') AND ($6='' OR owner_id=$6) ORDER BY created_at DESC LIMIT $4 OFFSET $5`, status, query, pattern, filter.Limit, filter.Offset, filter.OwnerID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	jobs := make([]Job, 0)
	for rows.Next() {
		var job Job
		if err = scanJob(rows, &job); err != nil {
			return nil, 0, err
		}
		jobs = append(jobs, job)
	}
	return jobs, total, rows.Err()
}

func (s *Service) Detail(ctx context.Context, id string) (JobDetail, error) {
	return s.DetailFor(ctx, id, "")
}

func (s *Service) DetailFor(ctx context.Context, id, ownerID string) (JobDetail, error) {
	job, err := s.GetFor(ctx, id, ownerID)
	if err != nil {
		return JobDetail{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT attempt_number,state,started_at,COALESCE(finished_at,'epoch'::timestamptz),exit_code,stdout,stderr,COALESCE(error,'') FROM conv_job_attempts WHERE job_id=$1 ORDER BY attempt_number`, id)
	if err != nil {
		return JobDetail{}, err
	}
	defer rows.Close()
	attempts := []Attempt{}
	for rows.Next() {
		var a Attempt
		var finished sql.NullTime
		var code sql.NullInt64
		if err = rows.Scan(&a.Number, &a.State, &a.StartedAt, &finished, &code, &a.Stdout, &a.Stderr, &a.Error); err != nil {
			return JobDetail{}, err
		}
		if finished.Valid {
			a.Finished = finished.Time
		}
		if code.Valid {
			v := int(code.Int64)
			a.ExitCode = &v
		}
		attempts = append(attempts, a)
	}
	return JobDetail{Job: job, Attempts: attempts}, rows.Err()
}

func (s *Service) Cancel(ctx context.Context, id string) (Job, error) {
	return s.CancelFor(ctx, id, "")
}

func (s *Service) CancelFor(ctx context.Context, id, ownerID string) (Job, error) {
	if !jobIDRE.MatchString(id) {
		return Job{}, ErrNotFound
	}
	_, err := s.db.ExecContext(ctx, `UPDATE conv_jobs SET state=CASE WHEN state='queued' THEN 'cancelled' ELSE state END,cancel_requested=CASE WHEN state='running' THEN true ELSE cancel_requested END,updated_at=now() WHERE id=$1 AND ($2='' OR owner_id=$2) AND state IN ('queued','running')`, id, ownerID)
	if err != nil {
		return Job{}, err
	}
	return s.GetFor(ctx, id, ownerID)
}

func (s *Service) OpenResult(ctx context.Context, id string) (*os.File, Job, string, error) {
	return s.OpenResultFor(ctx, id, "")
}

func (s *Service) OpenResultFor(ctx context.Context, id, ownerID string) (*os.File, Job, string, error) {
	job, err := s.GetFor(ctx, id, ownerID)
	if err != nil {
		return nil, Job{}, "", err
	}
	if job.State != Succeeded {
		return nil, Job{}, "", fmt.Errorf("%w: result is not ready", ErrConflict)
	}
	var key string
	if err = s.db.QueryRowContext(ctx, `SELECT COALESCE(result_key,'') FROM conv_jobs WHERE id=$1`, id).Scan(&key); err != nil {
		return nil, Job{}, "", err
	}
	if key == "" || !regexp.MustCompile(`^[a-f0-9-]{36}-[0-9]{1,6}\.bin$`).MatchString(key) {
		return nil, Job{}, "", fmt.Errorf("result record is invalid")
	}
	path := filepath.Join(s.resultRoot, key)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, Job{}, "", err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > MaxOutputBytes {
		return nil, Job{}, "", fmt.Errorf("result file failed validation")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, Job{}, "", err
	}
	opened, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, Job{}, "", err
	}
	if !opened.Mode().IsRegular() || opened.Size() != job.OutputSize || opened.Size() > MaxOutputBytes {
		f.Close()
		return nil, Job{}, "", fmt.Errorf("result file changed after validation")
	}
	return f, job, mime.TypeByExtension("." + job.TargetFormat), nil
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	x := hex.EncodeToString(b[:])
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:], nil
}
func digestRequest(uploadID, profileID, source, target string, spec []byte) (string, error) {
	b, err := json.Marshal([]any{uploadID, profileID, source, target, json.RawMessage(spec)})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
