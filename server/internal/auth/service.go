// Package auth stores local accounts and revocable API credentials.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	RoleAdmin = "admin"
	RoleUser  = "user"

	ScopeUpload                  = "conversion:upload"
	ScopeSubmit                  = "conversion:submit"
	ScopeRead                    = "jobs:read"
	ScopeCancel                  = "jobs:cancel"
	ScopeResult                  = "results:download"
	ScopeCompress                = "compression:submit"
	PermissionConversion         = "conversion.use"
	PermissionCompression        = "compression.use"
	PermissionJobsRead           = "jobs.read"
	PermissionJobsCancel         = "jobs.cancel"
	PermissionConvertersManage   = "converters.manage"
	PermissionRepositoriesManage = "repositories.manage"
	PermissionUsersManage        = "users.manage"
	PermissionGroupsManage       = "groups.manage"
	PermissionRolesManage        = "roles.manage"
	PermissionAPIKeysManage      = "api_keys.manage"
	PermissionDrivesManage       = "drives.manage"
	PermissionAutomationsManage  = "automations.manage"
	PermissionAutomationsExecute = "automations.execute"
	PermissionAutomationsRead    = "automations.read"
)

var PermissionNames = []string{PermissionConversion, PermissionCompression, PermissionJobsRead, PermissionJobsCancel, PermissionConvertersManage, PermissionRepositoriesManage, PermissionUsersManage, PermissionGroupsManage, PermissionRolesManage, PermissionAPIKeysManage, PermissionDrivesManage, PermissionAutomationsManage, PermissionAutomationsExecute, PermissionAutomationsRead}

type QuotaLimit struct {
	Mode  string `json:"mode"`
	Value *int64 `json:"value,omitempty"`
}
type Quotas struct {
	MaxFileBytes QuotaLimit `json:"maxFileBytes"`
	MonthlyJobs  QuotaLimit `json:"monthlyJobs"`
	MonthlyBytes QuotaLimit `json:"monthlyBytes"`
}
type PermissionRole struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Permissions map[string]bool `json:"permissions"`
	BuiltIn     bool            `json:"builtIn"`
}
type Group struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	RoleID      string          `json:"roleId,omitempty"`
	Permissions map[string]bool `json:"permissions"`
	Quotas      Quotas          `json:"quotas"`
	MemberCount int             `json:"memberCount"`
	CreatedAt   time.Time       `json:"createdAt"`
}
type PermissionOrigin struct {
	Source string `json:"source"`
	Effect bool   `json:"effect"`
}

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrNotFound           = errors.New("account or API key not found")
	ErrConflict           = errors.New("account already exists")
	ErrInvalidInput       = errors.New("invalid account or API key input")
	ErrDisabled           = errors.New("account is disabled")
	ErrRateLimited        = errors.New("anonymous request rate limit exceeded")
	usernamePattern       = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{2,63}$`)
	keyNamePattern        = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,80}$`)
)

type Principal struct {
	ID                string                        `json:"id"`
	Username          string                        `json:"username"`
	Role              string                        `json:"role"`
	RoleID            string                        `json:"roleId"`
	CanConvert        bool                          `json:"canConvert"`
	CanReadJobs       bool                          `json:"canReadJobs"`
	MaxFileBytes      int64                         `json:"maxFileBytes"`
	MonthlyJobs       int                           `json:"monthlyJobs"`
	MonthlyBytes      int64                         `json:"monthlyBytes"`
	Scopes            []string                      `json:"scopes,omitempty"`
	Permissions       map[string]bool               `json:"permissions"`
	PermissionOrigins map[string][]PermissionOrigin `json:"permissionOrigins"`
	Groups            []string                      `json:"groups"`
	GroupIDs          []string                      `json:"groupIds"`
	Quotas            Quotas                        `json:"quotas"`
	APIKey            bool                          `json:"-"`
	Guest             bool                          `json:"guest,omitempty"`
}

func AdminPrincipal() Principal {
	max := int64(100 << 20)
	return Principal{ID: "admin", Username: "Administrator", Role: RoleAdmin, CanConvert: true, CanReadJobs: true, MaxFileBytes: max, Permissions: map[string]bool{}, Quotas: Quotas{MaxFileBytes: QuotaLimit{Mode: "limited", Value: &max}, MonthlyJobs: QuotaLimit{Mode: "unlimited"}, MonthlyBytes: QuotaLimit{Mode: "unlimited"}}}
}

func (p Principal) IsAdmin() bool { return p.Role == RoleAdmin }

type User struct {
	Principal
	Enabled   bool             `json:"enabled"`
	CreatedAt time.Time        `json:"createdAt"`
	Usage     map[string]int64 `json:"usage,omitempty"`
}

type APIKey struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	CreatedAt time.Time  `json:"createdAt"`
	LastUsed  *time.Time `json:"lastUsed,omitempty"`
}

type CreatedAPIKey struct {
	APIKey
	Token string `json:"token"`
}

type NewUser struct {
	Username            string
	Password            string
	RoleID              string
	PermissionOverrides map[string]*bool
	Quotas              *Quotas
}

type UserUpdate struct {
	Enabled             *bool
	Password            *string
	Role                *string
	RoleID              *string
	GroupIDs            *[]string
	PermissionOverrides map[string]*bool
	Quotas              *Quotas
}

type Service struct{ db *sql.DB }

func NewService(db *sql.DB) (*Service, error) {
	if db == nil {
		return nil, errors.New("user authentication requires PostgreSQL")
	}
	return &Service{db: db}, nil
}

func (s *Service) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS conv_users (
 id TEXT PRIMARY KEY, username TEXT NOT NULL UNIQUE, password_salt BYTEA NOT NULL,
 password_hash BYTEA NOT NULL, role TEXT NOT NULL DEFAULT 'user', enabled BOOLEAN NOT NULL DEFAULT true,
 can_convert BOOLEAN NOT NULL DEFAULT true, can_read_jobs BOOLEAN NOT NULL DEFAULT true,
 max_file_bytes BIGINT NOT NULL DEFAULT 104857600, monthly_jobs INTEGER NOT NULL DEFAULT 0,
 monthly_bytes BIGINT NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (role IN ('user','admin')), CHECK (max_file_bytes > 0), CHECK (monthly_jobs >= 0), CHECK (monthly_bytes >= 0)
);
CREATE TABLE IF NOT EXISTS conv_api_keys (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES conv_users(id) ON DELETE CASCADE,
 name TEXT NOT NULL, token_hash BYTEA NOT NULL UNIQUE, scopes JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), last_used_at TIMESTAMPTZ, revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS conv_api_keys_user_idx ON conv_api_keys(user_id, created_at DESC);
