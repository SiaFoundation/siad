package test

import (
	"os"
	"path/filepath"
)

var (
	// SiaTestingDir is the directory that contains all of the files and
	// folders created during testing.
	SiaTestingDir = filepath.Join(os.TempDir(), "SiaTesting")
)
