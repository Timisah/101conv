// Package presets stores user intent separately from executable profile
// manifests. A preset may prefer a provider, but it never stores arbitrary
// command arguments or engine paths.
package presets

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/timisah/101conv/server/internal/capability"
	"github.com/timisah/101conv/server/internal/management"
)

var (
	ErrInvalidInput = errors.New("invalid preset input")
	ErrNotFound     = errors.New("preset not found")
	ErrForbidden    = errors.New("preset ownership does not permit this operation")
	ErrIncompatible = errors.New("preset is incompatible with the installed capabilities")
	formatRE        = regexp.MustCompile(`^[a-z0-9]{1,12}$`)
)

type Preset struct {
	ID                   string            `json:"id"`
	Name                 string            `json:"name"`
	Description          string            `json:"description,omitempty"`
	Mode                 string            `json:"mode"`
	SourceFormat         string            `json:"sourceFormat"`
	TargetFormat         string            `json:"targetFormat"`
	ProviderID           string            `json:"providerId,omitempty"`
	ProfileID            string            `json:"profileId,omitempty"`
	ProfileVersion       string            `json:"profileVersion,omitempty"`
	Options              map[string]string `json:"options,omitempty"`
	Shared               bool              `json:"shared"`
	Default              bool              `json:"default"`
	Compatible           bool              `json:"compatible"`
	CompatibilityMessage string            `json:"compatibilityMessage,omitempty"`
	CreatedAt            time.Time         `json:"createdAt"`
	UpdatedAt            time.Time         `json:"updatedAt"`
	owner                string
}

type WriteRequest struct {
	Name           string            `json:"name"`
	Description    string            `json:"description,omitempty"`
	Mode           string            `json:"mode"`
	SourceFormat   string            `json:"sourceFormat"`
	TargetFormat   string            `json:"targetFormat"`
	ProviderID     string            `json:"providerId,omitempty"`
	ProfileID      string            `json:"profileId,omitempty"`
	ProfileVersion string            `json:"profileVersion,omitempty"`
	Options        map[string]string `json:"options,omitempty"`
	Shared         bool              `json:"shared,omitempty"`
	Default        bool              `json:"default,omitempty"`
}

type Service struct {
	db         *sql.DB
	management *management.Service
}

func NewService(db *sql.DB, managementService *management.Service) (*Service, error) {
	if db == nil || managementService == nil {
		return nil, fmt.Errorf("presets require PostgreSQL and converter management")
	}
	return &Service{db: db, management: managementService}, nil
}

