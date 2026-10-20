// Package updatecheck checks the public 101conv release feed without granting
// the web client direct access to GitHub. Results are cached so an instance
// makes at most one request per configured interval.
package updatecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/timisah/101conv/server/internal/version"
)

const (
	DefaultURL      = "https://api.github.com/repos/Timisah/101conv/releases/latest"
	DefaultInterval = 12 * time.Hour
)

type Status struct {
	CurrentVersion  string    `json:"currentVersion"`
	LatestVersion   string    `json:"latestVersion,omitempty"`
	UpdateAvailable bool      `json:"updateAvailable"`
	ReleaseName     string    `json:"releaseName,omitempty"`
	ReleaseURL      string    `json:"releaseUrl,omitempty"`
	ReleaseNotes    string    `json:"releaseNotes,omitempty"`
	PublishedAt     string    `json:"publishedAt,omitempty"`
	CheckedAt       time.Time `json:"checkedAt"`
	Status          string    `json:"status"`
	Error           string    `json:"error,omitempty"`
}

type Checker struct {
	currentVersion string
	endpoint       string
	client         *http.Client
	interval       time.Duration

	mu        sync.Mutex
	checkedAt time.Time
	cached    Status
}

type releaseResponse struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	HTMLURL     string `json:"html_url"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
}

func New(currentVersion, endpoint string) (*Checker, error) {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultURL
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return nil, fmt.Errorf("update check URL must be an HTTPS URL without credentials")
	}
	if _, err = version.Parse(currentVersion); err != nil {
		return nil, fmt.Errorf("invalid current application version: %w", err)
	}
	return &Checker{
		currentVersion: strings.TrimSpace(currentVersion),
		endpoint:       endpoint,
		client:         &http.Client{Timeout: 10 * time.Second},
		interval:       DefaultInterval,
	}, nil
}

func (c *Checker) Check(ctx context.Context) Status {
	now := time.Now().UTC()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.checkedAt.IsZero() && now.Sub(c.checkedAt) < c.interval {
		return c.cached
	}
	result := c.fetch(ctx, now)
	c.checkedAt = now
	c.cached = result
	return result
}

func (c *Checker) fetch(ctx context.Context, checkedAt time.Time) Status {
	result := Status{CurrentVersion: c.currentVersion, CheckedAt: checkedAt, Status: "unavailable"}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		result.Error = "The release feed could not be requested."
		return result
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "101conv/"+c.currentVersion)
	response, err := c.client.Do(req)
	if err != nil {
		result.Error = "The release feed is temporarily unavailable."
		return result
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		result.Error = "No published release is available yet."
		return result
	}
	if response.StatusCode != http.StatusOK {
		result.Error = "The release feed returned an unexpected response."
		return result
	}
	var release releaseResponse
	if err = json.NewDecoder(io.LimitReader(response.Body, 256<<10)).Decode(&release); err != nil {
		result.Error = "The release feed returned invalid metadata."
		return result
	}
	if release.Draft || release.Prerelease {
		result.Error = "No stable published release is available yet."
		return result
	}
	latest := strings.TrimPrefix(strings.TrimSpace(release.TagName), "v")
	if _, err = version.Parse(latest); err != nil {
		result.Error = "The latest release has unsupported version metadata."
		return result
	}
	comparison, err := version.Compare(c.currentVersion, latest)
	if err != nil {
		result.Error = "The running version could not be compared with the release."
		return result
	}
	result.LatestVersion = latest
	result.ReleaseName = strings.TrimSpace(release.Name)
	result.ReleaseURL = safeReleaseURL(release.HTMLURL)
	result.ReleaseNotes = strings.TrimSpace(release.Body)
	result.PublishedAt = strings.TrimSpace(release.PublishedAt)
	result.UpdateAvailable = comparison < 0
	result.Status = "current"
	if result.UpdateAvailable {
		result.Status = "update_available"
	}
	return result
}

func safeReleaseURL(value string) string {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return ""
	}
	return u.String()
}
