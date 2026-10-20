// Package analysis owns bounded, ephemeral file inspection. It is a Core
// service, while media parsing is delegated to a pinned installed probe and
// the same typed probe contract is reused by the Worker.
package analysis

import (
	"archive/zip"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/timisah/101conv/server/internal/limits"
	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/media"
)

const (
	MaxHeaderBytes      = 4 << 20
	MaxOfficeEntries    = 10000
	MaxOfficeExpanded   = 256 << 20
	MaxAnalysisRawBytes = 12 << 20
)

var ErrInvalidFile = errors.New("file could not be safely inspected")

type File struct {
	ID        string
	Name      string
	Extension string
	Size      int64
	SHA256    string
	Path      string
}

type Service struct {
	management *management.Service
	root       string
	semaphore  chan struct{}
}

type Report struct {
	SchemaVersion int              `json:"schemaVersion"`
	File          FileSummary      `json:"file"`
	MIMEType      string           `json:"mimeType,omitempty"`
	DetectedType  string           `json:"detectedType,omitempty"`
	Summary       string           `json:"summary"`
	Media         *media.Probe     `json:"media,omitempty"`
	Image         *ImageSummary    `json:"image,omitempty"`
	Document      *DocumentSummary `json:"document,omitempty"`
	Warnings      []string         `json:"warnings,omitempty"`
	RawJSON       json.RawMessage  `json:"rawJson"`
}

type FileSummary struct {
	ID        string `json:"id"`
	Name      string `json:"fileName"`
	Extension string `json:"extension"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

type ImageSummary struct {
	Width       int            `json:"width"`
	Height      int            `json:"height"`
	ColorModel  string         `json:"colorModel,omitempty"`
	EXIFPresent bool           `json:"exifPresent,omitempty"`
	ICCProfile  bool           `json:"iccProfile,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type DocumentSummary struct {
	Kind           string            `json:"kind"`
	PDFVersion     string            `json:"pdfVersion,omitempty"`
	PackageEntries int               `json:"packageEntries,omitempty"`
	ExpandedBytes  uint64            `json:"expandedBytes,omitempty"`
	Metadata       map[string]string `json:"metadata,omitempty"`
}

func NewService(managementService *management.Service, uploadRoot string) (*Service, error) {
	if managementService == nil || strings.TrimSpace(uploadRoot) == "" {
		return nil, fmt.Errorf("analysis requires management storage and an upload path")
	}
	root, err := filepath.Abs(uploadRoot)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("analysis upload path is not a real directory")
	}
	if err := removeStaleInspectionFiles(root); err != nil {
		return nil, err
	}
	return &Service{management: managementService, root: root, semaphore: make(chan struct{}, 2)}, nil
}

func removeStaleInspectionFiles(root string) error {
	paths, err := filepath.Glob(filepath.Join(root, ".inspection-*"))
	if err != nil {
		return fmt.Errorf("find stale inspection files: %w", err)
	}
	for _, path := range paths {
		info, statErr := os.Lstat(path)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return fmt.Errorf("inspect stale temporary file: %w", statErr)
		}
		if info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove stale inspection file: %w", err)
			}
		}
	}
	return nil
}

