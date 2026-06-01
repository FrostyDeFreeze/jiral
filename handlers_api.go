package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
)

func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	if r.URL.Path == "/api/files" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleUpload(w, r)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/api/files/")
	if name == "" || strings.Contains(name, "/") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	switch r.Method {
	case http.MethodPatch:
		s.handleRename(w, r, name)
	case http.MethodDelete:
		s.handleDelete(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request, oldName string) {
	var body struct {
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if !validRenameBase(body.Name) {
		writeError(w, http.StatusBadRequest, "invalid name")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.index.Files[oldName]
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	newName := body.Name + "." + entry.Extension
	if _, exists := s.index.Files[newName]; exists {
		writeError(w, http.StatusConflict, "file already exists")
		return
	}
	oldPath := s.filePath(oldName)
	newPath := s.filePath(newName)
	if exists(newPath) {
		writeError(w, http.StatusConflict, "file already exists")
		return
	}

	oldPreview := s.previewPath(oldName)
	newPreview := s.previewPath(newName)
	oldPreviewExists := exists(oldPreview)
	if oldPreviewExists && exists(newPreview) {
		writeError(w, http.StatusConflict, "preview already exists")
		return
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		s.logError("rename_failed", field("old", oldName), field("new", newName), field("err", err))
		writeError(w, http.StatusInternalServerError, "rename failed")
		return
	}
	if oldPreviewExists {
		if err := os.Rename(oldPreview, newPreview); err != nil {
			s.rollbackRename(newPath, oldPath, oldName, newName)
			s.logError("preview_rename_failed", field("old", oldName), field("new", newName), field("err", err))
			writeError(w, http.StatusInternalServerError, "preview rename failed")
			return
		}
	}

	delete(s.index.Files, oldName)
	entry.Name = newName
	entry.UpdatedAt = s.timestamp()
	entry.URL = s.publicURL(newName)
	entry.ThumbnailURL = s.thumbnailURL(newName)
	entry.DeleteURL = s.deleteURL(newName, entry.DeleteCode)
	s.index.Files[newName] = entry
	if err := s.saveIndexLocked(); err != nil {
		s.logError("index_update_failed", field("action", "rename"), field("old", oldName), field("new", newName), field("err", err))
		writeError(w, http.StatusInternalServerError, "index update failed")
		return
	}
	s.logInfo("renamed", field("old", oldName), field("new", newName), field("url", entry.URL))
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.index.Files[name]
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err := s.deleteEntryLocked(name); err != nil {
		s.logError("delete_failed", field("name", name), field("source", "api"), field("err", err))
		writeError(w, http.StatusInternalServerError, "index update failed")
		return
	}
	s.logInfo("deleted", field("name", name), field("url", entry.URL), field("source", "api"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePublicDelete(w http.ResponseWriter, r *http.Request, name, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.index.Files[name]
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !constantTimeEqual(code, entry.DeleteCode) {
		writeError(w, http.StatusForbidden, "invalid delete code")
		return
	}
	if err := s.deleteEntryLocked(name); err != nil {
		s.logError("delete_failed", field("name", name), field("source", "public"), field("err", err))
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	s.logInfo("deleted", field("name", name), field("url", entry.URL), field("source", "public"))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("Deleted " + name + "\n"))
}

func validRenameBase(name string) bool {
	if name == "" || strings.TrimSpace(name) != name {
		return false
	}
	if strings.ContainsAny(name, `./\`) || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func (s *Server) rollbackRename(newPath, oldPath, oldName, newName string) {
	if err := os.Rename(newPath, oldPath); err != nil {
		s.logError("rename_rollback_failed", field("old", oldName), field("new", newName), field("err", err))
	}
}
