package thumbnails

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func TestRenderResizesAndPreservesPNGTransparency(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 30, G: 120, B: 200, A: 128})
		}
	}
	var raw bytes.Buffer
	if err := png.Encode(&raw, src); err != nil {
		t.Fatal(err)
	}
	out, contentType, err := Render(bytes.NewReader(raw.Bytes()), 128)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "image/png" {
		t.Fatalf("contentType = %q, want image/png", contentType)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 128 || cfg.Height != 64 {
		t.Fatalf("resized = %dx%d, want 128x64", cfg.Width, cfg.Height)
	}
}

func TestRenderAppliesJPEGExifOrientation6(t *testing.T) {
	// 40x20 landscape JPEG with EXIF Orientation=6 (rotate 90 CW -> 20x40 portrait).
	src := image.NewRGBA(image.Rect(0, 0, 40, 20))
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, src, nil); err != nil {
		t.Fatal(err)
	}
	jb := jpegBuf.Bytes()
	// Build minimal APP1 Exif segment with Orientation=6 (little-endian TIFF).
	var tiff bytes.Buffer
	tiff.WriteString("II")
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(42))
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(8))
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(1))      // 1 IFD entry
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0x0112)) // Orientation tag
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(3))      // SHORT
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(1))      // count
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(6))      // Rotate 90 CW
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0))
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(0)) // next IFD

	app1Payload := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	var withExif bytes.Buffer
	withExif.Write(jb[:2]) // SOI
	withExif.Write([]byte{0xFF, 0xE1})
	_ = binary.Write(&withExif, binary.BigEndian, uint16(len(app1Payload)+2))
	withExif.Write(app1Payload)
	withExif.Write(jb[2:])

	out, contentType, err := Render(bytes.NewReader(withExif.Bytes()), 128)
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "image/jpeg" {
		t.Fatalf("contentType = %q, want image/jpeg", contentType)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 20 || cfg.Height != 40 {
		t.Fatalf("oriented = %dx%d, want 20x40", cfg.Width, cfg.Height)
	}
}

func TestRenderRejectsNonImageBytes(t *testing.T) {
	if _, _, err := Render(bytes.NewReader([]byte("%PDF-1.4\nnot an image")), 128); !errors.Is(err, ErrUnsupportedFormat) {
		t.Fatalf("expected ErrUnsupportedFormat for PDF, got %v", err)
	}
}