`)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS conv_roles (
 id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '', permissions JSONB NOT NULL DEFAULT '{}'::jsonb,
 built_in BOOLEAN NOT NULL DEFAULT false, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO conv_roles(id,name,description,permissions,built_in) VALUES
	('converter','Converter','Convert and compress files, view and cancel owned jobs, and manage personal API keys.', '{"conversion.use":true,"compression.use":true,"jobs.read":true,"jobs.cancel":true,"api_keys.manage":true}'::jsonb,true),
 ('viewer','Viewer','View own jobs and download completed results.', '{"jobs.read":true}'::jsonb,true),
 ('custom','Custom','No permissions unless granted.', '{}'::jsonb,true)
ON CONFLICT (id) DO NOTHING;
ALTER TABLE conv_users ADD COLUMN IF NOT EXISTS role_id TEXT REFERENCES conv_roles(id);
ALTER TABLE conv_users ADD COLUMN IF NOT EXISTS permission_overrides JSONB;
ALTER TABLE conv_users ADD COLUMN IF NOT EXISTS max_file_mode TEXT;
ALTER TABLE conv_users ADD COLUMN IF NOT EXISTS max_file_limit BIGINT;
ALTER TABLE conv_users ADD COLUMN IF NOT EXISTS monthly_jobs_mode TEXT;
ALTER TABLE conv_users ADD COLUMN IF NOT EXISTS monthly_jobs_limit BIGINT;
ALTER TABLE conv_users ADD COLUMN IF NOT EXISTS monthly_bytes_mode TEXT;
ALTER TABLE conv_users ADD COLUMN IF NOT EXISTS monthly_bytes_limit BIGINT;
UPDATE conv_users SET permission_overrides=jsonb_build_object('conversion.use',can_convert,'compression.use',can_convert,'jobs.read',can_read_jobs,'jobs.cancel',can_convert,'api_keys.manage',true) WHERE permission_overrides IS NULL;
UPDATE conv_users SET role_id='custom' WHERE role_id IS NULL;
UPDATE conv_users SET max_file_mode='limited', max_file_limit=max_file_bytes WHERE max_file_mode IS NULL;
UPDATE conv_users SET monthly_jobs_mode=CASE WHEN monthly_jobs=0 THEN 'unlimited' ELSE 'limited' END, monthly_jobs_limit=NULLIF(monthly_jobs,0) WHERE monthly_jobs_mode IS NULL;
UPDATE conv_users SET monthly_bytes_mode=CASE WHEN monthly_bytes=0 THEN 'unlimited' ELSE 'limited' END, monthly_bytes_limit=NULLIF(monthly_bytes,0) WHERE monthly_bytes_mode IS NULL;
ALTER TABLE conv_users ALTER COLUMN permission_overrides SET DEFAULT '{}'::jsonb;
ALTER TABLE conv_users ALTER COLUMN permission_overrides SET NOT NULL;
ALTER TABLE conv_users ALTER COLUMN max_file_mode SET DEFAULT 'limited';
ALTER TABLE conv_users ALTER COLUMN monthly_jobs_mode SET DEFAULT 'unlimited';
ALTER TABLE conv_users ALTER COLUMN monthly_bytes_mode SET DEFAULT 'unlimited';
CREATE TABLE IF NOT EXISTS conv_groups (
 id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, description TEXT NOT NULL DEFAULT '', role_id TEXT REFERENCES conv_roles(id),
 permissions JSONB NOT NULL DEFAULT '{}'::jsonb, max_file_mode TEXT NOT NULL DEFAULT 'inherit', max_file_limit BIGINT,
 monthly_jobs_mode TEXT NOT NULL DEFAULT 'inherit', monthly_jobs_limit BIGINT,
 monthly_bytes_mode TEXT NOT NULL DEFAULT 'inherit', monthly_bytes_limit BIGINT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS conv_group_members (
 group_id TEXT NOT NULL REFERENCES conv_groups(id) ON DELETE CASCADE,
 user_id TEXT NOT NULL REFERENCES conv_users(id) ON DELETE CASCADE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(group_id,user_id)
);
CREATE INDEX IF NOT EXISTS conv_group_members_user_idx ON conv_group_members(user_id,group_id);
CREATE TABLE IF NOT EXISTS conv_audit_events (
 id BIGSERIAL PRIMARY KEY, actor_id TEXT NOT NULL, action TEXT NOT NULL, subject_type TEXT NOT NULL, subject_id TEXT NOT NULL,
 details JSONB NOT NULL DEFAULT '{}'::jsonb, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS conv_audit_events_created_idx ON conv_audit_events(created_at DESC);
CREATE TABLE IF NOT EXISTS conv_instance_setup (
 id INTEGER PRIMARY KEY CHECK (id=1), completed BOOLEAN NOT NULL DEFAULT false,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO conv_instance_setup(id,completed)
SELECT 1,EXISTS(SELECT 1 FROM conv_users) ON CONFLICT (id) DO NOTHING;
CREATE TABLE IF NOT EXISTS conv_anonymous_settings (
 id INTEGER PRIMARY KEY CHECK(id=1), enabled BOOLEAN NOT NULL DEFAULT false,
 allow_conversion BOOLEAN NOT NULL DEFAULT true, allow_compression BOOLEAN NOT NULL DEFAULT true,
 max_file_bytes BIGINT NOT NULL DEFAULT 10485760, max_batch_files INTEGER NOT NULL DEFAULT 5,
 max_guest_jobs INTEGER NOT NULL DEFAULT 5, uploads_per_minute INTEGER NOT NULL DEFAULT 10,
 submissions_per_minute INTEGER NOT NULL DEFAULT 5, global_uploads_per_minute INTEGER NOT NULL DEFAULT 120,
 global_submissions_per_minute INTEGER NOT NULL DEFAULT 60, max_global_jobs INTEGER NOT NULL DEFAULT 32,
 max_global_storage_bytes BIGINT NOT NULL DEFAULT 5368709120,
 result_expiration_hours INTEGER NOT NULL DEFAULT 24, session_expiration_hours INTEGER NOT NULL DEFAULT 24,
 upload_expiration_hours INTEGER NOT NULL DEFAULT 24, failed_expiration_hours INTEGER NOT NULL DEFAULT 24,
 metadata_expiration_hours INTEGER NOT NULL DEFAULT 168, updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK(max_file_bytes BETWEEN 1 AND 104857600), CHECK(max_batch_files BETWEEN 1 AND 20),
 CHECK(max_guest_jobs BETWEEN 1 AND 100), CHECK(uploads_per_minute BETWEEN 1 AND 120),
 CHECK(submissions_per_minute BETWEEN 1 AND 60), CHECK(max_global_jobs BETWEEN 1 AND 1000),
 CHECK(global_uploads_per_minute BETWEEN 1 AND 10000), CHECK(global_submissions_per_minute BETWEEN 1 AND 10000),
 CHECK(max_global_storage_bytes BETWEEN 10485760 AND 1099511627776),
 CHECK(result_expiration_hours BETWEEN 1 AND 720), CHECK(session_expiration_hours BETWEEN 1 AND 720),
 CHECK(upload_expiration_hours BETWEEN 1 AND 720), CHECK(failed_expiration_hours BETWEEN 1 AND 720),
 CHECK(metadata_expiration_hours BETWEEN 1 AND 8760)
);
INSERT INTO conv_anonymous_settings(id) VALUES(1) ON CONFLICT(id) DO NOTHING;
ALTER TABLE conv_anonymous_settings ADD COLUMN IF NOT EXISTS global_uploads_per_minute INTEGER NOT NULL DEFAULT 120 CHECK(global_uploads_per_minute BETWEEN 1 AND 10000);
ALTER TABLE conv_anonymous_settings ADD COLUMN IF NOT EXISTS global_submissions_per_minute INTEGER NOT NULL DEFAULT 60 CHECK(global_submissions_per_minute BETWEEN 1 AND 10000);
CREATE TABLE IF NOT EXISTS conv_guest_sessions (
 id TEXT PRIMARY KEY, token_hash BYTEA NOT NULL UNIQUE, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS conv_guest_sessions_expiry_idx ON conv_guest_sessions(expires_at);
CREATE TABLE IF NOT EXISTS conv_guest_rate_buckets (
 guest_id TEXT NOT NULL, action TEXT NOT NULL, bucket TIMESTAMPTZ NOT NULL, count INTEGER NOT NULL,
 PRIMARY KEY(guest_id,action,bucket)
);
CREATE INDEX IF NOT EXISTS conv_guest_rate_buckets_bucket_idx ON conv_guest_rate_buckets(bucket);
`)
	return err
}

