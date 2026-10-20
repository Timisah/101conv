// Package analysis owns bounded, privacy-safe file inspection. It is a Core
// service, while media parsing is delegated to a pinned installed probe and
// the same typed probe contract is reused by the Worker.
package analysis

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	"sort"
	"strings"

	"github.com/timisah/101conv/server/internal/management"
	"github.com/timisah/101conv/server/internal/media"
)

const (
	MaxHeaderBytes      = 4 << 20
	MaxOfficeEntries    = 10000
	MaxOfficeExpanded   = 256 << 20
	MaxAnalysisRawBytes = 1 << 20
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
	Name      string `json:"name"`
	Extension string `json:"extension"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
}

type ImageSummary struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	ColorModel  string `json:"colorModel,omitempty"`
	EXIFPresent bool   `json:"exifPresent,omitempty"`
	ICCProfile  bool   `json:"iccProfile,omitempty"`
}

type DocumentSummary struct {
	Kind           string `json:"kind"`
	PDFVersion     string `json:"pdfVersion,omitempty"`
	PackageEntries int    `json:"packageEntries,omitempty"`
	ExpandedBytes  uint64 `json:"expandedBytes,omitempty"`
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
	return &Service{management: managementService, root: root, semaphore: make(chan struct{}, 2)}, nil
}

func (s *Service) Analyze(ctx context.Context, file File) (Report, error) {
	if strings.TrimSpace(file.ID) == "" || strings.TrimSpace(file.Path) == "" || file.Size <= 0 || file.Size > 1<<30 || len(file.SHA256) != sha256.Size*2 {
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
	report := Report{SchemaVersion: 1, File: FileSummary{ID: file.ID, Name: file.Name, Extension: file.Extension, Size: file.Size, SHA256: file.SHA256}, MIMEType: mime.TypeByExtension("." + file.Extension), DetectedType: http.DetectContentType(header), Warnings: []string{"Location, GPS, serial-number, and ownership metadata are omitted from inspection output."}}
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
	case isMedia(file.Extension):
		if err := s.acquire(ctx); err != nil {
			return Report{}, err
		}
		probePath, probeErr := s.probeExecutable()
		if probeErr == nil {
			report.Media, probeErr = media.ProbeFile(ctx, probePath, filepath.Dir(path), path)
		}
		s.release()
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
	if report.Media == nil && isMedia(file.Extension) {
		report.Warnings = append(report.Warnings, "Inspection does not modify the uploaded file.")
	}
	// Raw JSON is a sanitized, bounded view of this report. It intentionally
	// excludes the recursive RawJSON field and therefore cannot leak arbitrary
	// parser output or private tags.
	copyReport := report
	copyReport.RawJSON = nil
	raw, err := json.Marshal(copyReport)
	if err != nil {
		return Report{}, err
	}
	if len(raw) > MaxAnalysisRawBytes {
		return Report{}, fmt.Errorf("%w: sanitized metadata is too large", ErrInvalidFile)
	}
	report.RawJSON = raw
	return report, nil
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

func (s *Service) probeExecutable() (string, error) {
	engines, err := s.management.ListEngines(context.Background())
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
	if _, err = io.Copy(hash, io.LimitReader(f, 1<<30+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
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
	summary := &ImageSummary{EXIFPresent: strings.Contains(string(header), "Exif\x00\x00"), ICCProfile: strings.Contains(string(header), "ICC_PROFILE") || strings.Contains(string(header), "iCCP")}
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
	return &DocumentSummary{Kind: "pdf", PDFVersion: version}, nil
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
	for _, entry := range z.File {
		if len(z.File) > MaxOfficeEntries || entry.UncompressedSize64 > MaxOfficeExpanded || expanded > MaxOfficeExpanded-entry.UncompressedSize64 {
			return nil, fmt.Errorf("%s package exceeds inspection limit", extension)
		}
		expanded += entry.UncompressedSize64
	}
	return &DocumentSummary{Kind: extension, PackageEntries: len(z.File), ExpandedBytes: expanded}, nil
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
