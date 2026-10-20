package repositorymanager

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timisah/101conv/server/internal/engine"
)

type documentPackageTransport func(*http.Request) (*http.Response, error)

func (f documentPackageTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOfficialRelativePackageIntegrityAndOrigin(t *testing.T) {
	original := secureRepositoryHTTPClient
	defer func() { secureRepositoryHTTPClient = original }()
	payload := "small managed adapter"
	sum := sha256.Sum256([]byte(payload))
	artifact := &engine.Artifact{URL: "packages/document-1.0.0.tar.gz", SizeBytes: int64(len(payload)), SHA256: hex.EncodeToString(sum[:]), Format: "tar.gz"}
	calls := 0
	secureRepositoryHTTPClient = &http.Client{Transport: documentPackageTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != officialURL+"/targets/"+artifact.URL {
			t.Fatalf("escaped official origin: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(payload)), ContentLength: int64(len(payload))}, nil
	})}
	if data, err := downloadPackageUnsigned(context.Background(), artifact, 1024); err != nil || string(data) != payload {
		t.Fatalf("download: %q %v", data, err)
	}
	artifact.SHA256 = strings.Repeat("0", 64)
	if _, err := downloadPackageUnsigned(context.Background(), artifact, 1024); err == nil {
		t.Fatal("accepted digest mismatch")
	}
	artifact.SizeBytes++
	if _, err := downloadPackageUnsigned(context.Background(), artifact, 1024); err == nil {
		t.Fatal("accepted size mismatch")
	}
	for _, target := range []string{"../escape", "/escape", "https://attacker.invalid/p", "packages/../../escape", "packages\\escape", "packages/%2e%2e/escape", "packages/adapter?query=1", "packages/adapter#fragment", "packages/a:version"} {
		artifact.URL = target
		before := calls
		if _, err := downloadPackageUnsigned(context.Background(), artifact, 1024); err == nil || calls != before {
			t.Fatalf("unsafe target %q requested", target)
		}
	}
}

func TestDocumentManagedArchiveIntegrity(t *testing.T) {
	root := filepath.Join("..", "..", "..", "catalog", "targets")
	for _, id := range []string{"libreoffice", "poppler"} {
		t.Run(id, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, "engines", id+"-linux-amd64.json"))
			if err != nil {
				t.Fatal(err)
			}
			var manifest EngineManifest
			if err = json.Unmarshal(raw, &manifest); err != nil {
				t.Fatal(err)
			}
			artifact := manifest.Engine.Artifact
			data, err := os.ReadFile(filepath.Join(root, artifact.URL))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			if int64(len(data)) != artifact.SizeBytes || hex.EncodeToString(sum[:]) != artifact.SHA256 {
				t.Fatal("committed adapter archive differs from manifest")
			}
			out := t.TempDir()
			if err = extractArchive(data, artifact.Format, out); err != nil {
				t.Fatal(err)
			}
			for _, name := range manifest.Engine.Executables {
				packaged, err := os.ReadFile(filepath.Join(out, name))
				if err != nil {
					t.Fatal(err)
				}
				source, err := os.ReadFile(filepath.Join("..", "..", "..", "engines", "document-tools", name))
				if err != nil || !bytes.Equal(packaged, source) {
					t.Fatal("packaged executable differs from adapter source")
				}
			}
		})
	}
}
