package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/timisah/101conv/server/internal/version"
)

var sha256Integrity = regexp.MustCompile(`^sha256:[0-9a-fA-F]{64}$`)

var (
	ErrExtensionNotFound = errors.New("extension release not found")
	ErrExtensionConflict = errors.New("extension release cannot be activated")
)

// ValidateExtensionManifest accepts declarative metadata only. In particular,
// it rejects executable-looking configuration keys and never accepts an
// archive, command, or runtime entrypoint from an extension package.
func ValidateExtensionManifest(manifest ExtensionManifest) error {
	if !idPattern.MatchString(manifest.ID) || strings.TrimSpace(manifest.Name) == "" || len(manifest.Name) > 120 || strings.TrimSpace(manifest.Publisher) == "" || len(manifest.Publisher) > 120 {
		return errors.New("extension id, name, and publisher are required")
	}
	if _, err := version.Parse(manifest.Version); err != nil {
		return fmt.Errorf("extension version is invalid: %w", err)
	}
	if manifest.ConfigurationSchema == 0 {
		manifest.ConfigurationSchema = 1
	}
	if manifest.ConfigurationSchema < 1 || manifest.ConfigurationSchema > 1000 {
		return errors.New("extension configuration schema is invalid")
	}
	if manifest.Integrity != "" && !sha256Integrity.MatchString(manifest.Integrity) {
		return errors.New("extension integrity must be a sha256 digest")
	}
	if manifest.ExtensionAPIRange.Minimum != "" {
		if _, err := version.Parse(manifest.ExtensionAPIRange.Minimum); err != nil {
			return fmt.Errorf("extension API minimum is invalid: %w", err)
		}
	}
	if manifest.ExtensionAPIRange.MaximumExclusive != "" {
		if _, err := version.Parse(manifest.ExtensionAPIRange.MaximumExclusive); err != nil {
			return fmt.Errorf("extension API maximum is invalid: %w", err)
		}
	}
	if manifest.ExtensionAPIRange.Minimum != "" && manifest.ExtensionAPIRange.MaximumExclusive != "" {
		if cmp, _ := version.Compare(manifest.ExtensionAPIRange.Minimum, manifest.ExtensionAPIRange.MaximumExclusive); cmp >= 0 {
			return errors.New("extension API range is empty")
		}
	}
	if len(manifest.Blocks) > 32 || len(manifest.Permissions) > 32 || len(manifest.Capabilities) > 32 || len(manifest.Dependencies) > 32 {
		return errors.New("extension declares too many entries")
	}
	seen := make(map[string]bool, len(manifest.Blocks))
	for _, block := range manifest.Blocks {
		if !graphIDPattern.MatchString(block.ID) || seen[block.ID] || strings.TrimSpace(block.Name) == "" || len(block.Description) > 500 {
			return fmt.Errorf("extension block %q is invalid or duplicated", block.ID)
		}
		seen[block.ID] = true
		if block.Kind != "trigger" && block.Kind != "condition" && block.Kind != "action" {
			return fmt.Errorf("extension block %q has unsupported kind", block.ID)
		}
		if _, err := version.Parse(block.Version); err != nil {
			return fmt.Errorf("extension block %q version is invalid: %w", block.ID, err)
		}
		if len(block.ConfigSchema) > 32 {
			return fmt.Errorf("extension block %q has too many configuration fields", block.ID)
		}
		for key := range block.ConfigSchema {
			lower := strings.ToLower(key)
			if lower == "command" || lower == "shell" || lower == "argv" || lower == "executable" || lower == "source" {
				return fmt.Errorf("extension block %q declares executable configuration", block.ID)
			}
		}
	}
	return nil
}

