package automation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/timisah/101conv/server/internal/version"
)

// allowedExtensionPermissions enumerates the only declarative permissions an
// extension manifest may request. Arbitrary host/shell/docker permissions are
// rejected at validation time.
var allowedExtensionPermissions = map[string]bool{
	"automations.trigger":   true,
	"automations.condition": true,
	"automations.action":    true,
	"metadata.read":         true,
	"files.read":            true,
	"files.write":           true,
	"immich.assets.read":    true,
	"immich.assets.upload":  true,
}

// ComputeManifestIntegrity returns a deterministic sha256:<hex> digest over
// the declarative manifest payload (excluding the Integrity field itself).
func ComputeManifestIntegrity(manifest ExtensionManifest) (string, error) {
	clone := manifest
	clone.Integrity = ""
	raw, err := json.Marshal(clone)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// VerifyExtensionSecurity validates declared permissions against the explicit
// allowlist and verifies the manifest's SHA-256 integrity digest when present.
func VerifyExtensionSecurity(manifest ExtensionManifest) error {
	for _, perm := range manifest.Permissions {
		if !allowedExtensionPermissions[perm] {
			return fmt.Errorf("extension requests unapproved permission %q", perm)
		}
	}
	if manifest.Integrity != "" {
		if !sha256Integrity.MatchString(manifest.Integrity) {
			return errors.New("extension integrity must be a sha256 digest")
		}
		expected, err := ComputeManifestIntegrity(manifest)
		if err != nil {
			return err
		}
		if !strings.EqualFold(manifest.Integrity, expected) {
			return fmt.Errorf("extension integrity digest mismatch: expected %s", expected)
		}
	}
	return nil
}

// ExternalVersionCompatible checks whether a reported external application
// version (for example Immich "1.118.2") satisfies a declarative version range
// string such as ">=1.115.0 <3.0.0" or ">=1.100.0 <1.120.0".
func ExternalVersionCompatible(constraint, reported string) (bool, string) {
	constraint = strings.TrimSpace(constraint)
	reported = strings.TrimPrefix(strings.TrimSpace(reported), "v")
	if constraint == "" {
		return true, ""
	}
	if reported == "" {
		return false, "external application did not report a version"
	}
	if _, err := version.Parse(reported); err != nil {
		return false, fmt.Sprintf("reported external version %q is not valid semver", reported)
	}
	for _, token := range strings.Fields(constraint) {
		switch {
		case strings.HasPrefix(token, ">="):
			min := strings.TrimPrefix(token, ">=")
			cmp, err := version.Compare(reported, min)
			if err != nil || cmp < 0 {
				return false, fmt.Sprintf("reported version %s is below minimum supported %s", reported, min)
			}
		case strings.HasPrefix(token, "<"):
			max := strings.TrimPrefix(token, "<")
			cmp, err := version.Compare(reported, max)
			if err != nil || cmp >= 0 {
				return false, fmt.Sprintf("reported version %s is at or above unsupported version %s", reported, max)
			}
		}
	}
	return true, ""
}

// BuiltinExtensionCatalog returns the curated, integrity-verified declarative
// extension releases available in the built-in marketplace, including older
// compatible releases so administrators can pin or roll back safely.
func BuiltinExtensionCatalog() []ExtensionManifest {
	manifests := []ExtensionManifest{
		{
			ID:                  "immich-connector",
			Name:                "Immich Media Connector",
			Publisher:           "101conv-official",
			Version:             "1.1.0",
			ExtensionAPI:        ExtensionAPIVersion,
			ExtensionAPIRange:   APIVersionRange{Minimum: "1.0.0", MaximumExclusive: "2.0.0"},
			ExternalApplication: "immich",
			ExternalVersion:     ">=1.115.0 <3.0.0",
			Permissions:         []string{"immich.assets.read", "immich.assets.upload", "automations.trigger", "automations.action"},
			Capabilities:        []string{"immich.asset.read", "immich.asset.upload"},
			ConfigurationSchema: 1,
			Blocks: []Block{
				{
					ID:                 "immich.asset-source",
					Version:            "1.1.0",
					Kind:               "trigger",
					Name:               "Immich asset source",
					Description:        "Discover and fetch original assets from a connected Immich server via documented REST APIs.",
					ConfigSchema:       map[string]any{"assetType": "IMAGE|VIDEO", "limit": "integer"},
					RequiredPermission: "immich.assets.read",
				},
				{
					ID:                 "immich.asset-upload",
					Version:            "1.1.0",
					Kind:               "action",
					Name:               "Upload new Immich asset",
					Description:        "Submit a processed file to Immich as a new asset via POST /api/assets. Does not replace originals in-place.",
					ConfigSchema:       map[string]any{"deviceId": "string"},
					RequiredPermission: "immich.assets.upload",
				},
			},
		},
		{
			ID:                  "immich-connector",
			Name:                "Immich Media Connector",
			Publisher:           "101conv-official",
			Version:             "1.0.0",
			ExtensionAPI:        ExtensionAPIVersion,
			ExtensionAPIRange:   APIVersionRange{Minimum: "1.0.0", MaximumExclusive: "2.0.0"},
			ExternalApplication: "immich",
			ExternalVersion:     ">=1.100.0 <1.120.0",
			Permissions:         []string{"immich.assets.read", "immich.assets.upload", "automations.trigger", "automations.action"},
			Capabilities:        []string{"immich.asset.read", "immich.asset.upload"},
			ConfigurationSchema: 1,
			Blocks: []Block{
				{
					ID:                 "immich.asset-source",
					Version:            "1.0.0",
					Kind:               "trigger",
					Name:               "Immich asset source (legacy Immich 1.100–1.119)",
					Description:        "Retained compatible release for older Immich servers.",
					ConfigSchema:       map[string]any{"assetType": "IMAGE|VIDEO", "limit": "integer"},
					RequiredPermission: "immich.assets.read",
				},
				{
					ID:                 "immich.asset-upload",
					Version:            "1.0.0",
					Kind:               "action",
					Name:               "Upload new Immich asset",
					Description:        "Submit a processed file to Immich as a new asset via POST /api/assets.",
					ConfigSchema:       map[string]any{"deviceId": "string"},
					RequiredPermission: "immich.assets.upload",
				},
			},
		},
	}
	for i := range manifests {
		if digest, err := ComputeManifestIntegrity(manifests[i]); err == nil {
			manifests[i].Integrity = digest
		}
	}
	return manifests
}

// ActiveExtensionByID returns the currently active release for an extension ID,
// if one is installed and active.
func (s *Service) ActiveExtensionByID(ctx context.Context, id string) (InstalledExtension, bool, error) {
	items, err := s.ListExtensions(ctx)
	if err != nil {
		return InstalledExtension{}, false, err
	}
	for _, item := range items {
		if item.Manifest.ID == id && item.Active {
			return item, true, nil
		}
	}
	return InstalledExtension{}, false, nil
}

// PauseAutomationsForExtension pauses ONLY automations whose active revision
// graph references the given extension ID, leaving all other automations
// unaffected. Returns the number of paused automations.
func (s *Service) PauseAutomationsForExtension(ctx context.Context, extensionID, reason string) (int, error) {
	if s == nil || s.db == nil || strings.TrimSpace(extensionID) == "" {
		return 0, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, r.definition FROM conv_automations a JOIN conv_automation_revisions r ON r.automation_id=a.id AND r.revision=a.current_revision WHERE a.enabled=true`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var affected []string
	for rows.Next() {
		var id string
		var raw []byte
		if scanErr := rows.Scan(&id, &raw); scanErr != nil {
			return 0, scanErr
		}
		var def Definition
		if json.Unmarshal(raw, &def) != nil || def.Graph == nil {
			continue
		}
		for _, node := range def.Graph.Nodes {
			if node.ExtensionID == extensionID {
				affected = append(affected, id)
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	diag := fmt.Sprintf("Paused due to incompatible external app for extension %s: %s. Update or roll back the extension to resume; other automations remain unaffected.", extensionID, strings.TrimSpace(reason))
	paused := 0
	for _, id := range affected {
		res, execErr := s.db.ExecContext(ctx, `UPDATE conv_automations SET enabled=false,status='paused_incompatible',diagnostic=$2,updated_at=now() WHERE id=$1`, id, diag)
		if execErr != nil {
			return paused, execErr
		}
		if n, _ := res.RowsAffected(); n > 0 {
			paused++
		}
	}
	return paused, nil
}
