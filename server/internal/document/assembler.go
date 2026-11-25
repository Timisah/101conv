package document

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

type resolvedSource struct {
	UploadID  string
	FileName  string
	Extension string
	Kind      string // "image" or "pdf" (office is converted to pdf before assembly)
	RawBytes  []byte
	PDFPath   string
	ParsedPDF *ParsedPDF
}

type AssembleRequest struct {
	Manifest        Manifest
	SourcePaths     map[string]SourceFileInput
	WorkDir         string
	OutputPath      string
	MaxOutputBytes  int64
	LibreOfficePath string // optional path to soffice/libreoffice or managed office-convert
	PDFToPPMPath    string // optional path to pdftoppm
}

type SourceFileInput struct {
	Path      string
	FileName  string
	Extension string
}

type AssembleResult struct {
	OutputPath string
	Size       int64
	PageCount  int
	Format     FormatInfo
}

var (
	imageSourceExts  = map[string]bool{"jpg": true, "jpeg": true, "png": true, "gif": true}
	officeSourceExts = map[string]bool{"docx": true, "odt": true, "pptx": true, "odp": true, "xlsx": true, "ods": true, "rtf": true, "doc": true, "xls": true, "ppt": true, "txt": true}
)

// ClassifySourceExtension classifies a file extension into image, pdf, office,
// or unsupported.
func ClassifySourceExtension(ext string) string {
	ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
	if ext == "pdf" {
		return "pdf"
	}
	if imageSourceExts[ext] {
		return "image"
	}
	if officeSourceExts[ext] {
		return "office"
	}
	return ""
}

// Assemble executes a validated Document Creator manifest into OutputPath,
// validating every source, preserving native PDF vector content and original
// image bytes where supported, and verifying the resulting output signature.
func Assemble(ctx context.Context, req AssembleRequest) (AssembleResult, error) {
	if err := req.Manifest.Validate(); err != nil {
		return AssembleResult{}, fmt.Errorf("invalid document manifest: %w", err)
	}
	format, ok := LookupFormat(req.Manifest.OutputFormat)
	if !ok {
		return AssembleResult{}, fmt.Errorf("unsupported output format %q", req.Manifest.OutputFormat)
	}
	if req.WorkDir == "" {
		return AssembleResult{}, fmt.Errorf("work directory is required")
	}
	if err := os.MkdirAll(req.WorkDir, 0700); err != nil {
		return AssembleResult{}, err
	}

	// Resolve each unique upload referenced by the manifest once.
	resolved := make(map[string]*resolvedSource)
	for _, page := range req.Manifest.Pages {
		if _, exists := resolved[page.UploadID]; exists {
			continue
		}
		input, ok := req.SourcePaths[page.UploadID]
		if !ok {
			return AssembleResult{}, fmt.Errorf("source upload %q is missing", page.UploadID)
		}
		src, err := prepareSource(ctx, page.UploadID, input, req.WorkDir, req.LibreOfficePath)
		if err != nil {
			return AssembleResult{}, err
		}
		resolved[page.UploadID] = src
	}

	// Validate every requested sourcePage is in bounds for its source.
	for i, page := range req.Manifest.Pages {
		src := resolved[page.UploadID]
		switch src.Kind {
		case "image":
			if page.SourcePage != 1 {
				return AssembleResult{}, fmt.Errorf("pages[%d]: image source %q only has 1 page (requested %d)", i, src.FileName, page.SourcePage)
			}
		case "pdf":
			if page.SourcePage < 1 || page.SourcePage > src.ParsedPDF.PageCount() {
				return AssembleResult{}, fmt.Errorf("pages[%d]: PDF source %q has %d page(s) (requested %d)", i, src.FileName, src.ParsedPDF.PageCount(), page.SourcePage)
			}
		}
	}

	pdftoppm := req.PDFToPPMPath
	if pdftoppm == "" {
		if found, err := exec.LookPath("pdftoppm"); err == nil {
			pdftoppm = found
		}
	}

	var output []byte
	var err error
	switch format.ID {
	case "pdf":
		output, err = assemblePDF(req.Manifest, resolved)
	case "cbz":
		output, err = assembleCBZ(ctx, req.Manifest, resolved, req.WorkDir, pdftoppm)
	case "zip":
		output, err = assembleOrderedZIP(ctx, req.Manifest, resolved, req.WorkDir, pdftoppm)
	case "tiff":
		output, err = assembleMultipageTIFF(ctx, req.Manifest, resolved, req.WorkDir, pdftoppm)
	case "docx":
		output, err = assembleDOCX(ctx, req.Manifest, resolved, req.WorkDir, pdftoppm)
	case "pptx":
		output, err = assemblePPTX(ctx, req.Manifest, resolved, req.WorkDir, pdftoppm)
	case "odt":
		output, err = assembleODF(ctx, "odt", req.Manifest, resolved, req.WorkDir, pdftoppm)
	case "odp":
		output, err = assembleODF(ctx, "odp", req.Manifest, resolved, req.WorkDir, pdftoppm)
	default:
		return AssembleResult{}, fmt.Errorf("unsupported output format %q", format.ID)
	}
	if err != nil {
		return AssembleResult{}, err
	}
	if len(output) == 0 {
		return AssembleResult{}, fmt.Errorf("assembled %s output is empty", format.ID)
	}
	if req.MaxOutputBytes > 0 && int64(len(output)) > req.MaxOutputBytes {
		return AssembleResult{}, fmt.Errorf("assembled %s output (%d bytes) exceeds maximum output limit (%d bytes)", format.ID, len(output), req.MaxOutputBytes)
	}
	if err := validateOutputSignature(format.ID, output); err != nil {
		return AssembleResult{}, err
	}

	if err := os.WriteFile(req.OutputPath, output, 0600); err != nil {
		return AssembleResult{}, fmt.Errorf("write assembled output: %w", err)
	}
	return AssembleResult{
		OutputPath: req.OutputPath,
		Size:       int64(len(output)),
		PageCount:  len(req.Manifest.Pages),
		Format:     format,
	}, nil
}

