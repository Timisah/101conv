// Package demo implements an optional, explicitly opt-in isolated public demo
// mode (FR-018). When DEMO_MODE is disabled (the default), every guard is a
// no-op and private self-hosted deployments behave identically to before.
//
// When enabled, this package enforces:
//   - an administrator-toggleable overload kill switch,
//   - global hourly compute budgets and active-job ceilings,
//   - per-file, per-batch, and profile allowlist bounds, and
//   - strictly aggregate, non-sensitive self-hosting funnel counters (no IPs,
//     no user agents, no filenames, and no session identifiers).
package demo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultMaxFileBytes         int64 = 15 << 20 // 15 MiB
	DefaultMaxBatchFiles        int   = 3
	DefaultMaxActiveJobs        int   = 4
	DefaultMaxGlobalJobsPerHour int   = 60
	DefaultMaxOutputBytes       int64 = 25 << 20 // 25 MiB
	DefaultRetentionMinutes     int   = 15
	DefaultRepoURL                    = "https://github.com/Timisah/101conv"
	DefaultNotice                     = "Public demonstration instance: strict size/rate limits apply and files are automatically deleted shortly after processing. Do not upload private or sensitive documents; self-host with Docker for full privacy."
)

// Funnel event types permitted for aggregate tracking. No free-form strings,
// filenames, or visitor identifiers are ever accepted or stored.
const (
	EventDemoVisit             = "demo_visit"
	EventDemoConversionStarted = "demo_conversion_started"
	EventDemoDownload          = "demo_download"
	EventCTAGitHubClick        = "cta_github_click"
	EventCTAInstallDocsClick   = "cta_install_docs_click"
	EventCTASelfHostCopy       = "cta_self_host_copy"
	EventBlockedOverload       = "demo_blocked_overload"
	EventBlockedKillSwitch     = "demo_blocked_kill_switch"
)

var allowedFunnelEvents = map[string]bool{
	EventDemoVisit:             true,
	EventDemoConversionStarted: true,
	EventDemoDownload:          true,
	EventCTAGitHubClick:        true,
	EventCTAInstallDocsClick:   true,
	EventCTASelfHostCopy:       true,
	EventBlockedOverload:       true,
	EventBlockedKillSwitch:     true,
}

// PublicFunnelEvents is the subset of events that browsers may submit through
// POST /api/v1/demo/funnel. Server-side events (submissions, downloads, blocks)
// are recorded directly by Core.
var PublicFunnelEvents = map[string]bool{
	EventDemoVisit:           true,
	EventCTAGitHubClick:      true,
	EventCTAInstallDocsClick: true,
	EventCTASelfHostCopy:     true,
}

type Policy struct {
	Enabled              bool
	KillSwitchActive     bool
	KillSwitchReason     string
	MaxFileBytes         int64
	MaxBatchFiles        int
	MaxActiveJobs        int
	MaxGlobalJobsPerHour int
	MaxOutputBytes       int64
	RetentionMinutes     int
	AllowedProfiles      []string
	RepoURL              string
	PrivacyURL           string
	TermsURL             string
	Notice               string
}

type Status struct {
	Enabled              bool     `json:"enabled"`
	KillSwitchActive     bool     `json:"killSwitchActive,omitempty"`
	KillSwitchReason     string   `json:"killSwitchReason,omitempty"`
	MaxFileBytes         int64    `json:"maxFileBytes,omitempty"`
	MaxBatchFiles        int      `json:"maxBatchFiles,omitempty"`
	MaxActiveJobs        int      `json:"maxActiveJobs,omitempty"`
	MaxGlobalJobsPerHour int      `json:"maxGlobalJobsPerHour,omitempty"`
	MaxOutputBytes       int64    `json:"maxOutputBytes,omitempty"`
	RetentionMinutes     int      `json:"retentionMinutes,omitempty"`
	AllowedProfiles      []string `json:"allowedProfiles,omitempty"`
	RepoURL              string   `json:"repoUrl,omitempty"`
	PrivacyURL           string   `json:"privacyUrl,omitempty"`
	TermsURL             string   `json:"termsUrl,omitempty"`
	Notice               string   `json:"notice,omitempty"`
}

