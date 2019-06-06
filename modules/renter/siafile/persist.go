package siafile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

var (
	// errUnknownSiaFileUpdate is returned when applyUpdates finds an update
	// that is unknown
	errUnknownSiaFileUpdate = errors.New("unknown siafile update")
)

// ApplyUpdates is a wrapper for applyUpdates that uses the production
// dependencies.
func ApplyUpdates(updates ...writeaheadlog.Update) error {
	return applyUpdates(modules.ProdDependencies, updates...)
}

// LoadSiaFile is a wrapper for loadSiaFile that uses the production
// dependencies.
func LoadSiaFile(path string, wal *writeaheadlog.WAL) (*SiaFile, error) {
	return loadSiaFile(path, wal, modules.ProdDependencies)
}

// LoadSiaFileFromReader allows loading a SiaFile from a different location that
// directly from disk as long as the source satisfies the SiaFileSource
// interface.
func LoadSiaFileFromReader(r io.ReadSeeker, path string, wal *writeaheadlog.WAL) (*SiaFile, error) {
	return loadSiaFileFromReader(r, path, wal, modules.ProdDependencies)
}

// LoadSiaFileMetadata is a wrapper for loadSiaFileMetadata that uses the
// production dependencies.
func LoadSiaFileMetadata(path string) (Metadata, error) {
	return loadSiaFileMetadata(path, modules.ProdDependencies)
}

// SetSiaFilePath sets the path of the siafile on disk.
func (sf *SiaFile) SetSiaFilePath(path string) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	sf.siaFilePath = path
}

// applyUpdates applies a number of writeaheadlog updates to the corresponding
// SiaFile. This method can apply updates from different SiaFiles and should
// only be run before the SiaFiles are loaded from disk right after the startup
// of siad. Otherwise we might run into concurrency issues.
//
// TODO - Does this need to be updated to handle deletion better? If a file is
// deleted and then there is a second update that update would fail because the
// file is gone. However that one expected failure would cause the rest of the
// updates not to return?
func applyUpdates(deps modules.Dependencies, updates ...writeaheadlog.Update) error {
	for _, u := range updates {
		err := func() error {
			switch u.Name {
			case updateDeleteName:
				return readAndApplyDeleteUpdate(deps, u)
			case updateInsertName:
				return readAndApplyInsertUpdate(deps, u)
			default:
				return errUnknownSiaFileUpdate
			}
		}()
		if err != nil {
			fmt.Println()
			fmt.Println("OMG ERR siafile applyUpdates:", err)
			fmt.Println()
			return errors.AddContext(err, "failed to apply update")
		}
	}
	return nil
}

// createDeleteUpdate is a helper method that creates a writeaheadlog for
// deleting a file.
func createDeleteUpdate(path string) writeaheadlog.Update {
	return writeaheadlog.Update{
		Name:         updateDeleteName,
		Instructions: []byte(path),
	}
}

// loadSiaFile loads a SiaFile from disk.
func loadSiaFile(path string, wal *writeaheadlog.WAL, deps modules.Dependencies) (*SiaFile, error) {
	// Open the file.
	f, err := deps.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return loadSiaFileFromReader(f, path, wal, deps)
}

