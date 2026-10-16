package repositorymanager

import (
	"context"
	"crypto/tls"
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
