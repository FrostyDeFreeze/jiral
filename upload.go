package main

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

var errTooLarge = errors.New("file too large")

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "multipart body required")
		return
	}

	var tmpPath string
	var sawFile bool
	cleanup := true
	defer func() {
		if cleanup && tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()

	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid multipart body")
			return
		}
		if part.FileName() == "" {
			continue
		}
		if part.FormName() != "file" {
			writeError(w, http.StatusBadRequest, "file field must be named file")
			return
		}
		if sawFile {
			writeError(w, http.StatusBadRequest, "exactly one file is allowed")
			return
		}
		sawFile = true

		tmpPath, err = s.writeUploadTemp(part)
		if errors.Is(err, errTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "file too large")
			return
		}
		if err != nil {
			s.logError("upload_failed", field("err", err))
			writeError(w, http.StatusInternalServerError, "upload failed")
			return
		}
	}
	if !sawFile {
		writeError(w, http.StatusBadRequest, "file field is required")
		return
	}

	mimeType, ext, err := detectMIME(tmpPath)
	if err != nil {
		s.logError("mime_detect_failed", field("err", err))
		writeError(w, http.StatusInternalServerError, "mime detection failed")
		return
	}
	if isSanitizable(mimeType) {
		s.sanitizeFile(tmpPath, ext)
	}
	info, err := os.Stat(tmpPath)
	if err != nil {
		s.logError("file_stat_failed", field("err", err))
		writeError(w, http.StatusInternalServerError, "file stat failed")
		return
	}

	now := s.timestamp()
	entry, err := s.publishUploadedFile(tmpPath, ext, mimeType, info.Size(), now)
	if err != nil {
		s.logError("file_save_failed", field("err", err))
		writeError(w, http.StatusInternalServerError, "file save failed")
		return
	}
	cleanup = false

	if isPreviewable(mimeType) {
		go s.generatePreview(entry.Name, mimeType)
	}
	s.logInfo("uploaded", field("name", entry.Name), field("mime", mimeBase(entry.MIME)), field("size", entry.Size), field("url", entry.URL))
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Server) writeUploadTemp(part *multipart.Part) (string, error) {
	tmp, err := os.CreateTemp(filepath.Join(s.cfg.DataDir, "tmp"), "upload-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	var size int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := part.Read(buf)
		if n > 0 {
			size += int64(n)
			if size > s.cfg.MaxFileSize {
				return "", errTooLarge
			}
			if _, err := tmp.Write(buf[:n]); err != nil {
				return "", err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	cleanup = false
	return tmpPath, nil
}

func (s *Server) publishUploadedFile(tmpPath, ext, mimeType string, size int64, now string) (FileEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := 0; i < 1000; i++ {
		code, err := randomCode(s.cfg.FileCodeLength)
		if err != nil {
			return FileEntry{}, err
		}
		name := code + "." + ext
		if _, inIndex := s.index.Files[name]; inIndex || exists(s.filePath(name)) {
			continue
		}

		entry := s.entryFor(name, ext, mimeType, size, now, now)
		finalPath := s.filePath(name)
		if err := os.Rename(tmpPath, finalPath); err != nil {
			return FileEntry{}, err
		}
		s.index.Files[name] = entry
		if err := s.saveIndexLocked(); err != nil {
			delete(s.index.Files, name)
			_ = os.Remove(finalPath)
			return FileEntry{}, err
		}
		return entry, nil
	}
	return FileEntry{}, fmt.Errorf("could not generate unique file name")
}
