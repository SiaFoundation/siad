package main

import (
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/test"
)

// TestDir joins the provided directories and prefixes them with the Sia
// testing directory, removing any files or directories that previously existed
// at that location.
func TestDir(dirs ...string) string {
	path := filepath.Join(test.SiaTestingDir, "cmd/siac", filepath.Join(dirs...))
	err := os.RemoveAll(path)
	if err != nil {
		panic(err)
	}
	return path
}
