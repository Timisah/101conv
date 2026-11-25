// Package document owns the non-destructive Document Creator manifest,
// project persistence, source inspection, and multi-format assembly.
// Exporters consume this typed contract; they never infer page order from
// filenames or mutate source uploads.
package document

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	SchemaVersion = 1
	MaxPages      = 500
	MaxPageNumber = 10000
	MaxNameBytes  = 120
)

var identifierRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// FormatInfo describes a supported Document Creator export format along with
// honest fidelity expectations so callers and UIs never overpromise editable
// text reconstruction for rasterized or paginated exports.
type FormatInfo struct {
	ID                 string `json:"id"`
	Label              string `json:"label"`
	Extension          string `json:"extension"`
	MIMEType           string `json:"mimeType"`
	PreservesNativePDF bool   `json:"preservesNativePdf"`
	RasterizesVector   bool   `json:"rasterizesVector"`
	FidelitySummary    string `json:"fidelitySummary"`
	FidelityWarning    string `json:"fidelityWarning,omitempty"`
}

var supportedFormats = []FormatInfo{
	{
		ID:                 "pdf",
		Label:              "PDF Document",
		Extension:          "pdf",
		MIMEType:           "application/pdf",
		PreservesNativePDF: true,
		RasterizesVector:   false,
		FidelitySummary:    "Preserves native PDF vector text/graphics and embeds original JPEG/PNG bytes without needless resampling.",
	},
	{
		ID:                 "cbz",
		Label:              "CBZ (Comic Book Archive)",
		Extension:          "cbz",
		MIMEType:           "application/vnd.comicbook+zip",
		PreservesNativePDF: false,
		RasterizesVector:   true,
		FidelitySummary:    "Ordered ZIP of page images preserving original JPEG/PNG bytes for unrotated raster pages.",
		FidelityWarning:    "PDF and Office pages are rendered as raster page images; selectable vector text is not preserved.",
	},
	{
		ID:                 "zip",
		Label:              "Ordered Images ZIP",
		Extension:          "zip",
		MIMEType:           "application/zip",
		PreservesNativePDF: false,
		RasterizesVector:   true,
		FidelitySummary:    "Numbered page images in export order alongside a non-destructive manifest.json.",
		FidelityWarning:    "PDF and Office pages are rendered as PNG images; vector text is not preserved.",
	},
	{
		ID:                 "tiff",
		Label:              "Multipage TIFF",
		Extension:          "tiff",
		MIMEType:           "image/tiff",
		PreservesNativePDF: false,
		RasterizesVector:   true,
		FidelitySummary:    "Single multipage TIFF file containing one uncompressed RGB page per input page.",
		FidelityWarning:    "All pages are stored as raster images; selectable PDF text is not preserved.",
	},
	{
		ID:                 "docx",
		Label:              "Word Document (DOCX)",
		Extension:          "docx",
		MIMEType:           "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		PreservesNativePDF: false,
		RasterizesVector:   true,
		FidelitySummary:    "Paginated DOCX placing one page image per document page at its physical dimensions.",
		FidelityWarning:    "Paginated image assembly — does not convert scans or PDFs into editable flowing Word paragraphs.",
	},
	{
		ID:                 "pptx",
		Label:              "PowerPoint Presentation (PPTX)",
		Extension:          "pptx",
		MIMEType:           "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		PreservesNativePDF: false,
		RasterizesVector:   true,
		FidelitySummary:    "Paginated PPTX placing one page image per slide sized to match the page aspect ratio.",
		FidelityWarning:    "Paginated slide assembly — pages are embedded as slide images rather than editable text boxes.",
	},
	{
		ID:                 "odt",
		Label:              "OpenDocument Text (ODT)",
		Extension:          "odt",
		MIMEType:           "application/vnd.oasis.opendocument.text",
		PreservesNativePDF: false,
		RasterizesVector:   true,
		FidelitySummary:    "Paginated ODT placing one page image per page at its physical dimensions.",
		FidelityWarning:    "Paginated image assembly — does not reconstruct editable flowing text.",
	},
	{
		ID:                 "odp",
		Label:              "OpenDocument Presentation (ODP)",
		Extension:          "odp",
		MIMEType:           "application/vnd.oasis.opendocument.presentation",
		PreservesNativePDF: false,
		RasterizesVector:   true,
		FidelitySummary:    "Paginated ODP placing one page image per slide.",
		FidelityWarning:    "Paginated slide assembly — pages are embedded as slide images rather than editable text boxes.",
	},
}

