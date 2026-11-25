package document

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrProjectNotFound = errors.New("document project not found")
	ErrProjectExpired  = errors.New("document project has expired")
	ErrForbidden       = errors.New("document project belongs to another user")
)

type Project struct {
	ID           string     `json:"id"`
	OwnerID      string     `json:"ownerId,omitempty"`
	IsGuest      bool       `json:"isGuest"`
	Name         string     `json:"name"`
	OutputFormat string     `json:"outputFormat"`
	PageCount    int        `json:"pageCount"`
	Manifest     Manifest   `json:"manifest"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

type ProjectStore struct {
	db *sql.DB
}

func NewProjectStore(db *sql.DB) (*ProjectStore, error) {
	if db == nil {
		return nil, fmt.Errorf("document project store requires PostgreSQL")
	}
	return &ProjectStore{db: db}, nil
}

func (s *ProjectStore) Migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS conv_document_projects (
 id TEXT PRIMARY KEY,
 owner_id TEXT NOT NULL,
 is_guest BOOLEAN NOT NULL DEFAULT false,
 name TEXT NOT NULL,
 output_format TEXT NOT NULL DEFAULT 'pdf',
 page_count INTEGER NOT NULL DEFAULT 0,
 manifest JSONB NOT NULL,
 expires_at TIMESTAMPTZ,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS conv_document_projects_owner_updated_idx ON conv_document_projects(owner_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS conv_document_projects_expires_idx ON conv_document_projects(expires_at) WHERE expires_at IS NOT NULL;
`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func newProjectID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "doc-" + hex.EncodeToString(b[:]), nil
}

func (s *ProjectStore) Create(ctx context.Context, ownerID string, isGuest bool, manifest Manifest, expiresAt *time.Time) (Project, error) {
	if strings.TrimSpace(ownerID) == "" {
		return Project{}, fmt.Errorf("owner id is required")
	}
	if err := manifest.Validate(); err != nil {
		return Project{}, err
	}
	id, err := newProjectID()
	if err != nil {
		return Project{}, err
	}
	manifest.ProjectID = id
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.OutputFormat = strings.ToLower(strings.TrimSpace(manifest.OutputFormat))
	raw, err := json.Marshal(manifest)
	if err != nil {
		return Project{}, err
	}
	var proj Project
	var rawOut []byte
	var exp sql.NullTime
	if expiresAt != nil {
		exp = sql.NullTime{Time: *expiresAt, Valid: true}
	}
	err = s.db.QueryRowContext(ctx,
		`INSERT INTO conv_document_projects(id, owner_id, is_guest, name, output_format, page_count, manifest, expires_at)
		 VALUES($1, $2, $3, $4, $5, $6, $7, $8)
		 RETURNING id, owner_id, is_guest, name, output_format, page_count, manifest, expires_at, created_at, updated_at`,
		id, ownerID, isGuest, manifest.Name, manifest.OutputFormat, len(manifest.Pages), raw, exp,
	).Scan(&proj.ID, &proj.OwnerID, &proj.IsGuest, &proj.Name, &proj.OutputFormat, &proj.PageCount, &rawOut, &exp, &proj.CreatedAt, &proj.UpdatedAt)
	if err != nil {
		return Project{}, err
	}
	if exp.Valid {
		t := exp.Time
		proj.ExpiresAt = &t
	}
	if err := json.Unmarshal(rawOut, &proj.Manifest); err != nil {
		return Project{}, err
	}
	return proj, nil
}