func (s *Service) Analyze(ctx context.Context, file File) (Report, error) {
	if strings.TrimSpace(file.ID) == "" || strings.TrimSpace(file.Path) == "" || file.Size <= 0 || file.Size > limits.MaxInputBytes || len(file.SHA256) != sha256.Size*2 {
		return Report{}, fmt.Errorf("%w: invalid file identity", ErrInvalidFile)
	}
	path, err := filepath.Abs(file.Path)
	if err != nil || filepath.Dir(path) != s.root {
		return Report{}, fmt.Errorf("%w: file path is outside upload storage", ErrInvalidFile)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != file.Size {
		return Report{}, fmt.Errorf("%w: uploaded file changed or is unavailable", ErrInvalidFile)
	}
	hash, err := hashFile(path)
	if err != nil || !strings.EqualFold(hash, file.SHA256) {
		return Report{}, fmt.Errorf("%w: uploaded file digest changed", ErrInvalidFile)
	}
	header, err := readHeader(path)
	if err != nil {
		return Report{}, fmt.Errorf("%w: read file header: %v", ErrInvalidFile, err)
	}
	report := Report{SchemaVersion: 1, File: FileSummary{ID: file.ID, Name: file.Name, Extension: file.Extension, Size: file.Size, SHA256: file.SHA256}, MIMEType: mime.TypeByExtension("." + file.Extension), DetectedType: http.DetectContentType(header), Warnings: []string{"This scan is ephemeral; the uploaded file is removed after inspection. Extracted metadata is returned in full and may contain GPS, location, device, ownership, or other sensitive values."}}
	if report.MIMEType == "" {
		report.MIMEType = report.DetectedType
	}

	switch {
	case isImage(file.Extension):
		report.Image, err = inspectImage(path, file.Extension, header)
		if err != nil {
			return Report{}, fmt.Errorf("%w: %v", ErrInvalidFile, err)
		}
		report.Summary = imageSummary(*report.Image)
		if report.Media, err = s.probeFile(ctx, path); err != nil {
			report.Warnings = append(report.Warnings, "Container-specific image metadata could not be extracted by the installed media probe.")
		}
	case isMedia(file.Extension):
		probeErr := error(nil)
		report.Media, probeErr = s.probeFile(ctx, path)
		if probeErr != nil {
			report.Warnings = append(report.Warnings, "Video/audio stream details are unavailable because the installed bounded media probe could not inspect this file.")
			report.Summary = "Media file; stream details unavailable"
		} else {
			report.Summary = mediaSummary(*report.Media)
		}
	case file.Extension == "pdf":
		report.Document, err = inspectPDF(header)
		if err != nil {
			return Report{}, fmt.Errorf("%w: %v", ErrInvalidFile, err)
		}
		report.Summary = "PDF document"
	case isOffice(file.Extension):
		report.Document, err = inspectOffice(path, file.Extension)
		if err != nil {
			return Report{}, fmt.Errorf("%w: %v", ErrInvalidFile, err)
		}
		report.Summary = "Office document package"
	default:
		report.Summary = "Uploaded file"
	}
	// Raw JSON is a bounded view of this report. It excludes only the recursive
	// RawJSON field; otherwise it retains the complete bounded parser output.
	copyReport := report
	copyReport.RawJSON = nil
	raw, err := json.Marshal(copyReport)
	if err != nil {
		return Report{}, err
	}
	if len(raw) > MaxAnalysisRawBytes {
		return Report{}, fmt.Errorf("%w: extracted metadata is too large", ErrInvalidFile)
	}
	report.RawJSON = raw
	return report, nil
}

// AnalyzeReader scans a request body from an ephemeral file. The file is
// removed on every return path and is never inserted into conv_uploads, so an
// inspection cannot become a retained conversion original.
func (s *Service) AnalyzeReader(ctx context.Context, name string, input io.Reader) (Report, error) {
	if input == nil {
		return Report{}, fmt.Errorf("%w: inspection body is required", ErrInvalidFile)
	}
	temporary, err := os.CreateTemp(s.root, ".inspection-*")
	if err != nil {
		return Report{}, fmt.Errorf("create ephemeral inspection file: %w", err)
	}
	path := temporary.Name()
	defer os.Remove(path)
	hash := sha256.New()
	reader := &contextReader{ctx: ctx, reader: io.LimitReader(input, limits.MaxInputBytes+1)}
	size, err := io.Copy(io.MultiWriter(temporary, hash), reader)
	if err != nil {
		_ = temporary.Close()
		return Report{}, fmt.Errorf("%w: read inspection upload: %v", ErrInvalidFile, err)
	}
	if closeErr := temporary.Close(); closeErr != nil {
		return Report{}, fmt.Errorf("%w: close inspection upload: %v", ErrInvalidFile, closeErr)
	}
	if size <= 0 || size > limits.MaxInputBytes {
		return Report{}, fmt.Errorf("%w: inspection upload exceeds the size limit", ErrInvalidFile)
	}
	identifier := make([]byte, 16)
	if _, err = cryptorand.Read(identifier); err != nil {
		return Report{}, fmt.Errorf("create inspection identifier: %w", err)
	}
	fileName := filepath.Base(name)
	report, err := s.Analyze(ctx, File{ID: "inspection-" + hex.EncodeToString(identifier), Name: fileName, Extension: extensionOf(fileName), Size: size, SHA256: hex.EncodeToString(hash.Sum(nil)), Path: path})
	if err != nil {
		return Report{}, err
	}
	return report, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.reader.Read(p)
	}
}

