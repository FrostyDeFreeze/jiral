package main

import (
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	cfg              Config
	index            Index
	mu               sync.Mutex
	logw             io.Writer
	now              func() time.Time
	remoteDownloader remoteDownloadFunc
}

type logField struct {
	key   string
	value any
}

func field(key string, value any) logField {
	return logField{key: key, value: value}
}

func NewServer(cfg Config) (*Server, error) {
	cfg, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "files"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "previews"), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "tmp"), 0o755); err != nil {
		return nil, err
	}

	index, err := loadIndex(filepath.Join(cfg.DataDir, "index.json"))
	if err != nil {
		return nil, err
	}
	if err := ensureDeleteLinks(cfg, &index); err != nil {
		return nil, err
	}
	if err := saveIndexFile(filepath.Join(cfg.DataDir, "index.json"), index); err != nil {
		return nil, err
	}

	return &Server{
		cfg:              cfg,
		index:            index,
		logw:             os.Stdout,
		now:              func() time.Time { return time.Now().UTC() },
		remoteDownloader: downloadRemoteMedia,
	}, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/files" || strings.HasPrefix(r.URL.Path, "/api/files/") {
		s.serveAPI(w, r)
		return
	}
	s.servePublic(w, r)
}

func (s *Server) authorized(r *http.Request) bool {
	return constantTimeEqual(r.Header.Get("Authorization"), "Bearer "+s.cfg.APIToken)
}

func (s *Server) entry(name string) (FileEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.index.Files[name]
	return entry, ok
}

func (s *Server) filePath(name string) string {
	return filepath.Join(s.cfg.DataDir, "files", name)
}

func (s *Server) previewPath(name string) string {
	return filepath.Join(s.cfg.DataDir, "previews", name+".webp")
}

func (s *Server) publicURL(name string) string {
	return s.cfg.BaseURL + "/" + name
}

func (s *Server) thumbnailURL(name string) string {
	return s.cfg.BaseURL + "/t/" + name
}

func (s *Server) deleteURL(name, code string) string {
	return s.publicURL(name) + "?delete=" + code
}

func (s *Server) timestamp() string {
	return s.now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func (s *Server) logInfo(event string, fields ...logField) {
	s.logEvent("info", event, fields...)
}

func (s *Server) logError(event string, fields ...logField) {
	s.logEvent("error", event, fields...)
}

func (s *Server) logEvent(level, event string, fields ...logField) {
	if s.logw == nil {
		return
	}
	writeStructuredLog(s.logw, s.timestamp(), level, event, fields...)
}

func logProcessError(w io.Writer, event string, err error) {
	writeStructuredLog(w, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "error", event, field("err", err))
}

func writeStructuredLog(w io.Writer, timestamp, level, event string, fields ...logField) {
	if w == nil {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s %s", timestamp, strings.ToLower(level), strings.ToLower(event))
	for _, f := range fields {
		if f.key == "" {
			continue
		}
		b.WriteByte(' ')
		b.WriteString(strings.ToLower(f.key))
		b.WriteByte('=')
		b.WriteString(logValue(f.value))
	}
	b.WriteByte('\n')
	_, _ = io.WriteString(w, b.String())
}

func logValue(value any) string {
	text := fmt.Sprint(value)
	if text == "" {
		return strconv.Quote(text)
	}
	if strings.ContainsAny(text, " \t\r\n\"") {
		return strconv.Quote(text)
	}
	return text
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
