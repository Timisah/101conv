package repositorymanager

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sorairolake/lzip-go"
	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/timisah/101conv/server/internal/engine"
	"github.com/timisah/101conv/server/internal/profile"
	"github.com/ulikunitz/xz"
)

func TestCatalogJSONIncludesEmptyIncompatibleProfiles(t *testing.T) {
	catalog := Catalog{
		Repository:           Source{ID: "test", Name: "Test", Enabled: true},
		Engines:              []engine.Definition{},
		Profiles:             []profile.Definition{},
		IncompatibleProfiles: []IncompatibleProfile{},
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"incompatibleProfiles":[]`)) {
		t.Fatalf("empty incompatibleProfiles must be present as an array: %s", raw)
	}
}

func TestOfficialGoCatalogManifestsValidate(t *testing.T) {
	root := filepath.Join("..", "..", "..", "catalog", "targets")
	indexBytes, err := os.ReadFile(filepath.Join(root, "catalog", "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index CatalogIndex
	if err = json.Unmarshal(indexBytes, &index); err != nil {
		t.Fatal(err)
	}
	if index.SchemaVersion != 1 || len(index.Engines) != 4 || len(index.Profiles) != 9 {
		t.Fatalf("unexpected Go catalog index: %#v", index)
	}
	types := map[string]int{}
	for _, entry := range index.Engines {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.ManifestTarget)))
		if err != nil {
			t.Fatal(err)
		}
		var manifest EngineManifest
		if err = json.Unmarshal(b, &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Engine.ID != entry.ID || manifest.Engine.Version != entry.Version || manifest.Engine.Name != entry.Name {
			t.Fatalf("engine index mismatch: %s", entry.ID)
		}
		if err = manifest.Engine.Validate(); err != nil {
			t.Fatalf("engine %s: %v", entry.ID, err)
		}
	}
	for _, entry := range index.Profiles {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.ManifestTarget)))
		if err != nil {
			t.Fatal(err)
		}
		var manifest ProfileManifest
		if err = json.Unmarshal(b, &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Profile.ID != entry.ID || manifest.Profile.Version != entry.Version || manifest.Profile.Name != entry.Name {
			t.Fatalf("profile index mismatch: %s", entry.ID)
		}
		if err = manifest.Profile.Validate(); err != nil {
			t.Fatalf("profile %s: %v", entry.ID, err)
		}
		types[manifest.Profile.Type]++
	}
	if types[profile.TypeConversion] != 6 || types[profile.TypeCompression] != 3 {
		t.Fatalf("catalog must expose six conversion and three compression profiles, got %v", types)
	}
}

func compressedTar(t *testing.T, format, name string) []byte {
	t.Helper()
	var out bytes.Buffer
	var compressor interface {
		Write([]byte) (int, error)
		Close() error
	}
	switch format {
	case "tar.xz":
		z, err := xz.NewWriter(&out)
		if err != nil {
			t.Fatal(err)
		}
		compressor = z
	case "tar.lz":
		compressor = lzip.NewWriter(&out)
	default:
		t.Fatalf("unsupported test format %s", format)
	}
	tw := tar.NewWriter(compressor)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0700, Size: 4, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("test")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func lzipMembers(t *testing.T, contents []byte, split int) []byte {
	t.Helper()
	compress := func(chunk []byte) []byte {
		var out bytes.Buffer
		w := lzip.NewWriter(&out)
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	return append(compress(contents[:split]), compress(contents[split:])...)
}

func TestExtractXzAndLzipArchivesSafely(t *testing.T) {
	for _, format := range []string{"tar.xz", "tar.lz"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			if err := extractArchive(compressedTar(t, format, "tools/converter"), format, root); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(root, "tools", "converter"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "test" {
				t.Fatalf("unexpected extracted bytes %q", got)
			}
		})
	}
}

func TestExtractConcatenatedLzipMembers(t *testing.T) {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	if err := tw.WriteHeader(&tar.Header{Name: "tools/cjxl", Mode: 0700, Size: 4, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("test")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	archive := lzipMembers(t, raw.Bytes(), len(raw.Bytes())/2)
	root := t.TempDir()
	if err := extractArchive(archive, "tar.lz", root); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, "tools", "cjxl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "test" {
		t.Fatalf("unexpected extracted bytes %q", got)
	}
	corrupt := append([]byte(nil), archive...)
	corrupt[len(corrupt)-1] ^= 0x01
	if err := extractArchive(corrupt, "tar.lz", t.TempDir()); err == nil {
		t.Fatal("accepted a corrupted footer in the final lzip member")
	}
}

func TestXzAndLzipArchiveRejectTraversal(t *testing.T) {
	for _, format := range []string{"tar.xz", "tar.lz"} {
		t.Run(format, func(t *testing.T) {
			err := extractArchive(compressedTar(t, format, "../escape"), format, t.TempDir())
			if err == nil || errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected path traversal rejection, got %v", err)
			}
		})
	}
}

func TestLzipRejectsInvalidAndExcessiveDictionaryHeaders(t *testing.T) {
	if _, err := newSafeLzipReader([]byte("LZIP\x01")); err == nil {
		t.Fatal("expected truncated input rejection")
	}
	archive := compressedTar(t, "tar.lz", "file")
	archive[5] = 28 // 256 MiB lzip dictionary, above the installer's 128 MiB limit.
	if _, err := newSafeLzipReader(archive); err == nil {
		t.Fatal("expected oversized dictionary rejection")
	}
}

func TestCompressedTarValidatesFooterAfterTarEndMarkers(t *testing.T) {
	for _, format := range []string{"tar.xz", "tar.lz"} {
		t.Run(format, func(t *testing.T) {
			archive := compressedTar(t, format, "file")
			archive[len(archive)-1] ^= 0x01
			if err := extractArchive(archive, format, t.TempDir()); err == nil {
				t.Fatal("accepted a corrupt compression footer")
			}
		})
	}
}

func TestExternalArtifactTargetsRequireSignedGitHubURL(t *testing.T) {
	raw := json.RawMessage(`{"downloadUrl":"https://github.com/example/project/releases/download/v1/tool.tar.xz"}`)
	info := &metadata.TargetFiles{Custom: &raw}
	got, err := targetExternalURL(info)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://github.com/example/project/releases/download/v1/tool.tar.xz" {
		t.Fatalf("unexpected target URL %q", got)
	}
	for _, bad := range []string{"http://github.com/owner/repo/file", "https://example.org/package", "https://user@github.com/owner/repo/file", "https://github.com:443/owner/repo/file"} {
		if err := validateExternalArtifactURL(bad); err == nil {
			t.Errorf("accepted unsafe release URL %q", bad)
		}
	}
}
