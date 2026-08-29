package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func newTestAuth(t *testing.T, bindAddr string) *authState {
	t.Helper()
	a := newAuthState()
	a.bindAddr = bindAddr
	a.loopbackOnly = true
	return a
}

func TestTokenRequired(t *testing.T) {
	a := newTestAuth(t, "127.0.0.1:54321")
	hits := int32(0)
	srv := httptest.NewServer(a.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()

	// No token -> 401.
	resp, err := http.Get(srv.URL + "/api/sessions")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: got %d, want 401", resp.StatusCode)
	}

	// Wrong token -> 401.
	req, _ := http.NewRequest("GET", srv.URL+"/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer wrong-value")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token: got %d, want 401", resp.StatusCode)
	}

	// Correct token -> 200.
	req, _ = http.NewRequest("GET", srv.URL+"/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid token: got %d, want 200", resp.StatusCode)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatal("protected handler should have been called exactly once")
	}
}

func TestMutationRequiresCustomHeader(t *testing.T) {
	a := newTestAuth(t, "127.0.0.1:54321")
	srv := httptest.NewServer(a.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()

	// POST without X-Requested-With -> 403 (CSRF defense).
	req, _ := http.NewRequest("POST", srv.URL+"/api/sessions/x/disconnect", nil)
	req.Header.Set("Authorization", "Bearer "+a.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST without custom header: got %d, want 403", resp.StatusCode)
	}

	// POST with X-Requested-With -> passes middleware.
	req, _ = http.NewRequest("POST", srv.URL+"/api/sessions/x/disconnect", nil)
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("X-Requested-With", "RemoraSFTP")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST with custom header: got %d, want 200", resp.StatusCode)
	}
}

func TestBootstrapCodeSingleUse(t *testing.T) {
	a := newTestAuth(t, "127.0.0.1:54321")
	code := a.newBootstrapCode(30 * time.Second)
	if !a.redeemBootstrapCode(code) {
		t.Fatal("first redemption should succeed")
	}
	if a.redeemBootstrapCode(code) {
		t.Fatal("one-time code must not redeem twice")
	}
	if a.redeemBootstrapCode("nonexistent") {
		t.Fatal("forged code must not redeem")
	}
	// Expired code.
	expired := a.newBootstrapCode(-time.Minute)
	if a.redeemBootstrapCode(expired) {
		t.Fatal("expired code must not redeem")
	}
}

func TestOriginValidation(t *testing.T) {
	a := newTestAuth(t, "127.0.0.1:54321")
	good := []string{
		"http://127.0.0.1:54321",
		"http://localhost:54321",
		"http://[::1]:54321",
		"", // non-browser client
	}
	for _, o := range good {
		if !a.isAllowedOrigin(o) {
			t.Errorf("origin %q should be allowed", o)
		}
	}
	bad := []string{
		"http://evil.example.com",
		"http://192.168.1.10:54321",
		"http://127.0.0.1:9999", // wrong port
		"https://127.0.0.1:54321",
	}
	for _, o := range bad {
		if a.isAllowedOrigin(o) {
			t.Errorf("origin %q must be rejected", o)
		}
	}
}

func TestHealthEndpointIsPublicButOriginChecked(t *testing.T) {
	a := newTestAuth(t, "127.0.0.1:54321")
	var captured int32
	h := a.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.StoreInt32(&captured, 1)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Health with no origin (CLI) works.
	resp, err := http.Get(srv.URL + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: %d", resp.StatusCode)
	}

	// Health from a disallowed origin is blocked even without token.
	req, _ := http.NewRequest("GET", srv.URL+"/api/health", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin health request: got %d, want 403", resp.StatusCode)
	}
}

func TestBootstrapExchangeOverHTTP(t *testing.T) {
	a := newTestAuth(t, "127.0.0.1:54321")
	mux := http.NewServeMux()
	mux.HandleFunc("/api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if !a.redeemBootstrapCode(req.Code) {
			writeErr(w, http.StatusUnauthorized, "bad code")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": a.token})
	})
	srv := httptest.NewServer(a.authMiddleware(mux))
	defer srv.Close()

	code := a.newBootstrapCode(time.Minute)
	body, _ := json.Marshal(map[string]string{"code": code})
	resp, err := http.Post(srv.URL+"/api/bootstrap", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out["token"] != a.token {
		t.Fatalf("bootstrap exchange returned wrong token: %q", out["token"])
	}
	if out["token"] == "" {
		t.Fatal("token should be non-empty")
	}
	// The token must be delivered in the body, not the URL.
	if resp.Request.URL.RawQuery != "" {
		t.Fatal("token must not appear in URL query string")
	}
}
