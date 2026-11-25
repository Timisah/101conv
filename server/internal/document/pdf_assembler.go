package document

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"sort"
	"strings"
)

type pdfWriter struct {
	buf     bytes.Buffer
	offsets []int // 1-indexed; offsets[0] unused
	nextID  int
}

func newPDFWriter() *pdfWriter {
	w := &pdfWriter{
		offsets: make([]int, 3), // reserve 1=Catalog, 2=Pages
		nextID:  3,
	}
	w.buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	return w
}

func (w *pdfWriter) alloc() int {
	id := w.nextID
	w.nextID++
	w.offsets = append(w.offsets, 0)
	return id
}

func (w *pdfWriter) writeRawObject(id int, body []byte) {
	for len(w.offsets) <= id {
		w.offsets = append(w.offsets, 0)
	}
	w.offsets[id] = w.buf.Len()
	fmt.Fprintf(&w.buf, "%d 0 obj\n", id)
	w.buf.Write(body)
	w.buf.WriteString("\nendobj\n")
}

func (w *pdfWriter) writeStreamObject(id int, dictEntries string, stream []byte) {
	for len(w.offsets) <= id {
		w.offsets = append(w.offsets, 0)
	}
	w.offsets[id] = w.buf.Len()
	fmt.Fprintf(&w.buf, "%d 0 obj\n<< %s /Length %d >>\nstream\n", id, dictEntries, len(stream))
	w.buf.Write(stream)
	w.buf.WriteString("\nendstream\nendobj\n")
}