// loadSiaFileFromReader allows loading a SiaFile from a different location that
// directly from disk as long as the source satisfies the SiaFileSource
// interface.
func loadSiaFileFromReader(r io.ReadSeeker, path string, wal *writeaheadlog.WAL, deps modules.Dependencies) (*SiaFile, error) {
	// Create the SiaFile
	sf := &SiaFile{
		deps:        deps,
		siaFilePath: path,
		wal:         wal,
	}
	// defer sf.checkPubkeyOffsets()
	// Load the metadata.
	decoder := json.NewDecoder(r)
	err := decoder.Decode(&sf.staticMetadata)
	if err != nil {
		return nil, errors.AddContext(err, "failed to decode metadata")
	}
	// COMPATv137 legacy files might not have a unique id.
	if sf.staticMetadata.UniqueID == "" {
		sf.staticMetadata.UniqueID = uniqueID()
	}
	// Create the erasure coder.
	sf.staticMetadata.staticErasureCode, err = unmarshalErasureCoder(sf.staticMetadata.StaticErasureCodeType, sf.staticMetadata.StaticErasureCodeParams)
	if err != nil {
		return nil, err
	}
	// COMPATv140 legacy 0-byte files might not have correct cached fields since we
	// never update them once they are created.
	if sf.staticMetadata.FileSize == 0 {
		ec := sf.staticMetadata.staticErasureCode
		sf.staticMetadata.CachedHealth = 0
		sf.staticMetadata.CachedStuckHealth = 0
		sf.staticMetadata.CachedRedundancy = float64(ec.NumPieces()) / float64(ec.MinPieces())
		sf.staticMetadata.CachedUploadProgress = 100
	}
	// Load the pubKeyTable.
	pubKeyTableLen := sf.staticMetadata.ChunkOffset - sf.staticMetadata.PubKeyTableOffset
	if pubKeyTableLen < 0 {
		return nil, fmt.Errorf("pubKeyTableLen is %v, can't load file", pubKeyTableLen)
	}
	rawPubKeyTable := make([]byte, pubKeyTableLen)
	if _, err := r.Seek(sf.staticMetadata.PubKeyTableOffset, io.SeekStart); err != nil {
		return nil, errors.AddContext(err, "failed to seek to pubKeyTable")
	}
	if _, err := r.Read(rawPubKeyTable); err != nil {
		return nil, errors.AddContext(err, "failed to read pubKeyTable from disk")
	}
	sf.pubKeyTable, err = unmarshalPubKeyTable(rawPubKeyTable)
	if err != nil {
		return nil, errors.AddContext(err, "failed to unmarshal pubKeyTable")
	}
	// Seek to the start of the chunks.
	off, err := r.Seek(sf.staticMetadata.ChunkOffset, io.SeekStart)
	if err != nil {
		return nil, err
	}
	// Sanity check that the offset is page aligned.
	if off%pageSize != 0 {
		return nil, errors.New("chunkOff is not page aligned")
	}
	// Load the chunks.
	chunkBytes := make([]byte, int(sf.staticMetadata.StaticPagesPerChunk)*pageSize)
	for {
		n, err := r.Read(chunkBytes)
		if n == 0 && err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		// fmt.Println("calling unmarshalChunk for", path)
		chunk, err := unmarshalChunk(uint32(sf.staticMetadata.staticErasureCode.NumPieces()), chunkBytes)
		if err != nil {
			return nil, err
		}
		sf.chunks = append(sf.chunks, chunk)
	}
	// sf.stuckChunkCheck()
	return sf, nil
}

// loadSiaFileMetadata loads only the metadata of a SiaFile from disk.
func loadSiaFileMetadata(path string, deps modules.Dependencies) (md Metadata, err error) {
	// Open the file.
	f, err := deps.Open(path)
	if err != nil {
		return Metadata{}, err
	}
	defer f.Close()
	// Load the metadata.
	decoder := json.NewDecoder(f)
	if err = decoder.Decode(&md); err != nil {
		return
	}
	// Create the erasure coder.
	md.staticErasureCode, err = unmarshalErasureCoder(md.StaticErasureCodeType, md.StaticErasureCodeParams)
	if err != nil {
		return
	}
	return
}

