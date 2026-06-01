package main

import (
	"fmt"
	"net/http"
	"strings"
)

func (s *Server) servePublic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	trimmed := strings.TrimPrefix(r.URL.Path, "/")
	if trimmed == "" || strings.Contains(trimmed, "..") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) > 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if parts[0] == "t" {
		if len(parts) != 2 || parts[1] == "" {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		s.serveThumbnail(w, r, parts[1])
		return
	}
	if len(parts) != 1 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	name := parts[0]
	if r.URL.Query().Has("delete") {
		s.handlePublicDelete(w, r, name, r.URL.Query().Get("delete"))
		return
	}

	s.servePublicFile(w, r, name)
}

func (s *Server) servePublicFile(w http.ResponseWriter, r *http.Request, name string) {
	entry, ok := s.entry(name)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	if wantsThumbnail(r.UserAgent()) && exists(s.previewPath(name)) {
		s.serveThumbnailFile(w, r, name)
		return
	}

	s.serveStoredFile(w, r, entry)
}

func (s *Server) serveStoredFile(w http.ResponseWriter, r *http.Request, entry FileEntry) {
	w.Header().Set("Content-Type", entry.MIME)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, entry.Name))
	http.ServeFile(w, r, s.filePath(entry.Name))
}

func (s *Server) serveThumbnail(w http.ResponseWriter, r *http.Request, name string) {
	if _, ok := s.entry(name); !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !exists(s.previewPath(name)) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	s.serveThumbnailFile(w, r, name)
}

func (s *Server) serveThumbnailFile(w http.ResponseWriter, r *http.Request, name string) {
	w.Header().Set("Content-Type", "image/webp")
	http.ServeFile(w, r, s.previewPath(name))
}

func wantsThumbnail(userAgent string) bool {
	return strings.HasPrefix(userAgent, "chatterino-api-cache/") ||
		strings.Contains(userAgent, "FFZBot/")
}
