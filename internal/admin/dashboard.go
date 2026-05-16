package admin

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:web/dist
var distFS embed.FS

func (a *API) handleSPA(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		http.Error(w, "ui not embedded", http.StatusInternalServerError)
		return
	}
	serveSPA(w, r, sub)
}

func serveSPA(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}

	f, err := sub.Open(p)
	if err != nil {
		// Unknown path: serve index.html so client-side routing handles it.
		idx, err := sub.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer func() { _ = idx.Close() }()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = io.Copy(w, idx)
		return
	}
	defer func() { _ = f.Close() }()

	setSPAHeaders(w, p)
	_, _ = io.Copy(w, f)
}

func setSPAHeaders(w http.ResponseWriter, p string) {
	switch path.Ext(p) {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	case ".ico":
		w.Header().Set("Content-Type", "image/x-icon")
	case ".woff2":
		w.Header().Set("Content-Type", "font/woff2")
	}
	// Hashed asset paths get long-cache headers; Vite emits content-hashed filenames.
	if strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
}

func (a *API) handleAgents(w http.ResponseWriter, _ *http.Request) {
	if a.agentsFn == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, a.agentsFn())
}

func (a *API) handleEvents(w http.ResponseWriter, _ *http.Request) {
	if a.eventsFn == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, a.eventsFn())
}

func (a *API) handleRemoveAgent(w http.ResponseWriter, r *http.Request) {
	if a.removeFn == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent management not available"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/agents/")
	if !isValidID(id) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid agent id"})
		return
	}
	if !a.removeFn(id) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found or is static"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "removed": id})
}

func (a *API) handleRemoveStaleAgents(w http.ResponseWriter, _ *http.Request) {
	if a.removeStaleFn == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent management not available"})
		return
	}
	count := a.removeStaleFn()
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "removed": count})
}