func (s *Service) acquire(ctx context.Context) error {
	select {
	case s.semaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) release() { <-s.semaphore }

func (s *Service) probeFile(ctx context.Context, path string) (*media.Probe, error) {
	if err := s.acquire(ctx); err != nil {
		return nil, err
	}
	defer s.release()
	probePath, err := s.probeExecutable(ctx)
	if err != nil {
		return nil, err
	}
	return media.ProbeFile(ctx, probePath, filepath.Dir(path), path)
}

func (s *Service) probeExecutable(ctx context.Context) (string, error) {
	engines, err := s.management.ListEngines(ctx)
	if err != nil {
		return "", err
	}
	type candidate struct{ id, version, path string }
	candidates := []candidate{}
	for _, record := range engines {
		capable := false
		for _, capability := range record.Definition.Capabilities {
			if capability == "media.probe" {
				capable = true
				break
			}
		}
		if !capable {
			continue
		}
		for name, path := range record.Installation.ExecutablePaths {
			if name != "ffprobe" && !strings.Contains(strings.ToLower(name), "probe") {
				continue
			}
			candidates = append(candidates, candidate{record.Definition.ID, record.Definition.Version, path})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].id != candidates[j].id {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].version > candidates[j].version
	})
	for _, candidate := range candidates {
		path, err := filepath.Abs(candidate.path)
		if err != nil {
			continue
		}
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Mode().Perm()&0111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("no installed media probe is available")
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, io.LimitReader(f, limits.MaxInputBytes+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func extensionOf(name string) string {
	extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(filepath.Base(name))), ".")
	if len(extension) > 12 {
		return ""
	}
	return extension
}

func readHeader(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, MaxHeaderBytes))
}

func isImage(ext string) bool {
	switch ext {
	case "jpg", "jpeg", "png", "gif", "bmp", "tif", "tiff", "webp", "avif", "jxl":
		return true
	}
	return false
}

func isMedia(ext string) bool {
	switch ext {
	case "mp4", "mkv", "mov", "avi", "webm", "m4v", "mpg", "mpeg", "3gp", "flv", "mp3", "aac", "m4a", "ogg", "opus", "wav", "flac", "wma", "amr":
		return true
	}
	return false
}

func isOffice(ext string) bool {
	switch ext {
	case "docx", "xlsx", "pptx", "odt", "ods", "odp", "doc", "xls", "ppt", "rtf", "txt":
		return true
	}
	return false
}

func inspectImage(path, extension string, header []byte) (*ImageSummary, error) {
	exifMetadata := parseJPEGEXIF(header)
	summary := &ImageSummary{EXIFPresent: len(exifMetadata) > 0 || strings.Contains(string(header), "Exif\x00\x00"), ICCProfile: strings.Contains(string(header), "ICC_PROFILE") || strings.Contains(string(header), "iCCP"), Metadata: exifMetadata}
	f, err := os.Open(path)
	if err == nil {
		config, _, decodeErr := image.DecodeConfig(io.LimitReader(f, MaxHeaderBytes))
		if decodeErr == nil {
			summary.Width, summary.Height = config.Width, config.Height
			if config.ColorModel != nil {
				summary.ColorModel = fmt.Sprintf("%T", config.ColorModel)
			}
		}
		_ = f.Close()
		if decodeErr != nil && (extension == "jpg" || extension == "jpeg" || extension == "png" || extension == "gif") {
			return nil, fmt.Errorf("invalid %s image", extension)
		}
	}
	return summary, nil
}

