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
)

const (
	adminCookie = "101conv_admin"
	sessionTTL  = 12 * time.Hour
)

type sessionStore struct {
	mu       sync.Mutex
	sessions map[[32]byte]sessionRecord
}

type sessionRecord struct {
	expires   time.Time
	principal auth.Principal
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[[32]byte]sessionRecord)}
}

func (s *sessionStore) create(principal auth.Principal) (string, time.Time, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(sessionTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
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
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: token, Path: "/api/v1", Expires: expires, MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, Secure: requestIsSecure(r), SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"authenticated": true, "username": principal.Username, "role": principal.Role, "canConvert": principal.CanConvert, "canReadJobs": principal.CanReadJobs, "expiresAt": expires.UTC()})
}

func (a *API) getSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, ok := a.identity(r)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "username": p.Username, "role": p.Role, "canConvert": p.CanConvert, "canReadJobs": p.CanReadJobs})
}

func (a *API) deleteSession(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := a.identityWithMethod(r); !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "sign-in required")
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
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func sameOriginRequest(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host != "" && strings.EqualFold(parsed.Host, r.Host) && (parsed.Scheme == "http" || parsed.Scheme == "https")
}
