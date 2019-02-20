package renter

import (
	"compress/gzip"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"

	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/errors"
)

// v137Persistence is the persistence struct of a renter that doesn't use the
// new SiaFile format yet.
type v137Persistence struct {
	MaxDownloadSpeed int64
	MaxUploadSpeed   int64
	StreamCacheSize  uint64
	Tracking         map[string]v137TrackedFile
}

// v137TrackedFile is the tracking information stored about a file on a legacy
// renter.
type v137TrackedFile struct {
	RepairPath string
}

// loadSiaFiles walks through the directory searching for siafiles and loading
// them into memory.
func (r *Renter) compatV137ConvertSiaFiles(tracking map[string]v137TrackedFile, oldContracts []modules.RenterContract) error {
	// Recursively convert all files found in renter directory.
	err := filepath.Walk(r.persistDir, func(path string, info os.FileInfo, err error) error {
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

		// Check if file was already converted.
		_, err = siafile.LoadSiaFile(path, r.wal)
		if err == nil {
			return nil
		}

		// Open the file.
		file, err := os.Open(path)
		if err != nil {
			return errors.AddContext(err, "unable to open file for conversion"+path)
		}

		// Load the file contents into the renter.
		_, err = r.compatV137loadSiaFilesFromReader(file, tracking, oldContracts)
		if err != nil {
			err = errors.AddContext(err, "unable to load v137 siafiles from reader")
			return errors.Compose(err, file.Close())
		}

		// Close the file and delete it since it was converted.
		if err := file.Close(); err != nil {
			return err
		}
		return os.Remove(path)
	})
	if err != nil {
		return err
	}
	// Cleanup folders in the renter subdir.
	fis, err := ioutil.ReadDir(r.persistDir)
	if err != nil {
		return err
	}
	for _, fi := range fis {
		// Ignore files.
		if !fi.IsDir() {
			continue
		}
		// Skip siafiles and contracts folders.
		if fi.Name() == modules.SiapathRoot || fi.Name() == "contracts" {
			continue
		}
		// Delete the folder.
		if err := os.RemoveAll(filepath.Join(r.persistDir, fi.Name())); err != nil {
			return err
		}
	}
	return nil
}

// compatV137LoadSiaFilesFromReader reads .sia data from reader and registers
// the contained files in the renter. It returns the nicknames of the loaded
// files.
func (r *Renter) compatV137loadSiaFilesFromReader(reader io.Reader, tracking map[string]v137TrackedFile, oldContracts []modules.RenterContract) ([]string, error) {
	// read header
	var header [15]byte
	var version string
	var numFiles uint64
	err := encoding.NewDecoder(reader, encoding.DefaultAllocLimit).DecodeAll(
		&header,
		&version,
		&numFiles,
	)
	if err != nil {
		return nil, errors.AddContext(err, "unable to read header")
	} else if header != shareHeader {
		return nil, ErrBadFile
	} else if version != shareVersion {
		return nil, ErrIncompatible
	}

	// Create decompressor.
	unzip, err := gzip.NewReader(reader)
	if err != nil {
		return nil, errors.AddContext(err, "unable to create gzip decompressor")
	}
	dec := encoding.NewDecoder(unzip, 100e6)

	// Read each file.
	files := make([]*file, numFiles)
	for i := range files {
		files[i] = new(file)
		err := dec.Decode(files[i])
		if err != nil {
			return nil, errors.AddContext(err, "unable to decode file")
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
		// Figure out the repair path.
		var repairPath string
		tf, ok := tracking[f.name]
		if ok {
			repairPath = tf.RepairPath
		}
		// Create and add a siadir to the SiaDirSet if one has not been created
		sd, errDir := r.staticDirSet.NewSiaDir(filepath.Dir(f.name))
		if errDir != nil && errDir != siadir.ErrPathOverload {
			errDir = errors.AddContext(errDir, "unable to create new sia dir")
			return nil, errors.Compose(err, errDir)
		}
		if errDir != siadir.ErrPathOverload {
			err = errors.Compose(err, sd.Close())
		}
		// fileToSiaFile adds siafile to the SiaFileSet so it does not need to
		// be returned here
		entry, err := r.fileToSiaFile(f, repairPath, oldContracts)
		if err != nil {
			return nil, errors.AddContext(err, "unable to transform old file to new file")
		}
		names[i] = f.name
		err = errors.Compose(err, entry.Close())
	}
	return names, err
}

// convertPersistVersionFrom133To140 upgrades a legacy persist file to the next
// version, converting legacy SiaFiles in the process.
func (r *Renter) convertPersistVersionFrom133To140(path string, oldContracts []modules.RenterContract) error {
	metadata := persist.Metadata{
		Header:  settingsMetadata.Header,
		Version: persistVersion133,
	}
	p := v137Persistence{
		Tracking: make(map[string]v137TrackedFile),
	}

	err := persist.LoadJSON(metadata, &p, path)
	if err != nil {
		return errors.AddContext(err, "could not load json")
	}
	metadata.Version = persistVersion140
	// Load potential legacy SiaFiles.
	if err := r.compatV137ConvertSiaFiles(p.Tracking, oldContracts); err != nil {
		return errors.AddContext(err, "conversion from v137 failed")
	}
	err = persist.SaveJSON(metadata, p, path)
	if err != nil {
		return errors.AddContext(err, "could not save json")
	}
	return nil
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
