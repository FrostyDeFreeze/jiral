package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type remoteDownloadFunc func(ctx context.Context, rawURL, tmpDir string, maxSize int64) (string, error)

var (
	errUnsupportedRemoteURL        = errors.New("unsupported remote url")
	errRemoteDownloaderUnavailable = errors.New("yt-dlp unavailable")
	errRemoteDownloadFailed        = errors.New("remote download failed")
	errRemoteDownloadTimeout       = errors.New("remote download timeout")
)

func (s *Server) handleRemoteFetch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	sourceURL := strings.TrimSpace(body.URL)
	host, err := validateRemoteMediaURL(sourceURL)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unsupported url")
		return
	}

	tmpDir, err := os.MkdirTemp(filepath.Join(s.cfg.DataDir, "tmp"), "remote-*")
	if err != nil {
		s.logError("remote_fetch_failed", field("host", host), field("err", err))
		writeError(w, http.StatusInternalServerError, "download failed")
		return
	}
	defer os.RemoveAll(tmpDir)

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(s.cfg.RemoteDownloadTimeoutSeconds)*time.Second)
	defer cancel()

	tmpPath, err := s.remoteDownloader(ctx, sourceURL, tmpDir, s.cfg.MaxFileSize)
	if err != nil {
		s.writeRemoteDownloadError(w, host, err)
		return
	}

	entry, err := s.storeTempFile(tmpPath)
	if err != nil {
		s.writeStoreTempFileError(w, err)
		return
	}

	s.logInfo("remote_fetched", field("host", host), field("name", entry.Name), field("mime", mimeBase(entry.MIME)), field("size", entry.Size), field("url", entry.URL))
	writeJSON(w, http.StatusCreated, entry)
}

func validateRemoteMediaURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errUnsupportedRemoteURL
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return "", errUnsupportedRemoteURL
	}

	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	for _, domain := range []string{"tiktok.com", "instagram.com"} {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return host, nil
		}
	}
	return "", errUnsupportedRemoteURL
}

func downloadRemoteMedia(ctx context.Context, rawURL, tmpDir string, maxSize int64) (string, error) {
	ytDLP, err := exec.LookPath("yt-dlp")
	if err != nil {
		return "", fmt.Errorf("%w: %v", errRemoteDownloaderUnavailable, err)
	}

	args := []string{
		"--force-ipv4",
		"--no-playlist",
		"--no-mtime",
		"--max-filesize", strconv.FormatInt(maxSize, 10),
		"--match-filters", "!is_live & !was_live",
		"-S", "vcodec:h264",
		"-f", "bv*[ext=mp4]+ba[ext=m4a]/b[ext=mp4]/best",
		"--merge-output-format", "mp4",
		"-o", filepath.Join(tmpDir, "download.%(ext)s"),
		rawURL,
	}
	cmd := exec.CommandContext(ctx, ytDLP, args...)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return "", errRemoteDownloadTimeout
	}
	if err != nil {
		if outputIndicatesTooLarge(output) {
			return "", errTooLarge
		}
		return "", fmt.Errorf("%w: %v", errRemoteDownloadFailed, err)
	}

	path, err := singleRemoteDownloadOutput(tmpDir)
	if err != nil {
		return "", err
	}
	return path, nil
}

func singleRemoteDownloadOutput(tmpDir string) (string, error) {
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return "", fmt.Errorf("%w: %v", errRemoteDownloadFailed, err)
	}

	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return "", fmt.Errorf("%w: %v", errRemoteDownloadFailed, err)
		}
		if info.Mode().IsRegular() {
			files = append(files, filepath.Join(tmpDir, entry.Name()))
		}
	}
	if len(files) != 1 {
		return "", fmt.Errorf("%w: got %d files", errRemoteDownloadFailed, len(files))
	}
	return files[0], nil
}

func outputIndicatesTooLarge(output []byte) bool {
	text := strings.ToLower(string(output))
	return strings.Contains(text, "larger than max-filesize") ||
		strings.Contains(text, "file is larger than")
}

func (s *Server) writeRemoteDownloadError(w http.ResponseWriter, host string, err error) {
	switch {
	case errors.Is(err, errTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "file too large")
	case errors.Is(err, errRemoteDownloadTimeout):
		s.logError("remote_fetch_failed", field("host", host), field("err", errRemoteDownloadTimeout))
		writeError(w, http.StatusGatewayTimeout, "download timed out")
	case errors.Is(err, errRemoteDownloaderUnavailable):
		s.logError("remote_fetch_failed", field("host", host), field("err", errRemoteDownloaderUnavailable))
		writeError(w, http.StatusInternalServerError, "yt-dlp unavailable")
	default:
		s.logError("remote_fetch_failed", field("host", host), field("err", errRemoteDownloadFailed))
		writeError(w, http.StatusBadGateway, "download failed")
	}
}
