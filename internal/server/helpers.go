package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"remorasftp/internal/version"
)

// safeHeaderName sanitizes a remote-derived filename for use in HTTP
// headers, stripping control characters (including CR/LF) that could enable
// response splitting or header injection.
func safeHeaderName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			continue
		}
		b.WriteRune(r)
	}
	out := b.String()
	if out == "" {
		return "file"
	}
	return out
}

func versionInfo() version.Info { return version.Get() }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20))
	return dec.Decode(v)
}

// pathTail returns the part of r.URL.Path after prefix, split by "/".
func pathTail(prefix, path string) []string {
	tail := path[len(prefix):]
	out := []string{}
	cur := ""
	for _, c := range tail {
		if c == '/' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
