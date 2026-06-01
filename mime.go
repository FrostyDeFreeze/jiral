package main

import (
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

func detectMIME(path string) (string, string, error) {
	mtype, err := mimetype.DetectFile(path)
	if err != nil {
		return "", "", err
	}
	mimeType := mtype.String()
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	ext := strings.TrimPrefix(strings.ToLower(mtype.Extension()), ".")
	if ext == "" {
		ext = "bin"
	}
	if mimeBase(mimeType) == "application/octet-stream" {
		ext = "bin"
	}
	return mimeType, ext, nil
}

func mimeBase(mimeType string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
}
