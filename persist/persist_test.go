package persist

import (
	"os"
	"path/filepath"
	"testing"

	"go.sia.tech/siad/build"
)

// TestIntegrationRandomSuffix checks that the random suffix creator creates
// valid files.
func TestIntegrationRandomSuffix(t *testing.T) {
	tmpDir := build.TempDir(persistDir, t.Name())
	err := os.MkdirAll(tmpDir, defaultDirPermissions)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		suffix := RandomSuffix()
		filename := filepath.Join(tmpDir, "test file - "+suffix+".nil")
		file, err := os.Create(filename)
		if err != nil {
			t.Fatal(err)
		}
		file.Close()
	}
}