func (s *ProjectStore) Get(ctx context.Context, id, ownerID string, isAdmin bool) (Project, error) {
	if !identifierRE.MatchString(id) {
		return Project{}, ErrProjectNotFound
	}
	var proj Project
	var raw []byte
	var exp sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT id, owner_id, is_guest, name, output_format, page_count, manifest, expires_at, created_at, updated_at
		 FROM conv_document_projects WHERE id = $1`, id,
	).Scan(&proj.ID, &proj.OwnerID, &proj.IsGuest, &proj.Name, &proj.OutputFormat, &proj.PageCount, &raw, &exp, &proj.CreatedAt, &proj.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrProjectNotFound
	}
	if err != nil {
		return Project{}, err
	}
	if !isAdmin && proj.OwnerID != ownerID {
		return Project{}, ErrForbidden
	}
	if exp.Valid {
		t := exp.Time
		if time.Now().After(t) {
			return Project{}, ErrProjectExpired
		}
		proj.ExpiresAt = &t
	}
	if err := json.Unmarshal(raw, &proj.Manifest); err != nil {
		return Project{}, err
	}
	return proj, nil
}

func (s *ProjectStore) List(ctx context.Context, ownerID string, limit int) ([]Project, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, owner_id, is_guest, name, output_format, page_count, manifest, expires_at, created_at, updated_at
		 FROM conv_document_projects
		 WHERE owner_id = $1 AND (expires_at IS NULL OR expires_at > now())
		 ORDER BY updated_at DESC
		 LIMIT $2`, ownerID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Project, 0)
	for rows.Next() {
		var proj Project
		var raw []byte
		var exp sql.NullTime
		if err := rows.Scan(&proj.ID, &proj.OwnerID, &proj.IsGuest, &proj.Name, &proj.OutputFormat, &proj.PageCount, &raw, &exp, &proj.CreatedAt, &proj.UpdatedAt); err != nil {
			return nil, err
		}
		if exp.Valid {
			t := exp.Time
			proj.ExpiresAt = &t
		}
		if err := json.Unmarshal(raw, &proj.Manifest); err != nil {
			return nil, err
		}
		out = append(out, proj)
	}
	return out, rows.Err()
}

func (s *ProjectStore) Update(ctx context.Context, id, ownerID string, isAdmin bool, manifest Manifest) (Project, error) {
	existing, err := s.Get(ctx, id, ownerID, isAdmin)
	if err != nil {
		return Project{}, err
	}
	manifest.ProjectID = existing.ID
	if err := manifest.Validate(); err != nil {
		return Project{}, err
	}
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.OutputFormat = strings.ToLower(strings.TrimSpace(manifest.OutputFormat))
	raw, err := json.Marshal(manifest)
	if err != nil {
		return Project{}, err
	}
	var proj Project
	var rawOut []byte
	var exp sql.NullTime
	err = s.db.QueryRowContext(ctx,
		`UPDATE conv_document_projects
		 SET name = $1, output_format = $2, page_count = $3, manifest = $4, updated_at = now()
		 WHERE id = $5
		 RETURNING id, owner_id, is_guest, name, output_format, page_count, manifest, expires_at, created_at, updated_at`,
		manifest.Name, manifest.OutputFormat, len(manifest.Pages), raw, existing.ID,
	).Scan(&proj.ID, &proj.OwnerID, &proj.IsGuest, &proj.Name, &proj.OutputFormat, &proj.PageCount, &rawOut, &exp, &proj.CreatedAt, &proj.UpdatedAt)
	if err != nil {
		return Project{}, err
	}
	if exp.Valid {
		t := exp.Time
		proj.ExpiresAt = &t
	}
	if err := json.Unmarshal(rawOut, &proj.Manifest); err != nil {
		return Project{}, err
	}
	return proj, nil
}

func (s *ProjectStore) Delete(ctx context.Context, id, ownerID string, isAdmin bool) error {
	if _, err := s.Get(ctx, id, ownerID, isAdmin); err != nil {
		if errors.Is(err, ErrProjectExpired) {
			// Allow deleting an already-expired owned project cleanly.
			_, delErr := s.db.ExecContext(ctx, `DELETE FROM conv_document_projects WHERE id = $1 AND ($2 OR owner_id = $3)`, id, isAdmin, ownerID)
			return delErr
		}
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM conv_document_projects WHERE id = $1`, id)
	return err
}

func (s *ProjectStore) PurgeExpired(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM conv_document_projects WHERE expires_at IS NOT NULL AND expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
