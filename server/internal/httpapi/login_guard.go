package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const loginAttemptWindow = 15 * time.Minute

type loginGuard struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	inFlight chan struct{}
}

func newLoginGuard() *loginGuard {
	return &loginGuard{attempts: map[string][]time.Time{}, inFlight: make(chan struct{}, 4)}
}

func (g *loginGuard) begin(r *http.Request) (func(bool), bool) {
	key := clientAddress(r)
	now := time.Now()
	g.mu.Lock()
	if _, exists := g.attempts[key]; !exists && len(g.attempts) >= 4096 {
		// Bound memory use even if clients rotate source addresses. The normal
		// path is small; sweep expired buckets only when the map reaches its cap.
		for address, timestamps := range g.attempts {
			kept := timestamps[:0]
			for _, at := range timestamps {
				if now.Sub(at) < loginAttemptWindow {
					kept = append(kept, at)
				}
			}
			if len(kept) == 0 {
				delete(g.attempts, address)
			} else {
				g.attempts[address] = kept
			}
		}
		if len(g.attempts) >= 4096 {
			g.mu.Unlock()
			return nil, false
		}
	}
	recent := g.attempts[key][:0]
	for _, at := range g.attempts[key] {
		if now.Sub(at) < loginAttemptWindow {
			recent = append(recent, at)
		}
	}
	if len(recent) >= 8 {
		g.attempts[key] = recent
		g.mu.Unlock()
		return nil, false
	}
	g.attempts[key] = append(recent, now)
	g.mu.Unlock()
	select {
	case g.inFlight <- struct{}{}:
	default:
		return nil, false
	}
	return func(success bool) {
		<-g.inFlight
		if success {
			g.mu.Lock()
			delete(g.attempts, key)
			g.mu.Unlock()
		}
	}, true
}

func clientAddress(r *http.Request) string {
	// X-Real-IP is meaningful only after the bundled proxy has marked the
	// request. Direct clients must not rotate a spoofed header to evade the
	// per-address login bucket. The proxy intentionally forwards the immediate
	// peer address, so multiple untrusted hops collapse to one safe bucket.
	if r.Header.Get(trustedProxyMarker) == "web" {
		if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
	}
	return "unknown"
}
