// Package server hosts the embedded browser UI and the local HTTP/WebSocket
// API of the RemoraSFTP engine.
package server

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:webassets
var embedded embed.FS

// webAssets returns the rooted filesystem for the built UI.
func webAssets() (fs.FS, error) {
	return fs.Sub(embedded, "webassets")
}

// cspForApp is the restrictive Content-Security-Policy applied to the app
// shell. Everything (scripts, styles, workers) is served from the loopback
// origin itself; no remote origins are allowed.
func cspForApp() string {
	return strings.Join([]string{
		"default-src 'none'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline'", // Vite injects small inline style blocks
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"media-src 'self' blob:",
		"object-src 'none'",
		"frame-src 'self' blob:",
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}

// spaHandler serves the built single-page application. Unknown non-API paths
// fall back to index.html so client-side views work on refresh.
type spaHandler struct {
	assets fs.FS
}

func (h *spaHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" || strings.HasSuffix(p, "/") {
		p = "index.html"
	}
	if strings.Contains(p, "..") {
		http.NotFound(w, r)
		return
	}
	if _, err := fs.Stat(h.assets, p); err != nil {
		p = "index.html"
	}
	if strings.HasSuffix(p, ".html") {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Security-Policy", cspForApp())
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.FileServer(http.FS(h.assets)).ServeHTTP(w, r)
}
