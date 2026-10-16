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
	for target, expectedSize := range map[string]int64{"packages/ffmpeg-n9.0.2-28-g669f5ab083-linux64-lgpl-9.0.tar.xz": 137920872, "packages/jxl-linux-x86_64-static.tar.lz": 4034223} {
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
