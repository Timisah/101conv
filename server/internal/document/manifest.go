// Package document owns the non-destructive Document Creator manifest.
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

type Manifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	ProjectID     string `json:"projectId,omitempty"`
	Name          string `json:"name"`
	OutputFormat  string `json:"outputFormat"`
	Layout        *Layout `json:"layout,omitempty"`
	Pages         []Page `json:"pages"`
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
	ID         string         `json:"id"`
	UploadID   string         `json:"uploadId"`
	SourcePage int            `json:"sourcePage"`
	Rotation   int            `json:"rotation"`
	Layout     *LayoutOverride `json:"layout,omitempty"`
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
	if strings.ToLower(strings.TrimSpace(m.OutputFormat)) != "pdf" {
		return fmt.Errorf("only PDF export is available in the first manifest version")
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
