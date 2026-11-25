package document

import "math"

const (
	PointsPerMM    = 72.0 / 25.4
	A4WidthMM      = 210.0
	A4HeightMM     = 297.0
	LetterWidthMM  = 215.9
	LetterHeightMM = 279.4

	autoDPI          = 150.0
	autoMinLongEdge  = 144.0
	autoMaxLongEdge  = 1080.0
	maxMarginMM      = 50.0
	minCustomMM      = 25.0
	maxCustomMM      = 1000.0
)

type Dimensions struct {
	Width  float64
	Height float64
}

type PageGeometry struct {
	PageWidth   float64
	PageHeight  float64
	ImageWidth  float64
	ImageHeight float64
	DrawX       float64
	DrawY       float64
	DrawWidth   float64
	DrawHeight  float64
	Mode        string
	Background  string
}

func DefaultLayout() Layout {
	return Layout{
		Mode:           "automatic",
		PageSize:       "auto",
		CustomWidthMM:  A4WidthMM,
		CustomHeightMM: A4HeightMM,
		MarginMM:       0,
		Background:     "white",
		PositionX:      0.5,
		PositionY:      0.5,
	}
}

func clampFloat(v, lo, hi float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func finiteOr(v, fallback float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fallback
	}
	return v
}

// MergeLayoutSettings mirrors web/src/features/documentLayout.ts so server
// export geometry matches browser preview geometry.
func MergeLayoutSettings(base *Layout, override *LayoutOverride) Layout {
	out := DefaultLayout()
	if base != nil {
		out = *base
	}
	if override != nil {
		if override.Mode != nil {
			out.Mode = *override.Mode
		}
		if override.PageSize != nil {
			out.PageSize = *override.PageSize
		}
		if override.CustomWidthMM != nil {
			out.CustomWidthMM = *override.CustomWidthMM
		}
		if override.CustomHeightMM != nil {
			out.CustomHeightMM = *override.CustomHeightMM
		}
		if override.MarginMM != nil {
			out.MarginMM = *override.MarginMM
		}
		if override.Background != nil {
			out.Background = *override.Background
		}
		if override.PositionX != nil {
			out.PositionX = *override.PositionX
		}
		if override.PositionY != nil {
			out.PositionY = *override.PositionY
		}
	}
	if out.Mode != "fit" && out.Mode != "fill" {
		out.Mode = "automatic"
	}
	if out.PageSize != "a4" && out.PageSize != "letter" && out.PageSize != "custom" {
		out.PageSize = "auto"
	}
	out.CustomWidthMM = clampFloat(finiteOr(out.CustomWidthMM, A4WidthMM), minCustomMM, maxCustomMM)
	out.CustomHeightMM = clampFloat(finiteOr(out.CustomHeightMM, A4HeightMM), minCustomMM, maxCustomMM)
	out.MarginMM = clampFloat(finiteOr(out.MarginMM, 0), 0, maxMarginMM)
	if out.Background != "transparent" && out.Background != "black" {
		out.Background = "white"
	}
	out.PositionX = clampFloat(finiteOr(out.PositionX, 0.5), 0, 1)
	out.PositionY = clampFloat(finiteOr(out.PositionY, 0.5), 0, 1)
	return out
}

func OrientedDimensions(dim Dimensions, rotation int) Dimensions {
	q := ((rotation / 90) % 4 + 4) % 4
	if q%2 == 0 {
		return dim
	}
	return Dimensions{Width: dim.Height, Height: dim.Width}
}

func automaticPageSize(dim Dimensions) Dimensions {
	w := math.Max(1, finiteOr(dim.Width, 1))
	h := math.Max(1, finiteOr(dim.Height, 1))
	baseScale := PointsPerMM * 25.4 / autoDPI
	pw := w * baseScale
	ph := h * baseScale
	longEdge := math.Max(pw, ph)
	if longEdge > autoMaxLongEdge {
		scale := autoMaxLongEdge / longEdge
		pw *= scale
		ph *= scale
	} else if longEdge < autoMinLongEdge {
		scale := autoMinLongEdge / longEdge
		pw *= scale
		ph *= scale
	}
	return Dimensions{Width: pw, Height: ph}
}

func fixedPageSize(layout Layout, dim Dimensions) Dimensions {
	sourceLandscape := dim.Width > dim.Height
	baseW, baseH := A4WidthMM, A4HeightMM
	switch layout.PageSize {
	case "letter":
		baseW, baseH = LetterWidthMM, LetterHeightMM
	case "custom":
		baseW, baseH = layout.CustomWidthMM, layout.CustomHeightMM
	}
	pageLandscape := baseW > baseH
	if sourceLandscape != pageLandscape {
		baseW, baseH = baseH, baseW
	}
	return Dimensions{Width: baseW * PointsPerMM, Height: baseH * PointsPerMM}
}

func ResolvePageGeometry(dim Dimensions, rotation int, base *Layout, override *LayoutOverride) PageGeometry {
	eff := MergeLayoutSettings(base, override)
	img := OrientedDimensions(dim, rotation)
	automatic := eff.Mode == "automatic"
	var page Dimensions
	if automatic {
		page = automaticPageSize(img)
	} else {
		page = fixedPageSize(eff, img)
	}
	margin := math.Max(0, math.Min(eff.MarginMM*PointsPerMM, math.Min(page.Width, page.Height)/2-1))
	contentW := math.Max(1, page.Width-margin*2)
	contentH := math.Max(1, page.Height-margin*2)
	containScale := math.Min(contentW/img.Width, contentH/img.Height)
	coverScale := math.Max(contentW/img.Width, contentH/img.Height)
	scale := containScale
	if eff.Mode == "fill" && !automatic {
		scale = coverScale
	}
	drawW := img.Width * scale
	drawH := img.Height * scale
	return PageGeometry{
		PageWidth:   page.Width,
		PageHeight:  page.Height,
		ImageWidth:  img.Width,
		ImageHeight: img.Height,
		DrawX:       margin + (contentW-drawW)*eff.PositionX,
		DrawY:       margin + (contentH-drawH)*eff.PositionY,
		DrawWidth:   drawW,
		DrawHeight:  drawH,
		Mode:        eff.Mode,
		Background:  eff.Background,
	}
}