func (s *Service) CreateUser(ctx context.Context, input NewUser) (User, error) {
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	if !usernamePattern.MatchString(input.Username) || len(input.Password) < 12 || len(input.Password) > 256 || input.Quotas == nil {
		return User{}, ErrInvalidInput
	}
	q := *input.Quotas
	if validateQuota(q.MaxFileBytes, true, 100<<20) != nil || validateQuota(q.MonthlyJobs, true, 0) != nil || validateQuota(q.MonthlyBytes, true, 0) != nil {
		return User{}, ErrInvalidInput
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return User{}, err
	}
	hash := passwordHash(input.Password, salt)
	id, err := randomID()
	if err != nil {
		return User{}, err
	}
	var created time.Time
	initialPermissions := map[string]bool{}
	permissionOverrides, _ := json.Marshal(initialPermissions)
	roleID := input.RoleID
	if roleID == "" {
		roleID = "custom"
	}
	if input.PermissionOverrides != nil {
		policies := map[string]bool{}
		for key, value := range input.PermissionOverrides {
			if !validPermission(key) {
				return User{}, ErrInvalidInput
			}
			if value != nil {
				policies[key] = *value
			}
		}
		permissionOverrides, _ = json.Marshal(policies)
	}
	maxMode, maxLimit := q.MaxFileBytes.Mode, quotaDBValue(q.MaxFileBytes)
	jobMode, jobLimit := q.MonthlyJobs.Mode, quotaDBValue(q.MonthlyJobs)
	byteMode, byteLimit := q.MonthlyBytes.Mode, quotaDBValue(q.MonthlyBytes)
	legacyMax, legacyJobs, legacyBytes := int64(100<<20), 0, int64(0)
	if q.MaxFileBytes.Mode == "limited" && q.MaxFileBytes.Value != nil {
		legacyMax = *q.MaxFileBytes.Value
	}
	if q.MonthlyJobs.Mode == "limited" && q.MonthlyJobs.Value != nil {
		legacyJobs = int(*q.MonthlyJobs.Value)
	}
	if q.MonthlyBytes.Mode == "limited" && q.MonthlyBytes.Value != nil {
		legacyBytes = *q.MonthlyBytes.Value
	}
	err = s.db.QueryRowContext(ctx, `INSERT INTO conv_users(id,username,password_salt,password_hash,can_convert,can_read_jobs,max_file_bytes,monthly_jobs,monthly_bytes,role_id,permission_overrides,max_file_mode,max_file_limit,monthly_jobs_mode,monthly_jobs_limit,monthly_bytes_mode,monthly_bytes_limit) VALUES($1,$2,$3,$4,false,false,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING created_at`, id, input.Username, salt, hash, legacyMax, legacyJobs, legacyBytes, roleID, permissionOverrides, maxMode, maxLimit, jobMode, jobLimit, byteMode, byteLimit).Scan(&created)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, ErrConflict
		}
		return User{}, err
	}
	p, err := s.Principal(ctx, id)
	if err != nil {
		return User{}, err
	}
	return User{Principal: p, Enabled: true, CreatedAt: created}, nil
}

func (s *Service) AuthenticatePassword(ctx context.Context, username, password string) (Principal, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if len(password) > 256 {
		return Principal{}, ErrInvalidCredentials
	}
	var p Principal
	var salt, expected []byte
	var enabled bool
	err := s.db.QueryRowContext(ctx, `SELECT id,username,role,can_convert,can_read_jobs,max_file_bytes,monthly_jobs,monthly_bytes,enabled,password_salt,password_hash FROM conv_users WHERE username=$1`, username).Scan(&p.ID, &p.Username, &p.Role, &p.CanConvert, &p.CanReadJobs, &p.MaxFileBytes, &p.MonthlyJobs, &p.MonthlyBytes, &enabled, &salt, &expected)
	if errors.Is(err, sql.ErrNoRows) {
		// Spend roughly the same password-hash work for unknown names.
		_ = passwordHash("constant-invalid-password", make([]byte, 16))
		return Principal{}, ErrInvalidCredentials
	}
	if err != nil {
		return Principal{}, err
	}
	got := passwordHash(password, salt)
	if len(got) != len(expected) || subtle.ConstantTimeCompare(got, expected) != 1 {
		return Principal{}, ErrInvalidCredentials
	}
	if !enabled {
		return Principal{}, ErrDisabled
	}
	return s.resolvePrincipal(ctx, p)
}

