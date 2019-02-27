package renter

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
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
	encryptionVersion   = "1.0"
)

// CreateBackup creates a backup of the renter's siafiles. If a secret is not
// nil, the backup will be encrypted using the provided secret.
func (r *Renter) CreateBackup(dst string, secret []byte) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	// Create the gzip file.
	var archive io.Writer
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	archive = f

	// Prepare a header for the backup.
	bh := backupHeader{
		Version: encryptionVersion,
	}

	// Wrap it for encryption if required.
	if secret != nil {
		bh.Encryption = encryptionTwofish
		bh.IV = fastrand.Bytes(twofish.BlockSize)
		c, err := twofish.NewCipher(secret)
		if err != nil {
			return err
		}
		sw := cipher.StreamWriter{
			S: cipher.NewOFB(c, bh.IV),
			W: archive,
		}
		archive = sw
	}

	// Write the header.
	enc := json.NewEncoder(f)
	if err := enc.Encode(bh); err != nil {
		return err
	}

	// Use a pipe to direct the output of the tar writer into the gzip reader.
	tarReader, tarWriter := io.Pipe()

	// Start creating the tarball.
	tarErr := make(chan error)
	go func() {
		err := r.managedTarSiaFiles(tarWriter)
		err = errors.Compose(err, tarWriter.Close())
		tarErr <- err
	}()

	// Gzip the tarball and grab the error from the other thread.
	err = gzipSiaFiles(tarReader, archive)
	return errors.Compose(err, <-tarErr)
}

// LoadBackup loads the siafiles of a previously created backup into the
// renter. If the backup is encrypted, secret will be used to decrypt it.
// Otherwise the argument is ignored.
func (r *Renter) LoadBackup(src string, secret []byte) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	// Open the gzip file.
	var archive io.Reader
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	archive = f

	// Read the header.
	dec := json.NewDecoder(archive)
	var bh backupHeader
	if err := dec.Decode(&bh); err != nil {
		return err
	}
	// Seek back to the beginning of the body.
	if buf, ok := dec.Buffered().(*bytes.Reader); ok {
		_, err := f.Seek(int64(1-buf.Len()), io.SeekCurrent)
		if err != nil {
			return err
		}
	}

	// Check if encryption is required.
	if bh.Version != encryptionVersion {
		return errors.New("unknown version")
	}
	switch bh.Encryption {
	case encryptionTwofish:
		c, err := twofish.NewCipher(secret)
		if err != nil {
			return err
		}
		sw := cipher.StreamReader{
			S: cipher.NewOFB(c, bh.IV),
			R: archive,
		}
		archive = sw
	case encryptionPlaintext:
	default:
		return errors.New("unknown cipher")
	}

	// Create a pipe to redirect the unzipped output to the tar reader.
	gzipReader, gzipWriter := io.Pipe()

	// Start unzipping the archive.
	gzipErr := make(chan error)
	go func() {
		err := gunzipSiaFiles(archive, gzipWriter)
		err = errors.Compose(err, gzipWriter.Close())
		gzipErr <- err
	}()

	// Untar the tarball and grab the error from the other thread.
	err = untarDir(gzipReader, r.staticFilesDir)
	return errors.Compose(err, <-gzipErr)
}

// gunzipSiaFiles unzips the data read from src and writes its output to dst.
func gunzipSiaFiles(src io.Reader, dst io.Writer) error {
	// Create the gzip reader.
	archive, err := gzip.NewReader(src)
	if err != nil {
		return err
	}
	defer archive.Close()

	// Unzip the archive.
	_, err = io.Copy(dst, archive)
	return err
}

// gzipSiaFiles gzips the data from src and writes the archive to dst.
func gzipSiaFiles(src io.Reader, dst io.Writer) error {
	// Init the gzip writer.
	gzw := gzip.NewWriter(dst)
	defer gzw.Close()

	// Gzip the tarball.
	_, err := io.Copy(gzw, src)
	return err
}

// managedTarSiaFiles creates a tarball from the renter's siafiles and writes
// it to dst.
func (r *Renter) managedTarSiaFiles(dst io.Writer) error {
	// Create the writer for the tarball.
	tarball := tar.NewWriter(dst)
	defer tarball.Close()

	// Walk over all the siafiles and add them to the tarball.
	return filepath.Walk(r.staticFilesDir, func(path string, info os.FileInfo, err error) error {
		// This error is non-nil if filepath.Walk couldn't stat a file or
		// folder.
		if err != nil {
			return err
		}
		// Nothing to do for non-folders and non-siafiles.
		if !info.IsDir() && filepath.Ext(path) != siafile.ShareExtension {
			return nil
		}
		// Create the header for the file/dir.
		header, err := tar.FileInfoHeader(info, info.Name())
		if err != nil {
			return err
		}
		relPath := strings.TrimPrefix(path, r.staticFilesDir)
		header.Name = relPath
		// Write the header.
		if err := tarball.WriteHeader(header); err != nil {
			return err
		}
		// If the info is a dir there is nothing more to do.
		if info.IsDir() {
			return nil
		}
		// Get the siafile.
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
		_, err = io.Copy(tarball, sr)
		return err
	})
}

// untarDir untars the archive from src and writes the contents to dstFolder
// while preserving the relative paths within the archive.
func untarDir(src io.Reader, dstFolder string) error {
	// Create tar reader.
	tarReader := tar.NewReader(src)
	// Copy the files from the tarball to the new location.
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		dst := filepath.Join(dstFolder, header.Name)

		// Check for dir.
		info := header.FileInfo()
		if info.IsDir() {
			if err = os.MkdirAll(dst, info.Mode()); err != nil {
				return err
			}
			continue
		}
		// Add a suffix to the dst path if the file already exists.
		dst = uniqueFilename(dst)
		// Create file while preserving mode.
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tarReader)

		// Close the file right away instead of defering it.
		_ = f.Close()

		// Check if io.Copy was successful.
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
