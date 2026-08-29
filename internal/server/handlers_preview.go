package server

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"path"
	"strings"

	"remorasftp/internal/events"
	"remorasftp/internal/protocol"
	"remorasftp/internal/safepath"
)

// Preview classification drives how the browser renders content.
type previewKind string

const (
	kindImage   previewKind = "image"
	kindMedia   previewKind = "media" // audio/video
	kindPDF     previewKind = "pdf"
	kindText    previewKind = "text"
	kindCode    previewKind = "code"
	kindData    previewKind = "data" // JSON/XML/YAML/TOML/CSV
	kindHTML    previewKind = "html"
	kindUnknown previewKind = "unknown"
)

func classify(name, mime string) previewKind {
	ext := strings.ToLower(path.Ext(name))
	switch {
	case strings.HasPrefix(mime, "image/"):
		return kindImage
	case strings.HasPrefix(mime, "video/"):
		return kindMedia
	case strings.HasPrefix(mime, "audio/"):
		return kindMedia
	case mime == "application/pdf":
		return kindPDF
	case mime == "text/html" || ext == ".html" || ext == ".htm" || ext == ".xhtml":
		return kindHTML
	case isSourceExt(ext) || strings.HasPrefix(mime, "text/x-") || mime == "application/x-sh":
		return kindCode
	case strings.HasPrefix(mime, "text/") || mime == "application/json" ||
		mime == "application/xml" || mime == "application/yaml" ||
		mime == "application/toml" || mime == "text/csv" ||
		mime == "text/tab-separated-values" || mime == "text/markdown":
		return kindText
	}
	return kindUnknown
}

func isSourceExt(ext string) bool {
	switch ext {
	case ".go", ".rs", ".py", ".rb", ".php", ".js", ".mjs", ".cjs", ".ts", ".tsx",
		".jsx", ".java", ".c", ".h", ".cc", ".cpp", ".hpp", ".cs", ".swift",
		".kt", ".kts", ".scala", ".sh", ".bash", ".zsh", ".fish", ".ps1", ".sql",
		".proto", ".vue", ".svelte", ".lua", ".pl", ".pm", ".r", ".dart", ".ex",
		".exs", ".erl", ".hs", ".ml", ".clj", ".groovy", ".gradle", ".makefile",
		".dockerfile", ".tf", ".css", ".scss", ".less", ".ini", ".conf", ".cfg",
		".env", ".gitignore", ".md", ".markdown":
		return true
	}
	return false
}

// handlePreview serves file content for the previewer with strict isolation:
//   - text/code/data: JSON envelope with base64 content (rendered by the app,
//     never as the document)
//   - image/media/pdf: streamed with a restrictive CSP and nosniff
//   - HTML: served from a dedicated sandboxed response with CSP that blocks
//     scripts and network access; the UI renders it only inside
//     <iframe sandbox=""> WITHOUT allow-same-origin, so the remote document
//     cannot touch the RemoraSFTP origin, its token, or the parent window.
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	cl, err := s.mgr.Client(sessionID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	remotePath := safepath.Clean(r.URL.Query().Get("path"))
	st, err := cl.Stat(r.Context(), remotePath)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	mime := st.MIMEType
	if mime == "" {
		mime = protocol.GuessMIME(path.Base(remotePath))
	}
	kind := classify(path.Base(remotePath), mime)

	s.bus.Info(events.TypePreviewOpened, "preview opened: "+path.Base(remotePath), events.P("session", sessionID))

	switch kind {
	case kindImage, kindMedia, kindPDF:
		s.servePreviewRaw(w, r, cl, remotePath, mime, kind)
	case kindHTML:
		s.servePreviewSandboxed(w, r, cl, remotePath)
	case kindText, kindCode, kindData:
		s.servePreviewText(w, r, cl, remotePath, mime, kind, st)
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"kind":    "unsupported",
			"mime":    mime,
			"entry":   st,
			"message": "binary format cannot be previewed; download to open locally",
		})
	}
}

func (s *Server) servePreviewRaw(w http.ResponseWriter, r *http.Request, cl protocol.Client, p, mime string, kind previewKind) {
	// Never allow an image/media response to become an active document:
	// force attachment-style download semantics for SVG (which can carry
	// script) while images/video/audio/PDF render inline with a locking CSP.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if mime == "image/svg+xml" {
		// SVG is an active content format. Do not serve it for in-browser
		// rendering; force download instead of risking script execution.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(p)))
		w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
		_ = kind
		if err := cl.Download(r.Context(), p, w, 0, nil); err != nil {
			s.bus.Warn("svg download interrupted: " + err.Error())
		}
		return
	}
	w.Header().Set("Content-Type", mime)
	// Strict CSP: the document may only display the media itself; no
	// scripts, no network, no ability to frame the app origin.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; img-src 'self' data: blob:; media-src 'self' blob:; style-src 'unsafe-inline'; frame-ancestors 'self'; sandbox")
	w.Header().Set("Content-Disposition", "inline; filename="+safeHeaderName(path.Base(p)))
	if err := cl.Download(r.Context(), p, w, 0, nil); err != nil {
		s.bus.Warn("preview stream interrupted: " + err.Error())
	}
}

// servePreviewSandboxed streams remote HTML into a response locked down so it
// cannot execute scripts or reach any origin. The UI additionally renders it
// inside an iframe with sandbox="" (no allow-same-origin, no allow-scripts),
// giving two independent isolation layers.
func (s *Server) servePreviewSandboxed(w http.ResponseWriter, r *http.Request, cl protocol.Client, p string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition", "inline; filename="+safeHeaderName(path.Base(p)))
	// Extremely restrictive: no scripts, no remote resources, no forms to
	// any origin, no plugins.
	w.Header().Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'none'",
		"img-src data: blob:",
		"style-src 'unsafe-inline'",
		"font-src data:",
		"script-src 'none'",
		"object-src 'none'",
		"form-action 'none'",
		"connect-src 'none'",
		// The document is rendered inside the RemoraSFTP iframe; allow that
		// framing but nothing else.
		"frame-ancestors 'self'",
		"sandbox",
	}, "; "))
	// The iframe additionally uses sandbox="" with neither allow-scripts nor
	// allow-same-origin (see web Preview component), so even if the CSP were
	// ignored the remote document cannot run script or touch our origin.
	if err := cl.Download(r.Context(), p, w, 0, nil); err != nil {
		s.bus.Warn("html preview interrupted: " + err.Error())
	}
}

func (s *Server) servePreviewText(w http.ResponseWriter, r *http.Request, cl protocol.Client, p, mime string, kind previewKind, st protocol.Entry) {
	caps := cl.Capabilities()
	maxBytes := caps.MaxPreviewBytes
	if maxBytes == 0 {
		maxBytes = 4 << 20
	}
	data, truncated, err := cl.ReadPartial(r.Context(), p, maxBytes)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":       string(kind),
		"mime":       mime,
		"entry":      st,
		"content":    base64.StdEncoding.EncodeToString(data),
		"truncated":  truncated,
		"encoding":   "base64",
		"sourceNote": sourceNoteFor(path.Base(p), mime),
	})
}

// sourceNoteFor reminds the user that server-side code is shown as source
// only and never executed by RemoraSFTP.
func sourceNoteFor(name, mime string) string {
	ext := strings.ToLower(path.Ext(name))
	switch ext {
	case ".php", ".py", ".rb", ".js", ".ts", ".sh", ".bash", ".go", ".rs", ".java", ".cs", ".c", ".cpp":
		return "Server-side source file - shown as code for inspection. RemoraSFTP never executes remote files for preview."
	}
	return ""
}