func (s *Service) Principal(ctx context.Context, id string) (Principal, error) {
	var p Principal
	var enabled bool
	err := s.db.QueryRowContext(ctx, `SELECT id,username,role,can_convert,can_read_jobs,max_file_bytes,monthly_jobs,monthly_bytes,enabled FROM conv_users WHERE id=$1`, id).Scan(&p.ID, &p.Username, &p.Role, &p.CanConvert, &p.CanReadJobs, &p.MaxFileBytes, &p.MonthlyJobs, &p.MonthlyBytes, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrNotFound
	}
	if err != nil {
		return Principal{}, err
	}
	if !enabled {
		return Principal{}, ErrDisabled
	}
	return s.resolvePrincipal(ctx, p)
}

func validPermission(name string) bool {
	for _, candidate := range PermissionNames {
		if name == candidate {
			return true
		}
	}
	return false
}

func decodePermissionMap(raw []byte) (map[string]bool, error) {
	out := map[string]bool{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) resolvePrincipal(ctx context.Context, p Principal) (Principal, error) {
	var roleID string
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(role_id,'custom'),permission_overrides FROM conv_users WHERE id=$1`, p.ID).Scan(&roleID, &raw); err != nil {
		return Principal{}, err
	}
	userOverrides, err := decodePermissionMap(raw)
	if err != nil {
		return Principal{}, err
	}
	p.RoleID = roleID
	base := map[string]bool{}
	var rolePermissions []byte
	if err = s.db.QueryRowContext(ctx, `SELECT permissions FROM conv_roles WHERE id=$1`, roleID).Scan(&rolePermissions); err == nil {
		base, err = decodePermissionMap(rolePermissions)
		if err != nil {
			return Principal{}, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Principal{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT g.id,g.name,g.permissions,r.permissions FROM conv_group_members m JOIN conv_groups g ON g.id=m.group_id LEFT JOIN conv_roles r ON r.id=g.role_id WHERE m.user_id=$1 ORDER BY g.id`, p.ID)
	if err != nil {
		return Principal{}, err
	}
	defer rows.Close()
	type groupPolicy struct {
		id, name     string
		direct, role []byte
	}
	groups := []groupPolicy{}
	for rows.Next() {
		var g groupPolicy
		if err = rows.Scan(&g.id, &g.name, &g.direct, &g.role); err != nil {
			return Principal{}, err
		}
		groups = append(groups, g)
	}
	if err = rows.Err(); err != nil {
		return Principal{}, err
	}
	grants := map[string]bool{}
	denies := map[string]bool{}
	sources := map[string][]PermissionOrigin{}
	apply := func(policy map[string]bool, source string, denyConflicts bool) {
		for k, v := range policy {
			if !validPermission(k) {
				continue
			}
			if v {
				grants[k] = true
			} else {
				denies[k] = true
			}
			sources[k] = append(sources[k], PermissionOrigin{Source: source, Effect: v})
		}
		_ = denyConflicts
	}
	apply(base, "role:"+roleID, false)
	groupNames := []string{}
	groupIDs := []string{}
	for _, g := range groups {
		groupNames = append(groupNames, g.name)
		groupIDs = append(groupIDs, g.id)
		gp, e := decodePermissionMap(g.role)
		if e != nil {
			return Principal{}, e
		}
		direct, e := decodePermissionMap(g.direct)
		if e != nil {
			return Principal{}, e
		}
		combined := map[string]bool{}
		for k, v := range gp {
			combined[k] = v
		}
		for k, v := range direct {
			combined[k] = v
		}
		apply(combined, "group:"+g.name, true)
	}
	permissions := map[string]bool{}
	for _, name := range PermissionNames {
		permissions[name] = grants[name] && !denies[name]
		if override, ok := userOverrides[name]; ok {
			permissions[name] = override
			sources[name] = append(sources[name], PermissionOrigin{Source: "user override", Effect: override})
		}
	}
	// Administrators always retain all capabilities; role reassignment cannot reduce this principal.
	if p.IsAdmin() {
		for _, name := range PermissionNames {
			permissions[name] = true
			sources[name] = append(sources[name], PermissionOrigin{Source: "administrator", Effect: true})
		}
	}
	p.Permissions = permissions
	p.PermissionOrigins = sources
	p.Groups = groupNames
	p.GroupIDs = groupIDs
	p.CanConvert = permissions[PermissionConversion]
	p.CanReadJobs = permissions[PermissionJobsRead]
	return s.resolveQuotas(ctx, p)
}

