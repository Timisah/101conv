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

	ScopeUpload = "conversion:upload"
	ScopeSubmit = "conversion:submit"
	ScopeRead   = "jobs:read"
	ScopeCancel = "jobs:cancel"
	ScopeResult = "results:download"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrNotFound           = errors.New("account or API key not found")
	ErrConflict           = errors.New("account already exists")
	ErrInvalidInput       = errors.New("invalid account or API key input")
	ErrDisabled           = errors.New("account is disabled")
	usernamePattern       = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{2,63}$`)
	keyNamePattern        = regexp.MustCompile(`^[^\x00-\x1f\x7f]{1,80}$`)
)

type Principal struct {
	ID           string   `json:"id"`
	Username     string   `json:"username"`
	Role         string   `json:"role"`
	CanConvert   bool     `json:"canConvert"`
	CanReadJobs  bool     `json:"canReadJobs"`
	MaxFileBytes int64    `json:"maxFileBytes"`
	MonthlyJobs  int      `json:"monthlyJobs"`
	MonthlyBytes int64    `json:"monthlyBytes"`
	Scopes       []string `json:"scopes,omitempty"`
	APIKey       bool     `json:"-"`
}

func AdminPrincipal() Principal {
	return Principal{ID: "admin", Username: "Administrator", Role: RoleAdmin, CanConvert: true, CanReadJobs: true}
}

func (p Principal) IsAdmin() bool { return p.Role == RoleAdmin }

type User struct {
	Principal
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
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
	Username     string
	Password     string
	CanConvert   bool
	CanReadJobs  bool
	MaxFileBytes int64
	MonthlyJobs  int
	MonthlyBytes int64
}

type UserUpdate struct {
	Enabled      *bool
	CanConvert   *bool
	CanReadJobs  *bool
	MaxFileBytes *int64
	MonthlyJobs  *int
	MonthlyBytes *int64
	Password     *string
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
	return err
}

func (s *Service) CreateUser(ctx context.Context, input NewUser) (User, error) {
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	if !usernamePattern.MatchString(input.Username) || len(input.Password) < 12 || len(input.Password) > 256 || input.MaxFileBytes <= 0 || input.MonthlyJobs < 0 || input.MonthlyBytes < 0 {
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
	err = s.db.QueryRowContext(ctx, `INSERT INTO conv_users(id,username,password_salt,password_hash,can_convert,can_read_jobs,max_file_bytes,monthly_jobs,monthly_bytes) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at`, id, input.Username, salt, hash, input.CanConvert, input.CanReadJobs, input.MaxFileBytes, input.MonthlyJobs, input.MonthlyBytes).Scan(&created)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, ErrConflict
		}
		return User{}, err
	}
	return User{Principal: Principal{ID: id, Username: input.Username, Role: RoleUser, CanConvert: input.CanConvert, CanReadJobs: input.CanReadJobs, MaxFileBytes: input.MaxFileBytes, MonthlyJobs: input.MonthlyJobs, MonthlyBytes: input.MonthlyBytes}, Enabled: true, CreatedAt: created}, nil
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
	return p, nil
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
	return p, nil
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
	return users, rows.Err()
}

func (s *Service) UpdateUser(ctx context.Context, id string, update UserUpdate) (User, error) {
	if update.MaxFileBytes != nil && *update.MaxFileBytes <= 0 || update.MonthlyJobs != nil && *update.MonthlyJobs < 0 || update.MonthlyBytes != nil && *update.MonthlyBytes < 0 {
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
	result, err := tx.ExecContext(ctx, `UPDATE conv_users SET enabled=COALESCE($2,enabled),can_convert=COALESCE($3,can_convert),can_read_jobs=COALESCE($4,can_read_jobs),max_file_bytes=COALESCE($5,max_file_bytes),monthly_jobs=COALESCE($6,monthly_jobs),monthly_bytes=COALESCE($7,monthly_bytes),password_salt=COALESCE($8,password_salt),password_hash=COALESCE($9,password_hash) WHERE id=$1`, id, update.Enabled, update.CanConvert, update.CanReadJobs, update.MaxFileBytes, update.MonthlyJobs, update.MonthlyBytes, salt, hash)
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

var allowedScopes = map[string]bool{ScopeUpload: true, ScopeSubmit: true, ScopeRead: true, ScopeCancel: true, ScopeResult: true}

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
	if !p.CanConvert && (unique[ScopeUpload] || unique[ScopeSubmit] || unique[ScopeCancel]) || !p.CanReadJobs && (unique[ScopeRead] || unique[ScopeResult]) {
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
	return p, nil
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
