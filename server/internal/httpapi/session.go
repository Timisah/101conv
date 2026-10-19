package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/timisah/101conv/server/internal/auth"
	"github.com/timisah/101conv/server/internal/management"
)

const (
	adminCookie = "101conv_admin"
	guestCookie = "101conv_guest"
)

type sessionStore struct {
	mu       sync.Mutex
	sessions map[[32]byte]sessionRecord
	ttl      time.Duration
}

type sessionRecord struct {
	expires   time.Time
	principal auth.Principal
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[[32]byte]sessionRecord), ttl: time.Duration(management.DefaultSessionTimeoutMinutes) * time.Minute}
}

func (s *sessionStore) setTTL(ttl time.Duration) { s.mu.Lock(); s.ttl = ttl; s.mu.Unlock() }

func (s *sessionStore) create(principal auth.Principal) (string, time.Time, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", time.Time{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expires := time.Now().Add(s.ttl)
	for digest, session := range s.sessions {
		if time.Now().After(session.expires) {
			delete(s.sessions, digest)
		}
	}
	s.sessions[sha256.Sum256(token[:])] = sessionRecord{expires: expires, principal: principal}
	return base64.RawURLEncoding.EncodeToString(token[:]), expires, nil
}

func (s *sessionStore) valid(r *http.Request) bool {
	_, ok := s.principal(r)
	return ok
}

func (s *sessionStore) principal(r *http.Request) (auth.Principal, bool) {
	cookie, err := r.Cookie(adminCookie)
	if err != nil {
		return auth.Principal{}, false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(decoded) != 32 {
		return auth.Principal{}, false
	}
	digest := sha256.Sum256(decoded)
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[digest]
	if !ok {
		return auth.Principal{}, false
	}
	if time.Now().After(session.expires) {
		delete(s.sessions, digest)
		return auth.Principal{}, false
	}
	return session.principal, true
}

func (s *sessionStore) revoke(r *http.Request) {
	cookie, err := r.Cookie(adminCookie)
	if err != nil {
		return
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(decoded) != 32 {
		return
	}
	digest := sha256.Sum256(decoded)
	s.mu.Lock()
	delete(s.sessions, digest)
	s.mu.Unlock()
}

func (a *API) createSession(w http.ResponseWriter, r *http.Request) {
	if a.management == nil || a.adminToken == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "management_disabled", "administrator authentication is not configured")
		return
	}
	finish, allowed := a.loginGuard.begin(r)
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(loginAttemptWindow.Seconds())))
		writeAPIError(w, http.StatusTooManyRequests, "login_rate_limited", "too many sign-in attempts; wait before trying again")
		return
	}
	success := false
	defer func() { finish(success) }()
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	principal := auth.AdminPrincipal()
	if strings.TrimSpace(req.Username) != "" {
		if a.accounts == nil {
			writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "username or password was not accepted")
			return
		}
		var err error
		principal, err = a.accounts.AuthenticatePassword(r.Context(), req.Username, req.Password)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrDisabled) {
				writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "username or password was not accepted")
			} else {
				writeAPIError(w, http.StatusInternalServerError, "authentication_error", "could not authenticate account")
			}
			return
		}
	} else if len(req.Password) != len(a.adminToken) || subtle.ConstantTimeCompare([]byte(req.Password), []byte(a.adminToken)) != 1 {
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "the administrator passphrase was not accepted")
		return
	}
	token, expires, err := a.sessions.create(principal)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "session_error", "could not create an administrator session")
		return
	}
	success = true
	if c, err := r.Cookie(guestCookie); err == nil {
		if a.accounts != nil {
			_ = a.accounts.RevokeGuestSession(r.Context(), c.Value)
		}
		http.SetCookie(w, &http.Cookie{Name: guestCookie, Value: "", Path: "/api/v1", MaxAge: -1, HttpOnly: true, Secure: requestIsSecure(r), SameSite: http.SameSiteStrictMode})
	}
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: token, Path: "/api/v1", Expires: expires, MaxAge: int(time.Until(expires).Seconds()),
		HttpOnly: true, Secure: requestIsSecure(r), SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"authenticated": true, "username": principal.Username, "role": principal.Role, "canConvert": principal.CanConvert, "canCompress": principal.Permissions[auth.PermissionCompression], "canReadJobs": principal.CanReadJobs, "permissions": principal.Permissions, "expiresAt": expires.UTC()})
}