func prepareSource(ctx context.Context, uploadID string, input SourceFileInput, workDir, libreOfficePath string) (*resolvedSource, error) {
	ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(input.Extension), "."))
	kind := ClassifySourceExtension(ext)
	switch kind {
	case "image":
		data, err := readBoundedFile(input.Path, maxPDFParseBytes)
		if err != nil {
			return nil, fmt.Errorf("read image %q: %w", input.FileName, err)
		}
		return &resolvedSource{
			UploadID:  uploadID,
			FileName:  input.FileName,
			Extension: ext,
			Kind:      "image",
			RawBytes:  data,
		}, nil

	case "pdf":
		data, err := readBoundedFile(input.Path, maxPDFParseBytes)
		if err != nil {
			return nil, fmt.Errorf("read PDF %q: %w", input.FileName, err)
		}
		parsed, err := ParsePDF(data)
		if err != nil {
			return nil, fmt.Errorf("parse PDF %q: %w", input.FileName, err)
		}
		return &resolvedSource{
			UploadID:  uploadID,
			FileName:  input.FileName,
			Extension: "pdf",
			Kind:      "pdf",
			RawBytes:  data,
			PDFPath:   input.Path,
			ParsedPDF: parsed,
		}, nil

	case "office":
		if err := ValidateOfficePackage(input.Path, ext); err != nil {
			return nil, fmt.Errorf("validate Office source %q: %w", input.FileName, err)
		}
		pdfPath, err := convertOfficeToPDF(ctx, input.Path, ext, workDir, libreOfficePath)
		if err != nil {
			return nil, fmt.Errorf("convert Office source %q to PDF: %w", input.FileName, err)
		}
		data, err := readBoundedFile(pdfPath, maxPDFParseBytes)
		if err != nil {
			return nil, fmt.Errorf("read converted Office PDF %q: %w", input.FileName, err)
		}
		parsed, err := ParsePDF(data)
		if err != nil {
			return nil, fmt.Errorf("parse converted Office PDF %q: %w", input.FileName, err)
		}
		return &resolvedSource{
			UploadID:  uploadID,
			FileName:  input.FileName,
			Extension: "pdf",
			Kind:      "pdf",
			RawBytes:  data,
			PDFPath:   pdfPath,
			ParsedPDF: parsed,
		}, nil

	default:
		return nil, fmt.Errorf("unsupported document source format %q for %q", ext, input.FileName)
	}
}

