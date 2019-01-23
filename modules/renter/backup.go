package renter

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
)

// CreateBackup creates a backup of the renter's siafiles by first copying them
// into a temporary directory and then zipping that directory.
// TODO add encryption support (follow-up)
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
		dst := filepath.Join(tmpDir, relPath)
		entry, err := r.staticFileSet.Open(strings.TrimSuffix(relPath, siafile.ShareExtension))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		if err := entry.Copy(dst); err != nil {
			return err
		}
		return entry.Close()
	})
	if err != nil {
		return err
	}
	// Zip the temporary directory. If it fails we delete the partial archive.
	if err := zipDir(tmpDir, dst); err != nil {
		return errors.Compose(err, os.RemoveAll(dst))
	}
	return nil
}

// zipDir archives and compresses all the files of a directory and writes them
// to dst.
func zipDir(dirPath, dst string) error {
	// Create the zip file.
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	// Init the zip writer.
	zw := zip.NewWriter(f)
	defer zw.Close()

	// Add all the files from the dir.
	return filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		// Skip dirs.
		if info.IsDir() {
			return nil
		}
		// Open the file to add to the archive.
		zf, err := os.Open(path)
		if err != nil {
			return err
		}
		defer zf.Close()
		// Get the file info.
		zfi, err := zf.Stat()
		if err != nil {
			return err
		}
		// Get the info header.
		header, err := zip.FileInfoHeader(zfi)
		if err != nil {
			return err
		}
		// Overwrite the header.Name field to preserve the folder structure
		// within the archive.
		header.Name = strings.TrimPrefix(path, dirPath)
		// Add compression.
		header.Method = zip.Deflate
		writer, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = io.Copy(writer, zf)
		return err
	})
}