type DailyCounts struct {
	Date   string           `json:"date"`
	Counts map[string]int64 `json:"counts"`
}

type MetricsSummary struct {
	Enabled                 bool             `json:"enabled"`
	Days                    int              `json:"days"`
	KillSwitchActive        bool             `json:"killSwitchActive"`
	KillSwitchReason        string           `json:"killSwitchReason,omitempty"`
	JobsUsedLastHour        int              `json:"jobsUsedLastHour"`
	MaxGlobalJobsPerHour    int              `json:"maxGlobalJobsPerHour"`
	Totals                  map[string]int64 `json:"totals"`
	Daily                   []DailyCounts    `json:"daily"`
	GitHubCTRPercent        float64          `json:"githubCtrPercent"`
	InstallDocsCTRPercent   float64          `json:"installDocsCtrPercent"`
	SelfHostCopyRatePercent float64          `json:"selfHostCopyRatePercent"`
}

type RejectionError struct {
	Code              string
	Message           string
	HTTPStatus        int
	RetryAfterSeconds int
}

func (e *RejectionError) Error() string { return e.Message }

type Service struct {
	mu           sync.RWMutex
	policy       Policy
	allowedSet   map[string]bool
	db           *sql.DB
	hourlyWindow []time.Time
	memCounts    map[string]map[string]int64
	now          func() time.Time
}

func PolicyFromEnv() Policy {
	p := Policy{
		Enabled:              parseBoolEnv("DEMO_MODE", false),
		KillSwitchActive:     parseBoolEnv("DEMO_KILL_SWITCH", false),
		KillSwitchReason:     strings.TrimSpace(os.Getenv("DEMO_KILL_SWITCH_REASON")),
		MaxFileBytes:         parseInt64Env("DEMO_MAX_FILE_BYTES", DefaultMaxFileBytes),
		MaxBatchFiles:        parseIntEnv("DEMO_MAX_BATCH_FILES", DefaultMaxBatchFiles),
		MaxActiveJobs:        parseIntEnv("DEMO_MAX_ACTIVE_JOBS", DefaultMaxActiveJobs),
		MaxGlobalJobsPerHour: parseIntEnv("DEMO_MAX_GLOBAL_JOBS_PER_HOUR", DefaultMaxGlobalJobsPerHour),
		MaxOutputBytes:       parseInt64Env("DEMO_MAX_OUTPUT_BYTES", DefaultMaxOutputBytes),
		RetentionMinutes:     parseIntEnv("DEMO_RETENTION_MINUTES", DefaultRetentionMinutes),
		AllowedProfiles:      parseCSVEnv("DEMO_ALLOWED_PROFILES"),
		RepoURL:              sanitizePublicURL(os.Getenv("DEMO_REPO_URL"), DefaultRepoURL),
		PrivacyURL:           sanitizePublicURL(os.Getenv("DEMO_PRIVACY_URL"), ""),
		TermsURL:             sanitizePublicURL(os.Getenv("DEMO_TERMS_URL"), ""),
		Notice:               strings.TrimSpace(os.Getenv("DEMO_NOTICE")),
	}
	return normalizePolicy(p)
}

