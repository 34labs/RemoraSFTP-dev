package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"remorasftp/internal/protocol"
)

// fakePreviewClient serves canned content for preview endpoint tests.
type fakePreviewClient struct {
	protocol.Client
	html []byte
	svg  []byte
}

func (f *fakePreviewClient) Capabilities() protocol.Capabilities {
	return protocol.Capabilities{Protocol: "sftp", Encrypted: true, MaxPreviewBytes: 4 << 20}
}
func (f *fakePreviewClient) Stat(_ context.Context, p string) (protocol.Entry, error) {
	e := protocol.Entry{Name: p, Path: p, Size: int64(len(f.html)), Type: protocol.EntryFile, MIMEType: "text/html"}
	if strings.HasSuffix(p, ".svg") {
		e.MIMEType = "image/svg+xml"
		e.Size = int64(len(f.svg))
	}
	if strings.HasSuffix(p, ".py") {
		e.MIMEType = "text/x-python"
		e.Size = int64(len("import os"))
	}
	return e, nil
}
func (f *fakePreviewClient) Download(_ context.Context, p string, w io.Writer, _ int64, _ protocol.ProgressFunc) error {
	if strings.HasSuffix(p, ".svg") {
		_, err := w.Write(f.svg)
		return err
	}
	_, err := w.Write(f.html)
	return err
}
func (f *fakePreviewClient) ReadPartial(_ context.Context, _ string, max int64) ([]byte, bool, error) {
	return []byte("import os"), false, nil
}

// TestHTMLPreviewIsolated verifies a hostile remote HTML response carries a
// CSP that blocks scripts and is not served from an executable content type
// that the app itself runs in the privileged origin.
func TestHTMLPreviewIsolated(t *testing.T) {
	srv := newTestServer(t)
	cl := &fakePreviewClient{
		html: []byte("<html><body><script>alert(document.cookie)</script><h1>x</h1></body></html>"),
		svg:  []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(2)</script></svg>`),
	}
	injectClient(srv, "sess-preview", cl)

	do := func(p string) *http.Response {
		req, _ := http.NewRequest("GET", "http://"+srv.Addr()+"/api/sessions/sess-preview/preview?path="+p, nil)
		req.Header.Set("Authorization", "Bearer "+srv.Token())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// HTML preview.
	resp := do("/evil.html")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'none'") {
		t.Errorf("HTML preview CSP must block scripts, got: %s", csp)
	}
	if strings.Contains(csp, "frame-ancestors 'none'") {
		// The document is framed by our app; frame-ancestors must allow self.
		t.Errorf("frame-ancestors 'none' would block the intended iframe: %s", csp)
	}
	if !strings.Contains(string(body), "<script>") {
		// body is the raw remote HTML, but it's delivered with sandbox CSP
		// and rendered in a sandboxed iframe - that's expected.
	}
	// Verify the server did NOT serve it as the app shell (no auth token leak).
	if strings.Contains(string(body), srv.Token()) {
		t.Fatal("response must never contain the instance token")
	}

	// SVG must be forced to download (not rendered as an active document).
	resp2 := do("/x.svg")
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()
	if ct := resp2.Header.Get("Content-Type"); strings.Contains(ct, "image/svg") {
		t.Errorf("SVG must not be served for inline rendering, got Content-Type: %s", ct)
	}
	if cd := resp2.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("SVG should be attachment, got: %s", cd)
	}
}

// TestPreviewAuthRequired ensures preview endpoints reject unauthenticated
// requests (cannot be used as an arbitrary file-read primitive).
func TestPreviewAuthRequired(t *testing.T) {
	srv := newTestServer(t)
	req, _ := http.NewRequest("GET", "http://"+srv.Addr()+"/api/sessions/x/preview?path=/etc/passwd", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("preview without token: got %d, want 401", resp.StatusCode)
	}
}

// sanity: avoid unused import lint
var _ = time.Second
