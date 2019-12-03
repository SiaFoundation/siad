package renter

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"path/filepath"
	"strings"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// createExportedFile appends the siafile at the specified siaPath to the
// tar.Writer.
func (r *Renter) createExportedFile(w *tar.Writer, siaPath modules.SiaPath) error {
	// Open SiaFile.
	entry, err := r.staticFileSystem.OpenSiaFile(siaPath)
	if err != nil {
		return errors.AddContext(err, "failed to open siafile for snapshotting")
	}
	defer entry.Close()
	// Get snapshot reader.
	sr, err := entry.SnapshotReader()
	if err != nil {
		return errors.AddContext(err, "failed to get snapshot reader")
	}
	defer sr.Close()
	info, err := sr.Stat()
	if err != nil {
		return err
	}
	// Create the header for the file.
	header, err := tar.FileInfoHeader(info, info.Name())
	if err != nil {
		return err
	}
	siaDirPath, err := siaPath.Dir()
	if err != nil {
		return err
	}
	siaFilePath := r.staticFileSystem.FilePath(siaPath)
	siaDirSysPath := r.staticFileSystem.DirPath(siaDirPath)
	header.Name = strings.TrimPrefix(siaFilePath, siaDirSysPath)
	header.Name = strings.TrimPrefix(header.Name, string(filepath.Separator))
	// Write the header first.
	if err := w.WriteHeader(header); err != nil {
		return err
	}
	// Write the file next.
	_, err = io.Copy(w, sr)
	return err
}

// Export creates an exported file or folder of the file or folder at the
// specified path.
func (r *Renter) Export(w io.Writer, siaPath modules.SiaPath) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	gzw := gzip.NewWriter(w)
	defer gzw.Close()
	tw := tar.NewWriter(gzw)
	defer tw.Close()

	// TODO: check for dir and handle appropriately.
	return errors.AddContext(r.createExportedFile(tw, siaPath), "createExportedFile failed")
}
