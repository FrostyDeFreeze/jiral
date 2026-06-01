package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func (s *Server) sanitizeFile(path, ext string) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		s.logError("sanitize_failed", field("path", path), field("err", err))
		return
	}

	tmp, err := os.CreateTemp(filepath.Join(s.cfg.DataDir, "tmp"), "sanitize-*."+ext)
	if err != nil {
		s.logError("sanitize_failed", field("path", path), field("err", err))
		return
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command("ffmpeg", "-hide_banner", "-y", "-i", path, "-map_metadata", "-1", "-c", "copy", tmpPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		s.logError("sanitize_failed", field("path", path), field("err", err), field("output", strings.TrimSpace(string(output))))
		return
	}
	if err := replaceFile(tmpPath, path); err != nil {
		s.logError("sanitize_failed", field("path", path), field("err", err))
	}
}

func (s *Server) generatePreview(name, mimeType string) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		s.logError("preview_failed", field("name", name), field("err", err))
		return
	}

	vf := s.previewFilter(mimeType)
	if vf == "" {
		return
	}

	tmp, err := os.CreateTemp(filepath.Join(s.cfg.DataDir, "tmp"), "preview-*.webp")
	if err != nil {
		s.logError("preview_failed", field("name", name), field("err", err))
		return
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(tmpPath)

	args := []string{
		"-hide_banner",
		"-y",
		"-i", s.filePath(name),
		"-f", "webp",
		"-vf", vf,
		"-preset", "picture",
		"-loop", "0",
		"-frames:v", strconv.Itoa(s.cfg.PreviewFrames),
		tmpPath,
	}
	cmd := exec.Command("ffmpeg", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		s.logError("preview_failed", field("name", name), field("err", err), field("output", strings.TrimSpace(string(output))))
		return
	}
	if err := os.Rename(tmpPath, s.previewPath(name)); err != nil {
		s.logError("preview_failed", field("name", name), field("err", err))
	}
}

func (s *Server) previewFilter(mimeType string) string {
	pixels := s.cfg.PreviewMaxPixels
	side := s.cfg.PreviewMaxSide
	if strings.HasPrefix(mimeType, "video/") {
		return fmt.Sprintf("scale='if(gt(iw*ih,%d),min(min(%d, sqrt(%d*iw/ih)), iw), iw)':'if(gt(iw*ih,%d),min(min(%d, sqrt(%d*ih/iw)), ih), ih)', fps=fps=%d, setpts=0.5*PTS",
			pixels, side, pixels, pixels, side, pixels, s.cfg.PreviewFPS)
	}
	if strings.HasPrefix(mimeType, "image/") {
		return fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", side, side)
	}
	return ""
}

func isSanitizable(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/") || strings.HasPrefix(mimeType, "video/")
}

func isPreviewable(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/") || strings.HasPrefix(mimeType, "video/")
}

func replaceFile(src, dst string) error {
	backup := dst + ".bak"
	_ = os.Remove(backup)
	if err := os.Rename(dst, backup); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		_ = os.Rename(backup, dst)
		return err
	}
	return os.Remove(backup)
}
