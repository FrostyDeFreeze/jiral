package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// sized for Chatterino's limit w*h*frames < 20971520; keep the filter formula as is
const (
	thumbnailMaxPixels = 51984
	thumbnailMaxSide   = 300
	thumbnailFPS       = 15
	thumbnailFrames    = 100
	toolTimeout        = 30 * time.Second
)

// Chatterino shows an image itself up to 4 MB, and only in formats Qt reads
const chatterinoMaxImage = 4_000_000

var chatterinoImages = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

type meta struct {
	mime          string
	size          int64
	width, height int
	duration      float64
}

var cardPage = template.Must(template.New("card").Parse(`<!doctype html>
<meta charset="utf-8">
<title>{{.Name}}</title>
<meta property="og:title" content="{{.Name}}">
<meta property="og:description" content="{{.Description}}">
{{- if .Image}}
<meta property="og:image" content="{{.Image}}">
{{- end}}
`))

func (s *server) thumbnailURL(name string) string {
	return s.BaseURL + "/api/thumbnail/" + name
}

func isMedia(ct string) bool {
	return strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "video/")
}

// handleCard answers Chatterino/FFZ, the only user agents Caddy proxies here: an og: card,
// or with ?raw an image itself for another site's og:image. Chatterino API fetches link
// info and thumbnails with identical requests, so only the URL can tell them apart
func (s *server) handleCard(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.exists(name) {
		fail(w, http.StatusNotFound, "not found", nil)
		return
	}

	src := filepath.Join(s.files, name)
	fi, err := os.Stat(src)
	if err != nil {
		fail(w, http.StatusNotFound, "not found", nil)
		return
	}

	ct, _, _ := strings.Cut(contentType(src), ";")
	if strings.HasPrefix(ct, "image/") && r.URL.Query().Has("raw") {
		if fi.Size() <= chatterinoMaxImage && chatterinoImages[ct] {
			http.ServeFile(w, r, src)
		} else {
			s.serveThumbnail(w, r, name, src, ct)
		}

		return
	}

	m := meta{mime: ct, size: fi.Size()}
	if isMedia(ct) || strings.HasPrefix(ct, "audio/") {
		if err := s.probe(r.Context(), src, &m); err != nil {
			slog.Error("ffprobe failed", "name", name, "err", err)
		}
	}

	card := struct{ Name, Description, Image string }{Name: name, Description: description(m)}
	if isMedia(ct) {
		card.Image = s.thumbnailURL(name)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := cardPage.Execute(w, card); err != nil {
		slog.Error("card failed", "name", name, "err", err)
	}
}

// handleThumbnail renders the WebP on every request; nothing is kept on disk
func (s *server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.exists(name) {
		fail(w, http.StatusNotFound, "not found", nil)
		return
	}

	src := filepath.Join(s.files, name)
	ct := contentType(src)
	if !isMedia(ct) {
		fail(w, http.StatusNotFound, "not found", nil)
		return
	}

	s.serveThumbnail(w, r, name, src, ct)
}

func (s *server) serveThumbnail(w http.ResponseWriter, r *http.Request, name, src, ct string) {
	tmp, err := os.CreateTemp(s.tmp, "thumbnail-*.webp")
	if err != nil {
		fail(w, http.StatusInternalServerError, "thumbnail failed", err)
		return
	}

	tmp.Close()
	defer os.Remove(tmp.Name())

	if err := s.renderThumbnail(r.Context(), src, ct, tmp.Name()); err != nil {
		fail(w, http.StatusInternalServerError, "thumbnail failed", fmt.Errorf("%s: %w", name, err))
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, tmp.Name())
}

// description is the og:description line, e.g. "video/mp4 · 1320x720 · 21s · 4.2 MB"
func description(m meta) string {
	parts := []string{m.mime}
	if m.width > 0 && m.height > 0 {
		parts = append(parts, fmt.Sprintf("%dx%d", m.width, m.height))
	}

	if d := time.Duration(m.duration * float64(time.Second)).Round(time.Second); d > 0 {
		parts = append(parts, d.String())
	}

	return strings.Join(append(parts, humanSize(m.size)), " · ")
}

func (s *server) probe(ctx context.Context, src string, m *meta) error {
	out, err := s.run(ctx, "ffprobe", "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height:format=duration", "-of", "json", src)
	if err != nil {
		return err
	}

	var res struct {
		Streams []struct{ Width, Height int }
		Format  struct{ Duration string }
	}

	if err := json.Unmarshal(out, &res); err != nil {
		return err
	}

	if len(res.Streams) > 0 {
		m.width, m.height = res.Streams[0].Width, res.Streams[0].Height
	}

	// a still image reports a one-frame duration, which means nothing
	if !strings.HasPrefix(m.mime, "image/") {
		m.duration, _ = strconv.ParseFloat(res.Format.Duration, 64)
	}

	return nil
}

func (s *server) renderThumbnail(ctx context.Context, src, ct, dst string) error {
	filter := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", thumbnailMaxSide, thumbnailMaxSide)
	if strings.HasPrefix(ct, "video/") {
		filter = fmt.Sprintf("scale='if(gt(iw*ih,%d),min(min(%d, sqrt(%d*iw/ih)), iw), iw)':'if(gt(iw*ih,%d),min(min(%d, sqrt(%d*ih/iw)), ih), ih)', fps=fps=%d, setpts=0.5*PTS",
			thumbnailMaxPixels, thumbnailMaxSide, thumbnailMaxPixels, thumbnailMaxPixels, thumbnailMaxSide, thumbnailMaxPixels, thumbnailFPS)
	}

	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-i", src}
	switch strings.ToLower(filepath.Ext(src)) {
	case ".heic", ".heif":
		// iPhone HEIC is a tile grid ffmpeg assembles on its own, which -vf can't be combined with
		args = append(args, "-filter_complex", "[0:g:0]"+filter)
	default:
		args = append(args, "-vf", filter)
	}

	args = append(args, "-f", "webp", "-preset", "picture", "-loop", "0", "-frames:v", strconv.Itoa(thumbnailFrames), dst)
	_, err := s.run(ctx, "ffmpeg", args...)
	return err
}

// run starts ffmpeg/ffprobe one at a time: public routes reach them, so a flood queues instead of eating the CPU
func (s *server) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	select {
	case s.tools <- struct{}{}:
		defer func() { <-s.tools }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, name, args...).Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		err = fmt.Errorf("%s: %w: %s", name, err, tail(ee.Stderr))
	}

	return out, err
}

func humanSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	units := []string{"KB", "MB", "GB", "TB", "PB"}
	div := int64(unit)
	exp := 0
	for n := size / unit; n >= unit && exp < len(units)-1; n /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %s", float64(size)/float64(div), units[exp])
}
