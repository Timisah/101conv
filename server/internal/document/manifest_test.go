package document

import "testing"

func validManifest() Manifest {
	return Manifest{
		SchemaVersion: SchemaVersion,
		ProjectID:     "project-1",
		Name:          "Holiday pages",
		OutputFormat:  "pdf",
		Pages: []Page{
			{ID: "page-1", UploadID: "upload-1", SourcePage: 1, Rotation: 0},
			{ID: "page-2", UploadID: "upload-2", SourcePage: 1, Rotation: 90},
		},
	}
}

func TestManifestValidate(t *testing.T) {
	if err := validManifest().Validate(); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestManifestValidateRejectsUnsafeOrAmbiguousPages(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"duplicate page id", func(m *Manifest) { m.Pages[1].ID = m.Pages[0].ID }},
		{"invalid rotation", func(m *Manifest) { m.Pages[0].Rotation = 45 }},
		{"invalid source page", func(m *Manifest) { m.Pages[0].SourcePage = 0 }},
		{"unsafe upload id", func(m *Manifest) { m.Pages[0].UploadID = "../secret" }},
		{"unsupported output", func(m *Manifest) { m.OutputFormat = "docx" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validManifest()
			test.mutate(&manifest)
			if err := manifest.Validate(); err == nil {
				t.Fatal("expected manifest validation error")
			}
		})
	}
}

func TestManifestValidateRejectsPageLimit(t *testing.T) {
	manifest := validManifest()
	manifest.Pages = make([]Page, MaxPages+1)
	for index := range manifest.Pages {
		manifest.Pages[index] = Page{ID: "page-" + string(rune('a'+index%26)), UploadID: "upload-1", SourcePage: 1}
	}
	if err := manifest.Validate(); err == nil {
		t.Fatal("expected page limit validation error")
	}
}