func normalizePolicy(p Policy) Policy {
	if p.MaxFileBytes <= 0 {
		p.MaxFileBytes = DefaultMaxFileBytes
	}
	if p.MaxBatchFiles <= 0 {
		p.MaxBatchFiles = DefaultMaxBatchFiles
	}
	if p.MaxActiveJobs <= 0 {
		p.MaxActiveJobs = DefaultMaxActiveJobs
	}
	if p.MaxGlobalJobsPerHour <= 0 {
		p.MaxGlobalJobsPerHour = DefaultMaxGlobalJobsPerHour
	}
	if p.MaxOutputBytes <= 0 {
		p.MaxOutputBytes = DefaultMaxOutputBytes
	}
	if p.RetentionMinutes <= 0 {
		p.RetentionMinutes = DefaultRetentionMinutes
	}
	if p.RepoURL == "" {
		p.RepoURL = DefaultRepoURL
	}
	if p.Notice == "" {
		p.Notice = DefaultNotice
	}
	if len(p.KillSwitchReason) > 200 {
		p.KillSwitchReason = p.KillSwitchReason[:200]
	}
	if len(p.Notice) > 500 {
		p.Notice = p.Notice[:500]
	}
	cleaned := make([]string, 0, len(p.AllowedProfiles))
	seen := make(map[string]bool, len(p.AllowedProfiles))
	for _, id := range p.AllowedProfiles {
		id = strings.TrimSpace(id)
		if id != "" && !seen[id] {
			seen[id] = true
			cleaned = append(cleaned, id)
		}
	}
	sort.Strings(cleaned)
	p.AllowedProfiles = cleaned
	return p
}

func NewService(policy Policy, db *sql.DB) *Service {
	p := normalizePolicy(policy)
	allowed := make(map[string]bool, len(p.AllowedProfiles))
	for _, id := range p.AllowedProfiles {
		allowed[id] = true
	}
	return &Service{
		policy:     p,
		allowedSet: allowed,
		db:         db,
		memCounts:  make(map[string]map[string]int64),
		now:        time.Now,
	}
}

func (s *Service) Migrate(ctx context.Context) error {
	if s == nil || s.db == nil {
		return nil
	}
	const schema = `
CREATE TABLE IF NOT EXISTS conv_demo_funnel_events (
 event_date DATE NOT NULL,
 event_type TEXT NOT NULL,
 event_count BIGINT NOT NULL DEFAULT 0,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY (event_date, event_type)
);
CREATE INDEX IF NOT EXISTS conv_demo_funnel_events_date_idx ON conv_demo_funnel_events(event_date DESC);
`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func (s *Service) Enabled() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.policy.Enabled
}

func (s *Service) Status() Status {
	if s == nil {
		return Status{Enabled: false}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.policy.Enabled {
		return Status{Enabled: false}
	}
	profiles := append([]string(nil), s.policy.AllowedProfiles...)
	return Status{
		Enabled:              true,
		KillSwitchActive:     s.policy.KillSwitchActive,
		KillSwitchReason:     s.policy.KillSwitchReason,
		MaxFileBytes:         s.policy.MaxFileBytes,
		MaxBatchFiles:        s.policy.MaxBatchFiles,
		MaxActiveJobs:        s.policy.MaxActiveJobs,
		MaxGlobalJobsPerHour: s.policy.MaxGlobalJobsPerHour,
		MaxOutputBytes:       s.policy.MaxOutputBytes,
		RetentionMinutes:     s.policy.RetentionMinutes,
		AllowedProfiles:      profiles,
		RepoURL:              s.policy.RepoURL,
		PrivacyURL:           s.policy.PrivacyURL,
		TermsURL:             s.policy.TermsURL,
		Notice:               s.policy.Notice,
	}
}

func (s *Service) SetKillSwitch(active bool, reason string) Status {
	s.mu.Lock()
	s.policy.KillSwitchActive = active
	reason = strings.TrimSpace(reason)
	if len(reason) > 200 {
		reason = reason[:200]
	}
	if !active {
		reason = ""
	}
	s.policy.KillSwitchReason = reason
	s.mu.Unlock()
	return s.Status()
}

func (s *Service) MaxFileBytes() int64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.policy.Enabled {
		return 0
	}
	return s.policy.MaxFileBytes
}