// SupportedFormats returns a copy of the verified export formats and their
// honest fidelity metadata.
func SupportedFormats() []FormatInfo {
	out := make([]FormatInfo, len(supportedFormats))
	copy(out, supportedFormats)
	return out
}

// LookupFormat resolves a normalized output format identifier.
func LookupFormat(format string) (FormatInfo, bool) {
	normalized := strings.ToLower(strings.TrimSpace(format))
	for _, info := range supportedFormats {
		if info.ID == normalized {
			return info, true
		}
	}
	return FormatInfo{}, false
}

type Manifest struct {
	SchemaVersion int     `json:"schemaVersion"`
	ProjectID     string  `json:"projectId,omitempty"`
	Name          string  `json:"name"`
	OutputFormat  string  `json:"outputFormat"`
	Layout        *Layout `json:"layout,omitempty"`
	Pages         []Page  `json:"pages"`
}

// Layout is the optional v1 page-placement contract. It is additive so older
// manifests continue to mean the original exporter defaults.
type Layout struct {
	Mode           string  `json:"mode"`
	PageSize       string  `json:"pageSize"`
	CustomWidthMM  float64 `json:"customWidthMm"`
	CustomHeightMM float64 `json:"customHeightMm"`
	MarginMM       float64 `json:"marginMm"`
	Background     string  `json:"background"`
	PositionX      float64 `json:"positionX"`
	PositionY      float64 `json:"positionY"`
}

// LayoutOverride contains only fields that a page intentionally overrides.
// Pointers distinguish an omitted value from a meaningful zero such as a
// zero-millimetre margin or a centered coordinate.
type LayoutOverride struct {
	Mode           *string  `json:"mode,omitempty"`
	PageSize       *string  `json:"pageSize,omitempty"`
	CustomWidthMM  *float64 `json:"customWidthMm,omitempty"`
	CustomHeightMM *float64 `json:"customHeightMm,omitempty"`
	MarginMM       *float64 `json:"marginMm,omitempty"`
	Background     *string  `json:"background,omitempty"`
	PositionX      *float64 `json:"positionX,omitempty"`
	PositionY      *float64 `json:"positionY,omitempty"`
}

// Page is a reference to an existing upload and source page. Page order is
// the order in Manifest.Pages; the source upload remains unchanged.
type Page struct {
	ID              string          `json:"id"`
	UploadID        string          `json:"uploadId"`
	SourcePage      int             `json:"sourcePage"`
	Rotation        int             `json:"rotation"`
	Layout          *LayoutOverride `json:"layout,omitempty"`
	SourceName      string          `json:"sourceName,omitempty"`
	SourceKind      string          `json:"sourceKind,omitempty"`
	SourcePageCount int             `json:"sourcePageCount,omitempty"`
}

