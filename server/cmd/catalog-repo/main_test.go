package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/timisah/101conv/server/internal/profile"
	"github.com/timisah/101conv/server/internal/repositorymanager"
)

func TestProvisionAndPublishTUFMetadata(t *testing.T) {
	rootDir := t.TempDir()
	keysDir := filepath.Join(rootDir, "keys")
	catalogDir := filepath.Join(rootDir, "catalog")
	rootFile := filepath.Join(catalogDir, "metadata", "root.json")
	embedFile := filepath.Join(rootDir, "embedded-root.json")
	if err := initRoot([]string{"--keys-dir", keysDir, "--root-file", rootFile, "--embed-root", embedFile}); err != nil {
		t.Fatal(err)
	}
	embedded, err := os.ReadFile(embedFile)
	if err != nil {
		t.Fatal(err)
	}
	root, err := metadata.Root().FromBytes(embedded)
	if err != nil {
		t.Fatal(err)
	}
	if err = root.VerifyDelegate(metadata.ROOT, root); err != nil {
		t.Fatalf("invalid bootstrap root: %v", err)
	}

	index := []byte(`{"schemaVersion":1,"engines":[],"profiles":[]}`)
	indexPath := filepath.Join(catalogDir, "targets", "catalog", "index.json")
	if err = os.MkdirAll(filepath.Dir(indexPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(indexPath, index, 0600); err != nil {
		t.Fatal(err)
	}
	if err = publish([]string{"--catalog-root", catalogDir, "--keys-dir", keysDir}); err != nil {
		t.Fatal(err)
	}
	targets, err := metadata.Targets().FromFile(filepath.Join(catalogDir, "metadata", "targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = root.VerifyDelegate(metadata.TARGETS, targets); err != nil {
		t.Fatalf("invalid targets signature: %v", err)
	}
	snapshot, err := metadata.Snapshot().FromFile(filepath.Join(catalogDir, "metadata", "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = root.VerifyDelegate(metadata.SNAPSHOT, snapshot); err != nil {
		t.Fatalf("invalid snapshot signature: %v", err)
	}
	timestamp, err := metadata.Timestamp().FromFile(filepath.Join(catalogDir, "metadata", "timestamp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = root.VerifyDelegate(metadata.TIMESTAMP, timestamp); err != nil {
		t.Fatalf("invalid timestamp signature: %v", err)
	}
	if err = publish([]string{"--catalog-root", catalogDir, "--keys-dir", keysDir}); err != nil {
		t.Fatal(err)
	}
	targets2, err := metadata.Targets().FromFile(filepath.Join(catalogDir, "metadata", "targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if targets2.Signed.Version != targets.Signed.Version+1 {
		t.Fatalf("targets version did not advance: %d -> %d", targets.Signed.Version, targets2.Signed.Version)
	}

	var timestampDoc struct {
		Signed struct {
			Expires time.Time `json:"expires"`
		} `json:"signed"`
	}
	b, err := os.ReadFile(filepath.Join(catalogDir, "metadata", "timestamp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &timestampDoc); err != nil {
		t.Fatal(err)
	}
	if time.Until(timestampDoc.Signed.Expires) < 6*24*time.Hour {
		t.Fatal("timestamp expiry is too short")
	}
}

func TestInitRefusesToOverwriteExistingRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root.json")
	if err := os.WriteFile(root, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	err := initRoot([]string{"--keys-dir", filepath.Join(dir, "keys"), "--root-file", root, "--embed-root", filepath.Join(dir, "embed.json")})
	if err == nil {
		t.Fatal("expected existing root protection")
	}
	if _, err = os.Stat(filepath.Join(dir, "keys")); !os.IsNotExist(err) {
		t.Fatalf("key directory was created before validation: %v", err)
	}
}

func TestValidateCheckedInCatalog(t *testing.T) {
	catalogRoot := filepath.Join("..", "..", "..", "catalog")
	if err := validateCatalog([]string{"--catalog-root", catalogRoot}); err != nil {
		t.Fatalf("checked-in catalog should pass author preflight: %v", err)
	}
}

func TestValidateCatalogRejectsUnsupportedProfilePlaceholder(t *testing.T) {
	root := t.TempDir()
	targets := filepath.Join(root, "targets")
	profileDef := profile.Definition{
		SchemaVersion: 1, ID: "test-profile", Version: "1.0.0", Name: "Test",
		Engines: []profile.EngineRequirement{{EngineID: "manual-engine"}},
		Rules:   []profile.ConversionRule{{EngineID: "manual-engine", Executable: "tool", Sources: []string{"png"}, Targets: []string{"jxl"}, Arguments: []string{"{source}", "{target}", "{quality}"}}},
	}
	manifest, err := json.Marshal(repositorymanager.ProfileManifest{Profile: profileDef})
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(targets, "profiles", "test.json")
	if err = os.MkdirAll(filepath.Dir(manifestPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	index, err := json.Marshal(repositorymanager.CatalogIndex{SchemaVersion: 1, Profiles: []repositorymanager.ProfileEntry{{ID: profileDef.ID, Version: profileDef.Version, ManifestTarget: "profiles/test.json"}}})
	if err != nil {
		t.Fatal(err)
	}
	indexPath := filepath.Join(targets, "catalog", "index.json")
	if err = os.MkdirAll(filepath.Dir(indexPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(indexPath, index, 0600); err != nil {
		t.Fatal(err)
	}
	if err = validateCatalog([]string{"--catalog-root", root}); err == nil || !strings.Contains(err.Error(), "placeholders") {
		t.Fatalf("expected unsupported placeholder validation error, got %v", err)
	}
}

func TestValidateCatalogEnforcesLatestProfileAppVersionCeiling(t *testing.T) {
	makeProfile := func(version, maxExclusive string) profile.Definition {
		return profile.Definition{
			SchemaVersion:              1,
			Type:                       profile.TypeConversion,
			ID:                         "sample-profile",
			Version:                    version,
			Name:                       "Sample Profile",
			MinimumAppVersion:          "0.0.1",
			MaximumAppVersionExclusive: maxExclusive,
			Engines:                    []profile.EngineRequirement{{EngineID: "manual-engine"}},
			Rules: []profile.ConversionRule{{
				EngineID:   "manual-engine",
				Executable: "tool",
				Sources:    []string{"png"},
				Targets:    []string{"webp"},
				Arguments:  []string{"{source}", "{target}"},
			}},
		}
	}
	writeCatalogFixture := func(t *testing.T, idx catalogIndex, manifests map[string]catalogProfileManifest) string {
		t.Helper()
		root := t.TempDir()
		targets := filepath.Join(root, "targets")
		for rel, manifest := range manifests {
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			full := filepath.Join(targets, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(full, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		indexBytes, err := json.Marshal(idx)
		if err != nil {
			t.Fatal(err)
		}
		indexPath := filepath.Join(targets, "catalog", "index.json")
		if err := os.MkdirAll(filepath.Dir(indexPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(indexPath, indexBytes, 0600); err != nil {
			t.Fatal(err)
		}
		return root
	}

	t.Run("rejects accidental narrow ceiling on latest release", func(t *testing.T) {
		root := writeCatalogFixture(t, catalogIndex{
			SchemaVersion:              1,
			MinimumAppVersion:          "0.1.0",
			MaximumAppVersionExclusive: "1.0.0",
			Profiles: []catalogProfileEntry{{
				ProfileEntry: repositorymanager.ProfileEntry{
					ID: "sample-profile", Version: "1.0.0", Name: "Sample Profile", ManifestTarget: "profiles/sample-1.0.0.json",
				},
			}},
		}, map[string]catalogProfileManifest{
			"profiles/sample-1.0.0.json": {Profile: catalogProfileDefinition{Definition: makeProfile("1.0.0", "0.2.0")}},
		})
		err := validateCatalog([]string{"--catalog-root", root})
		if err == nil || !strings.Contains(err.Error(), `sets maximumAppVersionExclusive "0.2.0" below catalog bound "1.0.0"`) {
			t.Fatalf("expected narrow latest ceiling error, got %v", err)
		}
	})

	t.Run("rejects whitespace-only justification on narrow latest release", func(t *testing.T) {
		root := writeCatalogFixture(t, catalogIndex{
			SchemaVersion:              1,
			MinimumAppVersion:          "0.1.0",
			MaximumAppVersionExclusive: "1.0.0",
			Profiles: []catalogProfileEntry{{
				ProfileEntry: repositorymanager.ProfileEntry{
					ID: "sample-profile", Version: "1.0.0", Name: "Sample Profile", ManifestTarget: "profiles/sample-1.0.0.json",
				},
				MaximumAppVersionReason: "   ",
			}},
		}, map[string]catalogProfileManifest{
			"profiles/sample-1.0.0.json": {
				Profile:                 catalogProfileDefinition{Definition: makeProfile("1.0.0", "0.2.0"), MaximumAppVersionReason: "\t"},
				MaximumAppVersionReason: " ",
			},
		})
		if err := validateCatalog([]string{"--catalog-root", root}); err == nil {
			t.Fatal("expected whitespace-only justification to be rejected")
		}
	})

	t.Run("allows superseded historical narrow ceiling regardless of index order", func(t *testing.T) {
		entriesAsc := []catalogProfileEntry{
			{ProfileEntry: repositorymanager.ProfileEntry{ID: "sample-profile", Version: "1.0.0", Name: "Sample Profile", ManifestTarget: "profiles/sample-1.0.0.json"}},
			{ProfileEntry: repositorymanager.ProfileEntry{ID: "sample-profile", Version: "1.1.0", Name: "Sample Profile", ManifestTarget: "profiles/sample-1.1.0.json"}},
		}
		entriesDesc := []catalogProfileEntry{entriesAsc[1], entriesAsc[0]}
		for _, entries := range [][]catalogProfileEntry{entriesAsc, entriesDesc} {
			root := writeCatalogFixture(t, catalogIndex{
				SchemaVersion:              1,
				MinimumAppVersion:          "0.1.0",
				MaximumAppVersionExclusive: "1.0.0",
				Profiles:                   entries,
			}, map[string]catalogProfileManifest{
				"profiles/sample-1.0.0.json": {Profile: catalogProfileDefinition{Definition: makeProfile("1.0.0", "0.2.0")}},
				"profiles/sample-1.1.0.json": {Profile: catalogProfileDefinition{Definition: makeProfile("1.1.0", "1.0.0")}},
			})
			if err := validateCatalog([]string{"--catalog-root", root}); err != nil {
				t.Fatalf("expected superseded historical ceiling to pass, got %v", err)
			}
		}
	})

	t.Run("allows narrow latest ceiling with explicit justification on profile or index", func(t *testing.T) {
		rootProfileReason := writeCatalogFixture(t, catalogIndex{
			SchemaVersion:              1,
			MinimumAppVersion:          "0.1.0",
			MaximumAppVersionExclusive: "1.0.0",
			Profiles: []catalogProfileEntry{{
				ProfileEntry: repositorymanager.ProfileEntry{
					ID: "sample-profile", Version: "1.0.0", Name: "Sample Profile", ManifestTarget: "profiles/sample-1.0.0.json",
				},
			}},
		}, map[string]catalogProfileManifest{
			"profiles/sample-1.0.0.json": {
				Profile: catalogProfileDefinition{
					Definition:              makeProfile("1.0.0", "0.2.0"),
					MaximumAppVersionReason: "Replaced by sample-v2 starting in 0.2.0",
				},
			},
		})
		if err := validateCatalog([]string{"--catalog-root", rootProfileReason}); err != nil {
			t.Fatalf("expected profile-level maximumAppVersionReason to pass, got %v", err)
		}

		rootIndexReason := writeCatalogFixture(t, catalogIndex{
			SchemaVersion:              1,
			MinimumAppVersion:          "0.1.0",
			MaximumAppVersionExclusive: "1.0.0",
			Profiles: []catalogProfileEntry{{
				ProfileEntry: repositorymanager.ProfileEntry{
					ID: "sample-profile", Version: "1.0.0", Name: "Sample Profile", ManifestTarget: "profiles/sample-1.0.0.json",
				},
				MaximumAppVersionReason: "Retired in 0.2.0 after runtime removal",
			}},
		}, map[string]catalogProfileManifest{
			"profiles/sample-1.0.0.json": {Profile: catalogProfileDefinition{Definition: makeProfile("1.0.0", "0.2.0")}},
		})
		if err := validateCatalog([]string{"--catalog-root", rootIndexReason}); err != nil {
			t.Fatalf("expected index-level maximumAppVersionReason to pass, got %v", err)
		}
	})

	t.Run("rejects invalid catalog app-version bounds", func(t *testing.T) {
		root := writeCatalogFixture(t, catalogIndex{
			SchemaVersion:              1,
			MinimumAppVersion:          "1.0.0",
			MaximumAppVersionExclusive: "0.2.0",
			Profiles: []catalogProfileEntry{{
				ProfileEntry: repositorymanager.ProfileEntry{
					ID: "sample-profile", Version: "1.0.0", Name: "Sample Profile", ManifestTarget: "profiles/sample-1.0.0.json",
				},
			}},
		}, map[string]catalogProfileManifest{
			"profiles/sample-1.0.0.json": {Profile: catalogProfileDefinition{Definition: makeProfile("1.0.0", "1.0.0")}},
		})
		if err := validateCatalog([]string{"--catalog-root", root}); err == nil || !strings.Contains(err.Error(), "catalog app-version maximum") {
			t.Fatalf("expected catalog app-version range error, got %v", err)
		}
	})
}

func TestPackageVerifierRejectsNonGitHubReleaseURL(t *testing.T) {
	if err := verifyExternalPackage("http://127.0.0.1/package", 1, strings.Repeat("0", 64)); err == nil {
		t.Fatal("accepted non-HTTPS, non-GitHub package source")
	}
}

func TestPublisherAddsSignedExternalPackageMetadataForOfficialManifests(t *testing.T) {
	rootDir := t.TempDir()
	catalogDir := filepath.Join(rootDir, "catalog")
	keysDir := filepath.Join(rootDir, "keys")
	targetRoot := filepath.Join(catalogDir, "targets")
	if err := copyTree(filepath.Join("..", "..", "..", "catalog", "targets"), targetRoot); err != nil {
		t.Fatal(err)
	}
	if err := initRoot([]string{"--keys-dir", keysDir, "--root-file", filepath.Join(catalogDir, "metadata", "root.json"), "--embed-root", filepath.Join(rootDir, "embedded-root.json")}); err != nil {
		t.Fatal(err)
	}
	verifyFixtures := func(string, int64, string) error { return nil }
	if err := publishWithVerifier([]string{"--catalog-root", catalogDir, "--keys-dir", keysDir}, verifyFixtures); err != nil {
		t.Fatal(err)
	}
	root, err := metadata.Root().FromFile(filepath.Join(catalogDir, "metadata", "root.json"))
	if err != nil {
		t.Fatal(err)
	}
	targets, err := metadata.Targets().FromFile(filepath.Join(catalogDir, "metadata", "targets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = root.VerifyDelegate(metadata.TARGETS, targets); err != nil {
		t.Fatal(err)
	}
	for target, expectedSize := range map[string]int64{"packages/ffmpeg-n9.0.2-39-g7adfbe40dc-linux64-lgpl-9.0.tar.xz": 136027168, "packages/jxl-linux-x86_64-static.tar.lz": 4034223} {
		info := targets.Signed.Targets[target]
		if info == nil || info.Length != expectedSize || len(info.Hashes["sha256"]) != 32 {
			t.Fatalf("missing or incorrect TUF package metadata for %s: %#v", target, info)
		}
		if info.Custom == nil {
			t.Fatalf("package %s lacks signed upstream URL metadata", target)
		}
	}
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0600)
	})
}
