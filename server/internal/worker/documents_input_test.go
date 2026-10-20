package worker

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestOfficePackagePreflight(t *testing.T) {
	for _, tt := range []struct {
		name, extension string
		entries         map[string]string
		valid           bool
	}{
		{"docx", "docx", map[string]string{"[Content_Types].xml": "types", "word/document.xml": "document"}, true},
		{"wrong-kind", "xlsx", map[string]string{"[Content_Types].xml": "types", "word/document.xml": "document"}, false},
		{"missing-types", "docx", map[string]string{"word/document.xml": "document"}, false},
		{"traversal", "docx", map[string]string{"[Content_Types].xml": "types", "word/document.xml": "document", "../escape": "escape"}, false},
		{"odt", "odt", map[string]string{"mimetype": "application/vnd.oasis.opendocument.text", "content.xml": "document"}, true},
		{"wrong-mime", "odt", map[string]string{"mimetype": "application/vnd.oasis.opendocument.spreadsheet", "content.xml": "document"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "input")
			f, err := os.Create(p)
			if err != nil {
				t.Fatal(err)
			}
			z := zip.NewWriter(f)
			for name, body := range tt.entries {
				w, err := z.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = w.Write([]byte(body)); err != nil {
					t.Fatal(err)
				}
			}
			if err = z.Close(); err != nil {
				t.Fatal(err)
			}
			f.Close()
			if err = validateOfficeInput(p, tt.extension); (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
		})
	}
	for _, ext := range []string{"docx", "xlsx", "pptx", "odt", "doc", "xls", "ppt", "rtf"} {
		p := filepath.Join(t.TempDir(), "corrupt")
		os.WriteFile(p, []byte("invalid"), 0600)
		if err := validateOfficeInput(p, ext); err == nil {
			t.Fatalf("accepted corrupt %s", ext)
		}
	}
}
