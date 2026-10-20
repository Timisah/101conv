package repositorymanager

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/config"
	"github.com/theupdateframework/go-tuf/v2/metadata/fetcher"
	"github.com/theupdateframework/go-tuf/v2/metadata/updater"
	"github.com/timisah/101conv/server/internal/engine"
)

type localFetcher struct{ root string }

var secureRepositoryHTTPClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many redirects")
		}
		if len(via) == 0 || via[0].URL.Scheme != "https" || req.URL.Scheme != "https" || !strings.EqualFold(via[0].URL.Host, req.URL.Host) {
			return fmt.Errorf("TUF repository redirect cannot change HTTPS origin")
		}
		return nil
	},
}

var secureArtifactHTTPClient = &http.Client{
	Timeout:   30 * time.Minute,
	Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || len(via) == 0 || via[0].URL.Scheme != "https" || via[0].URL.Hostname() != "github.com" || req.URL.Scheme != "https" {
			return fmt.Errorf("upstream release redirect is not allowed")
		}
		switch strings.ToLower(req.URL.Hostname()) {
		case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com", "github-cloud.s3.amazonaws.com":
			return nil
		default:
			return fmt.Errorf("upstream release redirected to an unapproved host")
		}
	},
}

func validateExternalArtifactURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "github.com") || u.Port() != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("external artifact URL must be a plain HTTPS GitHub release URL")
	}
	return nil
}

func targetExternalURL(info *metadata.TargetFiles) (string, error) {
	if info.Custom == nil {
		return "", fmt.Errorf("TUF artifact target is missing its signed upstream URL")
	}
	var custom struct {
		DownloadURL string `json:"downloadUrl"`
	}
	if err := json.Unmarshal(*info.Custom, &custom); err != nil {
		return "", err
	}
	if err := validateExternalArtifactURL(custom.DownloadURL); err != nil {
		return "", err
	}
	return custom.DownloadURL, nil
}

func downloadPackageVerified(ctx context.Context, up *updater.Updater, artifact *engine.Artifact, max int64) ([]byte, error) {
	if artifact == nil || !safeTarget(artifact.URL) {
		return nil, fmt.Errorf("unsafe package target")
	}
	if artifact.DownloadURL == "" {
		return downloadVerified(up, artifact.URL, max)
	}
	if err := validateExternalArtifactURL(artifact.DownloadURL); err != nil {
		return nil, err
	}
	info, err := up.GetTargetInfo(artifact.URL)
	if err != nil {
		return nil, classifyTUFError(err)
	}
	if info.Length < 0 || info.Length > max {
		return nil, fmt.Errorf("artifact exceeds package size limit")
	}
	urlFromMetadata, err := targetExternalURL(info)
	if err != nil {
		return nil, err
	}
	if urlFromMetadata != artifact.DownloadURL {
		return nil, fmt.Errorf("artifact upstream URL differs from signed TUF target metadata")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, urlFromMetadata, nil)
	if err != nil {
		return nil, err
	}
	response, err := secureArtifactHTTPClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download upstream release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream release returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > max || response.ContentLength >= 0 && response.ContentLength != info.Length {
		return nil, fmt.Errorf("upstream release length differs from signed TUF target")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, info.Length+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != info.Length || int64(len(data)) > max {
		return nil, fmt.Errorf("downloaded package length differs from signed TUF target")
	}
	actual := sha256.Sum256(data)
	signed := info.Hashes["sha256"]
	if len(signed) == 0 || !strings.EqualFold(hex.EncodeToString(actual[:]), hex.EncodeToString(signed)) {
		return nil, coded("signature_failure", fmt.Errorf("downloaded package SHA-256 differs from signed TUF target"))
	}
	return data, nil
}

// downloadPackageUnsigned is only available after an administrator explicitly
// approves unsigned installation from the built-in GitHub catalog. It limits
// the request to the same GitHub release hosts, enforces the manifest size and
// SHA-256, and relies on the existing safe archive extractor. These checks
// detect corruption but do not authenticate the publisher.
func downloadPackageUnsigned(ctx context.Context, artifact *engine.Artifact, max int64) ([]byte, error) {
	if artifact == nil {
		return nil, fmt.Errorf("unsigned official engine manifest has no artifact")
	}
	// Small adapters can be shipped as same-origin catalog targets. Keep their
	// download inside the built-in official repository, with no origin-changing
	// redirects; external releases retain the existing GitHub allowlist.
	downloadURL := artifact.DownloadURL
	client := secureArtifactHTTPClient
	if downloadURL == "" {
		if !safeTarget(artifact.URL) || strings.ContainsAny(artifact.URL, "%?#:") {
			return nil, fmt.Errorf("unsafe official package target")
		}
		downloadURL = officialURL + "/targets/" + artifact.URL
		client = secureRepositoryHTTPClient
	} else if err := validateExternalArtifactURL(downloadURL); err != nil {
		return nil, err
	}
	if artifact.SizeBytes <= 0 || artifact.SizeBytes > max {
		return nil, fmt.Errorf("unsigned package has invalid or oversized declared length")
	}
	if len(artifact.SHA256) != sha256.Size*2 {
		return nil, fmt.Errorf("unsigned package manifest has invalid SHA-256")
	}
	if _, err := hex.DecodeString(artifact.SHA256); err != nil {
		return nil, fmt.Errorf("unsigned package manifest has invalid SHA-256: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("download unsigned GitHub release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream release returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != artifact.SizeBytes {
		return nil, fmt.Errorf("unsigned package length differs from its catalog manifest")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, artifact.SizeBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != artifact.SizeBytes {
		return nil, fmt.Errorf("unsigned package length differs from its catalog manifest")
	}
	actual := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), artifact.SHA256) {
		return nil, fmt.Errorf("unsigned package SHA-256 differs from its catalog manifest")
	}
	return data, nil
}

func (f localFetcher) DownloadFile(raw string, max int64, _ time.Duration) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "file" {
		return nil, fmt.Errorf("local repository attempted a non-file URL")
	}
	if u.Host != "" {
		return nil, fmt.Errorf("local repository URL must not specify a host")
	}
	p, err := filepath.Abs(filepath.Join(u.Host, u.Path))
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(f.root, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("repository path escapes its configured directory")
	}
	current := f.root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, e := os.Lstat(current)
		if e != nil {
			if os.IsNotExist(e) {
				return nil, &metadata.ErrDownloadHTTP{StatusCode: http.StatusNotFound, URL: raw}
			}
			return nil, e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("local repository contains a symbolic link")
		}
	}
	file, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &metadata.ErrDownloadHTTP{StatusCode: http.StatusNotFound, URL: raw}
		}
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("repository file exceeds metadata size limit")
	}
	return data, nil
}

