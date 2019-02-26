package renter

import (
	"archive/zip"
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/fastrand"
	"golang.org/x/crypto/twofish"
)

// backupHeader defines the structure of the backup's JSON header.
type backupHeader struct {
	Version    string `json:"version"`
	Encryption string `json:"encryption"`
	IV         []byte `json:"iv"`
}

// The following specifiers are options for the encryption of backups.
var (
	encryptionPlaintext = ""
	encryptionTwofish   = "twofish-ofb"
)

// CreateBackup creates a backup of the renter's siafiles.
func (r *Renter) CreateBackup(dst string, encrypt bool) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	// Prepare the backup header.
	bh := backupHeader{
		Version: "1.0",
	}

	// Create the zip file.
	var zipFile io.Writer
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	zipFile = f

	// Wrap it for encryption if required.
	if encrypt {
		bh.Encryption = encryptionTwofish
		bh.IV = fastrand.Bytes(twofish.BlockSize)
		c, err := twofish.NewCipher([]byte{}) // TODO: use key
		if err != nil {
			return err
		}
		sw := cipher.StreamWriter{
			S: cipher.NewOFB(c, bh.IV),
			W: f,
		}
		zipFile = sw
	}

	// Write header
	jsonEncoder := json.NewEncoder(f)
	if err := jsonEncoder.Encode(bh); err != nil {
		return err
	}

	// Init the zip writer.
	zw := zip.NewWriter(zipFile)
	defer zw.Close()

	// Walk over all the siafiles and add them to the archive.
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
		// Add a suffix to the dst path if the file already exists.
		dst = uniqueFilename(dst)
		// Copy File.
		if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			return err
		}
		if _, err := os.Stat(dst); !os.IsNotExist(err) {
			continue
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

// uniqueFilename checks if a file exists at a certain destination. If it does
// it will append a suffix of the form _[num] and increment [num] until it can
// find a suffix that isn't in use yet.
func uniqueFilename(dst string) string {
	suffix := ""
	counter := 1
	extension := filepath.Ext(dst)
	nameNoExt := strings.TrimSuffix(dst, extension)
	for {
		path := nameNoExt + suffix + extension
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// File doesn't exist. We are done.
			return path
		}
		// Duplicate detected. Increment suffix and counter.
		suffix = fmt.Sprintf("_%v", counter)
		counter++
	}
}
