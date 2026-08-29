package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"remorasftp/internal/config"
	"remorasftp/internal/credentials"
	"remorasftp/internal/events"
	"remorasftp/internal/manager"
	"remorasftp/internal/protocol"
	"remorasftp/internal/transfers"
	"remorasftp/internal/trust"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SFTPBOX_DATA_DIR", dir)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	creds, err := credentials.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := trust.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus(dir, "info")
	mgr := manager.New(cfg, creds, tr, bus)
	tm := transfers.New(bus, mgr, 2)
	srv := New(Options{Config: cfg, Manager: mgr, Transfers: tm, Bus: bus})
	if err := srv.Start("127.0.0.1", 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return srv
}

// injectClient registers a live session in the manager backed by a provided
// protocol client (test support).
func injectClient(srv *Server, sessionID string, cl protocol.Client) {
	managerInjectClient(srv.mgr, sessionID, cl)
}

func TestBindsLoopbackOnly(t *testing.T) {
	srv := newTestServer(t)
	host, _, err := net.SplitHostPort(srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		t.Fatalf("server bound to non-loopback address: %s", host)
	}
}

func TestBootstrapFlow(t *testing.T) {
	srv := newTestServer(t)
	base := "http://" + srv.Addr()

	// Forged bootstrap code rejected.
	resp, err := http.Post(base+"/api/bootstrap", "application/json", strings.NewReader(`{"code":"forged"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("forged code: got %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()

	// Valid single-use code redeems once.
	code := srv.auth.newBootstrapCode(time.Minute)
	body, _ := json.Marshal(map[string]string{"code": code})
	resp, err = http.Post(base+"/api/bootstrap", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Token string `json:"token"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	if out.Token == "" || out.Token != srv.Token() {
		t.Fatal("bootstrap did not return the instance token")
	}

	// Second redemption of the same code fails.
	resp, _ = http.Post(base+"/api/bootstrap", "application/json", strings.NewReader(string(body)))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused code: got %d, want 401", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestConnectionProfileCRUD(t *testing.T) {
	srv := newTestServer(t)
	base := "http://" + srv.Addr()
	token := srv.Token()

	do := func(method, path string, body string) *http.Response {
		req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Requested-With", "RemoraSFTP")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Create.
	resp := do("POST", "/api/connections", `{"name":"T","protocol":"sftp","host":"h","port":22,"username":"u","password":"secret-pw"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	var created struct {
		Connection struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"connection"`
	}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	id := created.Connection.ID

	// List must not echo the password back.
	resp = do("GET", "/api/connections", "")
	var list struct {
		Connections []map[string]any `json:"connections"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "secret-pw") {
		t.Fatal("secret leaked through the connection list response")
	}

	// Delete.
	resp = do("DELETE", "/api/connections/"+id, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestSecurityHeaders(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get("http://" + srv.Addr() + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	for _, h := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if resp.Header.Get(h) == "" {
			t.Errorf("security header %q missing", h)
		}
	}
}
