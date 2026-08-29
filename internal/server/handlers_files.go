package server

import (
	"fmt"
	"net/http"
	"os"
	"path"
	"strconv"

	"remorasftp/internal/events"
	"remorasftp/internal/protocol"
	"remorasftp/internal/safepath"
	"remorasftp/internal/transfers"
)

func (s *Server) handleList(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "GET required")
		return
	}
	// Session must exist before any path work.
	if _, err := s.mgr.Sessions0(sessionID); err != nil {
		writeErr(w, http.StatusNotFound, "session not found (it may have disconnected)")
		return
	}
	dir := r.URL.Query().Get("path")
	if dir == "" {
		sess, err := s.mgr.Sessions0(sessionID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		dir = sess.StartDir
	}
	dir = safepath.Clean(dir)
	entries, err := s.mgr.List(r.Context(), sessionID, dir)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.mgr.SetCWD(sessionID, dir)
	cl, _ := s.mgr.Client(sessionID)
	caps := protocol.Capabilities{}
	if cl != nil {
		caps = cl.Capabilities()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":    dir,
		"entries": entries,
		"caps":    caps,
	})
}

func (s *Server) handleStat(w http.ResponseWriter, r *http.Request, sessionID string) {
	cl, err := s.mgr.Client(sessionID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	p := safepath.Clean(r.URL.Query().Get("path"))
	e, err := cl.Stat(r.Context(), p)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entry": e})
}

func (s *Server) handleFileOp(w http.ResponseWriter, r *http.Request, sessionID, action string) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	cl, err := s.mgr.Client(sessionID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var req struct {
		Path      string `json:"path"`
		From      string `json:"from"`
		To        string `json:"to"`
		Mode      string `json:"mode"`
		Recursive bool   `json:"recursive"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	ctx := r.Context()
	switch action {
	case "mkdir":
		if err := safepath.ValidateName(path.Base(req.Path)); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid folder name")
			return
		}
		if err := cl.Mkdir(ctx, req.Path); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.bus.Info(events.TypeInfo, "created folder "+req.Path, events.P("session", sessionID))
	case "remove":
		if err := cl.Remove(ctx, req.Path, req.Recursive); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.bus.Info(events.TypeInfo, "deleted "+req.Path, events.P("session", sessionID))
	case "rename":
		if err := safepath.ValidateName(path.Base(req.To)); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid name")
			return
		}
		if err := cl.Rename(ctx, req.From, req.To); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
		s.bus.Info(events.TypeInfo, "renamed "+req.From+" to "+path.Base(req.To), events.P("session", sessionID))
	case "chmod":
		mode, err := strconv.ParseUint(req.Mode, 8, 32)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "mode must be octal, e.g. 0644")
			return
		}
		if err := cl.Chmod(ctx, req.Path, os.FileMode(mode)); err != nil {
			writeErr(w, http.StatusBadGateway, err.Error())
			return
		}
	default:
		writeErr(w, http.StatusNotFound, "unknown op")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleUpload streams the request body directly to the remote file. The
// request body is never fully buffered: the transfer manager pipes it to the
// protocol adapter.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	cl, err := s.mgr.Client(sessionID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	remoteDir := safepath.Clean(r.URL.Query().Get("dir"))
	name := r.URL.Query().Get("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := safepath.ValidateName(path.Base(name)); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid file name")
		return
	}
	remotePath := safepath.Join(remoteDir, name)
	var total int64
	if ct := r.Header.Get("Content-Length"); ct != "" {
		total, _ = strconv.ParseInt(ct, 10, 64)
	}
	// Bound request bodies defensively (MaxBytesReader also makes the
	// stream cancellable on client disconnect).
	r.Body = http.MaxBytesReader(w, r.Body, 256<<30)
	// Stream the request body directly to the remote in the request
	// lifecycle (the response must not complete before the body is
	// consumed). Progress is tracked in the transfer queue.
	prog := s.tm.TrackDownload(sessionID, remotePath, name, total, transfers.Upload, cl.Capabilities())
	if err := cl.Upload(r.Context(), remotePath, r.Body, 0, prog); err != nil {
		s.tm.CompleteDownload(name, true)
		s.bus.Warn("upload to " + remotePath + " interrupted: " + err.Error())
		writeErr(w, http.StatusBadGateway, "upload failed: "+err.Error())
		return
	}
	s.tm.CompleteDownload(name, false)
	s.bus.Info(events.TypeInfo, "upload completed: "+name, events.P("session", sessionID))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": remotePath})
}

// handleDownload streams a remote file straight to the HTTP response, with
// progress reported to the transfer queue.
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, sessionID string) {
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
	if st.Type == protocol.EntryDir {
		writeErr(w, http.StatusBadRequest, "cannot download a folder directly")
		return
	}
	name := safeHeaderName(path.Base(remotePath))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	if st.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size, 10))
	}
	prog := s.tm.TrackDownload(sessionID, remotePath, name, st.Size, transfers.Download, cl.Capabilities())
	if err := cl.Download(r.Context(), remotePath, w, 0, prog); err != nil {
		s.tm.CompleteDownload(name, true)
		s.bus.Warn("download of " + remotePath + " interrupted: " + err.Error())
		return
	}
	s.tm.CompleteDownload(name, false)
	s.bus.Info(events.TypeInfo, "download completed: "+name, events.P("session", sessionID))
}