func inspectPDF(header []byte) (*DocumentSummary, error) {
	version := ""
	if len(header) >= 8 && strings.HasPrefix(string(header[:8]), "%PDF-") {
		version = strings.TrimSpace(string(header[5:8]))
	}
	if version == "" {
		return nil, fmt.Errorf("invalid PDF header")
	}
	return &DocumentSummary{Kind: "pdf", PDFVersion: version, Metadata: pdfMetadata(header)}, nil
}

func inspectOffice(path, extension string) (*DocumentSummary, error) {
	if extension == "doc" || extension == "xls" || extension == "ppt" || extension == "rtf" || extension == "txt" {
		return &DocumentSummary{Kind: extension}, nil
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("invalid %s package", extension)
	}
	defer z.Close()
	var expanded uint64
	metadata := map[string]string{}
	for _, entry := range z.File {
		if len(z.File) > MaxOfficeEntries || entry.UncompressedSize64 > MaxOfficeExpanded || expanded > MaxOfficeExpanded-entry.UncompressedSize64 {
			return nil, fmt.Errorf("%s package exceeds inspection limit", extension)
		}
		expanded += entry.UncompressedSize64
		if strings.HasPrefix(entry.Name, "docProps/") || entry.Name == "meta.xml" {
			for key, value := range zipXMLMetadata(entry) {
				metadata[entry.Name+":"+key] = value
			}
		}
	}
	if len(metadata) == 0 {
		metadata = nil
	}
	return &DocumentSummary{Kind: extension, PackageEntries: len(z.File), ExpandedBytes: expanded, Metadata: metadata}, nil
}

var pdfMetadataRE = regexp.MustCompile(`/([A-Za-z][A-Za-z0-9]+)\s*(?:\(([^()]*)\)|<([0-9A-Fa-f]+)>)`)

func pdfMetadata(header []byte) map[string]string {
	metadata := map[string]string{}
	for _, match := range pdfMetadataRE.FindAllSubmatch(header, 256) {
		value := string(match[2])
		if len(match[3]) > 0 {
			decoded, err := hex.DecodeString(string(match[3]))
			if err == nil {
				value = string(decoded)
			}
		}
		if value != "" {
			metadata[string(match[1])] = value
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func zipXMLMetadata(entry *zip.File) map[string]string {
	reader, err := entry.Open()
	if err != nil {
		return nil
	}
	defer reader.Close()
	decoder := xml.NewDecoder(io.LimitReader(reader, 2<<20))
	metadata := map[string]string{}
	var stack []string
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch value := token.(type) {
		case xml.StartElement:
			stack = append(stack, value.Name.Local)
			text.Reset()
		case xml.CharData:
			if len(stack) > 0 {
				text.Write([]byte(value))
			}
		case xml.EndElement:
			if len(stack) > 0 {
				valueText := strings.TrimSpace(text.String())
				if valueText != "" {
					metadata[strings.Join(stack, ".")] = valueText
				}
				stack = stack[:len(stack)-1]
				text.Reset()
			}
		}
	}
	return metadata
}

func imageSummary(image ImageSummary) string {
	if image.Width > 0 && image.Height > 0 {
		return fmt.Sprintf("Image, %d × %d pixels", image.Width, image.Height)
	}
	return "Image"
}

func mediaSummary(probe media.Probe) string {
	parts := []string{}
	if probe.Duration > 0 {
		parts = append(parts, fmt.Sprintf("%.1f seconds", probe.Duration))
	}
	counts := map[string]int{}
	for _, stream := range probe.Streams {
		counts[strings.ToLower(stream.CodecType)]++
	}
	for _, kind := range []string{"video", "audio", "subtitle", "attachment"} {
		if counts[kind] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s stream(s)", counts[kind], kind))
		}
	}
	if len(parts) == 0 {
		return "Media file"
	}
	return strings.Join(parts, ", ")
}
