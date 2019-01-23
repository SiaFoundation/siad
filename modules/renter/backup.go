package renter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
)

// CreateBackup creates a backup of the renter's siafiles by first copying them
// into a temporary directory and then zipping that directory. The final backup
// is then encrypted using a hash of the provided secret. If len(secret) == 0,
// encryption will be skipped.
func (r *Renter) CreateBackup(dst string, secret []byte) error {
	// Create a temporary folder named .[dst] at the destination of the backup.
	tmpDir := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst))
	if err := os.Mkdir(tmpDir, 0700); err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// Walk over all the siafiles and copy them to the temporary directory.
	err := filepath.Walk(r.filesDir, func(path string, info os.FileInfo, err error) error {
		// This error is non-nil if filepath.Walk couldn't stat a file or
		// folder.
		if err != nil {
			return err
		}

		// Skip folders and non-sia files.
		if info.IsDir() || filepath.Ext(path) != siafile.ShareExtension {
			return nil
		}

		// Copy the Siafile. The location within the temporary directory should
		// be relative to the file's location within the 'siafile' directory.
		relPath := strings.TrimPrefix(path, r.filesDir)
		newPath := filepath.Join(tmpDir, relPath)
		fmt.Println(relPath)
		fmt.Println(newPath)
		// TODO copy SiaFile
		return nil
	})
	if err != nil {
		return err
	}
	// TODO: Zip the backup.
	// TODO: Encrypt the backup if necessary.
	// TODO: Write the backup to the destination.
	return nil
}