func (s *Service) Migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS conv_presets (
 id TEXT PRIMARY KEY,
 owner_id TEXT NOT NULL,
 name TEXT NOT NULL,
 description TEXT NOT NULL DEFAULT '',
 mode TEXT NOT NULL CHECK (mode IN ('convert','compress')),
 source_format TEXT NOT NULL,
 target_format TEXT NOT NULL,
 provider_id TEXT NOT NULL DEFAULT '',
 profile_id TEXT NOT NULL DEFAULT '',
 profile_version TEXT NOT NULL DEFAULT '',
 options JSONB NOT NULL DEFAULT '{}'::jsonb,
 shared BOOLEAN NOT NULL DEFAULT false,
 is_default BOOLEAN NOT NULL DEFAULT false,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 CHECK (length(name) BETWEEN 1 AND 80),
 CHECK (source_format ~ '^[a-z0-9]{1,12}$' AND target_format ~ '^[a-z0-9]{1,12}$')
);
CREATE INDEX IF NOT EXISTS conv_presets_owner_idx ON conv_presets(owner_id, mode, updated_at DESC);
CREATE INDEX IF NOT EXISTS conv_presets_shared_idx ON conv_presets(shared, mode, updated_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS conv_presets_default_idx ON conv_presets(owner_id, mode, source_format, target_format) WHERE is_default;
`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func (s *Service) List(ctx context.Context, ownerID, mode string) ([]Preset, error) {
	if strings.TrimSpace(ownerID) == "" {
		return nil, fmt.Errorf("%w: owner is required", ErrInvalidInput)
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "" && mode != capability.OperationConvert && mode != capability.OperationCompress {
		return nil, fmt.Errorf("%w: mode must be convert or compress", ErrInvalidInput)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,name,description,mode,source_format,target_format,provider_id,profile_id,profile_version,options,shared,is_default,created_at,updated_at FROM conv_presets WHERE (owner_id=$1 OR shared=true) AND ($2='' OR mode=$2) ORDER BY is_default DESC, lower(name), id`, ownerID, mode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Preset{}
	for rows.Next() {
		var item Preset
		var storedOwner string
		var rawOptions []byte
		if err = rows.Scan(&item.ID, &storedOwner, &item.Name, &item.Description, &item.Mode, &item.SourceFormat, &item.TargetFormat, &item.ProviderID, &item.ProfileID, &item.ProfileVersion, &rawOptions, &item.Shared, &item.Default, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(rawOptions, &item.Options); err != nil {
			return nil, fmt.Errorf("preset %s options: %w", item.ID, err)
		}
		item.Compatible, item.CompatibilityMessage = s.compatibility(ctx, item)
		result = append(result, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) Create(ctx context.Context, ownerID string, request WriteRequest) (Preset, error) {
	return s.create(ctx, ownerID, request, request.Shared)
}

func (s *Service) Update(ctx context.Context, ownerID, id string, request WriteRequest, administrator bool) (Preset, error) {
	item, err := s.get(ctx, id)
	if err != nil {
		return Preset{}, err
	}
	if item.OwnerID() != ownerID && !(administrator && item.Shared) {
		return Preset{}, ErrForbidden
	}
	if request.Shared != item.Shared && !administrator {
		return Preset{}, ErrForbidden
	}
	if request.Shared && !administrator {
		return Preset{}, ErrForbidden
	}
	// Shared presets remain shared when edited. The public API intentionally
	// keeps sharing an administrator-owned lifecycle decision.
	if item.Shared {
		request.Shared = true
	}
	// Clearing a default is an explicit operation through SetDefault. A normal
	// edit must not silently unset a user's or administrator's saved default
	// because the boolean is omitted from a form update.
	if item.Default && !request.Default {
		request.Default = true
	}
	normalizeWrite(&request)
	if err = validateWrite(request); err != nil {
		return Preset{}, err
	}
	if err = s.validateCompatibility(ctx, request); err != nil {
		return Preset{}, err
	}
	options, _ := json.Marshal(request.Options)
	_, err = s.db.ExecContext(ctx, `UPDATE conv_presets SET name=$1,description=$2,mode=$3,source_format=$4,target_format=$5,provider_id=$6,profile_id=$7,profile_version=$8,options=$9,shared=$10,is_default=$11,updated_at=now() WHERE id=$12`, request.Name, request.Description, request.Mode, request.SourceFormat, request.TargetFormat, request.ProviderID, request.ProfileID, request.ProfileVersion, options, request.Shared, request.Default, id)
	if err != nil {
		return Preset{}, err
	}
	if request.Default {
		if err = s.clearOtherDefaults(ctx, item.OwnerID(), request.Mode, request.SourceFormat, request.TargetFormat, id); err != nil {
			return Preset{}, err
		}
	}
	return s.getForOwner(ctx, id, ownerID)
}

func (s *Service) Duplicate(ctx context.Context, ownerID, id, name string) (Preset, error) {
	item, err := s.get(ctx, id)
	if err != nil {
		return Preset{}, err
	}
	if !item.Shared && item.OwnerID() != ownerID {
		return Preset{}, ErrForbidden
	}
	if strings.TrimSpace(name) != "" {
		item.Name = name
	} else {
		item.Name = item.Name + " copy"
	}
	item.Shared = false
	item.Default = false
	return s.create(ctx, ownerID, WriteRequest{Name: item.Name, Description: item.Description, Mode: item.Mode, SourceFormat: item.SourceFormat, TargetFormat: item.TargetFormat, ProviderID: item.ProviderID, ProfileID: item.ProfileID, ProfileVersion: item.ProfileVersion, Options: item.Options}, false)
}

func (s *Service) Delete(ctx context.Context, ownerID, id string, administrator bool) error {
	item, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if item.OwnerID() != ownerID && !(administrator && item.Shared) {
		return ErrForbidden
	}
	_, err = s.db.ExecContext(ctx, `DELETE FROM conv_presets WHERE id=$1`, id)
	return err
}

func (s *Service) SetDefault(ctx context.Context, ownerID, id string, administrator bool, enabled bool) (Preset, error) {
	item, err := s.get(ctx, id)
	if err != nil {
		return Preset{}, err
	}
	if item.OwnerID() != ownerID && !(administrator && item.Shared) {
		return Preset{}, ErrForbidden
	}
	if item.Shared && item.OwnerID() != ownerID && !administrator {
		return Preset{}, ErrForbidden
	}
	if enabled {
		item.Compatible, item.CompatibilityMessage = s.compatibility(ctx, item)
		if !item.Compatible {
			return Preset{}, fmt.Errorf("%w: %s", ErrIncompatible, item.CompatibilityMessage)
		}
		if err = s.clearOtherDefaults(ctx, item.OwnerID(), item.Mode, item.SourceFormat, item.TargetFormat, id); err != nil {
			return Preset{}, err
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE conv_presets SET is_default=$1,updated_at=now() WHERE id=$2`, enabled, id)
	if err != nil {
		return Preset{}, err
	}
	return s.getForOwner(ctx, id, ownerID)
}

// OwnerID is intentionally not serialized; it is only used for authorization
// checks inside the service.
func (p Preset) OwnerID() string { return p.owner }

// Mode returns the operation covered by a preset after applying the same
// visibility check used by mutations. It lets HTTP handlers enforce the
// operation-specific permission before an ID-only mutation such as delete or
// duplicate.
func (s *Service) Mode(ctx context.Context, ownerID, id string) (string, error) {
	item, err := s.get(ctx, id)
	if err != nil {
		return "", err
	}
	if item.owner != ownerID && !item.Shared {
		return "", ErrForbidden
	}
	return item.Mode, nil
}

func (s *Service) create(ctx context.Context, ownerID string, request WriteRequest, shared bool) (Preset, error) {
	if strings.TrimSpace(ownerID) == "" {
		return Preset{}, fmt.Errorf("%w: owner is required", ErrInvalidInput)
	}
	request.Shared = shared
	normalizeWrite(&request)
	if err := validateWrite(request); err != nil {
		return Preset{}, err
	}
	if err := s.validateCompatibility(ctx, request); err != nil {
		return Preset{}, err
	}
	id, err := newID()
	if err != nil {
		return Preset{}, err
	}
	options, err := json.Marshal(request.Options)
	if err != nil {
		return Preset{}, err
	}
	if request.Default {
		if err = s.clearOtherDefaults(ctx, ownerID, request.Mode, request.SourceFormat, request.TargetFormat, ""); err != nil {
			return Preset{}, err
		}
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO conv_presets(id,owner_id,name,description,mode,source_format,target_format,provider_id,profile_id,profile_version,options,shared,is_default) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, id, ownerID, request.Name, request.Description, request.Mode, request.SourceFormat, request.TargetFormat, request.ProviderID, request.ProfileID, request.ProfileVersion, options, request.Shared, request.Default)
	if err != nil {
		return Preset{}, err
	}
	return s.getForOwner(ctx, id, ownerID)
}

func (s *Service) validateCompatibility(ctx context.Context, request WriteRequest) error {
	profiles, err := s.management.ListProfiles(ctx)
	if err != nil {
		return err
	}
	engines, err := s.management.ListEngines(ctx)
	if err != nil {
		return err
	}
	catalog := capability.Build(profiles, engines)
	preferred := request.ProviderID
	if preferred == "" {
		preferred = request.ProfileID
	}
	resolution, err := catalog.Resolve(capability.Request{Operation: request.Mode, SourceFormat: request.SourceFormat, TargetFormat: request.TargetFormat}, preferred)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrIncompatible, err)
	}
	if preferred != "" && resolution.Strategy != "explicit provider preference" {
		return fmt.Errorf("%w: preferred provider is no longer available", ErrIncompatible)
	}
	if request.ProfileVersion != "" && resolution.Provider.Version != request.ProfileVersion {
		return fmt.Errorf("%w: profile version %s is no longer installed", ErrIncompatible, request.ProfileVersion)
	}
	return validateOptions(resolution.Provider, request.Options)
}

func (s *Service) compatibility(ctx context.Context, item Preset) (bool, string) {
	err := s.validateCompatibility(ctx, WriteRequest{Name: item.Name, Description: item.Description, Mode: item.Mode, SourceFormat: item.SourceFormat, TargetFormat: item.TargetFormat, ProviderID: item.ProviderID, ProfileID: item.ProfileID, ProfileVersion: item.ProfileVersion, Options: item.Options})
	if err == nil {
		return true, ""
	}
	return false, strings.TrimPrefix(err.Error(), ErrIncompatible.Error()+": ")
}

func validateWrite(request WriteRequest) error {
	request.Name = strings.TrimSpace(request.Name)
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	request.SourceFormat = normalizeFormat(request.SourceFormat)
	request.TargetFormat = normalizeFormat(request.TargetFormat)
	if request.Name == "" || len(request.Name) > 80 || request.Mode == "" || (request.Mode != capability.OperationConvert && request.Mode != capability.OperationCompress) || !formatRE.MatchString(request.SourceFormat) || !formatRE.MatchString(request.TargetFormat) {
		return fmt.Errorf("%w: name, mode, source format, and target format are required", ErrInvalidInput)
	}
	if len(request.Description) > 500 || len(request.ProviderID) > 200 || len(request.ProfileID) > 120 || len(request.ProfileVersion) > 40 {
		return fmt.Errorf("%w: preset metadata is too long", ErrInvalidInput)
	}
	if len(request.Options) > 32 {
		return fmt.Errorf("%w: at most 32 options are allowed", ErrInvalidInput)
	}
	for key, value := range request.Options {
		if strings.TrimSpace(key) == "" || len(key) > 80 || len(value) > 256 {
			return fmt.Errorf("%w: option keys and values are bounded", ErrInvalidInput)
		}
	}
	return nil
}

func normalizeWrite(request *WriteRequest) {
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	request.SourceFormat = normalizeFormat(request.SourceFormat)
	request.TargetFormat = normalizeFormat(request.TargetFormat)
	request.ProviderID = strings.TrimSpace(request.ProviderID)
	request.ProfileID = strings.TrimSpace(request.ProfileID)
	request.ProfileVersion = strings.TrimSpace(request.ProfileVersion)
	if request.Options == nil {
		request.Options = map[string]string{}
	}
}

func validateOptions(provider capability.Provider, values map[string]string) error {
	allowed := map[string]capability.Option{}
	for _, item := range provider.Capabilities {
		for _, option := range item.Options {
			allowed[option.Key] = option
		}
	}
	for key, value := range values {
		option, ok := allowed[key]
		if !ok {
			return fmt.Errorf("%w: option %q is not declared by the selected provider", ErrIncompatible, key)
		}
		if option.Type == "select" {
			valid := false
			for _, choice := range option.Choices {
				if value == choice {
					valid = true
				}
			}
			if !valid {
				return fmt.Errorf("%w: option %q has an invalid choice", ErrIncompatible, key)
			}
		}
		switch option.Type {
		case "number":
			number, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return fmt.Errorf("%w: option %q must be numeric", ErrIncompatible, key)
			}
			if option.Min != nil && number < *option.Min || option.Max != nil && number > *option.Max {
				return fmt.Errorf("%w: option %q is outside the supported range", ErrIncompatible, key)
			}
		case "boolean":
			if value != "true" && value != "false" {
				return fmt.Errorf("%w: option %q must be true or false", ErrIncompatible, key)
			}
		}
	}
	return nil
}

func (s *Service) get(ctx context.Context, id string) (Preset, error) {
	var item Preset
	var owner string
	var raw []byte
	err := s.db.QueryRowContext(ctx, `SELECT id,owner_id,name,description,mode,source_format,target_format,provider_id,profile_id,profile_version,options,shared,is_default,created_at,updated_at FROM conv_presets WHERE id=$1`, id).Scan(&item.ID, &owner, &item.Name, &item.Description, &item.Mode, &item.SourceFormat, &item.TargetFormat, &item.ProviderID, &item.ProfileID, &item.ProfileVersion, &raw, &item.Shared, &item.Default, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Preset{}, ErrNotFound
	}
	if err != nil {
		return Preset{}, err
	}
	if err = json.Unmarshal(raw, &item.Options); err != nil {
		return Preset{}, err
	}
	item.owner = owner
	return item, nil
}

func (s *Service) getForOwner(ctx context.Context, id, ownerID string) (Preset, error) {
	item, err := s.get(ctx, id)
	if err != nil {
		return Preset{}, err
	}
	if item.owner != ownerID && !item.Shared {
		return Preset{}, ErrForbidden
	}
	item.Compatible, item.CompatibilityMessage = s.compatibility(ctx, item)
	return item, nil
}

func (s *Service) clearOtherDefaults(ctx context.Context, owner, mode, source, target, except string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE conv_presets SET is_default=false,updated_at=now() WHERE owner_id=$1 AND mode=$2 AND source_format=$3 AND target_format=$4 AND id<>$5 AND is_default=true`, owner, mode, source, target, except)
	return err
}

func normalizeFormat(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func newID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(bytes[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}
