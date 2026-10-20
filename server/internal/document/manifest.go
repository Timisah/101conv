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
	Pages         []Page `json:"pages"`
}

// Page is a reference to an existing upload and source page. Page order is
// the order in Manifest.Pages; the source upload remains unchanged.
type Page struct {
	ID         string `json:"id"`
	UploadID   string `json:"uploadId"`
	SourcePage int    `json:"sourcePage"`
	Rotation   int    `json:"rotation"`
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
	}
	return nil
}