// readAndApplyDeleteUpdate reads the delete update and applies it. This helper
// assumes that the file is not open
func readAndApplyDeleteUpdate(deps modules.Dependencies, update writeaheadlog.Update) error {
	err := deps.RemoveFile(readDeleteUpdate(update))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// readAndApplyInsertupdate reads the insert update and applies it. This helper
// assumes that the file is not open and so should only be called on start up
// before any siafiles are loaded from disk
func readAndApplyInsertUpdate(deps modules.Dependencies, update writeaheadlog.Update) error {
	// Decode update.
	path, index, data, err := readInsertUpdate(update)
	if err != nil {
		return err
	}

	// Open the file.
	f, err := deps.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write data.
	if n, err := f.WriteAt(data, index); err != nil {
		return err
	} else if n < len(data) {
		return fmt.Errorf("update was only applied partially - %v / %v", n, len(data))
	}
	// Sync file.
	return f.Sync()
}

// readDeleteUpdate unmarshals the update's instructions and returns the
// encoded path.
func readDeleteUpdate(update writeaheadlog.Update) string {
	return string(update.Instructions)
}

// readInsertUpdate unmarshals the update's instructions and returns the path, index
// and data encoded in the instructions.
func readInsertUpdate(update writeaheadlog.Update) (path string, index int64, data []byte, err error) {
	if !IsSiaFileUpdate(update) {
		err = errors.New("readUpdate can't read non-SiaFile update")
		build.Critical(err)
		return
	}
	err = encoding.UnmarshalAll(update.Instructions, &path, &index, &data)
	return
}

// allocateHeaderPage allocates a new page for the metadata and publicKeyTable.
// It returns an update that moves the chunkData back by one pageSize if
// applied and also updates the ChunkOffset of the metadata.
func (sf *SiaFile) allocateHeaderPage() (writeaheadlog.Update, error) {
	// Sanity check the chunk offset.
	if sf.staticMetadata.ChunkOffset%pageSize != 0 {
		build.Critical("the chunk offset is not page aligned")
	}
	// Open the file.
	f, err := sf.deps.OpenFile(sf.siaFilePath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return writeaheadlog.Update{}, errors.AddContext(err, "failed to open siafile")
	}
	defer f.Close()
	// Seek the chunk offset.
	_, err = f.Seek(sf.staticMetadata.ChunkOffset, io.SeekStart)
	if err != nil {
		return writeaheadlog.Update{}, err
	}
	// Read all the chunk data.
	chunkData, err := ioutil.ReadAll(f)
	if err != nil {
		return writeaheadlog.Update{}, err
	}
	// Move the offset back by a pageSize.
	sf.staticMetadata.ChunkOffset += pageSize

	// Create and return update.
	return sf.createInsertUpdate(sf.staticMetadata.ChunkOffset, chunkData), nil
}

// applyUpdates applies updates to the SiaFile. Only updates that belong to the
// SiaFile on which applyUpdates is called can be applied. Everything else will
// be considered a developer error and cause the update to not be applied to
// avoid corruption.  applyUpdates also syncs the SiaFile for convenience since
// it already has an open file handle.
func (sf *SiaFile) applyUpdates(updates ...writeaheadlog.Update) (err error) {
	// Sanity check that file hasn't been deleted.
	if sf.deleted {
		return errors.New("can't call applyUpdates on deleted file")
	}

	// If the set of updates contains a delete, all updates prior to that delete
	// are irrelevant, so perform the last delete and then process the remaining
	// updates. This also prevents a bug on Windows where we attempt to delete
	// the file while holding a open file handle.
	for i := len(updates) - 1; i >= 0; i-- {
		u := updates[i]
		switch u.Name {
		case updateDeleteName:
			if err := readAndApplyDeleteUpdate(sf.deps, u); err != nil {
				fmt.Println()
				fmt.Println("OMG ERR siafile.applyUpdates applying delete update:", err)
				fmt.Println()
				return err
			}
			updates = updates[i+1:]
			break
		default:
			continue
		}
	}
	if len(updates) == 0 {
		return nil
	}

	// Create the path if it doesn't exist yet.
	if err = os.MkdirAll(filepath.Dir(sf.siaFilePath), 0700); err != nil {
		fmt.Println()
		fmt.Println("OMG ERR siafile.applyUpdates creating directory:", err)
		fmt.Println()
		return err
	}
	// Create and/or open the file.
	f, err := sf.deps.OpenFile(sf.siaFilePath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		fmt.Println()
		fmt.Println("OMG ERR siafile.applyUpdates opening file:", err)
		fmt.Println()
		return err
	}
	defer func() {
		if err == nil {
			// If no error occurred we sync and close the file.
			err = errors.Compose(f.Sync(), f.Close())
		} else {
			// Otherwise we still need to close the file.
			err = errors.Compose(err, f.Close())
		}
		if err != nil {
			fmt.Println()
			fmt.Println("OMG ERR siafile.applyUpdates in defer func:", err)
			fmt.Println()
		}
	}()

	// Apply updates.
	for _, u := range updates {
		err := func() error {
			switch u.Name {
			case updateDeleteName:
				// Sanity check: all of the updates should be insert updates.
				build.Critical("Unexpected non-insert update", u.Name)
				return nil
			case updateInsertName:
				return sf.readAndApplyInsertUpdate(f, u)
			default:
				return errUnknownSiaFileUpdate
			}
		}()
		if err != nil {
			fmt.Println()
			fmt.Println("OMG ERR siafile.applyUpdates applying updates:", err)
			fmt.Println()
			return errors.AddContext(err, "failed to apply update")
		}
	}
	return nil
}

// chunkOffset returns the offset of a marshaled chunk withint the file.
func (sf *SiaFile) chunkOffset(chunkIndex int) int64 {
	if chunkIndex < 0 {
		panic("chunk index can't be negative")
	}
	return sf.staticMetadata.ChunkOffset + int64(chunkIndex)*int64(sf.staticMetadata.StaticPagesPerChunk)*pageSize
}

// createAndApplyTransaction is a helper method that creates a writeaheadlog
// transaction and applies it.
func (sf *SiaFile) createAndApplyTransaction(updates ...writeaheadlog.Update) error {
	s := fmt.Sprintf("Calling CreateAndApplyTransaction with %v updates for %v\n", len(updates), sf.siaFilePath)
	metadataUpdate := false
	for i, u := range updates {
		path, index, _, err := readInsertUpdate(u)
		if err != nil {
			panic(err)
		}
		if index == 0 {
			if len(updates) == 1 {
				metadataUpdate = true
			}
			// Metadata update
			s = s + fmt.Sprintf("Update %v is a %v for the metadata for %v\n", i, u.Name, path)
		} else if int(index) > len(sf.pubKeyTable) {
			// Chunk update
			s = s + fmt.Sprintf("Update %v is a %v for a chunk for %v\n", i, u.Name, path)
		} else {

			s = s + fmt.Sprintf("Update %v is a %v for something else for %v\n", i, u.Name, path)
		}
	}
	// fmt.Println(s)
	// check info in memory and on disk before write
	sf.stuckChunkAndPubKeyCheck(nil)
	sf2, err := LoadSiaFile(sf.siaFilePath, sf.wal)
	if err != nil && !os.IsNotExist(err) {
		panic(errors.AddContext(err, "error on loading siafile after applying updates"))
	} else if err == nil {
		sf2.stuckChunkAndPubKeyCheck(sf)
	}
	// Check memory vs disk
	if sf2 != nil && metadataUpdate {
		var panicE error
		if sf.staticMetadata.NumStuckChunks != sf2.staticMetadata.NumStuckChunks {
			fmt.Printf("About to Panic: in Memory file pointer %p %v\n", sf, sf.siaFilePath)
			panicE = fmt.Errorf("in memory %v doesn't match on disk %v NumStuckChunks", sf.staticMetadata.NumStuckChunks, sf2.staticMetadata.NumStuckChunks)
		}
		for i, c := range sf.chunks {
			if c.Stuck != sf2.chunks[i].Stuck {
				fmt.Printf("About to Panic: in Memory file pointer %p %v\n", sf, sf.siaFilePath)
				panicE = errors.Compose(panicE, fmt.Errorf("in memory %v doesn't match on disk %v chunk stuck", c.Stuck, sf2.chunks[i].Stuck))
			}
		}
		if panicE != nil {
			build.Critical(panicE)
		}
	}

	// Check memory and disk after write
	defer func() {
		sf2, err := LoadSiaFile(sf.siaFilePath, sf.wal)
		if err != nil && !os.IsNotExist(err) {
			panic(errors.AddContext(err, "error on loading siafile after applying updates"))
		} else if err == nil {
			// check memory vs disk
			// if sf2 != nil && metadataUpdate {
			var panicE error
			if sf.staticMetadata.NumStuckChunks != sf2.staticMetadata.NumStuckChunks {
				fmt.Printf("About to Panic: in Memory file pointer %p %v\n", sf, sf.siaFilePath)
				panicE = fmt.Errorf("in memory %v doesn't match on disk %v NumStuckChunks", sf.staticMetadata.NumStuckChunks, sf2.staticMetadata.NumStuckChunks)
			}
			for i, c := range sf.chunks {
				if c.Stuck != sf2.chunks[i].Stuck {
					fmt.Printf("About to Panic: in Memory file pointer %p %v\n", sf, sf.siaFilePath)
					panicE = errors.Compose(panicE, fmt.Errorf("in memory %v doesn't match on disk %v chunk stuck", c.Stuck, sf2.chunks[i].Stuck))
				}
			}
			if panicE != nil {
				build.Critical(panicE)
			}
			// }
			sf2.stuckChunkAndPubKeyCheck(sf)
		}
	}()
	defer sf.stuckChunkAndPubKeyCheck(nil)

	// Sanity check that file hasn't been deleted.
	if sf.deleted {
		return errors.New("can't call createAndApplyTransaction on deleted file")
	}
	if len(updates) == 0 {
		return nil
	}
	// Create the writeaheadlog transaction.
	txn, err := sf.wal.NewTransaction(updates)
	if err != nil {
		fmt.Println()
		fmt.Println("OMG ERR NewTransaction:", err)
		fmt.Println()
		return errors.AddContext(err, "failed to create wal txn")
	}
	// No extra setup is required. Signal that it is done.
	if err := <-txn.SignalSetupComplete(); err != nil {
		fmt.Println()
		fmt.Println("OMG ERR SignalSetupComplete:", err)
		fmt.Println()
		return errors.AddContext(err, "failed to signal setup completion")
	}
	// Apply the updates.
	if err := sf.applyUpdates(updates...); err != nil {
		fmt.Println()
		fmt.Println("OMG ERR apply updates:", err)
		fmt.Println()
		return errors.AddContext(err, "failed to apply updates")
	}
	// Updates are applied. Let the writeaheadlog know.
	if err := txn.SignalUpdatesApplied(); err != nil {
		fmt.Println()
		fmt.Println("OMG ERR SignalUpdatesApplied:", err)
		fmt.Println()
		return errors.AddContext(err, "failed to signal that updates are applied")
	}

	return nil
}

// createDeleteUpdate is a helper method that creates a writeaheadlog for
// deleting a file.
func (sf *SiaFile) createDeleteUpdate() writeaheadlog.Update {
	return createDeleteUpdate(sf.siaFilePath)
}

// createInsertUpdate is a helper method which creates a writeaheadlog update for
// writing the specified data to the provided index. It is usually not called
// directly but wrapped into another helper that creates an update for a
// specific part of the SiaFile. e.g. the metadata
func (sf *SiaFile) createInsertUpdate(index int64, data []byte) writeaheadlog.Update {
	if index < 0 {
		index = 0
		data = []byte{}
		build.Critical("index passed to createUpdate should never be negative")
	}
	// Create update
	// return writeaheadlog.Update{
	update := writeaheadlog.Update{
		Name:         updateInsertName,
		Instructions: encoding.MarshalAll(sf.siaFilePath, index, data),
	}
	_, indexr, datar, err := readInsertUpdate(update)
	if err != nil {
		build.Critical("error reading insert update", err)
	}
	if index != indexr {
		build.Critical("indexes not equal", index, indexr)
	}
	if bytes.Compare(data, datar) != 0 {
		build.Critical("data in update not equal", data, datar)
	}
	return update
}

// readAndApplyInsertUpdate reads the insert update for a SiaFile and then
// applies it
func (sf *SiaFile) readAndApplyInsertUpdate(f modules.File, update writeaheadlog.Update) error {
	// Decode update.
	path, index, data, err := readInsertUpdate(update)
	if err != nil {
		return err
	}

	// Sanity check path. Update should belong to SiaFile.
	if sf.siaFilePath != path {
		build.Critical(fmt.Sprintf("can't apply update for file %s to SiaFile %s", path, sf.siaFilePath))
		return nil
	}

	// Write data.
	if n, err := f.WriteAt(data, index); err != nil {
		return err
	} else if n < len(data) {
		return fmt.Errorf("update was only applied partially - %v / %v", n, len(data))
	}
	return nil
}

// saveFile saves the whole SiaFile atomically.
func (sf *SiaFile) saveFile() error {
	// Sanity check that file hasn't been deleted.
	if sf.deleted {
		return errors.New("can't call saveFile on deleted file")
	}
	headerUpdates, err := sf.saveHeaderUpdates()
	if err != nil {
		return errors.AddContext(err, "failed to to create save header updates")
	}
	chunksUpdates := sf.saveChunksUpdates()
	err = sf.createAndApplyTransaction(append(headerUpdates, chunksUpdates...)...)
	// sf2, err := LoadSiaFile(sf.siaFilePath, sf.wal)
	// if err != nil {
	// 	panic("not what should be happending")
	// }
	return errors.AddContext(err, "failed to apply saveFile updates")
}

// saveChunkUpdate creates a writeaheadlog update that saves a single marshaled chunk
// to disk when applied.
func (sf *SiaFile) saveChunkUpdate(chunkIndex int) writeaheadlog.Update {
	offset := sf.chunkOffset(chunkIndex)
	chunkBytes := marshalChunk(sf.chunks[chunkIndex])
	return sf.createInsertUpdate(offset, chunkBytes)
}

// saveChunksUpdates creates writeaheadlog updates which save the marshaled chunks of
// the SiaFile to disk when applied.
func (sf *SiaFile) saveChunksUpdates() []writeaheadlog.Update {
	// Marshal all the chunks and create updates for them.
	updates := make([]writeaheadlog.Update, 0, len(sf.chunks))
	for chunkIndex := range sf.chunks {
		update := sf.saveChunkUpdate(chunkIndex)
		updates = append(updates, update)
	}
	return updates
}

// saveHeaderUpdates creates writeaheadlog updates to saves the metadata and
// pubKeyTable of the SiaFile to disk using the writeaheadlog. If the metadata
// and overlap due to growing too large and would therefore corrupt if they
// were written to disk, a new page is allocated.
func (sf *SiaFile) saveHeaderUpdates() ([]writeaheadlog.Update, error) {
	// Create a list of updates which need to be applied to save the metadata.
	var updates []writeaheadlog.Update

	// Marshal the pubKeyTable.
	pubKeyTable, err := marshalPubKeyTable(sf.pubKeyTable)
	if err != nil {
		return nil, errors.AddContext(err, "failed to marshal pubkey table")
	}

	// Update the pubKeyTableOffset. This is not necessarily the final offset
	// but we need to marshal the metadata with this new offset to see if the
	// metadata and the pubKeyTable overlap.
	sf.staticMetadata.PubKeyTableOffset = sf.staticMetadata.ChunkOffset - int64(len(pubKeyTable))

	// Marshal the metadata.
	metadata, err := marshalMetadata(sf.staticMetadata)
	if err != nil {
		return nil, errors.AddContext(err, "failed to marshal metadata")
	}

	// If the metadata and the pubKeyTable overlap, we need to allocate a new
	// page for them. Afterwards we need to marshal the metadata again since
	// ChunkOffset and PubKeyTableOffset change when allocating a new page.
	for int64(len(metadata))+int64(len(pubKeyTable)) > sf.staticMetadata.ChunkOffset {
		// Create update to move chunkData back by a page.
		chunkUpdate, err := sf.allocateHeaderPage()
		if err != nil {
			return nil, errors.AddContext(err, "failed to allocate new header page")
		}
		updates = append(updates, chunkUpdate)
		// Update the PubKeyTableOffset.
		sf.staticMetadata.PubKeyTableOffset = sf.staticMetadata.ChunkOffset - int64(len(pubKeyTable))
		// Marshal the metadata again.
		metadata, err = marshalMetadata(sf.staticMetadata)
		if err != nil {
			return nil, errors.AddContext(err, "failed to marshal metadata again")
		}
	}

	// Create updates for the metadata and pubKeyTable.
	updates = append(updates, sf.createInsertUpdate(0, metadata))
	updates = append(updates, sf.createInsertUpdate(sf.staticMetadata.PubKeyTableOffset, pubKeyTable))
	return updates, nil
}

// saveMetadataUpdates saves the metadata of the SiaFile but not the
// publicKeyTable.  Most of the time updates are only made to the metadata and
// not to the publicKeyTable and the metadata fits within a single disk sector
// on the harddrive. This means that using saveMetadataUpdate instead of
// saveHeader is potentially faster for SiaFiles with a header that can not be
// marshaled within a single page.
func (sf *SiaFile) saveMetadataUpdates() ([]writeaheadlog.Update, error) {
	// Marshal the pubKeyTable.
	pubKeyTable, err := marshalPubKeyTable(sf.pubKeyTable)
	if err != nil {
		return nil, err
	}
	// Sanity check the length of the pubKeyTable to find out if the length of
	// the table changed. We should never just save the metadata if the table
	// changed as well as it might lead to corruptions.
	if sf.staticMetadata.PubKeyTableOffset+int64(len(pubKeyTable)) != sf.staticMetadata.ChunkOffset {
		build.Critical("never call saveMetadata if the pubKeyTable changed, call saveHeader instead")
		return sf.saveHeaderUpdates()
	}
	// Marshal the metadata.
	metadata, err := marshalMetadata(sf.staticMetadata)
	if err != nil {
		return nil, err
	}
	// If the header doesn't fit in the space between the beginning of the file
	// and the pubKeyTable, we need to call saveHeader since the pubKeyTable
	// needs to be moved as well and saveHeader is already handling that
	// edgecase.
	if int64(len(metadata)) > sf.staticMetadata.PubKeyTableOffset {
		fmt.Println()
		fmt.Println("Calling save header instead of save metadata")
		fmt.Println()
		return sf.saveHeaderUpdates()
	}
	// Otherwise we can create and return the updates.
	return []writeaheadlog.Update{sf.createInsertUpdate(0, metadata)}, nil
}
