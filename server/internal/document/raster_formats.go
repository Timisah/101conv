package document

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type renderedPage struct {
	Width  int
	Height int
	Ext    string // "jpg" or "png"
	Bytes  []byte
	Img    image.Image // lazily populated when TIFF needs raw RGB
}

var safeFilenameRE = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func sanitizeBaseName(name string) string {
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	base = strings.Trim(safeFilenameRE.ReplaceAllString(base, "-"), "-")
	if base == "" {
		return "page"
	}
	if len(base) > 48 {
		base = base[:48]
	}
	return base
}

func renderPageForRasterExport(ctx context.Context, page Page, src *resolvedSource, workDir, pdftoppmPath string) (renderedPage, error) {
	switch src.Kind {
	case "image":
		raster, err := decodeBoundedImage(src.RawBytes)
		if err != nil {
			return renderedPage{}, err
		}
		// Preserve exact original JPEG/PNG bytes when no rotation or EXIF
		// reorientation is needed.
		if page.Rotation == 0 && raster.ExifOrientation == 1 {
			if raster.IsJPEG {
				return renderedPage{Width: raster.Width, Height: raster.Height, Ext: "jpg", Bytes: raster.RawBytes}, nil
			}
			if isPNGBytes(src.RawBytes) {
				return renderedPage{Width: raster.Width, Height: raster.Height, Ext: "png", Bytes: raster.RawBytes, Img: raster.Img}, nil
			}
		}
		img := raster.Img
		if img == nil {
			var decErr error
			img, _, decErr = image.Decode(bytes.NewReader(src.RawBytes))
			if decErr != nil {
				return renderedPage{}, fmt.Errorf("decode image for rotation: %w", decErr)
			}
		}
		if page.Rotation != 0 {
			img = rotateImage(img, page.Rotation)
		}
		b := img.Bounds()
		if raster.IsJPEG {
			encoded, err := encodeJPEGBytes(img, 95)
			if err != nil {
				return renderedPage{}, err
			}
			return renderedPage{Width: b.Dx(), Height: b.Dy(), Ext: "jpg", Bytes: encoded, Img: img}, nil
		}
		encoded, err := encodePNGBytes(img)
		if err != nil {
			return renderedPage{}, err
		}
		return renderedPage{Width: b.Dx(), Height: b.Dy(), Ext: "png", Bytes: encoded, Img: img}, nil

	case "pdf":
		if pdftoppmPath == "" {
			return renderedPage{}, fmt.Errorf("rasterizing PDF page %d of %q requires the Poppler (pdftoppm) runtime in the Worker", page.SourcePage, src.FileName)
		}
		prefix := filepath.Join(workDir, fmt.Sprintf("render-%s-p%d", page.ID, page.SourcePage))
		cmd := exec.CommandContext(ctx, pdftoppmPath, "-png", "-r", "150", "-singlefile", "-f", strconv.Itoa(page.SourcePage), "-l", strconv.Itoa(page.SourcePage), src.PDFPath, prefix)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + workDir}
		if out, err := cmd.CombinedOutput(); err != nil {
			return renderedPage{}, fmt.Errorf("render PDF page %d of %q: %v (%s)", page.SourcePage, src.FileName, err, strings.TrimSpace(string(out)))
		}
		pngPath := prefix + ".png"
		defer os.Remove(pngPath)
		data, err := readBoundedFile(pngPath, maxPDFParseBytes)
		if err != nil {
			return renderedPage{}, err
		}
		raster, err := decodeBoundedImage(data)
		if err != nil {
			return renderedPage{}, err
		}
		img := raster.Img
		if page.Rotation != 0 {
			img = rotateImage(img, page.Rotation)
			data, err = encodePNGBytes(img)
			if err != nil {
				return renderedPage{}, err
			}
		}
		b := img.Bounds()
		return renderedPage{Width: b.Dx(), Height: b.Dy(), Ext: "png", Bytes: data, Img: img}, nil

	default:
		return renderedPage{}, fmt.Errorf("unsupported source kind %q", src.Kind)
	}
}

