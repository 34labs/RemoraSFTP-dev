package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

func splitHostPort(hostport string) (string, string, error) {
	return net.SplitHostPort(hostport)
}

// generateToken returns a URL-safe random secret (256-bit for the session
// token, 128-bit for one-time bootstrap codes).
func generateToken(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failure: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// bootstrapCode is a single-use, short-lived code delivered out-of-band in
// the browser launch URL. It is exchanged once for the session token over
// loopback, then invalidated immediately.
type bootstrapCode struct {
	code      string
	expiresAt time.Time
	used      bool
}

// authState holds the per-instance secrets and bootstrap codes.
type authState struct {
	mu           sync.Mutex
	token        string
	bindAddr     string // host:port the listener is bound to
	loopbackOnly bool
	codes        map[string]*bootstrapCode
}

func newAuthState() *authState {
	return &authState{
		token: generateToken(32),
		codes: map[string]*bootstrapCode{},
	}
}

// newBootstrapCode mints a single-use code valid for ttl.
func (a *authState) newBootstrapCode(ttl time.Duration) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := generateToken(16)
	a.codes[c] = &bootstrapCode{code: c, expiresAt: time.Now().Add(ttl)}
	// Opportunistic cleanup of expired codes.
	for k, v := range a.codes {
		if v.used || time.Now().After(v.expiresAt) {
			delete(a.codes, k)
		}
	}
	return c
}

// redeemBootstrapCode validates and consumes a one-time code.
func (a *authState) redeemBootstrapCode(code string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	c, ok := a.codes[code]
	if !ok || c.used || time.Now().After(c.expiresAt) {
		return false
	}
	c.used = true
	delete(a.codes, code)
	return true
}

// validToken reports whether t matches the session token (constant-time).
func (a *authState) validToken(t string) bool {
	a.mu.Lock()
	token := a.token
	a.mu.Unlock()
	if t == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1
}

// extractToken pulls the session token from a request. Preferred channel is
// the Authorization header (fetch/XHR). WebSocket cannot set headers from the
// browser, so it passes the token as a Sec-WebSocket-Protocol subprotocol
// value (never as a URL query parameter).
func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if h := r.Header.Get("X-RemoraSFTP-Token"); h != "" {
		return h
	}
	// WebSocket subprotocol: "remorasftp.<token>"
	if proto := r.Header.Get("Sec-WebSocket-Protocol"); proto != "" {
		for _, p := range strings.Split(proto, ",") {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "remorasftp.") {
				return strings.TrimPrefix(p, "remorasftp.")
			}
		}
	}
	return ""
}

// isAllowedOrigin validates the Origin header against the loopback listener.
// Defense-in-depth against cross-origin localhost abuse: a malicious website
// cannot make an authenticated, non-simple request because (a) it does not
// know the token, (b) it cannot set the X-Requested-With header without a
// CORS preflight that we refuse, and (c) even simple requests are checked
// here for an exact Origin match.
func (a *authState) isAllowedOrigin(origin string) bool {
	if origin == "" {
		// Non-browser clients (curl, the WebSocket upgrade on some
		// clients, CLI) send no Origin. Token auth still applies.
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// The local engine serves plain HTTP over loopback. An https Origin is
	// never ours.
	if u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return false
	}
	// Port must match the actual listener.
	port := u.Port()
	wantPort := ""
	if _, p, err := splitHostPort(a.bindAddr); err == nil {
		wantPort = p
	}
	if wantPort != "" && port != wantPort {
		return false
	}
	return true
}

// securityHeaders applies browser-side hardening headers to every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		// No CORS headers are emitted: cross-origin reads are blocked by
		// the browser same-origin policy by default.
		next.ServeHTTP(w, r)
	})
}

// authMiddleware enforces origin validation and token auth for /api routes.
// Exempt: GET /api/health and POST /api/bootstrap (they carry no privileges).
func (a *authState) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if !a.isAllowedOrigin(origin) {
			writeErr(w, http.StatusForbidden, "origin not allowed")
			return
		}
		path := r.URL.Path
		if path == "/api/health" || path == "/api/bootstrap" {
			next.ServeHTTP(w, r)
			return
		}
		// State-changing requests must carry the custom X-Requested-With
		// header. A cross-origin page cannot send that header without a
		// CORS preflight, and we answer no Access-Control-Allow-Origin, so
		// the browser blocks the request before it reaches us.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("X-Requested-With") != "RemoraSFTP" {
				writeErr(w, http.StatusForbidden, "invalid request")
				return
			}
		}
		token := extractToken(r)
		if !a.validToken(token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeErr(w, http.StatusUnauthorized, "invalid or missing instance token")
			return
		}
		next.ServeHTTP(w, r)
	})
}
