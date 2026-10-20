package analysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
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

func TestAnalyzeImageReturnsSummaryAndRawJSON(t *testing.T) {
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
	if !bytes.Contains(report.RawJSON, []byte(`"fileName":"sample.png"`)) {
		t.Fatalf("raw report does not expose fileName: %s", report.RawJSON)
	}
}

func TestAnalyzeReaderRemovesEphemeralFile(t *testing.T) {
	service, uploadRoot := testAnalysisService(t)
	var encoded bytes.Buffer
	imageFile := image.NewRGBA(image.Rect(0, 0, 2, 2))
	if err := png.Encode(&encoded, imageFile); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AnalyzeReader(context.Background(), "temporary.png", &encoded); err != nil {
		t.Fatal(err)
	}
	leftovers, err := filepath.Glob(filepath.Join(uploadRoot, ".inspection-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("ephemeral inspection files remain: %v", leftovers)
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

func TestInspectPDFExtractsInfoMetadata(t *testing.T) {
	report, err := inspectPDF([]byte("%PDF-1.7\n<< /Title (Quarterly report) /Author <4a6f7264> >>"))
	if err != nil {
		t.Fatal(err)
	}
	if report.Metadata["Title"] != "Quarterly report" || report.Metadata["Author"] != "Jord" {
		t.Fatalf("PDF metadata = %#v", report.Metadata)
	}
}

func TestParseJPEGEXIFExtractsCameraAndGPSMetadata(t *testing.T) {
	metadata := parseJPEGEXIF(testJPEGWithEXIF())
	if metadata["IFD0.Make"] != "Google" || metadata["IFD0.Model"] != "Pixel 8" {
		t.Fatalf("camera metadata = %#v", metadata)
	}
	latitude, latitudeOK := metadata["GPS.GPSLatitudeDecimal"].(float64)
	longitude, longitudeOK := metadata["GPS.GPSLongitudeDecimal"].(float64)
	if !latitudeOK || !longitudeOK || math.Abs(latitude-37.7675) > 0.000001 || math.Abs(longitude+122.4166666667) > 0.000001 {
		t.Fatalf("GPS decimals = %#v, %#v", metadata["GPS.GPSLatitudeDecimal"], metadata["GPS.GPSLongitudeDecimal"])
	}
}

func testJPEGWithEXIF() []byte {
	tiff := make([]byte, 220)
	copy(tiff[:2], "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 3)
	testIFDEntry(tiff, 10, 0x010f, 2, 7, 60)
	testIFDEntry(tiff, 22, 0x0110, 2, 8, 70)
	testIFDEntry(tiff, 34, 0x8825, 4, 1, 80)
	copy(tiff[60:67], "Google\x00")
	copy(tiff[70:78], "Pixel 8\x00")
	binary.LittleEndian.PutUint16(tiff[80:82], 4)
	testIFDEntry(tiff, 82, 1, 2, 2, 0)
	copy(tiff[90:92], "N\x00")
	testIFDEntry(tiff, 94, 2, 5, 3, 140)
	testIFDEntry(tiff, 106, 3, 2, 2, 0)
	copy(tiff[114:116], "W\x00")
	testIFDEntry(tiff, 118, 4, 5, 3, 188)
	testIFDRational(tiff, 140, 37, 1)
	testIFDRational(tiff, 148, 46, 1)
	testIFDRational(tiff, 156, 30, 10)
	testIFDRational(tiff, 188, 122, 1)
	testIFDRational(tiff, 196, 25, 1)
	testIFDRational(tiff, 204, 0, 1)

	payload := append([]byte("Exif\x00\x00"), tiff...)
	segmentLength := len(payload) + 2
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe1, byte(segmentLength >> 8), byte(segmentLength)}
	jpeg = append(jpeg, payload...)
	return append(jpeg, 0xff, 0xd9)
}

func testIFDEntry(data []byte, offset int, tag, valueType uint16, count, value uint32) {
	binary.LittleEndian.PutUint16(data[offset:offset+2], tag)
	binary.LittleEndian.PutUint16(data[offset+2:offset+4], valueType)
	binary.LittleEndian.PutUint32(data[offset+4:offset+8], count)
	binary.LittleEndian.PutUint32(data[offset+8:offset+12], value)
}

func testIFDRational(data []byte, offset int, numerator, denominator uint32) {
	binary.LittleEndian.PutUint32(data[offset:offset+4], numerator)
	binary.LittleEndian.PutUint32(data[offset+4:offset+8], denominator)
}
