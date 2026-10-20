package analysis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/timisah/101conv/server/internal/management"
)

func testAnalysisService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	manager, err := management.Open(filepath.Join(root, "management"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	uploadRoot := filepath.Join(root, "uploads")
	if err = os.MkdirAll(uploadRoot, 0700); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(manager, uploadRoot)
	if err != nil {
		t.Fatal(err)
	}
	return service, uploadRoot
}

func TestAnalyzeImageReturnsSafeSummaryAndRawJSON(t *testing.T) {
	service, uploadRoot := testAnalysisService(t)
	path := filepath.Join(uploadRoot, "11111111-1111-4111-8111-111111111111")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	imageFile := image.NewRGBA(image.Rect(0, 0, 3, 2))
	imageFile.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err = png.Encode(file, imageFile); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	report, err := service.Analyze(context.Background(), File{ID: filepath.Base(path), Name: "sample.png", Extension: "png", Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if report.Image == nil || report.Image.Width != 3 || report.Image.Height != 2 || len(report.RawJSON) == 0 {
		t.Fatalf("unexpected image report: %#v", report)
	}
	if report.Summary != "Image, 3 × 2 pixels" {
		t.Fatalf("summary = %q", report.Summary)
	}
}

func TestAnalyzeRejectsCorruptImageAndChangedUpload(t *testing.T) {
	service, uploadRoot := testAnalysisService(t)
	path := filepath.Join(uploadRoot, "22222222-2222-4222-8222-222222222222")
	data := []byte("not a png")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	_, err := service.Analyze(context.Background(), File{ID: filepath.Base(path), Name: "bad.png", Extension: "png", Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:]), Path: path})
	if !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("corrupt image error = %v", err)
	}
	_, err = service.Analyze(context.Background(), File{ID: filepath.Base(path), Name: "bad.png", Extension: "png", Size: int64(len(data)), SHA256: strings.Repeat("0", sha256.Size*2), Path: path})
	if !errors.Is(err, ErrInvalidFile) {
		t.Fatalf("changed digest error = %v", err)
	}
}