func (w *pdfWriter) finish(pageIDs []int) []byte {
	var kids strings.Builder
	for i, id := range pageIDs {
		if i > 0 {
			kids.WriteByte(' ')
		}
		fmt.Fprintf(&kids, "%d 0 R", id)
	}
	w.writeRawObject(1, []byte("<< /Type /Catalog /Pages 2 0 R >>"))
	w.writeRawObject(2, []byte(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), len(pageIDs))))

	xrefOffset := w.buf.Len()
	count := w.nextID
	fmt.Fprintf(&w.buf, "xref\n0 %d\n0000000000 65535 f \n", count)
	for id := 1; id < count; id++ {
		off := 0
		if id < len(w.offsets) {
			off = w.offsets[id]
		}
		fmt.Fprintf(&w.buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&w.buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", count, xrefOffset)
	return w.buf.Bytes()
}

// assemblePDF builds a single PDF preserving native PDF vector/text pages
// losslessly and embedding JPEG/PNG images without unnecessary resampling.
func assemblePDF(manifest Manifest, resolved map[string]*resolvedSource) ([]byte, error) {
	writer := newPDFWriter()
	pageIDs := make([]int, 0, len(manifest.Pages))

	for idx, page := range manifest.Pages {
		src, ok := resolved[page.UploadID]
		if !ok {
			return nil, fmt.Errorf("pages[%d]: source upload %q not found", idx, page.UploadID)
		}
		switch src.Kind {
		case "pdf":
			if src.ParsedPDF == nil {
				return nil, fmt.Errorf("pages[%d]: PDF source was not parsed", idx)
			}
			pageID, err := appendNativePDFPage(writer, src.ParsedPDF, page.SourcePage, page.Rotation)
			if err != nil {
				return nil, fmt.Errorf("pages[%d] (%s p.%d): %w", idx, src.FileName, page.SourcePage, err)
			}
			pageIDs = append(pageIDs, pageID)

		case "image":
			pageID, err := appendImagePDFPage(writer, src, page.Rotation, manifest.Layout, page.Layout)
			if err != nil {
				return nil, fmt.Errorf("pages[%d] (%s): %w", idx, src.FileName, err)
			}
			pageIDs = append(pageIDs, pageID)

		default:
			return nil, fmt.Errorf("pages[%d]: unsupported source kind %q for PDF assembly", idx, src.Kind)
		}
	}

	return writer.finish(pageIDs), nil
}

// appendNativePDFPage copies a single PDF page and its full indirect object
// closure into writer with renumbered object IDs, preserving native vector
// text, fonts, paths, and images without rasterization.
func appendNativePDFPage(writer *pdfWriter, parsed *ParsedPDF, sourcePage int, extraRotation int) (int, error) {
	pageDict, deps, err := parsed.ExtractPageClosure(sourcePage, extraRotation)
	if err != nil {
		return 0, err
	}

	// Deterministic sorted order of old object numbers for reproducible output.
	oldNums := make([]int, 0, len(deps))
	for oldNum := range deps {
		oldNums = append(oldNums, oldNum)
	}
	sort.Ints(oldNums)

	mapping := make(map[int]int, len(oldNums))
	for _, oldNum := range oldNums {
		mapping[oldNum] = writer.alloc()
	}

	for _, oldNum := range oldNums {
		newNum := mapping[oldNum]
		renumbered := renumberPDFRefs(deps[oldNum], mapping)
		writer.writeRawObject(newNum, renumbered)
	}

	pageID := writer.alloc()
	renumberedDict := renumberPDFRefs(pageDict, mapping)
	renumberedDict = insertDictEntry(renumberedDict, "/Parent 2 0 R")
	writer.writeRawObject(pageID, renumberedDict)
	return pageID, nil
}

// appendImagePDFPage embeds a raster image into a PDF page.
// Native JPEGs with EXIF orientation 1 are embedded verbatim with /DCTDecode
// (even when rotated 90/180/270 degrees via the PDF CTM), preserving exact
// original JPEG bytes with zero recompression. PNG/GIF images are embedded
// losslessly with /FlateDecode (+ /SMask when transparent).
func appendImagePDFPage(writer *pdfWriter, src *resolvedSource, rotation int, baseLayout *Layout, override *LayoutOverride) (int, error) {
	raster, err := decodeBoundedImage(src.RawBytes)
	if err != nil {
		return 0, err
	}

	geom := ResolvePageGeometry(Dimensions{Width: float64(raster.Width), Height: float64(raster.Height)}, rotation, baseLayout, override)
	imgID := writer.alloc()

	if raster.IsJPEG && raster.ExifOrientation == 1 {
		// Embed exact original JPEG bytes — zero re-encoding!
		writer.writeStreamObject(
			imgID,
			fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode", raster.Width, raster.Height),
			raster.RawBytes,
		)
	} else {
		if raster.Img == nil {
			return 0, fmt.Errorf("image pixels unavailable")
		}
		rgb, alpha, hasAlpha := flattenToRGB(raster.Img, geom.Background)
		compressedRGB, err := flateBytes(rgb)
		if err != nil {
			return 0, err
		}
		smaskEntry := ""
		if hasAlpha && len(alpha) > 0 {
			smaskID := writer.alloc()
			compressedAlpha, err := flateBytes(alpha)
			if err != nil {
				return 0, err
			}
			writer.writeStreamObject(
				smaskID,
				fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode", raster.Width, raster.Height),
				compressedAlpha,
			)
			smaskEntry = fmt.Sprintf(" /SMask %d 0 R", smaskID)
		}
		writer.writeStreamObject(
			imgID,
			fmt.Sprintf("/Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode%s", raster.Width, raster.Height, smaskEntry),
			compressedRGB,
		)
	}

	var content bytes.Buffer
	if geom.Background != "transparent" {
		r, g, b := 1, 1, 1
		if geom.Background == "black" {
			r, g, b = 0, 0, 0
		}
		fmt.Fprintf(&content, "%d %d %d rg 0 0 %.3f %.3f re f\n", r, g, b, geom.PageWidth, geom.PageHeight)
	}

	// Draw the image inside [DrawX, DrawY, DrawWidth, DrawHeight] (in PDF
	// bottom-left coordinates) while applying the clockwise page rotation via
	// the PDF current transformation matrix so JPEG bytes need not be re-encoded.
	// Note: geom.DrawY is top-down in documentLayout; convert to PDF bottom-up.
	pdfY := geom.PageHeight - geom.DrawY - geom.DrawHeight
	q := ((rotation / 90) % 4 + 4) % 4
	var a, b, c, d, e, f float64
	switch q {
	case 0:
		a, b, c, d = geom.DrawWidth, 0, 0, geom.DrawHeight
		e, f = geom.DrawX, pdfY
	case 1: // 90 deg CW
		a, b, c, d = 0, -geom.DrawHeight, geom.DrawWidth, 0
		e, f = geom.DrawX, pdfY+geom.DrawHeight
	case 2: // 180 deg
		a, b, c, d = -geom.DrawWidth, 0, 0, -geom.DrawHeight
		e, f = geom.DrawX+geom.DrawWidth, pdfY+geom.DrawHeight
	case 3: // 270 deg CW
		a, b, c, d = 0, geom.DrawHeight, -geom.DrawWidth, 0
		e, f = geom.DrawX+geom.DrawWidth, pdfY
	}
	fmt.Fprintf(&content, "q %.3f %.3f %.3f %.3f %.3f %.3f cm /Im0 Do Q\n", a, b, c, d, e, f)

	contentID := writer.alloc()
	writer.writeStreamObject(contentID, "", content.Bytes())

	pageID := writer.alloc()
	pageBody := fmt.Sprintf(
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.3f %.3f] /Resources << /ProcSet [/PDF /ImageC] /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>",
		geom.PageWidth, geom.PageHeight, imgID, contentID,
	)
	writer.writeRawObject(pageID, []byte(pageBody))
	return pageID, nil
}

func flateBytes(in []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(in); err != nil {
		_ = zw.Close()
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