// SourceSpec is the frozen, ownership- and digest-verified source upload
// reference embedded into an immutable Worker build specification.
type SourceSpec struct {
	UploadID  string `json:"uploadId"`
	FileName  string `json:"fileName"`
	Extension string `json:"extension"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

// BuildExecutionSpec is stored inside jobs.ExecutionSpec for durable Worker
// execution of Document Creator exports.
type BuildExecutionSpec struct {
	Manifest                 Manifest     `json:"manifest"`
	Sources                  []SourceSpec `json:"sources"`
	LibreOfficeEngineID      string       `json:"libreOfficeEngineId,omitempty"`
	LibreOfficeEngineVersion string       `json:"libreOfficeEngineVersion,omitempty"`
	LibreOfficeRelativePath  string       `json:"libreOfficeRelativePath,omitempty"`
	PopplerEngineID          string       `json:"popplerEngineId,omitempty"`
	PopplerEngineVersion     string       `json:"popplerEngineVersion,omitempty"`
	PopplerRelativePath      string       `json:"popplerRelativePath,omitempty"`
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported manifest schema version %d", m.SchemaVersion)
	}
	if m.ProjectID != "" && !identifierRE.MatchString(m.ProjectID) {
		return fmt.Errorf("projectId is invalid")
	}
	name := strings.TrimSpace(m.Name)
	if name == "" || len([]byte(name)) > MaxNameBytes {
		return fmt.Errorf("name is required and must be at most %d bytes", MaxNameBytes)
	}
	if _, ok := LookupFormat(m.OutputFormat); !ok {
		return fmt.Errorf("unsupported output format %q", m.OutputFormat)
	}
	if m.Layout != nil {
		if err := validateLayout(*m.Layout); err != nil {
			return fmt.Errorf("layout: %w", err)
		}
	}
	if len(m.Pages) == 0 || len(m.Pages) > MaxPages {
		return fmt.Errorf("pages must contain between 1 and %d items", MaxPages)
	}
	seen := make(map[string]struct{}, len(m.Pages))
	for index, page := range m.Pages {
		if !identifierRE.MatchString(page.ID) {
			return fmt.Errorf("pages[%d].id is invalid", index)
		}
		if _, exists := seen[page.ID]; exists {
			return fmt.Errorf("pages[%d].id is duplicated", index)
		}
		seen[page.ID] = struct{}{}
		if !identifierRE.MatchString(page.UploadID) {
			return fmt.Errorf("pages[%d].uploadId is invalid", index)
		}
		if page.SourcePage < 1 || page.SourcePage > MaxPageNumber {
			return fmt.Errorf("pages[%d].sourcePage must be between 1 and %d", index, MaxPageNumber)
		}
		switch page.Rotation {
		case 0, 90, 180, 270:
		default:
			return fmt.Errorf("pages[%d].rotation must be 0, 90, 180, or 270", index)
		}
		if page.Layout != nil {
			if err := page.Layout.Validate(); err != nil {
				return fmt.Errorf("pages[%d].layout: %w", index, err)
			}
		}
	}
	return nil
}

func (o LayoutOverride) Validate() error {
	if o.Mode != nil && !validLayoutMode(*o.Mode) {
		return fmt.Errorf("mode is invalid")
	}
	if o.PageSize != nil && !validPageSize(*o.PageSize) {
		return fmt.Errorf("pageSize is invalid")
	}
	if o.CustomWidthMM != nil && !validPageDimension(*o.CustomWidthMM) || o.CustomHeightMM != nil && !validPageDimension(*o.CustomHeightMM) {
		return fmt.Errorf("custom page dimensions must be between 25 and 1000 millimetres")
	}
	if o.MarginMM != nil && (*o.MarginMM < 0 || *o.MarginMM > 50) {
		return fmt.Errorf("marginMm must be between 0 and 50")
	}
	if o.Background != nil && !validBackground(*o.Background) {
		return fmt.Errorf("background is invalid")
	}
	if o.PositionX != nil && (*o.PositionX < 0 || *o.PositionX > 1) || o.PositionY != nil && (*o.PositionY < 0 || *o.PositionY > 1) {
		return fmt.Errorf("position must be between 0 and 1")
	}
	return nil
}

func validateLayout(layout Layout) error {
	if !validLayoutMode(layout.Mode) {
		return fmt.Errorf("mode is invalid")
	}
	if !validPageSize(layout.PageSize) {
		return fmt.Errorf("pageSize is invalid")
	}
	if !validPageDimension(layout.CustomWidthMM) || !validPageDimension(layout.CustomHeightMM) {
		return fmt.Errorf("custom page dimensions must be between 25 and 1000 millimetres")
	}
	if layout.MarginMM < 0 || layout.MarginMM > 50 {
		return fmt.Errorf("marginMm must be between 0 and 50")
	}
	if !validBackground(layout.Background) {
		return fmt.Errorf("background is invalid")
	}
	if layout.PositionX < 0 || layout.PositionX > 1 || layout.PositionY < 0 || layout.PositionY > 1 {
		return fmt.Errorf("position must be between 0 and 1")
	}
	return nil
}

func validLayoutMode(value string) bool {
	return value == "automatic" || value == "fit" || value == "fill"
}

func validPageSize(value string) bool {
	return value == "auto" || value == "a4" || value == "letter" || value == "custom"
}

func validPageDimension(value float64) bool {
	return value >= 25 && value <= 1000
}

func validBackground(value string) bool {
	return value == "white" || value == "transparent" || value == "black"
}