func assembleCBZ(ctx context.Context, manifest Manifest, resolved map[string]*resolvedSource, workDir, pdftoppmPath string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, page := range manifest.Pages {
		src, ok := resolved[page.UploadID]
		if !ok {
			_ = zw.Close()
			return nil, fmt.Errorf("pages[%d]: source upload %q not found", i, page.UploadID)
		}
		rendered, err := renderPageForRasterExport(ctx, page, src, workDir, pdftoppmPath)
		if err != nil {
			_ = zw.Close()
			return nil, fmt.Errorf("pages[%d]: %w", i, err)
		}
		name := fmt.Sprintf("%03d.%s", i+1, rendered.Ext)
		fw, err := zw.CreateHeader(&zip.FileHeader{
			Name:   name,
			Method: zip.Store, // JPEG/PNG are already compressed
		})
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		if _, err := fw.Write(rendered.Bytes); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func assembleOrderedZIP(ctx context.Context, manifest Manifest, resolved map[string]*resolvedSource, workDir, pdftoppmPath string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i, page := range manifest.Pages {
		src, ok := resolved[page.UploadID]
		if !ok {
			_ = zw.Close()
			return nil, fmt.Errorf("pages[%d]: source upload %q not found", i, page.UploadID)
		}
		rendered, err := renderPageForRasterExport(ctx, page, src, workDir, pdftoppmPath)
		if err != nil {
			_ = zw.Close()
			return nil, fmt.Errorf("pages[%d]: %w", i, err)
		}
		name := fmt.Sprintf("%03d-%s.%s", i+1, sanitizeBaseName(src.FileName), rendered.Ext)
		fw, err := zw.CreateHeader(&zip.FileHeader{
			Name:   name,
			Method: zip.Store,
		})
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		if _, err := fw.Write(rendered.Bytes); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		_ = zw.Close()
		return nil, err
	}
	mw, err := zw.Create("manifest.json")
	if err != nil {
		_ = zw.Close()
		return nil, err
	}
	if _, err := mw.Write(manifestJSON); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// assembleMultipageTIFF writes a spec-compliant baseline TIFF 6.0 file
// containing one linked IFD per page in exact manifest order.
func assembleMultipageTIFF(ctx context.Context, manifest Manifest, resolved map[string]*resolvedSource, workDir, pdftoppmPath string) ([]byte, error) {
	type tiffPage struct {
		width  int
		height int
		rgb    []byte
	}
	pages := make([]tiffPage, 0, len(manifest.Pages))
	var totalRGB int64
	const maxTIFFRGBBytes = 160 << 20 // 160 MiB safety cap

	for i, page := range manifest.Pages {
		src, ok := resolved[page.UploadID]
		if !ok {
			return nil, fmt.Errorf("pages[%d]: source upload %q not found", i, page.UploadID)
		}
		rendered, err := renderPageForRasterExport(ctx, page, src, workDir, pdftoppmPath)
		if err != nil {
			return nil, fmt.Errorf("pages[%d]: %w", i, err)
		}
		img := rendered.Img
		if img == nil {
			img, _, err = image.Decode(bytes.NewReader(rendered.Bytes))
			if err != nil {
				return nil, fmt.Errorf("pages[%d]: decode raster for TIFF: %w", i, err)
			}
		}
		b := img.Bounds()
		w, h := b.Dx(), b.Dy()
		pageBytes := int64(w) * int64(h) * 3
		if totalRGB+pageBytes > maxTIFFRGBBytes {
			return nil, fmt.Errorf("multipage TIFF uncompressed pixel buffer exceeds safe 160 MiB limit")
		}
		totalRGB += pageBytes
		rgb, _, _ := flattenToRGB(img, "white")
		pages = append(pages, tiffPage{width: w, height: h, rgb: rgb})
	}

	var buf bytes.Buffer
	// Little-endian TIFF header: "II", magic 42, first IFD offset (8).
	buf.Write([]byte{'I', 'I'})
	_ = binary.Write(&buf, binary.LittleEndian, uint16(42))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(8))

	for idx, pg := range pages {
		// Each IFD has 11 entries:
		// 256 ImageWidth, 257 ImageLength, 258 BitsPerSample (3x SHORT -> extra 6 bytes),
		// 259 Compression (1=uncompressed), 262 PhotometricInterpretation (2=RGB),
		// 273 StripOffsets, 277 SamplesPerPixel (3), 278 RowsPerStrip,
		// 279 StripByteCounts, 282 XResolution (RATIONAL -> 8 bytes), 283 YResolution (RATIONAL -> 8 bytes).
		const numEntries = 11
		ifdStart := uint32(buf.Len())
		extraStart := ifdStart + 2 + numEntries*12 + 4
		bitsOffset := extraStart
		xResOffset := bitsOffset + 6
		yResOffset := xResOffset + 8
		stripOffset := yResOffset + 8
		stripLen := uint32(len(pg.rgb))
		nextIFDOffset := uint32(0)
		if idx+1 < len(pages) {
			nextIFDOffset = stripOffset + stripLen
			if nextIFDOffset%2 == 1 {
				nextIFDOffset++ // word-align next IFD
			}
		}

		writeEntry := func(tag, typ uint16, count, val uint32) {
			_ = binary.Write(&buf, binary.LittleEndian, tag)
			_ = binary.Write(&buf, binary.LittleEndian, typ)
			_ = binary.Write(&buf, binary.LittleEndian, count)
			_ = binary.Write(&buf, binary.LittleEndian, val)
		}

		_ = binary.Write(&buf, binary.LittleEndian, uint16(numEntries))
		writeEntry(256, 4, 1, uint32(pg.width))          // ImageWidth (LONG)
		writeEntry(257, 4, 1, uint32(pg.height))         // ImageLength (LONG)
		writeEntry(258, 3, 3, bitsOffset)                // BitsPerSample (3x SHORT)
		writeEntry(259, 3, 1, 1)                         // Compression = None
		writeEntry(262, 3, 1, 2)                         // Photometric = RGB
		writeEntry(273, 4, 1, stripOffset)               // StripOffsets
		writeEntry(277, 3, 1, 3)                         // SamplesPerPixel = 3
		writeEntry(278, 4, 1, uint32(pg.height))         // RowsPerStrip
		writeEntry(279, 4, 1, stripLen)                  // StripByteCounts
		writeEntry(282, 5, 1, xResOffset)                // XResolution (RATIONAL)
		writeEntry(283, 5, 1, yResOffset)                // YResolution (RATIONAL)
		_ = binary.Write(&buf, binary.LittleEndian, nextIFDOffset)

		// Extra tag values: BitsPerSample (8,8,8), XResolution (150/1), YResolution (150/1).
		_ = binary.Write(&buf, binary.LittleEndian, uint16(8))
		_ = binary.Write(&buf, binary.LittleEndian, uint16(8))
		_ = binary.Write(&buf, binary.LittleEndian, uint16(8))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(150))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(1))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(150))
		_ = binary.Write(&buf, binary.LittleEndian, uint32(1))

		buf.Write(pg.rgb)
		if idx+1 < len(pages) && buf.Len()%2 == 1 {
			buf.WriteByte(0)
		}
	}

	return buf.Bytes(), nil
}
