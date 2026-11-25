package document

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeSampleVectorPDF builds a real multi-page PDF with distinct vector text
// streams and page dimensions so tests can prove lossless native PDF merging.
func makeSampleVectorPDF(pages []struct {
	text string
	w, h float64
}) []byte {
	w := newPDFWriter()
	fontID := w.alloc()
	w.writeRawObject(fontID, []byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"))

	pageIDs := make([]int, 0, len(pages))
	for _, pg := range pages {
		stream := fmt.Sprintf("BT /F1 18 Tf 36 72 Td (%s) Tj ET\n", pg.text)
		contentID := w.alloc()
		w.writeStreamObject(contentID, "", []byte(stream))
		pageID := w.alloc()
		w.writeRawObject(pageID, []byte(fmt.Sprintf(
			"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.1f %.1f] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>",
			pg.w, pg.h, fontID, contentID,
		)))
		pageIDs = append(pageIDs, pageID)
	}
	return w.finish(pageIDs)
}

func makeSampleJPEG(t *testing.T, width, height int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode sample jpeg: %v", err)
	}
	return buf.Bytes()
}

func makeSamplePNG(t *testing.T, width, height int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode sample png: %v", err)
	}
	return buf.Bytes()
}

func TestParsePDFAndInspectSource(t *testing.T) {
	pdfBytes := makeSampleVectorPDF([]struct {
		text string
		w, h float64
	}{
		{"First Vector Page", 595, 842},
		{"Second Landscape Page", 842, 595},
		{"Third Square Page", 400, 400},
	})
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "sample.pdf")
	if err := os.WriteFile(pdfPath, pdfBytes, 0600); err != nil {
		t.Fatal(err)
	}

	info, err := InspectSource("u-pdf", pdfPath, "sample.pdf", "pdf", int64(len(pdfBytes)))
	if err != nil {
		t.Fatalf("InspectSource PDF failed: %v", err)
	}
	if info.Kind != "pdf" || info.PageCount != 3 || len(info.Pages) != 3 {
		t.Fatalf("unexpected PDF inspect result: %+v", info)
	}
	if info.Pages[1].WidthPt <= info.Pages[1].HeightPt {
		t.Fatalf("expected page 2 to be landscape, got %+v", info.Pages[1])
	}
}

func TestParsePDFRejectsEncrypted(t *testing.T) {
	encPDF := []byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n2 0 obj\n<< /Type /Pages /Kids [] /Count 0 >>\nendobj\ntrailer\n<< /Root 1 0 R /Encrypt 9 0 R >>\n%%EOF\n")
	_, err := ParsePDF(encPDF)
	if err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("expected encrypted PDF error, got: %v", err)
	}
}

func TestAssemblePDFPreservesNativeVectorTextAndOriginalJPEG(t *testing.T) {
	dir := t.TempDir()
	pdfBytes := makeSampleVectorPDF([]struct {
		text string
		w, h float64
	}{
		{"VECTOR_PAGE_ONE_KEEP", 595, 842},
		{"VECTOR_PAGE_TWO_KEEP", 612, 792},
	})
	jpgBytes := makeSampleJPEG(t, 320, 200, color.RGBA{R: 200, G: 80, B: 40, A: 255})
	pngBytes := makeSamplePNG(t, 180, 240, color.NRGBA{R: 40, G: 120, B: 220, A: 200})

	pdfPath := filepath.Join(dir, "src.pdf")
	jpgPath := filepath.Join(dir, "photo.jpg")
	pngPath := filepath.Join(dir, "graphic.png")
	for p, b := range map[string][]byte{pdfPath: pdfBytes, jpgPath: jpgBytes, pngPath: pngBytes} {
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
	}

	outPath := filepath.Join(dir, "assembled.pdf")
	manifest := Manifest{
		SchemaVersion: SchemaVersion,
		Name:          "Mixed Document",
		OutputFormat:  "pdf",
		Pages: []Page{
			{ID: "p1", UploadID: "u-pdf", SourcePage: 2, Rotation: 90},
			{ID: "p2", UploadID: "u-jpg", SourcePage: 1, Rotation: 0},
			{ID: "p3", UploadID: "u-pdf", SourcePage: 1, Rotation: 0},
			{ID: "p4", UploadID: "u-png", SourcePage: 1, Rotation: 270},
		},
	}

	res, err := Assemble(context.Background(), AssembleRequest{
		Manifest: manifest,
		SourcePaths: map[string]SourceFileInput{
			"u-pdf": {Path: pdfPath, FileName: "src.pdf", Extension: "pdf"},
			"u-jpg": {Path: jpgPath, FileName: "photo.jpg", Extension: "jpg"},
			"u-png": {Path: pngPath, FileName: "graphic.png", Extension: "png"},
		},
		WorkDir:        filepath.Join(dir, "work"),
		OutputPath:     outPath,
		MaxOutputBytes: 50 << 20,
	})
	if err != nil {
		t.Fatalf("Assemble PDF failed: %v", err)
	}
	if res.PageCount != 4 || res.Size == 0 {
		t.Fatalf("unexpected assemble result: %+v", res)
	}

	outBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}

	// Verify native vector text survived intact without rasterization.
	if !bytes.Contains(outBytes, []byte("VECTOR_PAGE_TWO_KEEP")) || !bytes.Contains(outBytes, []byte("VECTOR_PAGE_ONE_KEEP")) {
		t.Fatal("expected native PDF vector text streams to be preserved verbatim")
	}
	// Verify original JPEG bytes were embedded verbatim with /DCTDecode.
	if !bytes.Contains(outBytes, jpgBytes) {
		t.Fatal("expected original JPEG bytes to be embedded verbatim without recompression")
	}
	// Re-parse the assembled PDF and verify page count and rotation.
	parsed, err := ParsePDF(outBytes)
	if err != nil {
		t.Fatalf("assembled PDF failed re-parse: %v", err)
	}
	if parsed.PageCount() != 4 {
		t.Fatalf("expected 4 pages in assembled PDF, got %d", parsed.PageCount())
	}
	infos := parsed.PageInfos()
	if infos[0].Rotation != 90 {
		t.Fatalf("expected first page rotation 90, got %d", infos[0].Rotation)
	}
	if infos[0].WidthPt <= infos[0].HeightPt {
		t.Fatalf("expected rotated portrait PDF page 1 to report landscape dimensions, got %+v", infos[0])
	}
}