func (a *API) getSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, _, ok := a.identityWithMethod(r)
	if !ok {
		result := map[string]any{"authenticated": false}
		if a.accounts != nil {
			if settings, err := a.accounts.AnonymousSettings(r.Context()); err == nil {
				setup, _ := a.accounts.InitialSetupStatus(r.Context())
				result["anonymousEnabled"] = settings.Enabled && setup.Completed
				result["guestResultExpirationHours"] = settings.ResultExpirationHours
				result["guestMaxBatchFiles"] = settings.MaxBatchFiles
			}
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	result := map[string]any{"authenticated": true, "username": p.Username, "role": p.Role, "canConvert": p.CanConvert, "canCompress": p.Permissions[auth.PermissionCompression], "canReadJobs": p.CanReadJobs, "permissions": p.Permissions, "guest": p.Guest}
	if p.Guest && a.accounts != nil {
		if settings, err := a.accounts.AnonymousSettings(r.Context()); err == nil {
			result["guestResultExpirationHours"] = settings.ResultExpirationHours
			result["guestMaxBatchFiles"] = settings.MaxBatchFiles
		}
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) createGuestSession(w http.ResponseWriter, r *http.Request) {
	if a.accounts == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "guest_unavailable", "guest access is not configured")
		return
	}
	if !guestSessionRequest(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
		return
	}
	p, token, expires, settings, err := a.accounts.CreateGuestSession(r.Context())
	if err != nil {
		if errors.Is(err, auth.ErrRateLimited) {
			w.Header().Set("Retry-After", "60")
			writeAPIError(w, http.StatusTooManyRequests, "guest_rate_limited", "too many guest sessions were started; try again in a minute")
			return
		}
		if errors.Is(err, auth.ErrDisabled) {
			writeAPIError(w, http.StatusForbidden, "guest_disabled", "anonymous conversions are not enabled")
		} else {
			writeAPIError(w, http.StatusInternalServerError, "guest_session_error", "could not start a guest session")
		}
		return
	}
	http.SetCookie(w, &http.Cookie{Name: guestCookie, Value: token, Path: "/api/v1", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, Secure: requestIsSecure(r), SameSite: http.SameSiteStrictMode})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"authenticated": true, "guest": true, "username": p.Username, "role": p.Role, "canConvert": p.CanConvert, "canCompress": settings.AllowCompression, "canReadJobs": true, "permissions": p.Permissions, "expiresAt": expires, "guestResultExpirationHours": settings.ResultExpirationHours, "guestMaxBatchFiles": settings.MaxBatchFiles})
}

func (a *API) deleteGuestSession(w http.ResponseWriter, r *http.Request) {
	if !guestSessionRequest(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
		return
	}
	if c, err := r.Cookie(guestCookie); err == nil && a.accounts != nil {
		if err := a.accounts.RevokeGuestSession(r.Context(), c.Value); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "guest_session_error", "could not end guest session")
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: guestCookie, Value: "", Path: "/api/v1", MaxAge: -1, HttpOnly: true, Secure: requestIsSecure(r), SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) getAnonymousSettings(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	if a.accounts == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "accounts_unavailable", "anonymous settings storage is unavailable")
		return
	}
	settings, err := a.accounts.AnonymousSettings(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "settings_error", "could not read anonymous access settings")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, settings)
}

func (a *API) updateAnonymousSettings(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	var settings auth.AnonymousSettings
	if !decodeBody(w, r, &settings) {
		return
	}
	if a.accounts == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "accounts_unavailable", "anonymous settings storage is unavailable")
		return
	}
	current, err := a.accounts.AnonymousSettings(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "settings_error", "could not read current anonymous access settings")
		return
	}
	retentionChanged := current.ResultExpirationHours != settings.ResultExpirationHours ||
		current.UploadExpirationHours != settings.UploadExpirationHours ||
		current.FailedExpirationHours != settings.FailedExpirationHours ||
		current.MetadataExpirationHours != settings.MetadataExpirationHours
	var unlock func()
	if a.jobs != nil && retentionChanged {
		unlock = a.jobs.LockGuestRetention()
		defer unlock()
	}
	if err := a.accounts.SetAnonymousSettingsSelective(r.Context(), settings, retentionChanged, current.SessionExpirationHours != settings.SessionExpirationHours); err != nil {
		if errors.Is(err, auth.ErrInvalidInput) {
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_anonymous_settings", "check the supported ranges; total guest storage must fit the largest input plus one maximum-size result")
		} else {
			writeAPIError(w, http.StatusInternalServerError, "settings_error", "could not save anonymous access settings")
		}
		return
	}
	if p, ok := a.identity(r); ok {
		if err := a.accounts.RecordAudit(r.Context(), p.ID, "anonymous.settings_updated", "instance", "1", map[string]any{"enabled": settings.Enabled, "resultExpirationHours": settings.ResultExpirationHours, "sessionExpirationHours": settings.SessionExpirationHours}); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "audit_error", "settings were saved but the audit record could not be written")
			return
		}
	}
	settings, err = a.accounts.AnonymousSettings(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "settings_error", "settings were saved but could not be reloaded")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (a *API) getSessionSettings(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	minutes := management.DefaultSessionTimeoutMinutes
	if a.management != nil {
		minutes = a.management.SessionTimeout()
	}
	writeJSON(w, http.StatusOK, map[string]int{"sessionTimeoutMinutes": minutes})
}

