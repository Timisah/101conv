package document

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"math"
	"strings"
)

const (
	emuPerPoint = 12700.0
	twipsPerPt  = 20.0
)

func addZipEntry(zw *zip.Writer, name string, method uint16, data []byte) error {
	fw, err := zw.CreateHeader(&zip.FileHeader{
		Name:   name,
		Method: method,
	})
	if err != nil {
		return err
	}
	_, err = fw.Write(data)
	return err
}

// assembleDOCX builds a spec-compliant paginated OOXML Word document placing
// one page image per document page at its physical dimensions.
func assembleDOCX(ctx context.Context, manifest Manifest, resolved map[string]*resolvedSource, workDir, pdftoppmPath string) ([]byte, error) {
	pages, err := renderAllPages(ctx, manifest, resolved, workDir, pdftoppmPath)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	var rels strings.Builder
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	rels.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)

	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	body.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><w:body>`)

	hasJPG, hasPNG := false, false
	lastW, lastH := 11906, 16838 // A4 in twips

	for i, pg := range pages {
		mediaName := fmt.Sprintf("media/page%03d.%s", i+1, pg.Ext)
		if pg.Ext == "jpg" {
			hasJPG = true
		} else {
			hasPNG = true
		}
		if err := addZipEntry(zw, "word/"+mediaName, zip.Store, pg.Bytes); err != nil {
			_ = zw.Close()
			return nil, err
		}
		rID := fmt.Sprintf("rId%d", i+1)
		fmt.Fprintf(&rels, `<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="%s"/>`, rID, mediaName)

		geom := ResolvePageGeometry(Dimensions{Width: float64(pg.Width), Height: float64(pg.Height)}, 0, manifest.Layout, manifest.Pages[i].Layout)
		cx := int64(math.Round(geom.DrawWidth * emuPerPoint))
		cy := int64(math.Round(geom.DrawHeight * emuPerPoint))
		pageW := int(math.Round(geom.PageWidth * twipsPerPt))
		pageH := int(math.Round(geom.PageHeight * twipsPerPt))
		lastW, lastH = pageW, pageH

		docPrID := i + 1
		fmt.Fprintf(&body,
			`<w:p><w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0"><wp:extent cx="%d" cy="%d"/><wp:docPr id="%d" name="Page %d"/><a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic><pic:nvPicPr><pic:cNvPr id="%d" name="page%03d.%s"/><pic:cNvPicPr/></pic:nvPicPr><pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill><pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`,
			cx, cy, docPrID, docPrID, docPrID, docPrID, pg.Ext, rID, cx, cy,
		)
		if i+1 < len(pages) {
			body.WriteString(`<w:p><w:r><w:br w:type="page"/></w:r></w:p>`)
		}
	}
	fmt.Fprintf(&body, `<w:sectPr><w:pgSz w:w="%d" w:h="%d"/><w:pgMar w:top="0" w:right="0" w:bottom="0" w:left="0" w:header="0" w:footer="0" w:gutter="0"/></w:sectPr></w:body></w:document>`, lastW, lastH)
	rels.WriteString(`</Relationships>`)

	var ct strings.Builder
	ct.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	ct.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	ct.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	ct.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	if hasJPG {
		ct.WriteString(`<Default Extension="jpg" ContentType="image/jpeg"/>`)
	}
	if hasPNG {
		ct.WriteString(`<Default Extension="png" ContentType="image/png"/>`)
	}
	ct.WriteString(`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>`)
	ct.WriteString(`</Types>`)

	rootRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
		`</Relationships>`

	for name, data := range map[string]string{
		"[Content_Types].xml":          ct.String(),
		"_rels/.rels":                  rootRels,
		"word/document.xml":            body.String(),
		"word/_rels/document.xml.rels": rels.String(),
	} {
		if err := addZipEntry(zw, name, zip.Deflate, []byte(data)); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// assemblePPTX builds a spec-compliant paginated OOXML PowerPoint deck with
// one slide per page sized to match the document geometry.
func assemblePPTX(ctx context.Context, manifest Manifest, resolved map[string]*resolvedSource, workDir, pdftoppmPath string) ([]byte, error) {
	pages, err := renderAllPages(ctx, manifest, resolved, workDir, pdftoppmPath)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	firstGeom := ResolvePageGeometry(Dimensions{Width: float64(pages[0].Width), Height: float64(pages[0].Height)}, 0, manifest.Layout, manifest.Pages[0].Layout)
	slideW := int64(math.Round(firstGeom.PageWidth * emuPerPoint))
	slideH := int64(math.Round(firstGeom.PageHeight * emuPerPoint))

	var presRels strings.Builder
	presRels.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	presRels.WriteString(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`)

	var sldIdLst strings.Builder
	var ctOverrides strings.Builder
	hasJPG, hasPNG := false, false

	for i, pg := range pages {
		idx := i + 1
		mediaName := fmt.Sprintf("page%03d.%s", idx, pg.Ext)
		if pg.Ext == "jpg" {
			hasJPG = true
		} else {
			hasPNG = true
		}
		if err := addZipEntry(zw, "ppt/media/"+mediaName, zip.Store, pg.Bytes); err != nil {
			_ = zw.Close()
			return nil, err
		}

		// Fit image cleanly inside slideW x slideH keeping aspect ratio.
		imgAspect := float64(pg.Width) / math.Max(1, float64(pg.Height))
		slideAspect := float64(slideW) / math.Max(1, float64(slideH))
		var drawW, drawH int64
		if imgAspect > slideAspect {
			drawW = slideW
			drawH = int64(math.Round(float64(slideW) / imgAspect))
		} else {
			drawH = slideH
			drawW = int64(math.Round(float64(slideH) * imgAspect))
		}
		offX := (slideW - drawW) / 2
		offY := (slideH - drawH) / 2

		slideXML := fmt.Sprintf(
			`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+"\n"+
				`<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr/><p:pic><p:nvPicPr><p:cNvPr id="2" name="Page %d"/><p:cNvPicPr/><p:nvPr/></p:nvPicPr><p:blipFill><a:blip r:embed="rId1"/><a:stretch><a:fillRect/></a:stretch></p:blipFill><p:spPr><a:xfrm><a:off x="%d" y="%d"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></p:spPr></p:pic></p:spTree></p:cSld></p:sld>`,
			idx, offX, offY, drawW, drawH,
		)
		slideRel := fmt.Sprintf(
			`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+"\n"+
				`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="../media/%s"/></Relationships>`,
			mediaName,
		)
		if err := addZipEntry(zw, fmt.Sprintf("ppt/slides/slide%d.xml", idx), zip.Deflate, []byte(slideXML)); err != nil {
			_ = zw.Close()
			return nil, err
		}
		if err := addZipEntry(zw, fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", idx), zip.Deflate, []byte(slideRel)); err != nil {
			_ = zw.Close()
			return nil, err
		}
		rID := fmt.Sprintf("rId%d", idx)
		fmt.Fprintf(&presRels, `<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, rID, idx)
		fmt.Fprintf(&sldIdLst, `<p:sldId id="%d" r:id="%s"/>`, 255+idx, rID)
		fmt.Fprintf(&ctOverrides, `<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, idx)
	}
	presRels.WriteString(`</Relationships>`)

	presXML := fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+"\n"+
			`<p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldIdLst>%s</p:sldIdLst><p:sldSz cx="%d" cy="%d"/><p:notesSz cx="%d" cy="%d"/></p:presentation>`,
		sldIdLst.String(), slideW, slideH, slideH, slideW,
	)

	var ct strings.Builder
	ct.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n")
	ct.WriteString(`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">`)
	ct.WriteString(`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>`)
	ct.WriteString(`<Default Extension="xml" ContentType="application/xml"/>`)
	if hasJPG {
		ct.WriteString(`<Default Extension="jpg" ContentType="image/jpeg"/>`)
	}
	if hasPNG {
		ct.WriteString(`<Default Extension="png" ContentType="image/png"/>`)
	}
	ct.WriteString(`<Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>`)
	ct.WriteString(ctOverrides.String())
	ct.WriteString(`</Types>`)

	rootRels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/>` +
		`</Relationships>`

	for name, data := range map[string]string{
		"[Content_Types].xml":            ct.String(),
		"_rels/.rels":                    rootRels,
		"ppt/presentation.xml":           presXML,
		"ppt/_rels/presentation.xml.rels": presRels.String(),
	} {
		if err := addZipEntry(zw, name, zip.Deflate, []byte(data)); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// assembleODF builds a spec-compliant OpenDocument Text (odt) or Presentation
// (odp) archive with uncompressed leading mimetype and META-INF/manifest.xml.
func assembleODF(ctx context.Context, kind string, manifest Manifest, resolved map[string]*resolvedSource, workDir, pdftoppmPath string) ([]byte, error) {
	pages, err := renderAllPages(ctx, manifest, resolved, workDir, pdftoppmPath)
	if err != nil {
		return nil, err
	}

	mimeType := "application/vnd.oasis.opendocument.text"
	if kind == "odp" {
		mimeType = "application/vnd.oasis.opendocument.presentation"
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Per ODF spec, mimetype MUST be the first entry and stored uncompressed.
	if err := addZipEntry(zw, "mimetype", zip.Store, []byte(mimeType)); err != nil {
		_ = zw.Close()
		return nil, err
	}

	var mfEntries strings.Builder
	fmt.Fprintf(&mfEntries, `<manifest:file-entry manifest:full-path="/" manifest:media-type="%s"/>`, mimeType)
	mfEntries.WriteString(`<manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/>`)
	mfEntries.WriteString(`<manifest:file-entry manifest:full-path="styles.xml" manifest:media-type="text/xml"/>`)

	var body strings.Builder
	for i, pg := range pages {
		picPath := fmt.Sprintf("Pictures/page%03d.%s", i+1, pg.Ext)
		picMime := "image/png"
		if pg.Ext == "jpg" {
			picMime = "image/jpeg"
		}
		if err := addZipEntry(zw, picPath, zip.Store, pg.Bytes); err != nil {
			_ = zw.Close()
			return nil, err
		}
		fmt.Fprintf(&mfEntries, `<manifest:file-entry manifest:full-path="%s" manifest:media-type="%s"/>`, picPath, picMime)

		geom := ResolvePageGeometry(Dimensions{Width: float64(pg.Width), Height: float64(pg.Height)}, 0, manifest.Layout, manifest.Pages[i].Layout)
		wCM := geom.DrawWidth / PointsPerMM / 10.0
		hCM := geom.DrawHeight / PointsPerMM / 10.0

		if kind == "odp" {
			fmt.Fprintf(&body,
				`<draw:page draw:name="page%d" draw:master-page-name="Default"><draw:frame draw:name="Image%d" svg:width="%.2fcm" svg:height="%.2fcm" svg:x="0cm" svg:y="0cm"><draw:image xlink:href="%s" xlink:type="simple" xlink:show="embed" xlink:actuate="onLoad"/></draw:frame></draw:page>`,
				i+1, i+1, wCM, hCM, picPath,
			)
		} else {
			styleName := "P1"
			if i > 0 {
				styleName = "PBreak"
			}
			fmt.Fprintf(&body,
				`<text:p text:style-name="%s"><draw:frame draw:name="Image%d" text:anchor-type="as-char" svg:width="%.2fcm" svg:height="%.2fcm"><draw:image xlink:href="%s" xlink:type="simple" xlink:show="embed" xlink:actuate="onLoad"/></draw:frame></text:p>`,
				styleName, i+1, wCM, hCM, picPath,
			)
		}
	}

	var contentXML string
	if kind == "odp" {
		contentXML = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
			`<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0" xmlns:xlink="http://www.w3.org/1999/xlink" office:version="1.2"><office:body><office:presentation>` +
			body.String() + `</office:presentation></office:body></office:document-content>`
	} else {
		contentXML = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
			`<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0" xmlns:xlink="http://www.w3.org/1999/xlink" office:version="1.2">` +
			`<office:automatic-styles><style:style style:name="P1" style:family="paragraph"/><style:style style:name="PBreak" style:family="paragraph"><style:paragraph-properties fo:break-before="page"/></style:style></office:automatic-styles>` +
			`<office:body><office:text>` + body.String() + `</office:text></office:body></office:document-content>`
	}

	stylesXML := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<office:document-styles xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" office:version="1.2"><office:styles/></office:document-styles>`

	manifestXML := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" manifest:version="1.2">` +
		mfEntries.String() + `</manifest:manifest>`

	for name, data := range map[string]string{
		"content.xml":           contentXML,
		"styles.xml":            stylesXML,
		"META-INF/manifest.xml": manifestXML,
	} {
		if err := addZipEntry(zw, name, zip.Deflate, []byte(data)); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func renderAllPages(ctx context.Context, manifest Manifest, resolved map[string]*resolvedSource, workDir, pdftoppmPath string) ([]renderedPage, error) {
	out := make([]renderedPage, 0, len(manifest.Pages))
	for i, page := range manifest.Pages {
		src, ok := resolved[page.UploadID]
		if !ok {
			return nil, fmt.Errorf("pages[%d]: source upload %q not found", i, page.UploadID)
		}
		rendered, err := renderPageForRasterExport(ctx, page, src, workDir, pdftoppmPath)
		if err != nil {
			return nil, fmt.Errorf("pages[%d]: %w", i, err)
		}
		out = append(out, rendered)
	}
	return out, nil
}
