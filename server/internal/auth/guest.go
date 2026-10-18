package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
)

const GuestOwnerPrefix = "guest:"

// AnonymousSettings contains public guest limits and administrator-only
// controls. Defaults intentionally keep guest access disabled; completing the
// first-run setup enables it only for a newly initialized instance.
type AnonymousSettings struct {
	Enabled                    bool      `json:"enabled"`
	AllowConversion            bool      `json:"allowConversion"`
	AllowCompression           bool      `json:"allowCompression"`
	MaxFileBytes               int64     `json:"maxFileBytes"`
	MaxBatchFiles              int       `json:"maxBatchFiles"`
	MaxSimultaneousJobs        int       `json:"maxSimultaneousJobs"`
	UploadsPerMinute           int       `json:"uploadsPerMinute"`
	SubmissionsPerMinute       int       `json:"submissionsPerMinute"`
	GlobalUploadsPerMinute     int       `json:"globalUploadsPerMinute"`
	GlobalSubmissionsPerMinute int       `json:"globalSubmissionsPerMinute"`
	MaxGlobalJobs              int       `json:"maxGlobalJobs"`
	MaxGlobalStorageBytes      int64     `json:"maxGlobalStorageBytes"`
	ResultExpirationHours      int       `json:"resultExpirationHours"`
	SessionExpirationHours     int       `json:"sessionExpirationHours"`
	UploadExpirationHours      int       `json:"uploadExpirationHours"`
	FailedExpirationHours      int       `json:"failedExpirationHours"`
	MetadataExpirationHours    int       `json:"metadataExpirationHours"`
	UpdatedAt                  time.Time `json:"updatedAt"`
}

func (s *Service) AnonymousSettings(ctx context.Context) (AnonymousSettings, error) {
	var v AnonymousSettings
	err := s.db.QueryRowContext(ctx, `SELECT enabled,allow_conversion,allow_compression,max_file_bytes,max_batch_files,max_guest_jobs,uploads_per_minute,submissions_per_minute,global_uploads_per_minute,global_submissions_per_minute,max_global_jobs,max_global_storage_bytes,result_expiration_hours,session_expiration_hours,upload_expiration_hours,failed_expiration_hours,metadata_expiration_hours,updated_at FROM conv_anonymous_settings WHERE id=1`).Scan(
		&v.Enabled, &v.AllowConversion, &v.AllowCompression, &v.MaxFileBytes, &v.MaxBatchFiles, &v.MaxSimultaneousJobs,
		&v.UploadsPerMinute, &v.SubmissionsPerMinute, &v.GlobalUploadsPerMinute, &v.GlobalSubmissionsPerMinute, &v.MaxGlobalJobs, &v.MaxGlobalStorageBytes,
		&v.ResultExpirationHours, &v.SessionExpirationHours, &v.UploadExpirationHours, &v.FailedExpirationHours,
		&v.MetadataExpirationHours, &v.UpdatedAt,
	)
	return v, err
}