func (a *API) updateSessionSettings(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	var req struct {
		SessionTimeoutMinutes int `json:"sessionTimeoutMinutes"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.SessionTimeoutMinutes < 5 || req.SessionTimeoutMinutes > 43200 {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_session_timeout", "session timeout must be between 5 minutes and 30 days")
		return
	}
	if err := a.management.SetSessionTimeout(req.SessionTimeoutMinutes); err != nil {
		writeManagementError(w, err)
		return
	}
	a.sessions.setTTL(time.Duration(req.SessionTimeoutMinutes) * time.Minute)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]int{"sessionTimeoutMinutes": req.SessionTimeoutMinutes})
}

func (a *API) deleteSession(w http.ResponseWriter, r *http.Request) {
	p, _, ok := a.identityWithMethod(r)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "sign-in required")
		return
	}
	if p.Guest {
		if !guestSessionRequest(r) {
			writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
			return
		}
		a.deleteGuestSession(w, r)
		return
	}
	if r.Header.Get("Authorization") == "" && !sameOriginRequest(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_rejected", "request origin did not match this site")
		return
	}
	if a.sessions == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "session_error", "session service is unavailable")
		return
	}
	a.sessions.revoke(r)
	http.SetCookie(w, &http.Cookie{Name: adminCookie, Value: "", Path: "/api/v1", MaxAge: -1, HttpOnly: true, Secure: requestIsSecure(r), SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
}

func requestIsSecure(r *http.Request) bool {
	return requestScheme(r) == "https"
}

func forwardedValue(r *http.Request, name string) string {
	value := strings.TrimSpace(r.Header.Get(name))
	if comma := strings.IndexByte(value, ','); comma >= 0 {
		value = value[:comma]
	}
	return strings.TrimSpace(value)
}

func requestHost(r *http.Request) string {
	if host := forwardedValue(r, "X-Forwarded-Host"); host != "" {
		return host
	}
	return r.Host
}

func requestScheme(r *http.Request) string {
	if proto := strings.ToLower(forwardedValue(r, "X-Forwarded-Proto")); proto == "http" || proto == "https" {
		return proto
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func sameOriginRequest(r *http.Request) bool {
	// Origin is the authoritative browser CSRF signal. Fetch Metadata is an
	// additional cross-site rejection, but values such as "same-site" can be
	// emitted by browsers/proxies for a request whose exact Origin still matches
	// this instance. Do not reject those same-origin requests before comparing
	// the full origin below.
	fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
	if fetchSite == "cross-site" {
		return false
	}
	if fetchSite == "same-origin" {
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	// Chromium can omit both Origin and Referer for same-origin fetches when
	// the response uses Referrer-Policy: no-referrer. Fetch Metadata is still
	// present in that case and distinguishes a same-origin request from a
	// cross-site request. Cookies remain host-only and SameSite=Strict, so a
	// same-site request from another host cannot use an authenticated session.
	if origin == "" {
		return fetchSite == "same-origin" || fetchSite == "same-site"
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || !strings.EqualFold(parsed.Host, requestHost(r)) || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return strings.EqualFold(parsed.Scheme, requestScheme(r))
}

func guestSessionRequest(r *http.Request) bool {
	if sameOriginRequest(r) {
		return true
	}
	// Some LAN reverse proxies rewrite the public Origin or host. The web client
	// marks guest mutations with a non-simple header; a cross-origin browser
	// fetch cannot send it without a preflight, and an HTML form cannot produce
	// it. JSON also cannot be submitted by an HTML form. Explicit cross-site
	// Fetch Metadata remains rejected.
	fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
	return fetchSite != "cross-site" &&
		(r.Header.Get("X-101conv-Request") == "guest-session" || strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json"))
}
