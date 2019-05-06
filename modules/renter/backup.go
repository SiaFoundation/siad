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

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
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
	encryptionPlaintext = "plaintext"
	encryptionTwofish   = "twofish-ctr"
	encryptionVersion   = "1.0"
)

// CreateBackup creates a backup of the renter's siafiles. If a secret is not
// nil, the backup will be encrypted using the provided secret.
func (r *Renter) CreateBackup(dst string, secret []byte) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()
	return r.managedCreateBackup(dst, secret)
}

// managedCreateBackup creates a backup of the renter's siafiles. If a secret is
// not nil, the backup will be encrypted using the provided secret.
func (r *Renter) managedCreateBackup(dst string, secret []byte) error {
	// Create the gzip file.
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	archive := io.Writer(f)

	// Prepare a header for the backup and default to no encryption. This will
	// potentially be overwritten later.
	bh := backupHeader{
		Version:    encryptionVersion,
		Encryption: encryptionPlaintext,
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
			S: cipher.NewCTR(c, bh.IV),
			W: archive,
		}
		archive = sw
	}

	// Skip the checkum for now.
	if _, err := f.Seek(crypto.HashSize, io.SeekStart); err != nil {
		return err
	}
	// Write the header.
	enc := json.NewEncoder(f)
	if err := enc.Encode(bh); err != nil {
		return err
	}
	// Wrap the archive in a multiwriter to hash the contents of the archive
	// before encrypting it.
	h := crypto.NewHash()
	archive = io.MultiWriter(archive, h)
	// Wrap the potentially encrypted writer into a gzip writer.
	gzw := gzip.NewWriter(archive)
	// Wrap the gzip writer into a tar writer.
	tw := tar.NewWriter(gzw)
	// Add the files to the archive.
	if err := r.managedTarSiaFiles(tw); err != nil {
		twErr := tw.Close()
		gzwErr := gzw.Close()
		return errors.Compose(err, twErr, gzwErr)
	}
	// Close writers to flush them before computing the hash.
	twErr := tw.Close()
	gzwErr := gzw.Close()
	// Write the hash to the beginning of the file.
	_, err = f.WriteAt(h.Sum(nil), 0)
	return errors.Compose(err, twErr, gzwErr)
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
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	archive := io.Reader(f)

	// Read the checksum.
	var chks crypto.Hash
	_, err = io.ReadFull(f, chks[:])
	if err != nil {
		return err
	}
	// Read the header.
	dec := json.NewDecoder(archive)
	var bh backupHeader
	if err := dec.Decode(&bh); err != nil {
		return err
	}
	// Seek back by the amount of data left in the decoder's buffer. That gives
	// us the offset of the body.
	var off int64
	if buf, ok := dec.Buffered().(*bytes.Reader); ok {
		off, err = f.Seek(int64(1-buf.Len()), io.SeekCurrent)
		if err != nil {
			return err
		}
	} else {
		build.Critical("Buffered should return a bytes.Reader")
	}
	// Check the version number.
	if bh.Version != encryptionVersion {
		return errors.New("unknown version")
	}
	// Wrap the file in the correct streamcipher.
	archive, err = wrapReaderInCipher(f, bh, secret)
	if err != nil {
		return err
	}
	// Pipe the remaining file into the hasher to verify that the hash is
	// correct.
	h := crypto.NewHash()
	_, err = io.Copy(h, archive)
	if err != nil {
		return err
	}
	// Verify the hash.
	if !bytes.Equal(h.Sum(nil), chks[:]) {
		return errors.New("checksum doesn't match")
	}
	// Seek back to the beginning of the body.
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return err
	}
	// Wrap the file again.
	archive, err = wrapReaderInCipher(f, bh, secret)
	if err != nil {
		return err
	}
	// Wrap the potentially encrypted reader in a gzip reader.
	gzr, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}
	defer gzr.Close()
	// Wrap the gzip reader in a tar reader.
	tr := tar.NewReader(gzr)
	// Untar the files.
	return untarDir(tr, r.staticFilesDir)
}

// managedTarSiaFiles creates a tarball from the renter's siafiles and writes
// it to dst.
func (r *Renter) managedTarSiaFiles(tw *tar.Writer) error {
	// Walk over all the siafiles and add them to the tarball.
	return filepath.Walk(r.staticFilesDir, func(path string, info os.FileInfo, err error) error {
		// This error is non-nil if filepath.Walk couldn't stat a file or
		// folder.
		if err != nil {
			return err
		}
		// Nothing to do for non-folders and non-siafiles.
		if !info.IsDir() && filepath.Ext(path) != modules.SiaFileExtension {
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
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		// If the info is a dir there is nothing more to do.
		if info.IsDir() {
			return nil
		}
		// Get the siafile.
		siaPath, err := modules.NewSiaPath(strings.TrimSuffix(relPath, modules.SiaFileExtension))
		if err != nil {
			return err
		}
		entry, err := r.staticFileSet.Open(siaPath)
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
		_, err = io.Copy(tw, sr)
		return err
	})
}

// untarDir untars the archive from src and writes the contents to dstFolder
// while preserving the relative paths within the archive.
func untarDir(tr *tar.Reader, dstFolder string) error {
	// Copy the files from the tarball to the new location.
	for {
		header, err := tr.Next()
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
		_, err = io.Copy(f, tr)

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

// wrapReaderInCipher wraps the reader r into another reader according to the
// used encryption specified in the backupHeader.
func wrapReaderInCipher(r io.Reader, bh backupHeader, secret []byte) (io.Reader, error) {
	// Check if encryption is required and wrap the archive into a cipher if
	// necessary.
	switch bh.Encryption {
	case encryptionTwofish:
		c, err := twofish.NewCipher(secret)
		if err != nil {
			return nil, err
		}
		return cipher.StreamReader{
			S: cipher.NewCTR(c, bh.IV),
			R: r,
		}, nil
	case encryptionPlaintext:
		return r, nil
	default:
		return nil, errors.New("unknown cipher")
	}
}
