package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type FileEntry struct {
	Name         string `json:"name"`
	Extension    string `json:"extension"`
	MIME         string `json:"mime"`
	Size         int64  `json:"size"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnailUrl"`
	DeleteURL    string `json:"deleteUrl"`
	DeleteCode   string `json:"deleteCode"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

type Index struct {
	Files map[string]FileEntry `json:"files"`
}

func loadIndex(path string) (Index, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Index{Files: map[string]FileEntry{}}, nil
	}
	if err != nil {
		return Index{}, err
	}
	var index Index
	if err := json.Unmarshal(raw, &index); err != nil {
		return Index{}, err
	}
	if index.Files == nil {
		index.Files = map[string]FileEntry{}
	}
	return index, nil
}

func (s *Server) saveIndexLocked() error {
	return saveIndexFile(filepath.Join(s.cfg.DataDir, "index.json"), s.index)
}

func saveIndexFile(path string, index Index) error {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileStable(path, data, 0o644)
}

func ensureDeleteLinks(cfg Config, index *Index) error {
	if index.Files == nil {
		index.Files = map[string]FileEntry{}
	}
	for name, entry := range index.Files {
		if len(entry.DeleteCode) != deleteCodeLength {
			code, err := randomCode(deleteCodeLength)
			if err != nil {
				return err
			}
			entry.DeleteCode = code
		}
		url := cfg.BaseURL + "/" + name
		if entry.URL != url {
			entry.URL = url
		}
		thumbnailURL := cfg.BaseURL + "/t/" + name
		if entry.ThumbnailURL != thumbnailURL {
			entry.ThumbnailURL = thumbnailURL
		}
		deleteURL := cfg.BaseURL + "/" + name + "?delete=" + entry.DeleteCode
		if entry.DeleteURL != deleteURL {
			entry.DeleteURL = deleteURL
		}
		index.Files[name] = entry
	}
	return nil
}

func (s *Server) deleteEntryLocked(name string) error {
	if err := os.Remove(s.filePath(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(s.previewPath(name)); err != nil && !os.IsNotExist(err) {
		return err
	}
	delete(s.index.Files, name)
	return s.saveIndexLocked()
}

func (s *Server) entryFor(name, ext, mimeType string, size int64, createdAt, updatedAt string) FileEntry {
	deleteCode, err := randomCode(deleteCodeLength)
	if err != nil {
		deleteCode = ""
	}
	url := s.publicURL(name)
	return FileEntry{
		Name:         name,
		Extension:    ext,
		MIME:         mimeType,
		Size:         size,
		URL:          url,
		ThumbnailURL: s.thumbnailURL(name),
		DeleteURL:    s.deleteURL(name, deleteCode),
		DeleteCode:   deleteCode,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}
}
