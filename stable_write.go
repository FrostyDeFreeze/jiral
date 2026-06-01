package main

import (
	"os"

	"github.com/google/renameio/v2/maybe"
)

func writeFileStable(path string, data []byte, perm os.FileMode) error {
	return maybe.WriteFile(path, data, perm)
}
