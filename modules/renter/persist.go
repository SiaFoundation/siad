package renter

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

const (
	logFile = modules.RenterDir + ".log"
	// PersistFilename is the filename to be used when persisting renter
	// information to a JSON file
	PersistFilename = "renter.json"
	// SiaDirMetadata is the name of the metadata file for the sia directory
	SiaDirMetadata = ".siadir"
	// walFile is the filename of the renter's writeaheadlog's file.
	walFile = modules.RenterDir + ".wal"
)

var (
	//ErrBadFile is an error when a file does not qualify as .sia file
	ErrBadFile = errors.New("not a .sia file")
	// ErrIncompatible is an error when file is not compatible with current
	// version
	ErrIncompatible = errors.New("file is not compatible with current version")
	// ErrNoNicknames is an error when no nickname is given
	ErrNoNicknames = errors.New("at least one nickname must be supplied")
	// ErrNonShareSuffix is an error when the suffix of a file does not match
	// the defined share extension
	ErrNonShareSuffix = errors.New("suffix of file must be " + siafile.ShareExtension)

	settingsMetadata = persist.Metadata{
		Header:  "Renter Persistence",
		Version: persistVersion,
	}

	shareHeader  = [15]byte{'S', 'i', 'a', ' ', 'S', 'h', 'a', 'r', 'e', 'd', ' ', 'F', 'i', 'l', 'e'}
	shareVersion = "0.4"

	// Persist Version Numbers
	persistVersion040 = "0.4"
	persistVersion133 = "1.3.3"
)

type (
	// persist contains all of the persistent renter data.
	persistence struct {
		MaxDownloadSpeed int64
		MaxUploadSpeed   int64
		StreamCacheSize  uint64
	}
)

// createBubbleHealthUpdate is a helper method that creates a writeaheadlog for
// bubbling up the health of a directory.
func createBubbleHealthUpdate(siaPath string) writeaheadlog.Update {
	return writeaheadlog.Update{
		Name:         updateBubbleHealthName,
		Instructions: []byte(siaPath),
	}
}

// isBubbleHealthUpdate is a helper method that makes sure that a wal update is
// a renter bubble health update
func isBubbleHealthUpdate(update writeaheadlog.Update) bool {
	switch update.Name {
	case updateBubbleHealthName:
		return true
	default:
		return false
	}
}

// readBubbleHealthUpdate unmarshals the update's instructions and returns the
// encoded path. Errors will be considered developer errors
func readBubbleHealthUpdate(update writeaheadlog.Update) string {
	if !isBubbleHealthUpdate(update) {
		err := errors.New("update is not bubble health update")
		build.Critical(err)
		return ""
	}
	return string(update.Instructions)
}

// MarshalSia implements the encoding.SiaMarshaller interface, writing the
// file data to w.
func (f *file) MarshalSia(w io.Writer) error {
	enc := encoding.NewEncoder(w)

	// encode easy fields
	err := enc.EncodeAll(
		f.name,
		f.size,
		f.masterKey,
		f.pieceSize,
		f.mode,
	)
	if err != nil {
		return err
	}
	// COMPATv0.4.3 - encode the bytesUploaded and chunksUploaded fields
	// TODO: the resulting .sia file may confuse old clients.
	err = enc.EncodeAll(f.pieceSize*f.numChunks()*uint64(f.erasureCode.NumPieces()), f.numChunks())
	if err != nil {
		return err
	}

	// encode erasureCode
	switch code := f.erasureCode.(type) {
	case *siafile.RSCode:
		err = enc.EncodeAll(
			"Reed-Solomon",
			uint64(code.MinPieces()),
			uint64(code.NumPieces()-code.MinPieces()),
		)
		if err != nil {
			return err
		}
	default:
		if build.DEBUG {
			panic("unknown erasure code")
		}
		return errors.New("unknown erasure code")
	}
	// encode contracts
	if err := enc.Encode(uint64(len(f.contracts))); err != nil {
		return err
	}
	for _, c := range f.contracts {
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	return nil
}

// UnmarshalSia implements the encoding.SiaUnmarshaler interface,
// reconstructing a file from the encoded bytes read from r.
func (f *file) UnmarshalSia(r io.Reader) error {
	dec := encoding.NewDecoder(r)

	// COMPATv0.4.3 - decode bytesUploaded and chunksUploaded into dummy vars.
	var bytesUploaded, chunksUploaded uint64

	// Decode easy fields.
	err := dec.DecodeAll(
		&f.name,
		&f.size,
		&f.masterKey,
		&f.pieceSize,
		&f.mode,
		&bytesUploaded,
		&chunksUploaded,
	)
	if err != nil {
		return err
	}
	f.staticUID = persist.RandomSuffix()

	// Decode erasure coder.
	var codeType string
	if err := dec.Decode(&codeType); err != nil {
		return err
	}
	switch codeType {
	case "Reed-Solomon":
		var nData, nParity uint64
		err = dec.DecodeAll(
			&nData,
			&nParity,
		)
		if err != nil {
			return err
		}
		rsc, err := siafile.NewRSCode(int(nData), int(nParity))
		if err != nil {
			return err
		}
		f.erasureCode = rsc
	default:
		return errors.New("unrecognized erasure code type: " + codeType)
	}

	// Decode contracts.
	var nContracts uint64
	if err := dec.Decode(&nContracts); err != nil {
		return err
	}
	f.contracts = make(map[types.FileContractID]fileContract)
	var contract fileContract
	for i := uint64(0); i < nContracts; i++ {
		if err := dec.Decode(&contract); err != nil {
			return err
		}
		f.contracts[contract.ID] = contract
	}
	return nil
}

// saveSync stores the current renter data to disk and then syncs to disk.
func (r *Renter) saveSync() error {
	return persist.SaveJSON(settingsMetadata, r.persist, filepath.Join(r.persistDir, PersistFilename))
}

// load fetches the saved renter data from disk.
func (r *Renter) loadSettings() error {
	r.persist = persistence{}
	err := persist.LoadJSON(settingsMetadata, &r.persist, filepath.Join(r.persistDir, PersistFilename))
	if os.IsNotExist(err) {
		// No persistence yet, set the defaults and continue.
		r.persist.MaxDownloadSpeed = DefaultMaxDownloadSpeed
		r.persist.MaxUploadSpeed = DefaultMaxUploadSpeed
		r.persist.StreamCacheSize = DefaultStreamCacheSize
		err = r.saveSync()
		if err != nil {
			return err
		}
	} else if err == persist.ErrBadVersion {
		// Outdated version, try the 040 to 133 upgrade.
		err = convertPersistVersionFrom040To133(filepath.Join(r.persistDir, PersistFilename))
		if err != nil {
			// Nothing left to try.
			return err
		}
		// Re-load the settings now that the file has been upgraded.
		return r.loadSettings()
	} else if err != nil {
		return err
	}

	// Set the bandwidth limits on the contractor, which was already initialized
	// without bandwidth limits.
	return r.setBandwidthLimits(r.persist.MaxDownloadSpeed, r.persist.MaxUploadSpeed)
}

// loadSharedFiles reads .sia data from reader and registers the contained
// files in the renter. It returns the nicknames of the loaded files.
func (r *Renter) loadSharedFiles(reader io.Reader, repairPath string) ([]string, error) {
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
			exists, err := r.staticFileSet.Exists(files[i].name)
			if err != nil {
				return nil, err
			}
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
		siafilePath := filepath.Join(r.filesDir, f.name)
		entry, err := r.fileToSiaFile(f, siafilePath)
		if err != nil {
			return nil, err
		}
		names[i] = f.name
		err = errors.Compose(err, entry.Close())
	}
	// TODO Save the file in the new format.
	return names, err
}

