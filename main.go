package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const fetchTimeout = 120 * time.Second

type config struct {
	Addr          string `json:"addr"`
	BaseURL       string `json:"baseUrl"`
	APIToken      string `json:"apiToken"`
	DataDir       string `json:"dataDir"`
	NameLength    int    `json:"nameLength"`
	MaxFileSizeMB int    `json:"maxFileSizeMB"`
}

type server struct {
	config
	files, tmp string
	maxSize    int64         // bytes, from MaxFileSizeMB
	tools      chan struct{} // one ffmpeg/ffprobe at a time
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	cfg, err := loadConfig("config.json")
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(1)
	}

	s := &server{
		config:  cfg,
		files:   filepath.Join(cfg.DataDir, "files"),
		tmp:     filepath.Join(cfg.DataDir, "tmp"),
		maxSize: int64(cfg.MaxFileSizeMB) << 20,
		tools:   make(chan struct{}, 1),
	}

	for _, dir := range []string{s.files, s.tmp} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			slog.Error("data dir", "err", err)
			os.Exit(1)
		}
	}

	slog.Info("listening", "addr", cfg.Addr)
	srv := &http.Server{Addr: cfg.Addr, Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	slog.Error("server", "err", srv.ListenAndServe())
	os.Exit(1)
}

func loadConfig(path string) (config, error) {
	f, err := os.Open(path)
	if err != nil {
		return config{}, err
	}
	defer f.Close()

	cfg := config{Addr: "127.0.0.1:7000", DataDir: "./data", NameLength: 3, MaxFileSizeMB: 256}
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return config{}, fmt.Errorf("parse %s: %w", path, err)
	}

	if cfg.APIToken == "" {
		return config{}, errors.New("apiToken is required")
	}

	if cfg.NameLength < 1 || cfg.NameLength > 32 {
		return config{}, errors.New("nameLength must be 1-32")
	}

	if cfg.MaxFileSizeMB < 1 {
		return config{}, errors.New("maxFileSizeMB must be at least 1")
	}

	if cfg.BaseURL == "" {
		host := cfg.Addr
		if strings.HasPrefix(host, ":") {
			host = "localhost" + host
		}

		cfg.BaseURL = "http://" + host
	}

	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	return cfg, nil
}

// Caddy serves the files; Go sees /api/* and Chatterino/FFZ requests for /{name}
func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/upload", s.auth(s.handleUpload))
	mux.HandleFunc("POST /api/fetch", s.auth(s.handleFetch))
	mux.HandleFunc("GET /api/delete/{name}/{code}", s.handleDeleteLink)
	mux.HandleFunc("DELETE /api/delete/{name}", s.auth(s.handleAPIDelete))
	mux.HandleFunc("GET /api/thumbnail/{name}", s.handleThumbnail)
	mux.HandleFunc("GET /{name}", s.handleCard)

	return mux
}

func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	want := []byte("Bearer " + s.APIToken)

	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			fail(w, http.StatusUnauthorized, "unauthorized", nil)
			return
		}

		next(w, r)
	}
}

// exists accepts only a plain file directly inside files/
func (s *server) exists(name string) bool {
	if name == "" || name[0] == '.' || strings.ContainsAny(name, `/\`) {
		return false
	}

	fi, err := os.Stat(filepath.Join(s.files, name))
	return err == nil && fi.Mode().IsRegular()
}

// handleDeleteLink is a GET so Chatterino and ShareX can open it in a browser
func (s *server) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.exists(name) {
		fail(w, http.StatusNotFound, "not found", nil)
		return
	}

	if !hmac.Equal([]byte(r.PathValue("code")), []byte(s.deleteCode(name))) {
		fail(w, http.StatusForbidden, "invalid delete code", nil)
		return
	}

	if err := s.remove(name, "link"); err != nil {
		fail(w, http.StatusInternalServerError, "delete failed", err)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "Deleted %s\n", name)
}

func (s *server) handleAPIDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.exists(name) {
		fail(w, http.StatusNotFound, "not found", nil)
		return
	}

	if err := s.remove(name, "api"); err != nil {
		fail(w, http.StatusInternalServerError, "delete failed", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *server) remove(name, source string) error {
	if err := os.Remove(filepath.Join(s.files, name)); err != nil {
		return err
	}

	slog.Info("deleted", "name", name, "source", source)

	return nil
}

// deleteCode is stateless: a renamed file gets a new code, a new apiToken voids all codes
func (s *server) deleteCode(name string) string {
	mac := hmac.New(sha256.New, []byte(s.APIToken))
	mac.Write([]byte(name))

	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))[:10]
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// fail logs err (when set) and answers with a JSON error
func fail(w http.ResponseWriter, status int, msg string, err error) {
	if err != nil {
		slog.Error(msg, "err", err)
	}

	writeJSON(w, status, map[string]string{"error": msg})
}

func tail(out []byte) string {
	return strings.TrimSpace(string(out[max(0, len(out)-500):]))
}