func (s *Service) resolveQuotas(ctx context.Context, p Principal) (Principal, error) {
	var own Quotas
	var maxMode, jobMode, byteMode string
	var max, job, bytes sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT max_file_mode,max_file_limit,monthly_jobs_mode,monthly_jobs_limit,monthly_bytes_mode,monthly_bytes_limit FROM conv_users WHERE id=$1`, p.ID).Scan(&maxMode, &max, &jobMode, &job, &byteMode, &bytes)
	if err != nil {
		return Principal{}, err
	}
	own = Quotas{MaxFileBytes: QuotaLimit{Mode: maxMode}, MonthlyJobs: QuotaLimit{Mode: jobMode}, MonthlyBytes: QuotaLimit{Mode: byteMode}}
	if max.Valid {
		v := max.Int64
		own.MaxFileBytes.Value = &v
	}
	if job.Valid {
		v := job.Int64
		own.MonthlyJobs.Value = &v
	}
	if bytes.Valid {
		v := bytes.Int64
		own.MonthlyBytes.Value = &v
	}
	groups, err := s.groupQuotas(ctx, p.ID)
	if err != nil {
		return Principal{}, err
	}
	own.MaxFileBytes = effectiveQuota(own.MaxFileBytes, groups, func(q Quotas) QuotaLimit { return q.MaxFileBytes }, 100<<20)
	own.MonthlyJobs = effectiveQuota(own.MonthlyJobs, groups, func(q Quotas) QuotaLimit { return q.MonthlyJobs }, 0)
	own.MonthlyBytes = effectiveQuota(own.MonthlyBytes, groups, func(q Quotas) QuotaLimit { return q.MonthlyBytes }, 0)
	p.Quotas = own
	if own.MaxFileBytes.Value != nil {
		p.MaxFileBytes = *own.MaxFileBytes.Value
	} else {
		p.MaxFileBytes = 100 << 20
	}
	if own.MonthlyJobs.Value != nil {
		p.MonthlyJobs = int(*own.MonthlyJobs.Value)
	} else {
		p.MonthlyJobs = 0
	}
	if own.MonthlyBytes.Value != nil {
		p.MonthlyBytes = *own.MonthlyBytes.Value
	} else {
		p.MonthlyBytes = 0
	}
	return p, nil
}

func effectiveQuota(user QuotaLimit, groups []Quotas, selectValue func(Quotas) QuotaLimit, serverDefault int64) QuotaLimit {
	if user.Mode == "limited" || user.Mode == "unlimited" {
		return user
	}
	var smallest *int64
	hasUnlimited := false
	for _, g := range groups {
		q := selectValue(g)
		if q.Mode == "limited" && q.Value != nil && (smallest == nil || *q.Value < *smallest) {
			v := *q.Value
			smallest = &v
		}
		if q.Mode == "unlimited" {
			hasUnlimited = true
		}
	}
	if smallest != nil {
		return QuotaLimit{Mode: "limited", Value: smallest}
	}
	if hasUnlimited {
		return QuotaLimit{Mode: "unlimited"}
	}
	if serverDefault > 0 {
		v := serverDefault
		return QuotaLimit{Mode: "limited", Value: &v}
	}
	return QuotaLimit{Mode: "unlimited"}
}

func (s *Service) groupQuotas(ctx context.Context, userID string) ([]Quotas, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.max_file_mode,g.max_file_limit,g.monthly_jobs_mode,g.monthly_jobs_limit,g.monthly_bytes_mode,g.monthly_bytes_limit FROM conv_group_members m JOIN conv_groups g ON g.id=m.group_id WHERE m.user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Quotas{}
	for rows.Next() {
		var a, b, c string
		var av, bv, cv sql.NullInt64
		if err = rows.Scan(&a, &av, &b, &bv, &c, &cv); err != nil {
			return nil, err
		}
		q := Quotas{MaxFileBytes: QuotaLimit{Mode: a}, MonthlyJobs: QuotaLimit{Mode: b}, MonthlyBytes: QuotaLimit{Mode: c}}
		if av.Valid {
			v := av.Int64
			q.MaxFileBytes.Value = &v
		}
		if bv.Valid {
			v := bv.Int64
			q.MonthlyJobs.Value = &v
		}
		if cv.Valid {
			v := cv.Int64
			q.MonthlyBytes.Value = &v
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,username,role,can_convert,can_read_jobs,max_file_bytes,monthly_jobs,monthly_bytes,enabled,created_at FROM conv_users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CanConvert, &u.CanReadJobs, &u.MaxFileBytes, &u.MonthlyJobs, &u.MonthlyBytes, &u.Enabled, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i := range users {
		p, e := s.resolvePrincipal(ctx, users[i].Principal)
		if e != nil {
			return nil, e
		}
		users[i].Principal = p
	}
	return users, nil
}

func (s *Service) UpdateUser(ctx context.Context, id string, update UserUpdate) (User, error) {
	return s.UpdateUserBy(ctx, "admin", id, update)
}
func (s *Service) UpdateUserBy(ctx context.Context, actorID, id string, update UserUpdate) (User, error) {
	if update.Role != nil && *update.Role != RoleAdmin && *update.Role != RoleUser {
		return User{}, ErrInvalidInput
	}
	var salt, hash []byte
	if update.Password != nil {
		if len(*update.Password) < 12 || len(*update.Password) > 256 {
			return User{}, ErrInvalidInput
		}
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return User{}, err
		}
		hash = passwordHash(*update.Password, salt)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	if update.Role != nil || update.Enabled != nil {
		if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('101conv-admin-guard',0))`); err != nil {
			return User{}, err
		}
		var current string
		var enabled bool
		if err = tx.QueryRowContext(ctx, `SELECT role,enabled FROM conv_users WHERE id=$1 FOR UPDATE`, id).Scan(&current, &enabled); errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		} else if err != nil {
			return User{}, err
		}
		nextRole := current
		nextEnabled := enabled
		if update.Role != nil {
			nextRole = *update.Role
		}
		if update.Enabled != nil {
			nextEnabled = *update.Enabled
		}
		if current == RoleAdmin && enabled && !(nextRole == RoleAdmin && nextEnabled) {
			var admins int
			if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM conv_users WHERE role='admin' AND enabled=true`).Scan(&admins); err != nil {
				return User{}, err
			}
			if admins <= 1 {
				return User{}, ErrConflict
			}
		}
	}
	var overridesJSON []byte
	if update.PermissionOverrides != nil {
		var current []byte
		if err = tx.QueryRowContext(ctx, `SELECT permission_overrides FROM conv_users WHERE id=$1`, id).Scan(&current); err != nil {
			return User{}, err
		}
		overrides, e := decodePermissionMap(current)
		if e != nil {
			return User{}, e
		}
		for key, value := range update.PermissionOverrides {
			if !validPermission(key) {
				return User{}, ErrInvalidInput
			}
			if value == nil {
				delete(overrides, key)
			} else {
				overrides[key] = *value
			}
		}
		overridesJSON = encode(overrides)
	}
	var roleID any
	if update.RoleID != nil {
		roleID = *update.RoleID
	}
	var maxMode, jobMode, byteMode any
	var maxLimit, jobLimit, byteLimit any
	if update.Quotas != nil {
		q := *update.Quotas
		if validateQuota(q.MaxFileBytes, true, 100<<20) != nil || validateQuota(q.MonthlyJobs, true, 0) != nil || validateQuota(q.MonthlyBytes, true, 0) != nil {
			return User{}, ErrInvalidInput
		}
		maxMode = q.MaxFileBytes.Mode
		maxLimit = quotaDBValue(q.MaxFileBytes)
		if q.MaxFileBytes.Value == nil {
			maxLimit = nil
		}
		jobMode = q.MonthlyJobs.Mode
		jobLimit = quotaDBValue(q.MonthlyJobs)
		if q.MonthlyJobs.Value == nil {
			jobLimit = nil
		}
		byteMode = q.MonthlyBytes.Mode
		byteLimit = quotaDBValue(q.MonthlyBytes)
		if q.MonthlyBytes.Value == nil {
			byteLimit = nil
		}
	}
	var role any
	if update.Role != nil {
		role = *update.Role
	}
	result, err := tx.ExecContext(ctx, `UPDATE conv_users SET enabled=COALESCE($2,enabled),role=COALESCE($3,role),password_salt=COALESCE($4,password_salt),password_hash=COALESCE($5,password_hash),permission_overrides=COALESCE($6,permission_overrides),role_id=COALESCE($7,role_id),max_file_mode=COALESCE($8,max_file_mode),max_file_limit=CASE WHEN $8 IS NULL THEN max_file_limit ELSE $9 END,monthly_jobs_mode=COALESCE($10,monthly_jobs_mode),monthly_jobs_limit=CASE WHEN $10 IS NULL THEN monthly_jobs_limit ELSE $11 END,monthly_bytes_mode=COALESCE($12,monthly_bytes_mode),monthly_bytes_limit=CASE WHEN $12 IS NULL THEN monthly_bytes_limit ELSE $13 END WHERE id=$1`, id, update.Enabled, role, salt, hash, nullableBytes(overridesJSON), roleID, maxMode, maxLimit, jobMode, jobLimit, byteMode, byteLimit)
	if err != nil {
		return User{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return User{}, err
	}
	if n == 0 {
		return User{}, ErrNotFound
	}
	if update.Enabled != nil && !*update.Enabled {
		if _, err = tx.ExecContext(ctx, `UPDATE conv_api_keys SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); err != nil {
			return User{}, err
		}
	}
	if update.GroupIDs != nil {
		if _, err = tx.ExecContext(ctx, `DELETE FROM conv_group_members WHERE user_id=$1`, id); err != nil {
			return User{}, err
		}
		for _, gid := range uniqueStrings(*update.GroupIDs) {
			if _, err = tx.ExecContext(ctx, `INSERT INTO conv_group_members(group_id,user_id) SELECT id,$2 FROM conv_groups WHERE id=$1`, gid, id); err != nil {
				return User{}, err
			}
		}
		var n int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM conv_group_members WHERE user_id=$1`, id).Scan(&n); err != nil {
			return User{}, err
		}
		if n != len(uniqueStrings(*update.GroupIDs)) {
			return User{}, ErrNotFound
		}
	}
	if actorID == "" {
		actorID = "admin"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO conv_audit_events(actor_id,action,subject_type,subject_id,details) VALUES($1,'user.updated','user',$2,$3)`, actorID, id, encode(map[string]any{"role": update.Role, "roleId": update.RoleID, "enabled": update.Enabled, "groups": update.GroupIDs, "quotas": update.Quotas, "permissions": update.PermissionOverrides})); err != nil {
		return User{}, err
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	users, err := s.ListUsers(ctx)
	if err != nil {
		return User{}, err
	}
	for _, user := range users {
		if user.ID == id {
			return user, nil
		}
	}
	return User{}, ErrNotFound
}