// initPersist handles all of the persistence initialization, such as creating
// the persistence directory and starting the logger.
func (r *Renter) initPersist() error {
	// Create the persist and files directories if they do not yet exist.
	err := os.MkdirAll(r.filesDir, 0700)
	if err != nil {
		return err
	}

	// Initialize the logger.
	r.log, err = persist.NewFileLogger(filepath.Join(r.persistDir, logFile))
	if err != nil {
		return err
	}

	// Load the prior persistence structures.
	err = r.loadSettings()
	if err != nil {
		return err
	}

	// Initialize the writeaheadlog.
	txns, wal, err := writeaheadlog.New(filepath.Join(r.persistDir, walFile))
	if err != nil {
		return err
	}
	r.wal = wal
	r.staticFileSet = siafile.NewSiaFileSet(r.filesDir, wal)
	r.staticDirSet = siadir.NewSiaDirSet(r.filesDir, wal)

	// Apply unapplied wal txns.
	for _, txn := range txns {
		applyTxn := true
		for _, update := range txn.Updates {
			if siafile.IsSiaFileUpdate(update) {
				if err := siafile.ApplyUpdates(update); err != nil {
					return errors.AddContext(err, "failed to apply SiaFile update")
				}
			} else if siadir.IsSiaDirUpdate(update) {
				if err := siadir.ApplyUpdates(update); err != nil {
					return errors.AddContext(err, "failed to apply SiaDir update")
				}
			} else if isBubbleHealthUpdate(update) {
				if err := r.managedApplyBubbleUpdate(update); err != nil {
					return errors.AddContext(err, "failed to apply bubble update")
				}
			} else {
				applyTxn = false
			}
		}
		if applyTxn {
			if err := txn.SignalUpdatesApplied(); err != nil {
				return err
			}
		}
	}

	return nil
}

// LoadSharedFiles loads a .sia file into the renter. It returns the nicknames
// of the loaded files.
func (r *Renter) LoadSharedFiles(filename string) ([]string, error) {
	lockID := r.mu.Lock()
	defer r.mu.Unlock(lockID)

	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return r.loadSharedFiles(file, filename)
}

// LoadSharedFilesASCII loads an ASCII-encoded .sia file into the renter. It
// returns the nicknames of the loaded files.
func (r *Renter) LoadSharedFilesASCII(asciiSia string) ([]string, error) {
	lockID := r.mu.Lock()
	defer r.mu.Unlock(lockID)

	dec := base64.NewDecoder(base64.URLEncoding, bytes.NewBufferString(asciiSia))
	return r.loadSharedFiles(dec, "")
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

// managedApplyBubbleUpdate applies the Bubble Health wal updates
func (r *Renter) managedApplyBubbleUpdate(update writeaheadlog.Update) error {
	// Read update
	siaPath := readBubbleHealthUpdate(update)

	// Bubble Health
	return r.BubbleHealth(siaPath)
}
