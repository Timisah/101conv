package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	adminCookie = "101conv_admin"
	sessionTTL  = 12 * time.Hour
)

type sessionStore struct {
	mu       sync.Mutex
	sessions map[[32]byte]time.Time
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[[32]byte]time.Time)}
}

func (s *sessionStore) create() (string, time.Time, error) {
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", time.Time{}, err
	}
	expires := time.Now().Add(sessionTTL)
	s.mu.Lock()
	defer s.mu.Unlock()
	for digest, expiry := range s.sessions {
		if time.Now().After(expiry) {
			delete(s.sessions, digest)
		}
	}
	s.sessions[sha256.Sum256(token[:])] = expires
	return base64.RawURLEncoding.EncodeToString(token[:]), expires, nil
}

func (s *sessionStore) valid(r *http.Request) bool {
	cookie, err := r.Cookie(adminCookie)
	if err != nil {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(decoded) != 32 {
		return false
	}
	digest := sha256.Sum256(decoded)
	s.mu.Lock()
	defer s.mu.Unlock()
	expires, ok := s.sessions[digest]
	if !ok {
		return false
	}
	if time.Now().After(expires) {
		delete(s.sessions, digest)
		return false
	}
	return true
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
	var req struct {
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if len(req.Password) != len(a.adminToken) || subtle.ConstantTimeCompare([]byte(req.Password), []byte(a.adminToken)) != 1 {
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "the administrator passphrase was not accepted")
		return
	}
	token, expires, err := a.sessions.create()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "session_error", "could not create an administrator session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: token, Path: "/api/v1", Expires: expires, MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, Secure: requestIsSecure(r), SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"authenticated": true, "expiresAt": expires.UTC()})
}

func (a *API) getSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": a.sessions != nil && a.sessions.valid(r)})
}

func (a *API) deleteSession(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(w, r) {
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
