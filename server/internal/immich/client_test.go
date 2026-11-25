package immich

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateServerURL(t *testing.T) {
	if got, err := ValidateServerURL("https://photos.example.local:2283/"); err != nil || got != "https://photos.example.local:2283" {
		t.Fatalf("unexpected valid URL result: %q err=%v", got, err)
	}
	for _, bad := range []string{
		"",
		"ftp://photos.example.local",
		"http://user:pass@photos.example.local",
		"http://169.254.169.254/latest/meta-data",
	} {
		if _, err := ValidateServerURL(bad); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

func TestImmichClientWorkflowAndBounds(t *testing.T) {
	const secretKey = "immich-super-secret-api-key-123"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != secretKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/server/about":
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "v1.118.2"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/search/metadata":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"assets": map[string]any{
					"items": []map[string]any{
						{"id": "asset-1", "originalFileName": "IMG_0001.JPG", "type": "IMAGE", "originalMimeType": "image/jpeg"},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/assets/asset-1/original":
			w.Header().Set("Content-Disposition", `attachment; filename="IMG_0001.JPG"`)
			_, _ = w.Write([]byte("fake-jpeg-bytes"))
		case r.Method == http.MethodGet && r.URL.Path == "/api/assets/huge-asset/original":
			_, _ = w.Write([]byte(strings.Repeat("x", 2048)))
		case r.Method == http.MethodPost && r.URL.Path == "/api/assets":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f, hdr, err := r.FormFile("assetData")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			defer f.Close()
			body, _ := io.ReadAll(f)
			if hdr.Filename != "converted.webp" || string(body) != "webp-data" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "new-asset-99", "status": "created"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client, err := NewClient(srv.URL, secretKey, srv.Client())
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	about, err := client.About(context.Background())
	if err != nil || about.Version != "1.118.2" {
		t.Fatalf("About = (%+v, %v), want 1.118.2", about, err)
	}

	assets, err := client.ListAssets(context.Background(), 10)
	if err != nil || len(assets) != 1 || assets[0].ID != "asset-1" {
		t.Fatalf("ListAssets = (%+v, %v)", assets, err)
	}

	data, name, err := client.DownloadOriginal(context.Background(), "asset-1", 1024)
	if err != nil || string(data) != "fake-jpeg-bytes" || name != "IMG_0001.JPG" {
		t.Fatalf("DownloadOriginal = (%q, %q, %v)", string(data), name, err)
	}

	// Oversized download must be rejected cleanly.
	if _, _, err := client.DownloadOriginal(context.Background(), "huge-asset", 512); err == nil {
		t.Fatal("expected oversized download to be rejected")
	}

	// Upload returns new asset ID and honest disclosure.
	up, err := client.UploadAsset(context.Background(), "converted.webp", []byte("webp-data"), "dev-1", time.Now())
	if err != nil || up.ID != "new-asset-99" || !strings.Contains(up.Disclosure, "new assets") {
		t.Fatalf("UploadAsset = (%+v, %v)", up, err)
	}

	// Secret redaction check.
	redacted := client.redact(io.ErrUnexpectedEOF)
	if strings.Contains(redacted.Error(), secretKey) {
		t.Fatal("secret leaked")
	}
}
