package immich

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

const connectorID = "immich"

// ConfigView is the safe, redacted API representation of the Immich connector.
// The stored API key is never returned in API responses.
type ConfigView struct {
	Configured       bool       `json:"configured"`
	BaseURL          string     `json:"baseUrl,omitempty"`
	APIKeyConfigured bool       `json:"apiKeyConfigured"`
	LastVersion      string     `json:"lastVersion,omitempty"`
	Compatible       bool       `json:"compatible"`
	LastError        string     `json:"lastError,omitempty"`
	LastCheckedAt    *time.Time `json:"lastCheckedAt,omitempty"`
	Disclosure       string     `json:"disclosure"`
}

type Store struct {
	db *sql.DB
}

func NewStore(ctx context.Context, db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("database is required for Immich connector store")
	}
	_, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS conv_connector_configs (
 id TEXT PRIMARY KEY,
 base_url TEXT NOT NULL DEFAULT '',
 api_key TEXT NOT NULL DEFAULT '',
 last_version TEXT NOT NULL DEFAULT '',
 compatible BOOLEAN NOT NULL DEFAULT false,
 last_error TEXT NOT NULL DEFAULT '',
 last_checked_at TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);`)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) GetView(ctx context.Context) (ConfigView, error) {
	view := ConfigView{Disclosure: HonestDisclosure}
	var apiKey string
	var checkedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT base_url, api_key, last_version, compatible, last_error, last_checked_at FROM conv_connector_configs WHERE id=$1`, connectorID).
		Scan(&view.BaseURL, &apiKey, &view.LastVersion, &view.Compatible, &view.LastError, &checkedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return view, nil
	}
	if err != nil {
		return ConfigView{}, err
	}
	view.APIKeyConfigured = strings.TrimSpace(apiKey) != ""
	view.Configured = view.BaseURL != "" && view.APIKeyConfigured
	if checkedAt.Valid {
		t := checkedAt.Time
		view.LastCheckedAt = &t
	}
	return view, nil
}

// Save stores the validated Immich URL and API key. A blank apiKey preserves
// the previously stored key so administrators can update the URL without
// re-entering the secret.
func (s *Store) Save(ctx context.Context, baseURL, apiKey string) (ConfigView, error) {
	cleanURL, err := ValidateServerURL(baseURL)
	if err != nil {
		return ConfigView{}, err
	}
	trimmedKey := strings.TrimSpace(apiKey)
	if trimmedKey == "" {
		var existing string
		_ = s.db.QueryRowContext(ctx, `SELECT api_key FROM conv_connector_configs WHERE id=$1`, connectorID).Scan(&existing)
		trimmedKey = strings.TrimSpace(existing)
	}
	if trimmedKey == "" {
		return ConfigView{}, errors.New("Immich API key is required")
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO conv_connector_configs(id,base_url,api_key,updated_at) VALUES($1,$2,$3,now())
ON CONFLICT(id) DO UPDATE SET base_url=EXCLUDED.base_url,api_key=EXCLUDED.api_key,updated_at=now()`, connectorID, cleanURL, trimmedKey)
	if err != nil {
		return ConfigView{}, err
	}
	return s.GetView(ctx)
}

func (s *Store) LoadClient(ctx context.Context) (*Client, error) {
	var baseURL, apiKey string
	err := s.db.QueryRowContext(ctx, `SELECT base_url, api_key FROM conv_connector_configs WHERE id=$1`, connectorID).Scan(&baseURL, &apiKey)
	if errors.Is(err, sql.ErrNoRows) || strings.TrimSpace(baseURL) == "" || strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("Immich connector is not configured")
	}
	if err != nil {
		return nil, err
	}
	return NewClient(baseURL, apiKey, nil)
}

func (s *Store) RecordCheck(ctx context.Context, version string, compatible bool, checkErr string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conv_connector_configs SET last_version=$2, compatible=$3, last_error=$4, last_checked_at=now(), updated_at=now() WHERE id=$1`, connectorID, version, compatible, checkErr)
	return err
}
