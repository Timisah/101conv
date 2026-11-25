// Package immich implements a bounded, secret-safe client for documented
// Immich REST APIs. It supports server version/health discovery, read-only
// asset listing, bounded original download, and new-asset upload. It never
// claims in-place ID-preserving asset replacement or complete preservation of
// Immich albums, faces, tags, or proprietary metadata.
package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	HonestDisclosure = "Immich integration uses documented REST APIs to read originals and upload processed files as new assets. In-place ID-preserving replacement and complete preservation of Immich albums, faces, tags, or sidecar relationships are not guaranteed."
	maxMetadataBytes = 2 << 20 // 2 MiB max for JSON metadata responses
)

var safeAssetID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type ServerAbout struct {
	Version string `json:"version"`
}

type Asset struct {
	ID               string `json:"id"`
	OriginalFileName string `json:"originalFileName"`
	Type             string `json:"type"`
	MimeType         string `json:"originalMimeType,omitempty"`
	FileCreatedAt    string `json:"fileCreatedAt,omitempty"`
}

type UploadResult struct {
	ID         string `json:"id"`
	Status     string `json:"status,omitempty"`
	Duplicate  bool   `json:"duplicate,omitempty"`
	Disclosure string `json:"disclosure"`
}

// ValidateServerURL enforces http/https schemes, rejects embedded credentials,
// and blocks cloud instance metadata endpoints.
func ValidateServerURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", errors.New("Immich server URL is required")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid Immich server URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("Immich server URL must use http or https")
	}
	if u.Host == "" {
		return "", errors.New("Immich server URL must include a host")
	}
	if u.User != nil {
		return "", errors.New("Immich server URL must not contain embedded credentials")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil && ip.IsLinkLocalUnicast() {
		return "", errors.New("link-local metadata addresses are not allowed")
	}
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewClient(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	cleanURL, err := ValidateServerURL(baseURL)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("Immich API key is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{baseURL: cleanURL, apiKey: strings.TrimSpace(apiKey), http: httpClient}, nil
}

func (c *Client) redact(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if c.apiKey != "" && strings.Contains(msg, c.apiKey) {
		msg = strings.ReplaceAll(msg, c.apiKey, "[redacted]")
	}
	return errors.New(msg)
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, c.redact(err)
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// About calls GET /api/server/about to verify connectivity and retrieve the
// Immich server semver version.
func (c *Client) About(ctx context.Context) (ServerAbout, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/api/server/about", nil)
	if err != nil {
		return ServerAbout{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ServerAbout{}, c.redact(fmt.Errorf("connect to Immich: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ServerAbout{}, fmt.Errorf("Immich /api/server/about returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return ServerAbout{}, c.redact(err)
	}
	var payload struct {
		Version string `json:"version"`
		Major   int    `json:"major"`
		Minor   int    `json:"minor"`
		Patch   int    `json:"patch"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ServerAbout{}, fmt.Errorf("decode Immich server about response: %w", err)
	}
	ver := strings.TrimPrefix(strings.TrimSpace(payload.Version), "v")
	if ver == "" && (payload.Major > 0 || payload.Minor > 0 || payload.Patch > 0) {
		ver = fmt.Sprintf("%d.%d.%d", payload.Major, payload.Minor, payload.Patch)
	}
	return ServerAbout{Version: ver}, nil
}

// ListAssets queries POST /api/search/metadata for a bounded list of assets.
func (c *Client) ListAssets(ctx context.Context, limit int) ([]Asset, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	body, _ := json.Marshal(map[string]any{"size": limit, "page": 1})
	req, err := c.newRequest(ctx, http.MethodPost, "/api/search/metadata", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, c.redact(fmt.Errorf("query Immich assets: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Immich /api/search/metadata returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes))
	if err != nil {
		return nil, c.redact(err)
	}
	var parsed struct {
		Assets struct {
			Items []Asset `json:"items"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode Immich asset list: %w", err)
	}
	if parsed.Assets.Items == nil {
		return []Asset{}, nil
	}
	return parsed.Assets.Items, nil
}

// DownloadOriginal streams GET /api/assets/{id}/original with a strict byte
// limit so an external response cannot exhaust server memory.
func (c *Client) DownloadOriginal(ctx context.Context, assetID string, maxBytes int64) ([]byte, string, error) {
	if !safeAssetID.MatchString(assetID) {
		return nil, "", errors.New("invalid Immich asset ID")
	}
	if maxBytes <= 0 {
		maxBytes = 100 << 20
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/api/assets/"+assetID+"/original", nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "*/*")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", c.redact(fmt.Errorf("download Immich asset: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("Immich asset download returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", c.redact(err)
	}
	if int64(len(data)) > maxBytes {
		return nil, "", fmt.Errorf("Immich asset exceeds maximum allowed import size (%d bytes)", maxBytes)
	}
	filename := assetID + ".bin"
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if idx := strings.Index(strings.ToLower(cd), "filename="); idx >= 0 {
			name := strings.Trim(strings.TrimSpace(cd[idx+len("filename="):]), `"'`)
			if clean := filepath.Base(name); clean != "" && clean != "." && clean != "/" {
				filename = clean
			}
		}
	}
	return data, filename, nil
}

// UploadAsset submits a processed file to Immich as a new asset using the
// documented POST /api/assets multipart endpoint.
func (c *Client) UploadAsset(ctx context.Context, filename string, content []byte, deviceAssetID string, createdAt time.Time) (UploadResult, error) {
	if len(content) == 0 {
		return UploadResult{}, errors.New("cannot upload empty asset")
	}
	cleanName := filepath.Base(strings.TrimSpace(filename))
	if cleanName == "" || cleanName == "." || cleanName == "/" {
		cleanName = "converted.bin"
	}
	if deviceAssetID == "" {
		deviceAssetID = fmt.Sprintf("101conv-%d", time.Now().UnixNano())
	}
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("deviceAssetId", deviceAssetID)
	_ = writer.WriteField("deviceId", "101conv")
	_ = writer.WriteField("fileCreatedAt", createdAt.UTC().Format(time.RFC3339))
	_ = writer.WriteField("fileModifiedAt", createdAt.UTC().Format(time.RFC3339))
	part, err := writer.CreateFormFile("assetData", cleanName)
	if err != nil {
		return UploadResult{}, err
	}
	if _, err := part.Write(content); err != nil {
		return UploadResult{}, err
	}
	if err := writer.Close(); err != nil {
		return UploadResult{}, err
	}

	req, err := c.newRequest(ctx, http.MethodPost, "/api/assets", &buf)
	if err != nil {
		return UploadResult{}, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		return UploadResult{}, c.redact(fmt.Errorf("upload asset to Immich: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return UploadResult{}, fmt.Errorf("Immich /api/assets upload returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return UploadResult{}, c.redact(err)
	}
	var res UploadResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return UploadResult{}, fmt.Errorf("decode Immich upload response: %w", err)
	}
	res.Disclosure = HonestDisclosure
	return res, nil
}