// InstallExtension retains every release and optionally makes a compatible
// release active. The database is the source of truth for rollback, so an
// incompatible update never replaces the last working active release.
func (s *Service) InstallExtension(ctx context.Context, manifest ExtensionManifest, activate bool) (InstalledExtension, error) {
	if err := ValidateExtensionManifest(manifest); err != nil {
		return InstalledExtension{}, err
	}
	compatible := ExtensionCompatible(manifest, ExtensionAPIVersion)
	status := "incompatible"
	diagnostic := "This release does not support the running automation API. It was retained for review and rollback safety."
	if compatible {
		status, diagnostic = "compatible", ""
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return InstalledExtension{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InstalledExtension{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "extension:"+manifest.ID); err != nil {
		return InstalledExtension{}, err
	}
	var existingActive bool
	if err = tx.QueryRowContext(ctx, `SELECT active FROM conv_automation_extensions WHERE id=$1 AND version=$2`, manifest.ID, manifest.Version).Scan(&existingActive); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return InstalledExtension{}, err
	}
	if existingActive && !compatible {
		return InstalledExtension{}, fmt.Errorf("%w: an active release cannot be replaced by an incompatible manifest", ErrExtensionConflict)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO conv_automation_extensions(id,version,publisher,manifest,compatible,active,status,diagnostic)
VALUES($1,$2,$3,$4,$5,false,$6,$7)
ON CONFLICT(id,version) DO UPDATE SET publisher=EXCLUDED.publisher,manifest=EXCLUDED.manifest,compatible=EXCLUDED.compatible,status=EXCLUDED.status,diagnostic=EXCLUDED.diagnostic,updated_at=now()`, manifest.ID, manifest.Version, manifest.Publisher, raw, compatible, status, diagnostic)
	if err != nil {
		return InstalledExtension{}, err
	}
	if activate && compatible {
		if _, err = tx.ExecContext(ctx, `UPDATE conv_automation_extensions SET active=false,status=CASE WHEN compatible THEN 'compatible' ELSE 'incompatible' END,updated_at=now() WHERE id=$1`, manifest.ID); err != nil {
			return InstalledExtension{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE conv_automation_extensions SET active=true,status='active',diagnostic='',updated_at=now() WHERE id=$1 AND version=$2`, manifest.ID, manifest.Version); err != nil {
			return InstalledExtension{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_automation_extensions SET status='active',diagnostic='',updated_at=now() WHERE id=$1 AND version=$2 AND active`, manifest.ID, manifest.Version); err != nil {
		return InstalledExtension{}, err
	}
	if err = tx.Commit(); err != nil {
		return InstalledExtension{}, err
	}
	return s.GetExtension(ctx, manifest.ID, manifest.Version)
}

func (s *Service) GetExtension(ctx context.Context, id, release string) (InstalledExtension, error) {
	var out InstalledExtension
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT manifest,compatible,active,status,diagnostic,installed_at,updated_at FROM conv_automation_extensions WHERE id=$1 AND version=$2`, id, release).Scan(&raw, &out.Compatible, &out.Active, &out.Status, &out.Diagnostic, &out.InstalledAt, &out.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return InstalledExtension{}, ErrExtensionNotFound
	}
	if err != nil {
		return InstalledExtension{}, err
	}
	if err = json.Unmarshal(raw, &out.Manifest); err != nil {
		return InstalledExtension{}, err
	}
	return out, nil
}

func (s *Service) ListExtensions(ctx context.Context) ([]InstalledExtension, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT manifest,compatible,active,status,diagnostic,installed_at,updated_at FROM conv_automation_extensions ORDER BY id,version DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]InstalledExtension, 0)
	for rows.Next() {
		var item InstalledExtension
		var raw []byte
		if err = rows.Scan(&raw, &item.Compatible, &item.Active, &item.Status, &item.Diagnostic, &item.InstalledAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &item.Manifest); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Service) ActivateExtension(ctx context.Context, id, release string) (InstalledExtension, error) {
	item, err := s.GetExtension(ctx, id, release)
	if err != nil {
		return InstalledExtension{}, err
	}
	if !item.Compatible || !ExtensionCompatible(item.Manifest, ExtensionAPIVersion) {
		return InstalledExtension{}, fmt.Errorf("%w: extension release is incompatible with API %s", ErrExtensionConflict, ExtensionAPIVersion)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return InstalledExtension{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "extension:"+id); err != nil {
		return InstalledExtension{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_automation_extensions SET active=false,status=CASE WHEN compatible THEN 'compatible' ELSE 'incompatible' END,updated_at=now() WHERE id=$1`, id); err != nil {
		return InstalledExtension{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE conv_automation_extensions SET active=true,status='active',diagnostic='',updated_at=now() WHERE id=$1 AND version=$2 AND compatible`, id, release); err != nil {
		return InstalledExtension{}, err
	}
	if err = tx.Commit(); err != nil {
		return InstalledExtension{}, err
	}
	return s.GetExtension(ctx, id, release)
}

// RollbackExtension selects the highest retained compatible release below the
// active release unless the administrator supplies an explicit target.
func (s *Service) RollbackExtension(ctx context.Context, id, target string) (InstalledExtension, error) {
	items, err := s.ListExtensions(ctx)
	if err != nil {
		return InstalledExtension{}, err
	}
	var active *InstalledExtension
	candidates := make([]InstalledExtension, 0)
	for i := range items {
		if items[i].Manifest.ID != id {
			continue
		}
		if items[i].Active {
			active = &items[i]
		}
		if items[i].Compatible {
			candidates = append(candidates, items[i])
		}
	}
	if target != "" {
		return s.ActivateExtension(ctx, id, target)
	}
	if active == nil {
		return InstalledExtension{}, fmt.Errorf("%w: no active release", ErrExtensionConflict)
	}
	previous := make([]InstalledExtension, 0)
	for _, candidate := range candidates {
		cmp, compareErr := version.Compare(candidate.Manifest.Version, active.Manifest.Version)
		if compareErr == nil && cmp < 0 {
			previous = append(previous, candidate)
		}
	}
	if len(previous) == 0 {
		return InstalledExtension{}, fmt.Errorf("%w: no older compatible release is retained", ErrExtensionConflict)
	}
	sort.Slice(previous, func(i, j int) bool {
		cmp, _ := version.Compare(previous[i].Manifest.Version, previous[j].Manifest.Version)
		return cmp > 0
	})
	return s.ActivateExtension(ctx, id, previous[0].Manifest.Version)
}

// ActiveBlocks returns built-ins plus blocks from active, API-compatible
// declarative extensions. Duplicate IDs are suppressed in favor of built-ins.
func (s *Service) ActiveBlocks(ctx context.Context) ([]Block, error) {
	blocks := append([]Block(nil), Blocks...)
	seen := make(map[string]bool, len(blocks))
	for _, block := range blocks {
		seen[block.ID] = true
	}
	items, err := s.ListExtensions(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if !item.Active || !item.Compatible {
			continue
		}
		for _, block := range item.Manifest.Blocks {
			if !seen[block.ID] {
				blocks = append(blocks, block)
				seen[block.ID] = true
			}
		}
	}
	return blocks, nil
}
