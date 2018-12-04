package renter

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/errors"
)

// loadSiaFiles walks through the directory searching for siafiles and loading
// them into memory.
func (r *Renter) compatV137ConvertSiaFiles() error {
	// Recursively load all files found in renter directory. Errors
	// encountered during loading are logged, but are not considered fatal.
	return filepath.Walk(r.persistDir, func(path string, info os.FileInfo, err error) error {
		// This error is non-nil if filepath.Walk couldn't stat a file or
		// folder.
		if err != nil {
			r.log.Println("WARN: could not stat file or folder during walk:", err)
			return nil
		}

		// Skip folders and non-sia files.
		if info.IsDir() || filepath.Ext(path) != siafile.ShareExtension {
			return nil
		}

		// Open the file.
		file, err := os.Open(path)
		if err != nil {
			r.log.Println("ERROR: could not open .sia file:", err)
			return nil
		}
		defer file.Close()

		// Load the file contents into the renter.
		_, err = r.compatV137loadSiaFilesFromReader(file, path)
		if err != nil {
			r.log.Println("ERROR: could not load .sia file:", err)
			return nil
		}
		return nil
	})
}

// compatV137LoadSiaFilesFromReader reads .sia data from reader and registers
// the contained files in the renter. It returns the nicknames of the loaded
// files.
func (r *Renter) compatV137loadSiaFilesFromReader(reader io.Reader, repairPath string) ([]string, error) {
	// read header
	var header [15]byte
	var version string
	var numFiles uint64
	err := encoding.NewDecoder(reader).DecodeAll(
		&header,
		&version,
		&numFiles,
	)
	if err != nil {
		return nil, err
	} else if header != shareHeader {
		return nil, ErrBadFile
	} else if version != shareVersion {
		return nil, ErrIncompatible
	}

	// Create decompressor.
	unzip, err := gzip.NewReader(reader)
	if err != nil {
		return nil, err
	}
	dec := encoding.NewDecoder(unzip)

	// Read each file.
	files := make([]*file, numFiles)
	for i := range files {
		files[i] = new(file)
		err := dec.Decode(files[i])
		if err != nil {
			return nil, err
		}

		// Make sure the file's name does not conflict with existing files.
		dupCount := 0
		origName := files[i].name
		for {
			exists := r.staticFileSet.Exists(files[i].name)
			if !exists {
				break
			}
			dupCount++
			files[i].name = origName + "_" + strconv.Itoa(dupCount)
		}
	}

	// Add files to renter.
	names := make([]string, numFiles)
	for i, f := range files {
		// fileToSiaFile adds siafile to the SiaFileSet so it does not need to
		// be returned here
		entry, err := r.fileToSiaFile(f, repairPath)
		if err != nil {
			return nil, err
		}
		names[i] = f.name
		err = errors.Compose(err, entry.Close())
	}
	// TODO Save the file in the new format.
	return names, err
}

// convertPersistVersionFrom133To140 upgrades a legacy persist file to the next
// version, converting legacy SiaFiles in the process.
func (r *Renter) convertPersistVersionFrom133To140(path string) error {
	metadata := persist.Metadata{
		Header:  settingsMetadata.Header,
		Version: persistVersion133,
	}
	p := persistence{}

	err := persist.LoadJSON(metadata, &p, path)
	if err != nil {
		return err
	}
	metadata.Version = persistVersion140
	// Load potential legacy SiaFiles.
	if err := r.compatV137ConvertSiaFiles(); err != nil {
		return err
	}
	return persist.SaveJSON(metadata, p, path)
}

// convertPersistVersionFrom040to133 upgrades a legacy persist file to the next
// version, adding new fields with their default values.
func convertPersistVersionFrom040To133(path string) error {
	metadata := persist.Metadata{
		Header:  settingsMetadata.Header,
		Version: persistVersion040,
	}
	p := persistence{}

	err := persist.LoadJSON(metadata, &p, path)
	if err != nil {
		return err
	}
	metadata.Version = persistVersion133
	p.MaxDownloadSpeed = DefaultMaxDownloadSpeed
	p.MaxUploadSpeed = DefaultMaxUploadSpeed
	p.StreamCacheSize = DefaultStreamCacheSize
	return persist.SaveJSON(metadata, p, path)
}