func nullableBytes(v []byte) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

func quotaDBValue(q QuotaLimit) any {
	if q.Mode != "limited" || q.Value == nil {
		return nil
	}
	return *q.Value
}
func uniqueStrings(in []string) []string {
	set := map[string]bool{}
	out := []string{}
	for _, v := range in {
		if !set[v] {
			set[v] = true
			out = append(out, v)
		}
	}
	return out
}

func (s *Service) DeleteUser(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE conv_users SET enabled=false WHERE id=$1 AND role='user'`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_api_keys SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL`, id); err != nil {
		return err
	}
	return tx.Commit()
}

var allowedScopes = map[string]bool{ScopeUpload: true, ScopeSubmit: true, ScopeCompress: true, ScopeRead: true, ScopeCancel: true, ScopeResult: true}

func (s *Service) CreateAPIKey(ctx context.Context, userID, name string, scopes []string) (CreatedAPIKey, error) {
	name = strings.TrimSpace(name)
	if !keyNamePattern.MatchString(name) || len(scopes) == 0 {
		return CreatedAPIKey{}, ErrInvalidInput
	}
	unique := map[string]bool{}
	for _, scope := range scopes {
		if !allowedScopes[scope] {
			return CreatedAPIKey{}, ErrInvalidInput
		}
		unique[scope] = true
	}
	scopes = scopes[:0]
	for scope := range unique {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	p, err := s.Principal(ctx, userID)
	if err != nil {
		return CreatedAPIKey{}, err
	}
	if unique[ScopeUpload] && !p.Permissions[PermissionConversion] && !p.Permissions[PermissionCompression] || unique[ScopeSubmit] && !p.Permissions[PermissionConversion] || unique[ScopeCompress] && !p.Permissions[PermissionCompression] || unique[ScopeCancel] && !p.Permissions[PermissionJobsCancel] || (unique[ScopeRead] || unique[ScopeResult]) && !p.Permissions[PermissionJobsRead] {
		return CreatedAPIKey{}, ErrInvalidInput
	}
	var tokenBytes [32]byte
	if _, err = rand.Read(tokenBytes[:]); err != nil {
		return CreatedAPIKey{}, err
	}
	token := "101c_" + base64.RawURLEncoding.EncodeToString(tokenBytes[:])
	sum := sha256.Sum256([]byte(token))
	id, err := randomID()
	if err != nil {
		return CreatedAPIKey{}, err
	}
	scopeJSON, err := json.Marshal(scopes)
	if err != nil {
		return CreatedAPIKey{}, err
	}
	var created time.Time
	err = s.db.QueryRowContext(ctx, `INSERT INTO conv_api_keys(id,user_id,name,token_hash,scopes) VALUES($1,$2,$3,$4,$5) RETURNING created_at`, id, userID, name, sum[:], scopeJSON).Scan(&created)
	if err != nil {
		return CreatedAPIKey{}, err
	}
	return CreatedAPIKey{APIKey: APIKey{ID: id, Name: name, Scopes: scopes, CreatedAt: created}, Token: token}, nil
}

func (s *Service) ListAPIKeys(ctx context.Context, userID string) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,scopes,created_at,last_used_at FROM conv_api_keys WHERE user_id=$1 AND revoked_at IS NULL ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := []APIKey{}
	for rows.Next() {
		var key APIKey
		var raw []byte
		var last sql.NullTime
		if err = rows.Scan(&key.ID, &key.Name, &raw, &key.CreatedAt, &last); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &key.Scopes); err != nil {
			return nil, err
		}
		if last.Valid {
			key.LastUsed = &last.Time
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func validatePolicies(p map[string]bool) error {
	for k := range p {
		if !validPermission(k) {
			return ErrInvalidInput
		}
	}
	return nil
}
func validateQuota(q QuotaLimit, inherit bool, max int64) error {
	if q.Mode != "limited" && q.Mode != "unlimited" && !(inherit && q.Mode == "inherit") {
		return ErrInvalidInput
	}
	if q.Mode == "limited" && (q.Value == nil || *q.Value <= 0 || max > 0 && *q.Value > max) {
		return ErrInvalidInput
	}
	if q.Mode != "limited" && q.Value != nil {
		return ErrInvalidInput
	}
	return nil
}
func encode(v any) []byte { b, _ := json.Marshal(v); return b }
func (s *Service) RecordAudit(ctx context.Context, actor, action, subjectType, subjectID string, details any) error {
	if actor == "" {
		actor = "admin"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO conv_audit_events(actor_id,action,subject_type,subject_id,details) VALUES($1,$2,$3,$4,$5)`, actor, action, subjectType, subjectID, encode(details))
	return err
}
func (s *Service) ListRoles(ctx context.Context) ([]PermissionRole, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,description,permissions,built_in FROM conv_roles ORDER BY built_in DESC,name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PermissionRole{}
	for rows.Next() {
		var r PermissionRole
		var raw []byte
		if err = rows.Scan(&r.ID, &r.Name, &r.Description, &raw, &r.BuiltIn); err != nil {
			return nil, err
		}
		if r.Permissions, err = decodePermissionMap(raw); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Service) CanGrantPolicies(ctx context.Context, actorID string, permissions map[string]bool) error {
	actor, err := s.Principal(ctx, actorID)
	if err != nil {
		return err
	}
	for name, grant := range permissions {
		if grant && !actor.IsAdmin() && !actor.Permissions[name] {
			return ErrInvalidInput
		}
	}
	return nil
}
func (s *Service) CanGrantRole(ctx context.Context, actorID, roleID string) error {
	var raw []byte
	if err := s.db.QueryRowContext(ctx, `SELECT permissions FROM conv_roles WHERE id=$1`, roleID).Scan(&raw); err != nil {
		return err
	}
	policies, err := decodePermissionMap(raw)
	if err != nil {
		return err
	}
	return s.CanGrantPolicies(ctx, actorID, policies)
}
func (s *Service) CanGrantGroup(ctx context.Context, actorID, groupID string) error {
	var direct, roleRaw []byte
	err := s.db.QueryRowContext(ctx, `SELECT g.permissions,r.permissions FROM conv_groups g LEFT JOIN conv_roles r ON r.id=g.role_id WHERE g.id=$1`, groupID).Scan(&direct, &roleRaw)
	if err != nil {
		return err
	}
	combined, err := decodePermissionMap(roleRaw)
	if err != nil {
		return err
	}
	own, err := decodePermissionMap(direct)
	if err != nil {
		return err
	}
	for k, v := range own {
		combined[k] = v
	}
	return s.CanGrantPolicies(ctx, actorID, combined)
}
func (s *Service) IsGroupMember(ctx context.Context, userID, groupID string) (bool, error) {
	var member bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_group_members WHERE user_id=$1 AND group_id=$2)`, userID, groupID).Scan(&member)
	return member, err
}
func (s *Service) UsesRole(ctx context.Context, userID, roleID string) (bool, error) {
	var assigned bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_users WHERE id=$1 AND role_id=$2) OR EXISTS(SELECT 1 FROM conv_group_members m JOIN conv_groups g ON g.id=m.group_id WHERE m.user_id=$1 AND g.role_id=$2)`, userID, roleID).Scan(&assigned)
	return assigned, err
}
func (s *Service) SaveRole(ctx context.Context, r PermissionRole) (PermissionRole, error) {
	r.ID = strings.TrimSpace(r.ID)
	r.Name = strings.TrimSpace(r.Name)
	if r.ID == "" || len(r.ID) > 80 || r.Name == "" || len(r.Name) > 100 || validatePolicies(r.Permissions) != nil {
		return PermissionRole{}, ErrInvalidInput
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO conv_roles(id,name,description,permissions) VALUES($1,$2,$3,$4) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,description=EXCLUDED.description,permissions=EXCLUDED.permissions WHERE conv_roles.built_in=false`, r.ID, r.Name, r.Description, encode(r.Permissions))
	if err != nil {
		return PermissionRole{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return PermissionRole{}, ErrConflict
	}
	return r, nil
}
func (s *Service) DeleteRole(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM conv_roles WHERE id=$1 AND built_in=false AND NOT EXISTS(SELECT 1 FROM conv_users WHERE role_id=$1) AND NOT EXISTS(SELECT 1 FROM conv_groups WHERE role_id=$1)`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrConflict
	}
	return nil
}
func (s *Service) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT g.id,g.name,g.description,COALESCE(g.role_id,''),g.permissions,g.max_file_mode,g.max_file_limit,g.monthly_jobs_mode,g.monthly_jobs_limit,g.monthly_bytes_mode,g.monthly_bytes_limit,g.created_at,count(m.user_id) FROM conv_groups g LEFT JOIN conv_group_members m ON m.group_id=g.id GROUP BY g.id ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		var raw []byte
		var max, jobs, bytes sql.NullInt64
		if err = rows.Scan(&g.ID, &g.Name, &g.Description, &g.RoleID, &raw, &g.Quotas.MaxFileBytes.Mode, &max, &g.Quotas.MonthlyJobs.Mode, &jobs, &g.Quotas.MonthlyBytes.Mode, &bytes, &g.CreatedAt, &g.MemberCount); err != nil {
			return nil, err
		}
		g.Permissions, err = decodePermissionMap(raw)
		if err != nil {
			return nil, err
		}
		if max.Valid {
			v := max.Int64
			g.Quotas.MaxFileBytes.Value = &v
		}
		if jobs.Valid {
			v := jobs.Int64
			g.Quotas.MonthlyJobs.Value = &v
		}
		if bytes.Valid {
			v := bytes.Int64
			g.Quotas.MonthlyBytes.Value = &v
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Service) SaveGroup(ctx context.Context, g Group) (Group, error) {
	g.ID = strings.TrimSpace(g.ID)
	g.Name = strings.TrimSpace(g.Name)
	if g.ID == "" || len(g.ID) > 80 || g.Name == "" || len(g.Name) > 100 || validatePolicies(g.Permissions) != nil || validateQuota(g.Quotas.MaxFileBytes, true, 100<<20) != nil || validateQuota(g.Quotas.MonthlyJobs, true, 0) != nil || validateQuota(g.Quotas.MonthlyBytes, true, 0) != nil {
		return Group{}, ErrInvalidInput
	}
	var role any
	if g.RoleID != "" {
		role = g.RoleID
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO conv_groups(id,name,description,role_id,permissions,max_file_mode,max_file_limit,monthly_jobs_mode,monthly_jobs_limit,monthly_bytes_mode,monthly_bytes_limit) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,description=EXCLUDED.description,role_id=EXCLUDED.role_id,permissions=EXCLUDED.permissions,max_file_mode=EXCLUDED.max_file_mode,max_file_limit=EXCLUDED.max_file_limit,monthly_jobs_mode=EXCLUDED.monthly_jobs_mode,monthly_jobs_limit=EXCLUDED.monthly_jobs_limit,monthly_bytes_mode=EXCLUDED.monthly_bytes_mode,monthly_bytes_limit=EXCLUDED.monthly_bytes_limit`, g.ID, g.Name, g.Description, role, encode(g.Permissions), g.Quotas.MaxFileBytes.Mode, quotaDBValue(g.Quotas.MaxFileBytes), g.Quotas.MonthlyJobs.Mode, quotaDBValue(g.Quotas.MonthlyJobs), g.Quotas.MonthlyBytes.Mode, quotaDBValue(g.Quotas.MonthlyBytes))
	if err != nil {
		return Group{}, err
	}
	_ = res
	return g, nil
}
func (s *Service) DeleteGroup(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM conv_groups WHERE id=$1`, id)
	return err
}
func (s *Service) SetGroupMembers(ctx context.Context, groupID string, userIDs []string, add bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_groups WHERE id=$1)`, groupID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	for _, id := range userIDs {
		if add {
			_, err = tx.ExecContext(ctx, `INSERT INTO conv_group_members(group_id,user_id) SELECT $1,id FROM conv_users WHERE id=$2 AND enabled ON CONFLICT DO NOTHING`, groupID, id)
		} else {
			_, err = tx.ExecContext(ctx, `DELETE FROM conv_group_members WHERE group_id=$1 AND user_id=$2`, groupID, id)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) ReplaceGroupMembers(ctx context.Context, groupID string, userIDs []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conv_groups WHERE id=$1)`, groupID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM conv_group_members WHERE group_id=$1`, groupID); err != nil {
		return err
	}
	for _, uid := range uniqueStrings(userIDs) {
		res, e := tx.ExecContext(ctx, `INSERT INTO conv_group_members(group_id,user_id) SELECT $1,id FROM conv_users WHERE id=$2 AND enabled`, groupID, uid)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return ErrNotFound
		}
	}
	return tx.Commit()
}
func (s *Service) SetUserGroups(ctx context.Context, userID string, ids []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM conv_group_members WHERE user_id=$1`, userID); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, `INSERT INTO conv_group_members(group_id,user_id) SELECT id,$2 FROM conv_groups WHERE id=$1`, id, userID); err != nil {
			return err
		}
	}
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM conv_group_members WHERE user_id=$1`, userID).Scan(&n); err != nil {
		return err
	}
	if n != len(ids) {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s *Service) RevokeAPIKey(ctx context.Context, userID, keyID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE conv_api_keys SET revoked_at=now() WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, keyID, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) AuthenticateAPIKey(ctx context.Context, token string) (Principal, error) {
	if !strings.HasPrefix(token, "101c_") || len(token) > 100 {
		return Principal{}, ErrInvalidCredentials
	}
	sum := sha256.Sum256([]byte(token))
	var p Principal
	var raw []byte
	var enabled bool
	err := s.db.QueryRowContext(ctx, `SELECT u.id,u.username,u.role,u.can_convert,u.can_read_jobs,u.max_file_bytes,u.monthly_jobs,u.monthly_bytes,u.enabled,k.scopes FROM conv_api_keys k JOIN conv_users u ON u.id=k.user_id WHERE k.token_hash=$1 AND k.revoked_at IS NULL`, sum[:]).Scan(&p.ID, &p.Username, &p.Role, &p.CanConvert, &p.CanReadJobs, &p.MaxFileBytes, &p.MonthlyJobs, &p.MonthlyBytes, &enabled, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Principal{}, ErrInvalidCredentials
	}
	if err != nil {
		return Principal{}, err
	}
	if !enabled {
		return Principal{}, ErrDisabled
	}
	if err = json.Unmarshal(raw, &p.Scopes); err != nil {
		return Principal{}, err
	}
	p.APIKey = true
	_, _ = s.db.ExecContext(ctx, `UPDATE conv_api_keys SET last_used_at=now() WHERE token_hash=$1 AND (last_used_at IS NULL OR last_used_at < now()-interval '5 minutes')`, sum[:])
	return s.resolvePrincipal(ctx, p)
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func passwordHash(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 2, 64*1024, 2, 32)
}

func (s *Service) ChangePassword(ctx context.Context, id, current, newPassword string) error {
	if len(newPassword) < 12 || len(newPassword) > 256 {
		return ErrInvalidInput
	}
	var salt, expected []byte
	err := s.db.QueryRowContext(ctx, `SELECT password_salt,password_hash FROM conv_users WHERE id=$1`, id).Scan(&salt, &expected)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	got := passwordHash(current, salt)
	if len(got) != len(expected) || subtle.ConstantTimeCompare(got, expected) != 1 {
		return ErrInvalidCredentials
	}
	newSalt := make([]byte, 16)
	if _, err = rand.Read(newSalt); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE conv_users SET password_salt=$2,password_hash=$3 WHERE id=$1`, id, newSalt, passwordHash(newPassword, newSalt))
	return err
}

func ScopeAllows(p Principal, scope string) bool {
	if p.IsAdmin() {
		return true
	}
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

func (s *Service) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