func TestAssembleAllSecondaryFormats(t *testing.T) {
	dir := t.TempDir()
	jpgBytes := makeSampleJPEG(t, 240, 160, color.RGBA{R: 180, G: 60, B: 60, A: 255})
	pngBytes := makeSamplePNG(t, 120, 180, color.NRGBA{R: 50, G: 160, B: 90, A: 255})

	jpgPath := filepath.Join(dir, "one.jpg")
	pngPath := filepath.Join(dir, "two.png")
	if err := os.WriteFile(jpgPath, jpgBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pngPath, pngBytes, 0600); err != nil {
		t.Fatal(err)
	}

	sources := map[string]SourceFileInput{
		"u-jpg": {Path: jpgPath, FileName: "one.jpg", Extension: "jpg"},
		"u-png": {Path: pngPath, FileName: "two.png", Extension: "png"},
	}

	formats := []string{"cbz", "zip", "tiff", "docx", "pptx", "odt", "odp"}
	for _, formatID := range formats {
		t.Run(formatID, func(t *testing.T) {
			outPath := filepath.Join(dir, "out."+formatID)
			manifest := Manifest{
				SchemaVersion: SchemaVersion,
				Name:          "Multi Format Test",
				OutputFormat:  formatID,
				Pages: []Page{
					{ID: "p1", UploadID: "u-jpg", SourcePage: 1, Rotation: 0},
					{ID: "p2", UploadID: "u-png", SourcePage: 1, Rotation: 90},
				},
			}
			res, err := Assemble(context.Background(), AssembleRequest{
				Manifest:       manifest,
				SourcePaths:    sources,
				WorkDir:        filepath.Join(dir, "work-"+formatID),
				OutputPath:     outPath,
				MaxOutputBytes: 50 << 20,
			})
			if err != nil {
				t.Fatalf("Assemble %s failed: %v", formatID, err)
			}
			if res.PageCount != 2 || res.Size == 0 {
				t.Fatalf("unexpected %s result: %+v", formatID, res)
			}

			switch formatID {
			case "cbz":
				zr, err := zip.OpenReader(outPath)
				if err != nil {
					t.Fatal(err)
				}
				defer zr.Close()
				if len(zr.File) != 2 || zr.File[0].Name != "001.jpg" || zr.File[1].Name != "002.png" {
					t.Fatalf("unexpected CBZ entries: %v", zipNames(zr.File))
				}
				rc, _ := zr.File[0].Open()
				got, _ := io.ReadAll(rc)
				_ = rc.Close()
				if !bytes.Equal(got, jpgBytes) {
					t.Fatal("expected unrotated JPEG bytes in CBZ to be preserved verbatim")
				}

			case "zip":
				zr, err := zip.OpenReader(outPath)
				if err != nil {
					t.Fatal(err)
				}
				defer zr.Close()
				names := zipNames(zr.File)
				if len(names) != 3 || names[2] != "manifest.json" {
					t.Fatalf("unexpected ordered ZIP entries: %v", names)
				}

			case "tiff":
				raw, err := os.ReadFile(outPath)
				if err != nil {
					t.Fatal(err)
				}
				if count := countTIFFPages(raw); count != 2 {
					t.Fatalf("expected 2 IFD pages in multipage TIFF, got %d", count)
				}

			case "docx", "pptx", "odt", "odp":
				if err := ValidateOfficePackage(outPath, formatID); err != nil {
					t.Fatalf("generated %s failed Office package validation: %v", formatID, err)
				}
				if formatID == "odt" || formatID == "odp" {
					zr, err := zip.OpenReader(outPath)
					if err != nil {
						t.Fatal(err)
					}
					defer zr.Close()
					if len(zr.File) == 0 || zr.File[0].Name != "mimetype" || zr.File[0].Method != zip.Store {
						t.Fatalf("expected uncompressed leading mimetype in %s", formatID)
					}
				}
			}
		})
	}
}

func zipNames(files []*zip.File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	return out
}

func countTIFFPages(data []byte) int {
	if len(data) < 8 || data[0] != 'I' || data[1] != 'I' || binary.LittleEndian.Uint16(data[2:4]) != 42 {
		return 0
	}
	off := int(binary.LittleEndian.Uint32(data[4:8]))
	pages := 0
	for off > 0 && off+2 <= len(data) && pages < 1000 {
		entries := int(binary.LittleEndian.Uint16(data[off : off+2]))
		nextPos := off + 2 + entries*12
		if nextPos+4 > len(data) {
			break
		}
		pages++
		off = int(binary.LittleEndian.Uint32(data[nextPos : nextPos+4]))
	}
	return pages
}
