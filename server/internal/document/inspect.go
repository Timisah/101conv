package document

import (
	"bytes"
	"fmt"
	"image"
	"math"
	"strings"
)

type SourcePageInfo struct {
	PageNumber int     `json:"pageNumber"`
	WidthPt    float64 `json:"widthPt"`
	HeightPt   float64 `json:"heightPt"`
	Rotation   int     `json:"rotation"`
}

type SourceInfo struct {
	UploadID         string           `json:"uploadId"`
	FileName         string           `json:"fileName"`
	Extension        string           `json:"extension"`
	Size             int64            `json:"size"`
	Kind             string           `json:"kind"` // "image", "pdf", "office"
	PageCount        int              `json:"pageCount"`
	ExpandedOnExport bool             `json:"expandedOnExport,omitempty"`
	Pages            []SourcePageInfo `json:"pages"`
}

// InspectSource safely inspects an uploaded image, PDF, or Office file to
// determine its page count and per-page geometry without mutating the source.
func InspectSource(uploadID, filePath, fileName, ext string, size int64) (SourceInfo, error) {
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	kind := ClassifySourceExtension(ext)
	if kind == "" {
		return SourceInfo{}, fmt.Errorf("unsupported document source format %q", ext)
	}
	info := SourceInfo{
		UploadID:  uploadID,
		FileName:  fileName,
		Extension: ext,
		Size:      size,
		Kind:      kind,
	}
	switch kind {
	case "image":
		data, err := readBoundedFile(filePath, maxPDFParseBytes)
		if err != nil {
			return SourceInfo{}, err
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return SourceInfo{}, fmt.Errorf("invalid image source: %w", err)
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxImagePixels {
			return SourceInfo{}, fmt.Errorf("image dimensions exceed safe bounds")
		}
		w, h := cfg.Width, cfg.Height
		if format == "jpeg" || isJPEGBytes(data) {
			if exifSwapsAxes(readJPEGExifOrientation(data)) {
				w, h = h, w
			}
		}
		pageDim := automaticPageSize(Dimensions{Width: float64(w), Height: float64(h)})
		info.PageCount = 1
		info.Pages = []SourcePageInfo{{
			PageNumber: 1,
			WidthPt:    math.Round(pageDim.Width*100) / 100,
			HeightPt:   math.Round(pageDim.Height*100) / 100,
			Rotation:   0,
		}}
		return info, nil

	case "pdf":
		data, err := readBoundedFile(filePath, maxPDFParseBytes)
		if err != nil {
			return SourceInfo{}, err
		}
		parsed, err := ParsePDF(data)
		if err != nil {
			return SourceInfo{}, err
		}
		pdfPages := parsed.PageInfos()
		info.PageCount = len(pdfPages)
		info.Pages = make([]SourcePageInfo, len(pdfPages))
		for i, p := range pdfPages {
			info.Pages[i] = SourcePageInfo{
				PageNumber: p.PageNumber,
				WidthPt:    p.WidthPt,
				HeightPt:   p.HeightPt,
				Rotation:   p.Rotation,
			}
		}
		return info, nil

	case "office":
		if err := ValidateOfficePackage(filePath, ext); err != nil {
			return SourceInfo{}, err
		}
		info.PageCount = 1
		info.ExpandedOnExport = true
		info.Pages = []SourcePageInfo{{
			PageNumber: 1,
			WidthPt:    math.Round(A4WidthMM*PointsPerMM*100) / 100,
			HeightPt:   math.Round(A4HeightMM*PointsPerMM*100) / 100,
			Rotation:   0,
		}}
		return info, nil
	}
	return SourceInfo{}, fmt.Errorf("unsupported document source format %q", ext)
}
