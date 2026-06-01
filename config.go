package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	configFileName          = "config.json"
	deleteCodeLength        = 7
	defaultAddr             = "127.0.0.1:7000"
	defaultDataDir          = "./data"
	defaultFileCodeLength   = 3
	defaultMaxFileSize      = 268435456
	defaultPreviewMaxPixels = 51984
	defaultPreviewMaxSide   = 300
	defaultPreviewFPS       = 15
	defaultPreviewFrames    = 100
)

type Config struct {
	Addr             string `json:"addr"`
	BaseURL          string `json:"baseUrl"`
	APIToken         string `json:"apiToken"`
	DataDir          string `json:"dataDir"`
	FileCodeLength   int    `json:"fileCodeLength"`
	MaxFileSize      int64  `json:"maxFileSize"`
	PreviewMaxPixels int    `json:"previewMaxPixels"`
	PreviewMaxSide   int    `json:"previewMaxSide"`
	PreviewFPS       int    `json:"previewFPS"`
	PreviewFrames    int    `json:"previewFrames"`
}

func LoadConfig() (Config, error) {
	return LoadConfigFromFile(configFileName)
}

func LoadConfigFromFile(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	cfg := Config{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return normalizeConfig(cfg)
}

func normalizeConfig(cfg Config) (Config, error) {
	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL(cfg.Addr)
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.APIToken == "" {
		return Config{}, fmt.Errorf("apiToken is required")
	}
	if cfg.DataDir == "" {
		cfg.DataDir = defaultDataDir
	}
	if cfg.FileCodeLength == 0 {
		cfg.FileCodeLength = defaultFileCodeLength
	}
	if cfg.FileCodeLength < 1 {
		return Config{}, fmt.Errorf("fileCodeLength must be positive")
	}
	if cfg.MaxFileSize == 0 {
		cfg.MaxFileSize = defaultMaxFileSize
	}
	if cfg.MaxFileSize < 1 {
		return Config{}, fmt.Errorf("maxFileSize must be positive")
	}
	if cfg.PreviewMaxPixels == 0 {
		cfg.PreviewMaxPixels = defaultPreviewMaxPixels
	}
	if cfg.PreviewMaxSide == 0 {
		cfg.PreviewMaxSide = defaultPreviewMaxSide
	}
	if cfg.PreviewFPS == 0 {
		cfg.PreviewFPS = defaultPreviewFPS
	}
	if cfg.PreviewFrames == 0 {
		cfg.PreviewFrames = defaultPreviewFrames
	}
	return cfg, nil
}

func defaultBaseURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}