func (s *Service) SetAnonymousSettings(ctx context.Context, v AnonymousSettings) error {
	if v.MaxFileBytes < 1 || v.MaxFileBytes > 100<<20 || v.MaxBatchFiles < 1 || v.MaxBatchFiles > 20 ||
		v.MaxSimultaneousJobs < 1 || v.MaxSimultaneousJobs > 100 || v.UploadsPerMinute < 1 || v.UploadsPerMinute > 120 ||
		v.SubmissionsPerMinute < 1 || v.SubmissionsPerMinute > 60 || v.MaxGlobalJobs < 1 || v.MaxGlobalJobs > 1000 ||
		v.GlobalUploadsPerMinute < 1 || v.GlobalUploadsPerMinute > 10000 || v.GlobalSubmissionsPerMinute < 1 || v.GlobalSubmissionsPerMinute > 10000 ||
		v.MaxGlobalStorageBytes < 10<<20 || v.MaxGlobalStorageBytes > 1<<40 || v.MaxGlobalStorageBytes < v.MaxFileBytes+(200<<20) ||
		v.ResultExpirationHours < 1 || v.ResultExpirationHours > 720 || v.SessionExpirationHours < 1 || v.SessionExpirationHours > 720 ||
		v.UploadExpirationHours < 1 || v.UploadExpirationHours > 720 || v.FailedExpirationHours < 1 || v.FailedExpirationHours > 720 ||
		v.MetadataExpirationHours < 1 || v.MetadataExpirationHours > 8760 {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE conv_anonymous_settings SET enabled=$1,allow_conversion=$2,allow_compression=$3,max_file_bytes=$4,max_batch_files=$5,max_guest_jobs=$6,uploads_per_minute=$7,submissions_per_minute=$8,global_uploads_per_minute=$9,global_submissions_per_minute=$10,max_global_jobs=$11,max_global_storage_bytes=$12,result_expiration_hours=$13,session_expiration_hours=$14,upload_expiration_hours=$15,failed_expiration_hours=$16,metadata_expiration_hours=$17,updated_at=now() WHERE id=1`,
		v.Enabled, v.AllowConversion, v.AllowCompression, v.MaxFileBytes, v.MaxBatchFiles, v.MaxSimultaneousJobs,
		v.UploadsPerMinute, v.SubmissionsPerMinute, v.GlobalUploadsPerMinute, v.GlobalSubmissionsPerMinute, v.MaxGlobalJobs, v.MaxGlobalStorageBytes,
		v.ResultExpirationHours, v.SessionExpirationHours, v.UploadExpirationHours, v.FailedExpirationHours, v.MetadataExpirationHours,
	)
	if err != nil {
		return err
	}
	// Tightening session lifetime applies immediately. Increasing it never
	// extends a credential that was already issued.
	if _, err = tx.ExecContext(ctx, `UPDATE conv_guest_sessions SET expires_at=LEAST(expires_at,created_at+($1::int*interval '1 hour'))`, v.SessionExpirationHours); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateGuestSession returns an opaque cookie credential and a unique owner.
// Only a digest of the credential is persisted, so database read access alone
// cannot be used to impersonate a guest.
func (s *Service) CreateGuestSession(ctx context.Context) (Principal, string, time.Time, AnonymousSettings, error) {
	settings, err := s.AnonymousSettings(ctx)
	if err != nil {
		return Principal{}, "", time.Time{}, settings, err
	}
	if !settings.Enabled {
		return Principal{}, "", time.Time{}, settings, ErrDisabled
	}
	setup, err := s.InitialSetupStatus(ctx)
	if err != nil {
		return Principal{}, "", time.Time{}, settings, err
	}
	if !setup.Completed || !setup.AdministratorCreated {
		return Principal{}, "", time.Time{}, settings, ErrDisabled
	}
	allowed, err := s.ConsumeGuestRate(ctx, "*", "session-create", 1, 60)
	if err != nil {
		return Principal{}, "", time.Time{}, settings, err
	}
	if !allowed {
		return Principal{}, "", time.Time{}, settings, ErrRateLimited
	}
	var tokenBytes [32]byte
	if _, err = rand.Read(tokenBytes[:]); err != nil {
		return Principal{}, "", time.Time{}, settings, err
	}
	id, err := randomID()
	if err != nil {
		return Principal{}, "", time.Time{}, settings, err
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes[:])
	expires := time.Now().UTC().Add(time.Duration(settings.SessionExpirationHours) * time.Hour)
	digest := sha256.Sum256(tokenBytes[:])
	if _, err = s.db.ExecContext(ctx, `INSERT INTO conv_guest_sessions(id,token_hash,expires_at) VALUES($1,$2,$3)`, id, digest[:], expires); err != nil {
		return Principal{}, "", time.Time{}, settings, err
	}
	return guestPrincipal(id, settings), token, expires, settings, nil
}

func (s *Service) AuthenticateGuest(ctx context.Context, token string) (Principal, AnonymousSettings, error) {
	var p Principal
	var id string
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return Principal{}, AnonymousSettings{}, ErrInvalidCredentials
	}
	settings, err := s.AnonymousSettings(ctx)
	if err != nil {
		return Principal{}, settings, err
	}
	if !settings.Enabled {
		return Principal{}, settings, ErrDisabled
	}
	digest := sha256.Sum256(decoded)
	if err = s.db.QueryRowContext(ctx, `SELECT id FROM conv_guest_sessions WHERE token_hash=$1 AND expires_at>now()`, digest[:]).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return Principal{}, settings, ErrInvalidCredentials
	} else if err != nil {
		return Principal{}, settings, err
	}
	p = guestPrincipal(id, settings)
	return p, settings, nil
}

func guestPrincipal(id string, settings AnonymousSettings) Principal {
	permissions := map[string]bool{
		PermissionConversion:  settings.AllowConversion,
		PermissionCompression: settings.AllowCompression,
		PermissionJobsRead:    true,
		PermissionJobsCancel:  true,
	}
	return Principal{
		ID: GuestOwnerPrefix + id, Username: "Guest", Role: "guest", Guest: true,
		CanConvert: settings.AllowConversion, CanReadJobs: true,
		MaxFileBytes: settings.MaxFileBytes, Permissions: permissions,
		Quotas: Quotas{MaxFileBytes: QuotaLimit{Mode: "limited", Value: &settings.MaxFileBytes}, MonthlyJobs: QuotaLimit{Mode: "unlimited"}, MonthlyBytes: QuotaLimit{Mode: "unlimited"}},
	}
}

func (s *Service) RevokeGuestSession(ctx context.Context, token string) error {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return nil
	}
	digest := sha256.Sum256(decoded)
	_, err = s.db.ExecContext(ctx, `DELETE FROM conv_guest_sessions WHERE token_hash=$1`, digest[:])
	return err
}

// ConsumeGuestRate records both per-session and instance-wide fixed-minute
// buckets in one transaction. Identity is the random guest owner, never a
// client-supplied or forwarded IP address.
func (s *Service) ConsumeGuestRate(ctx context.Context, ownerID, action string, perGuest, global int) (bool, error) {
	if (ownerID != "*" && !strings.HasPrefix(ownerID, GuestOwnerPrefix)) || action == "" || perGuest < 1 || global < 1 {
		return false, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var guestCount, globalCount int
	query := `INSERT INTO conv_guest_rate_buckets(guest_id,action,bucket,count) VALUES($1,$2,date_trunc('minute',now()),1) ON CONFLICT(guest_id,action,bucket) DO UPDATE SET count=conv_guest_rate_buckets.count+1 RETURNING count`
	if err = tx.QueryRowContext(ctx, query, ownerID, action).Scan(&guestCount); err != nil {
		return false, err
	}
	if ownerID == "*" {
		globalCount = guestCount
	} else {
		if err = tx.QueryRowContext(ctx, query, "*", action).Scan(&globalCount); err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return (ownerID == "*" || guestCount <= perGuest) && globalCount <= global, nil
}

func (s *Service) PurgeExpiredGuestSessions(ctx context.Context) error {
	for _, query := range []string{
		`WITH expired AS (SELECT ctid FROM conv_guest_sessions WHERE expires_at<now()-interval '7 days' ORDER BY expires_at LIMIT 1000) DELETE FROM conv_guest_sessions s USING expired e WHERE s.ctid=e.ctid`,
		`WITH expired AS (SELECT ctid FROM conv_guest_rate_buckets WHERE bucket<now()-interval '10 minutes' ORDER BY bucket LIMIT 1000) DELETE FROM conv_guest_rate_buckets b USING expired e WHERE b.ctid=e.ctid`,
	} {
		for i := 0; i < 10; i++ {
			result, err := s.db.ExecContext(ctx, query)
			if err != nil {
				return fmt.Errorf("purge expired guest credentials and rate buckets: %w", err)
			}
			deleted, err := result.RowsAffected()
			if err != nil || deleted < 1000 {
				break
			}
		}
	}
	return nil
}