func convertOfficeToPDF(ctx context.Context, inputPath, ext, workDir, libreOfficePath string) (string, error) {
	bin := libreOfficePath
	if bin == "" {
		for _, candidate := range []string{"soffice", "libreoffice"} {
			if found, err := exec.LookPath(candidate); err == nil {
				bin = found
				break
			}
		}
	}
	if bin == "" {
		return "", fmt.Errorf("LibreOffice runtime is not installed in the Worker")
	}
	stageDir, err := os.MkdirTemp(workDir, "office-*")
	if err != nil {
		return "", err
	}
	profileDir := filepath.Join(stageDir, "lo-profile")
	outDir := filepath.Join(stageDir, "out")
	if err := os.MkdirAll(profileDir, 0700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(outDir, 0700); err != nil {
		return "", err
	}
	stagedInput := filepath.Join(stageDir, "source."+ext)
	inData, err := readBoundedFile(inputPath, maxPDFParseBytes)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(stagedInput, inData, 0600); err != nil {
		return "", err
	}

	var cmd *exec.Cmd
	if filepath.Base(bin) == "office-convert" {
		outPDF := filepath.Join(outDir, "source.pdf")
		cmd = exec.CommandContext(ctx, bin, stagedInput, outPDF, ext, "pdf")
	} else {
		cmd = exec.CommandContext(ctx, bin, "--headless", "--nologo", "--nodefault", "--nolockcheck", "--norestore", "-env:UserInstallation=file://"+profileDir, "--convert-to", "pdf", "--outdir", outDir, stagedInput)
	}
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + stageDir, "TMPDIR=" + stageDir}
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("office conversion failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	outPDF := filepath.Join(outDir, "source.pdf")
	if _, err := os.Stat(outPDF); err != nil {
		return "", fmt.Errorf("LibreOffice did not produce expected PDF output")
	}
	return outPDF, nil
}

// ValidateOfficePackage checks Office ZIP/OLE2 packages for traversal,
// compression bombs, and expected structure before passing to LibreOffice.
func ValidateOfficePackage(filename, extension string) error {
	required := map[string]string{
		"docx": "word/document.xml",
		"xlsx": "xl/workbook.xml",
		"pptx": "ppt/presentation.xml",
		"odt":  "content.xml",
		"ods":  "content.xml",
		"odp":  "content.xml",
	}
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
	case "txt":
	default:
		return fmt.Errorf("unsupported Office source format")
	}
	return nil
}

func validateOutputSignature(formatID string, data []byte) error {
	switch formatID {
	case "pdf":
		if !bytes.HasPrefix(data, []byte("%PDF-")) {
			return fmt.Errorf("generated PDF is missing %%PDF header")
		}
	case "tiff":
		if len(data) < 8 || !(data[0] == 'I' && data[1] == 'I' && data[2] == 42 && data[3] == 0) {
			return fmt.Errorf("generated TIFF has invalid header")
		}
	case "cbz", "zip", "docx", "pptx", "odt", "odp":
		if len(data) < 4 || !bytes.HasPrefix(data, []byte("PK\x03\x04")) {
			return fmt.Errorf("generated %s archive has invalid ZIP header", formatID)
		}
	}
	return nil
}
