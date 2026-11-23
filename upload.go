package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"mime"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gabriel-vasile/mimetype"
)

var extPattern = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

type fileInfo struct {
	Name         string `json:"name"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnailUrl"`
	DeleteURL    string `json:"deleteUrl"`
	MIME         string `json:"mime"`
	Size         int64  `json:"size"`
}

func (s *server) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, s.maxSize+1<<20) // 1 MiB for multipart headers
	mr, err := r.MultipartReader()
	if err != nil {
		fail(w, http.StatusBadRequest, "multipart body required", nil)
		return
	}

	part, err := mr.NextPart()
	for err == nil && (part.FormName() != "file" || part.FileName() == "") {
		part, err = mr.NextPart()
	}

	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		fail(w, http.StatusRequestEntityTooLarge, "file too large", nil)
		return
	}

	if err != nil {
		fail(w, http.StatusBadRequest, "file field is required", nil)
		return
	}

	tmp, err := os.CreateTemp(s.tmp, "upload-*")
	if err != nil {
		fail(w, http.StatusInternalServerError, "upload failed", err)
		return
	}
	defer os.Remove(tmp.Name())

	n, err := io.Copy(tmp, io.LimitReader(part, s.maxSize+1))
	if closeErr := tmp.Close(); closeErr != nil {
		fail(w, http.StatusInternalServerError, "upload failed", closeErr)
		return
	}

	if n > s.maxSize || errors.As(err, &tooBig) {
		fail(w, http.StatusRequestEntityTooLarge, "file too large", nil)
		return
	}

	if err != nil {
		fail(w, http.StatusBadRequest, "upload failed", nil)
		return
	}

	s.store(w, tmp.Name(), part.FileName(), "uploaded")
}

func (s *server) handleFetch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}

	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "invalid json", nil)
		return
	}

	raw := strings.TrimSpace(body.URL)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		fail(w, http.StatusBadRequest, "unsupported url", nil)
		return
	}

	dir, err := os.MkdirTemp(s.tmp, "remote-*")
	if err != nil {
		fail(w, http.StatusInternalServerError, "download failed", err)
		return
	}
	defer os.RemoveAll(dir)

	ctx, cancel := context.WithTimeout(r.Context(), fetchTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--force-ipv4",
		"--no-playlist",
		"--no-mtime",
		"--max-filesize", strconv.FormatInt(s.maxSize, 10),
		"--match-filters", "!is_live & !was_live",
		"-S", "vcodec:h264",
		"-f", "bv*[ext=mp4]+ba[ext=m4a]/b[ext=mp4]/best",
		"--merge-output-format", "mp4",
		"-o", filepath.Join(dir, "download.%(ext)s"),
		raw,
	)

	// shortcut: on timeout only yt-dlp is killed and its ffmpeg child may linger; kill the process group if that shows up in practice
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	files, _ := filepath.Glob(filepath.Join(dir, "*"))
	lower := strings.ToLower(string(out))

	switch {
	case errors.Is(err, exec.ErrNotFound):
		fail(w, http.StatusInternalServerError, "yt-dlp unavailable", err)
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		fail(w, http.StatusGatewayTimeout, "download timed out", fmt.Errorf("host %s", u.Hostname()))
	case strings.Contains(lower, "larger than max-filesize") || strings.Contains(lower, "file is larger than"):
		fail(w, http.StatusRequestEntityTooLarge, "file too large", nil)
	case err != nil || len(files) != 1:
		fail(w, http.StatusBadGateway, "download failed", fmt.Errorf("host %s: %v, %d files: %s", u.Hostname(), err, len(files), tail(out)))
	default:
		s.store(w, files[0], files[0], "fetched")
	}
}

// store is the shared tail of upload and fetch: pick extension, publish
func (s *server) store(w http.ResponseWriter, path, originalName, event string) {
	ext, err := pickExt(path, originalName)
	if err != nil {
		fail(w, http.StatusInternalServerError, "mime detection failed", err)
		return
	}

	// contentType goes by extension
	p := path + "." + ext
	if err := os.Rename(path, p); err != nil {
		fail(w, http.StatusInternalServerError, "file save failed", err)
		return
	}
	defer os.Remove(p)

	// CreateTemp makes 0600, but Caddy runs as another user and must read the file
	if err := os.Chmod(p, 0o644); err != nil {
		fail(w, http.StatusInternalServerError, "file save failed", err)
		return
	}

	ct := contentType(p)
	fi, err := os.Stat(p)
	if err != nil {
		fail(w, http.StatusInternalServerError, "file save failed", err)
		return
	}

	if fi.Size() > s.maxSize {
		fail(w, http.StatusRequestEntityTooLarge, "file too large", nil)
		return
	}

	name, err := s.publish(p, ext)
	if err != nil {
		fail(w, http.StatusInternalServerError, "file save failed", err)
		return
	}

	slog.Info(event, "name", name, "mime", ct, "size", fi.Size())
	writeJSON(w, http.StatusCreated, fileInfo{
		Name:         name,
		URL:          s.BaseURL + "/" + name,
		ThumbnailURL: s.thumbnailURL(name),
		DeleteURL:    s.BaseURL + "/api/delete/" + name + "/" + s.deleteCode(name),
		MIME:         ct,
		Size:         fi.Size(),
	})
}

// pickExt keeps a sane extension from the original name, otherwise sniffs the content
func pickExt(path, originalName string) (string, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(originalName), "."))
	if extPattern.MatchString(ext) && ext != "bin" {
		return ext, nil
	}

	m, err := mimetype.DetectFile(path)
	if err != nil {
		return "", err
	}

	ext = strings.TrimPrefix(m.Extension(), ".")
	if ext == "" || m.Is("application/octet-stream") {
		ext = "bin"
	}

	return ext, nil
}

// contentType matches what http.ServeContent sends: by extension, else by the first 512 bytes
func contentType(path string) string {
	if ct := mime.TypeByExtension(filepath.Ext(path)); ct != "" {
		return ct
	}

	f, err := os.Open(path)
	if err != nil {
		return "application/octet-stream"
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, _ := io.ReadFull(f, buf)

	return http.DetectContentType(buf[:n])
}

// publish hard-links the file under a free random name; Link fails on an existing name, so no lock is needed
func (s *server) publish(path, ext string) (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	code := make([]byte, s.NameLength)

	for range 100 {
		for i := range code {
			code[i] = alphabet[rand.IntN(len(alphabet))]
		}

		name := string(code) + "." + ext
		if err := os.Link(path, filepath.Join(s.files, name)); !os.IsExist(err) {
			return name, err
		}
	}

	return "", errors.New("no free name after 100 attempts")
}
