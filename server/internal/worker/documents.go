package worker

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

// Reject disguised inputs and obviously unsafe/compression-bomb Office packages
// before invoking LibreOffice. Parsing still occurs inside the isolated Worker.
func validateOfficeInput(filename, extension string) error {
	required := map[string]string{"docx": "word/document.xml", "xlsx": "xl/workbook.xml", "pptx": "ppt/presentation.xml", "odt": "content.xml", "ods": "content.xml", "odp": "content.xml"}
	if member, ok := required[extension]; ok {
		z, err := zip.OpenReader(filename)
		if err != nil {
			return fmt.Errorf("invalid or encrypted %s document: expected an Office ZIP package", extension)
		}
		defer z.Close()
		if len(z.File) > 10000 {
			return fmt.Errorf("Office package has too many entries")
		}
		var total uint64
		found, types := false, false
		for _, f := range z.File {
			name := strings.TrimSuffix(f.Name, "/")
			if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.ContainsAny(name, "\\\x00") || f.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("unsafe Office package entry")
			}
			if f.UncompressedSize64 > 256<<20 || total > 256<<20-f.UncompressedSize64 {
				return fmt.Errorf("Office package exceeds the 256 MiB expanded limit")
			}
			total += f.UncompressedSize64
			found = found || f.Name == member
			types = types || f.Name == "[Content_Types].xml"
		}
		if !found {
			return fmt.Errorf("Office package is missing %s", member)
		}
		if strings.HasSuffix(extension, "x") && !types {
			return fmt.Errorf("Office package is missing content types")
		}
		if strings.HasPrefix(extension, "od") {
			f, err := z.Open("mimetype")
			if err != nil {
				return fmt.Errorf("OpenDocument package is missing its MIME type")
			}
			defer f.Close()
			b, err := io.ReadAll(io.LimitReader(f, 256))
			mime := map[string]string{"odt": "text", "ods": "spreadsheet", "odp": "presentation"}
			if err != nil || string(b) != "application/vnd.oasis.opendocument."+mime[extension] {
				return fmt.Errorf("OpenDocument MIME type does not match %s", extension)
			}
		}
		return nil
	}
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	header := make([]byte, 8)
	n, err := f.Read(header)
	if err != nil && err != io.EOF {
		return err
	}
	header = header[:n]
	switch extension {
	case "doc", "xls", "ppt":
		if string(header) != "\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1" {
			return fmt.Errorf("invalid or unsupported legacy Office document")
		}
	case "rtf":
		if !strings.HasPrefix(string(header), "{\\rtf") {
			return fmt.Errorf("invalid RTF document")
		}
	case "txt": // LibreOffice detects text encoding; a filename alone cannot prove it.
	default:
		return fmt.Errorf("unsupported Office source format")
	}
	return nil
}