func (s *Service) CheckUpload(ctx context.Context, contentLength int64) error {
	if !s.Enabled() {
		return nil
	}
	s.mu.RLock()
	killActive := s.policy.KillSwitchActive
	killReason := s.policy.KillSwitchReason
	maxBytes := s.policy.MaxFileBytes
	s.mu.RUnlock()

	if killActive {
		_ = s.RecordEvent(ctx, EventBlockedKillSwitch)
		msg := "the public demo is temporarily paused to protect host capacity; self-host with Docker for unlimited local use"
		if killReason != "" {
			msg = fmt.Sprintf("the public demo is temporarily paused (%s)", killReason)
		}
		return &RejectionError{
			Code:              "demo_kill_switch_active",
			Message:           msg,
			HTTPStatus:        http.StatusServiceUnavailable,
			RetryAfterSeconds: 300,
		}
	}
	if contentLength > 0 && contentLength > maxBytes+(1<<20) {
		return &RejectionError{
			Code:       "demo_file_limit_exceeded",
			Message:    fmt.Sprintf("public demo uploads are limited to %d MiB per file; self-host with Docker for larger files", maxBytes>>20),
			HTTPStatus: http.StatusRequestEntityTooLarge,
		}
	}
	return nil
}

func (s *Service) CheckSubmission(ctx context.Context, activeJobs int, profileID string, fileCount int) error {
	if !s.Enabled() {
		return nil
	}
	if fileCount <= 0 {
		fileCount = 1
	}

	s.mu.Lock()
	killActive := s.policy.KillSwitchActive
	killReason := s.policy.KillSwitchReason
	maxBatch := s.policy.MaxBatchFiles
	maxActive := s.policy.MaxActiveJobs
	maxHourly := s.policy.MaxGlobalJobsPerHour
	hasAllowlist := len(s.allowedSet) > 0
	profileAllowed := !hasAllowlist || profileID == "" || s.allowedSet[profileID]
	s.pruneWindowLocked()
	usedLastHour := len(s.hourlyWindow)
	s.mu.Unlock()

	if killActive {
		_ = s.RecordEvent(ctx, EventBlockedKillSwitch)
		msg := "the public demo is temporarily paused to protect host capacity; self-host with Docker for unlimited local use"
		if killReason != "" {
			msg = fmt.Sprintf("the public demo is temporarily paused (%s)", killReason)
		}
		return &RejectionError{
			Code:              "demo_kill_switch_active",
			Message:           msg,
			HTTPStatus:        http.StatusServiceUnavailable,
			RetryAfterSeconds: 300,
		}
	}
	if fileCount > maxBatch {
		return &RejectionError{
			Code:       "demo_batch_limit_exceeded",
			Message:    fmt.Sprintf("public demo batches are limited to %d files at a time; self-host with Docker for larger batches", maxBatch),
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if !profileAllowed {
		return &RejectionError{
			Code:       "demo_profile_not_allowed",
			Message:    fmt.Sprintf("profile %q is not enabled on this public demo instance", profileID),
			HTTPStatus: http.StatusForbidden,
		}
	}
	if activeJobs >= maxActive {
		_ = s.RecordEvent(ctx, EventBlockedOverload)
		return &RejectionError{
			Code:              "demo_overload",
			Message:           "the public demo is currently at capacity; wait a moment and try again, or self-host with Docker",
			HTTPStatus:        http.StatusServiceUnavailable,
			RetryAfterSeconds: 30,
		}
	}
	if usedLastHour+fileCount > maxHourly {
		_ = s.RecordEvent(ctx, EventBlockedOverload)
		return &RejectionError{
			Code:              "demo_hourly_budget_exceeded",
			Message:           "the public demo hourly compute budget has been reached; try again later or self-host with Docker",
			HTTPStatus:        http.StatusTooManyRequests,
			RetryAfterSeconds: 300,
		}
	}
	return nil
}

func (s *Service) RecordSubmission(ctx context.Context, fileCount int) {
	if !s.Enabled() {
		return
	}
	if fileCount <= 0 {
		fileCount = 1
	}
	s.mu.Lock()
	now := s.now().UTC()
	s.pruneWindowLocked()
	for i := 0; i < fileCount; i++ {
		s.hourlyWindow = append(s.hourlyWindow, now)
	}
	s.mu.Unlock()
	_ = s.RecordEvent(ctx, EventDemoConversionStarted)
}

func (s *Service) pruneWindowLocked() {
	cutoff := s.now().UTC().Add(-time.Hour)
	idx := 0
	for idx < len(s.hourlyWindow) && s.hourlyWindow[idx].Before(cutoff) {
		idx++
	}
	if idx > 0 {
		s.hourlyWindow = append([]time.Time(nil), s.hourlyWindow[idx:]...)
	}
}

func (s *Service) RecordEvent(ctx context.Context, eventType string) error {
	if !s.Enabled() {
		return nil
	}
	if !allowedFunnelEvents[eventType] {
		return errors.New("unsupported demo funnel event")
	}
	day := s.now().UTC().Format("2006-01-02")
	s.mu.Lock()
	if s.memCounts[day] == nil {
		s.memCounts[day] = make(map[string]int64)
	}
	s.memCounts[day][eventType]++
	db := s.db
	s.mu.Unlock()

	if db == nil {
		return nil
	}
	_, err := db.ExecContext(ctx, `
INSERT INTO conv_demo_funnel_events (event_date, event_type, event_count, updated_at)
VALUES ($1::date, $2, 1, now())
ON CONFLICT (event_date, event_type)
DO UPDATE SET event_count = conv_demo_funnel_events.event_count + 1, updated_at = now()
`, day, eventType)
	return err
}

func (s *Service) Metrics(ctx context.Context, days int) (MetricsSummary, error) {
	if days <= 0 || days > 90 {
		days = 30
	}
	s.mu.Lock()
	s.pruneWindowLocked()
	usedLastHour := len(s.hourlyWindow)
	policy := s.policy
	db := s.db
	s.mu.Unlock()

	summary := MetricsSummary{
		Enabled:              policy.Enabled,
		Days:                 days,
		KillSwitchActive:     policy.KillSwitchActive,
		KillSwitchReason:     policy.KillSwitchReason,
		JobsUsedLastHour:     usedLastHour,
		MaxGlobalJobsPerHour: policy.MaxGlobalJobsPerHour,
		Totals:               make(map[string]int64),
		Daily:                []DailyCounts{},
	}

	byDay := make(map[string]map[string]int64)
	cutoff := s.now().UTC().AddDate(0, 0, -(days - 1)).Format("2006-01-02")

	if db != nil {
		rows, err := db.QueryContext(ctx, `
SELECT to_char(event_date, 'YYYY-MM-DD'), event_type, event_count
FROM conv_demo_funnel_events
WHERE event_date >= $1::date
ORDER BY event_date ASC, event_type ASC
`, cutoff)
		if err != nil {
			return summary, err
		}
		defer rows.Close()
		for rows.Next() {
			var d, ev string
			var c int64
			if err := rows.Scan(&d, &ev, &c); err != nil {
				return summary, err
			}
			if byDay[d] == nil {
				byDay[d] = make(map[string]int64)
			}
			byDay[d][ev] += c
			summary.Totals[ev] += c
		}
		if err := rows.Err(); err != nil {
			return summary, err
		}
	} else {
		s.mu.RLock()
		for d, events := range s.memCounts {
			if d < cutoff {
				continue
			}
			byDay[d] = make(map[string]int64, len(events))
			for ev, c := range events {
				byDay[d][ev] = c
				summary.Totals[ev] += c
			}
		}
		s.mu.RUnlock()
	}

	dates := make([]string, 0, len(byDay))
	for d := range byDay {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	for _, d := range dates {
		summary.Daily = append(summary.Daily, DailyCounts{Date: d, Counts: byDay[d]})
	}

	base := summary.Totals[EventDemoConversionStarted]
	if base == 0 {
		base = summary.Totals[EventDemoVisit]
	}
	if base > 0 {
		summary.GitHubCTRPercent = round2(float64(summary.Totals[EventCTAGitHubClick]) * 100 / float64(base))
		summary.InstallDocsCTRPercent = round2(float64(summary.Totals[EventCTAInstallDocsClick]) * 100 / float64(base))
		summary.SelfHostCopyRatePercent = round2(float64(summary.Totals[EventCTASelfHostCopy]) * 100 / float64(base))
	}
	return summary, nil
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

func parseBoolEnv(key string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func parseIntEnv(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func parseInt64Env(key string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func parseCSVEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func sanitizePublicURL(raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fallback
	}
	return u.String()
}