func newUpdater(ctx context.Context, source Source, root []byte, cache string) (*updater.Updater, error) {
	parsedSource, err := url.Parse(source.URL)
	if err != nil {
		return nil, err
	}
	if source.Kind == KindCommunity && (parsedSource.Scheme != "https" || parsedSource.Host == "") {
		return nil, fmt.Errorf("community repository must use HTTPS")
	}
	if source.Kind == KindLocal && (parsedSource.Scheme != "file" || parsedSource.Host != "") {
		return nil, fmt.Errorf("local repository must use a hostless file URL")
	}
	metadataURL := strings.TrimRight(source.URL, "/") + "/metadata"
	cfg, err := config.New(metadataURL, root)
	if err != nil {
		return nil, err
	}
	cfg.LocalMetadataDir = filepath.Join(cache, "metadata")
	cfg.LocalTargetsDir = filepath.Join(cache, "targets")
	cfg.RemoteTargetsURL = strings.TrimRight(source.URL, "/") + "/targets"
	cfg.PrefixTargetsWithHash = true
	if source.Kind == KindLocal {
		base, err := filepath.Abs(filepath.Join(parsedSource.Host, parsedSource.Path))
		if err != nil {
			return nil, err
		}
		cfg.Fetcher = localFetcher{root: base}
	}
	if source.Kind != KindLocal {
		if err = cfg.SetDefaultFetcherHTTPClient(secureRepositoryHTTPClient); err != nil {
			return nil, err
		}
	}
	_ = ctx
	// Reuse the latest locally verified root from the restricted TUF cache so
	// sequential root rotations and root-version rollback checks survive restarts.
	if cached, readErr := os.ReadFile(filepath.Join(cfg.LocalMetadataDir, "root.json")); readErr == nil {
		root = cached
	} else if !os.IsNotExist(readErr) {
		return nil, readErr
	}
	cfg.LocalTrustedRoot = root
	return updater.New(cfg)
}

var _ fetcher.Fetcher = localFetcher{}
