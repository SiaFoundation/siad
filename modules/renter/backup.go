package renter

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
)

// CreateBackup creates a backup of the renter's siafiles.
// TODO add encryption support (follow-up)
func (r *Renter) CreateBackup(dst string) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	// Create the zip file.
	zipFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	// Init the zip writer.
	zw := zip.NewWriter(zipFile)
	defer zw.Close()

	// Walk over all the siafiles and copy them to the temporary directory.
	return filepath.Walk(r.staticFilesDir, func(path string, info os.FileInfo, err error) error {
		// This error is non-nil if filepath.Walk couldn't stat a file or
		// folder.
		if err != nil {
			return err
		}
		// Skip folders and non-sia files.
		if info.IsDir() || filepath.Ext(path) != siafile.ShareExtension {
			return nil
		}
		// Get the siafile.
		relPath := strings.TrimPrefix(path, r.staticFilesDir)
		entry, err := r.staticFileSet.Open(strings.TrimSuffix(relPath, siafile.ShareExtension))
		if err != nil {
			return err
		}
		defer entry.Close()
		// Get a reader to read from the siafile.
		sr, err := entry.SnapshotReader()
		if err != nil {
			return err
		}
		defer sr.Close()
		// Add the file to the archive.
		return addFileToZip(zw, sr, strings.TrimPrefix(path, r.staticFilesDir))
	})
}

// LoadBackup loads the siafiles of a previously created backup into the
// renter.
// TODO add decryption support (follow-up)
func (r *Renter) LoadBackup(src string) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	return unzipDir(src, r.staticFilesDir)
}

// addFileToZip adds a file to a zip archive.
func addFileToZip(zw *zip.Writer, file *siafile.SnapshotReader, headerName string) error {
	// Get the file info.
	zfi, err := file.Stat()
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
	header.Name = headerName
	// Add compression.
	header.Method = zip.Deflate
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(writer, file)
	return err
}

// unzipDir unzips the archive at zipPath and writes the contents to dstFolder
// while preserving the relative paths within the archive.
func unzipDir(zipPath, dstFolder string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	// Copy the files from the archive to the new location.
	for _, f := range r.File {
		// Open the archived file.
		rc, err := f.Open()
		if err != nil {
			return err
		}
		dst := filepath.Join(dstFolder, f.Name)

		// Search for zipslip.
		if !strings.HasPrefix(dst, filepath.Clean(dstFolder)+string(os.PathSeparator)) {
			return fmt.Errorf("%s: illegal file path", dst)
		}
		// Check for folder.
		if f.FileInfo().IsDir() {
			continue
		}
		// Copy File.
		if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		f, err := os.Create(dst)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, rc)

		// Close the file.
		_ = f.Close()

		if err != nil {
			return err
		}
	}
	return nil
}
