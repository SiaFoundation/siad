package siafile

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// ApplyUpdates applies a number of writeaheadlog updates to the corresponding
// SiaFile. This method can apply updates from different SiaFiles and should
// only be run before the SiaFiles are loaded from disk right after the startup
// of siad. Otherwise we might run into concurrency issues.
func ApplyUpdates(updates ...writeaheadlog.Update) error {
	for _, u := range updates {
		err := func() error {
			// Check if it is a delete update.
			if u.Name == updateDeleteName {
				if err := os.Remove(readDeleteUpdate(u)); os.IsNotExist(err) {
					return nil
				} else if err != nil {
					return err
				}
			}

			// Decode update.
			path, index, data, err := readInsertUpdate(u)
			if err != nil {
				return err
			}

			// Open the file.
			f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
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
		}()
		if err != nil {
			return errors.AddContext(err, "failed to apply update")
		}
	}
	return nil
}

// LoadSiaFile loads a SiaFile from disk.
func LoadSiaFile(path string, wal *writeaheadlog.WAL) (*SiaFile, error) {
	// Create the SiaFile
	sf := &SiaFile{
		staticUID:   hex.EncodeToString(fastrand.Bytes(8)),
		siaFilePath: path,
		wal:         wal,
	}
	// Open the file.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// Load the metadata.
	decoder := json.NewDecoder(f)
	if err := decoder.Decode(&sf.staticMetadata); err != nil {
		return nil, errors.AddContext(err, "failed to decode metadata")
	}
	// Load the pubKeyTable.
	pubKeyTableLen := sf.staticMetadata.ChunkOffset - sf.staticMetadata.PubKeyTableOffset
	if pubKeyTableLen < 0 {
		return nil, fmt.Errorf("pubKeyTableLen is %v, can't load file", pubKeyTableLen)
	}
	rawPubKeyTable := make([]byte, pubKeyTableLen)
	if _, err := f.ReadAt(rawPubKeyTable, sf.staticMetadata.PubKeyTableOffset); err != nil {
		return nil, errors.AddContext(err, "failed to read pubKeyTable from disk")
	}
	sf.pubKeyTable, err = unmarshalPubKeyTable(rawPubKeyTable)
	if err != nil {
		return nil, errors.AddContext(err, "failed to unmarshal pubKeyTable")
	}
	// Load the chunks.
	_, err = f.Seek(sf.staticMetadata.ChunkOffset, io.SeekStart)
	if err != nil {
		return nil, errors.AddContext(err, "failed to seek to chunkOffset")
	}
	rawChunks, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, errors.AddContext(err, "failed to read chunks from disk")
	}
	sf.staticChunks, err = unmarshalChunks(rawChunks)
	if err != nil {
		return nil, errors.AddContext(err, "failed to unmarshal chunks")
	}
	return sf, nil
}

// marshalChunks marshals the chunks of the SiaFile using json encoding.
func marshalChunks(chunks []chunk) ([]byte, error) {
	// Encode the chunks.
	jsonChunks, err := json.Marshal(chunks)
	if err != nil {
		return nil, err
	}
	return jsonChunks, nil
}

// marshalErasureCoder marshals an erasure coder into its type and params.
func marshalErasureCoder(ec modules.ErasureCoder) ([4]byte, [8]byte) {
	// Since we only support one type we assume it is ReedSolomon for now.
	ecType := ecReedSolomon
	// Read params from ec.
	ecParams := [8]byte{}
	binary.LittleEndian.PutUint32(ecParams[:4], uint32(ec.MinPieces()))
	binary.LittleEndian.PutUint32(ecParams[4:], uint32(ec.NumPieces()-ec.MinPieces()))
	return ecType, ecParams
}

// marshalMetadata marshals the metadata of the SiaFile using json encoding.
func marshalMetadata(md metadata) ([]byte, error) {
	// Encode the metadata.
	jsonMD, err := json.Marshal(md)
	if err != nil {
		return nil, err
	}
	return jsonMD, nil
}

// marshalPubKeyTable marshals the public key table of the SiaFile using Sia
// encoding.
func marshalPubKeyTable(pubKeyTable []types.SiaPublicKey) ([]byte, error) {
	// Create a buffer.
	buf := bytes.NewBuffer(nil)
	// Marshal all the data into the buffer
	for _, pk := range pubKeyTable {
		if err := pk.MarshalSia(buf); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// unmarshalChunks unmarshals the json encoded chunks of the SiaFile.
func unmarshalChunks(raw []byte) (chunks []chunk, err error) {
	err = json.Unmarshal(raw, &chunks)
	if err != nil {
		return
	}
	// We also need to create the modules.ErasureCoder object for each chunk.
	for i, chunk := range chunks {
		ec, err := unmarshalErasureCoder(chunk.StaticErasureCodeType, chunk.StaticErasureCodeParams)
		if err != nil {
			return nil, err
		}
		chunks[i].staticErasureCode = ec
	}
	return
}

// unmarshalErasureCoder unmarshals an ErasureCoder from the given params.
func unmarshalErasureCoder(ecType [4]byte, ecParams [8]byte) (modules.ErasureCoder, error) {
	if ecType != ecReedSolomon {
		return nil, errors.New("unknown erasure code type")
	}
	dataPieces := int(binary.LittleEndian.Uint32(ecParams[:4]))
	parityPieces := int(binary.LittleEndian.Uint32(ecParams[4:]))
	return NewRSCode(dataPieces, parityPieces)
}

// unmarshalMetadata unmarshals the json encoded metadata of the SiaFile.
func unmarshalMetadata(raw []byte) (md metadata, err error) {
	err = json.Unmarshal(raw, &md)
	return
}

// unmarshalPubKeyTable unmarshals a sia encoded public key table.
func unmarshalPubKeyTable(raw []byte) ([]types.SiaPublicKey, error) {
	// Create the buffer.
	r := bytes.NewBuffer(raw)
	var err error
	var spks []types.SiaPublicKey
	// Unmarshal the keys one by one until EOF or a different error occur.
	for {
		var spk types.SiaPublicKey
		if err = spk.UnmarshalSia(r); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		spks = append(spks, spk)
	}
	return spks, nil
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
		panic("readUpdate can't read non-SiaFile update")
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
	f, err := os.Open(sf.siaFilePath)
	if err != nil {
		return writeaheadlog.Update{}, err
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
// be considered a developer error and cause a panic to avoid corruption.
// applyUpdates also syncs the SiaFile for convenience since it already has an
// open file handle.
func (sf *SiaFile) applyUpdates(updates ...writeaheadlog.Update) (err error) {
	// Open the file.
	f, err := os.OpenFile(sf.siaFilePath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer func() {
		if err == nil {
			// If no error occured we sync and close the file.
			err = errors.Compose(f.Sync(), f.Close())
		} else {
			// Otherwise we still need to close the file.
			err = errors.Compose(err, f.Close())
		}
	}()

	// Apply updates.
	for _, u := range updates {
		err := func() error {
			// Check if it is a delete update.
			if u.Name == updateDeleteName {
				err := os.Remove(readDeleteUpdate(u))
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			// Decode update.
			path, index, data, err := readInsertUpdate(u)
			if err != nil {
				return err
			}

			// Sanity check path. Update should belong to SiaFile.
			if sf.siaFilePath != path {
				panic(fmt.Sprintf("can't apply update for file %s to SiaFile %s", path, sf.siaFilePath))
			}

			// Write data.
			if n, err := f.WriteAt(data, index); err != nil {
				return err
			} else if n < len(data) {
				return fmt.Errorf("update was only applied partially - %v / %v", n, len(data))
			}
			return nil
		}()
		if err != nil {
			return errors.AddContext(err, "failed to apply update")
		}
	}
	return nil
}

// createAndApplyTransaction is a helper method that creates a writeaheadlog
// transaction and applies it.
func (sf *SiaFile) createAndApplyTransaction(updates ...writeaheadlog.Update) error {
	// This should never be called on a deleted file.
	if sf.deleted {
		return errors.New("shouldn't apply udates on deleted file")
	}
	// Create the writeaheadlog transaction.
	txn, err := sf.wal.NewTransaction(updates)
	if err != nil {
		return errors.AddContext(err, "failed to create wal txn")
	}
	// No extra setup is required. Signal that it is done.
	if err := <-txn.SignalSetupComplete(); err != nil {
		return errors.AddContext(err, "failed to signal setup completion")
	}
	// Apply the updates.
	if err := sf.applyUpdates(updates...); err != nil {
		return errors.AddContext(err, "failed to apply updates")
	}

	// Updates are applied. Let the writeaheadlog know.
	return errors.AddContext(err, "failed to signal that updates are applied")
}

// createDeleteUpdate is a helper method that creates a writeaheadlog for
// deleting a file.
func (sf *SiaFile) createDeleteUpdate() writeaheadlog.Update {
	return writeaheadlog.Update{
		Name:         updateDeleteName,
		Instructions: []byte(sf.siaFilePath),
	}
}

// createInsertUpdate is a helper method which creates a writeaheadlog update for
// writing the specified data to the provided index. It is usually not called
// directly but wrapped into another helper that creates an update for a
// specific part of the SiaFile. e.g. the metadata
func (sf *SiaFile) createInsertUpdate(index int64, data []byte) writeaheadlog.Update {
	if index < 0 {
		panic("index passed to createUpdate should never be negative")
	}
	// Create update
	return writeaheadlog.Update{
		Name:         updateInsertName,
		Instructions: encoding.MarshalAll(sf.siaFilePath, index, data),
	}
}

// saveFile saves the whole SiaFile atomically.
func (sf *SiaFile) saveFile() error {
	headerUpdates, err := sf.saveHeader()
	if err != nil {
		return err
	}
	chunksUpdate, err := sf.saveChunks()
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(append(headerUpdates, chunksUpdate)...)
}

// saveChunks creates a writeaheadlog update that saves the marshaled chunks of
// the SiaFile to disk when applied.
func (sf *SiaFile) saveChunks() (writeaheadlog.Update, error) {
	chunks, err := marshalChunks(sf.staticChunks)
	if err != nil {
		return writeaheadlog.Update{}, errors.AddContext(err, "failed to marshal chunks")
	}
	return sf.createInsertUpdate(sf.staticMetadata.ChunkOffset, chunks), nil
}

// saveHeader creates writeaheadlog updates to saves the metadata and
// pubKeyTable of the SiaFile to disk using the writeaheadlog. If the metadata
// and overlap due to growing too large and would therefore corrupt if they
// were written to disk, a new page is allocated.
func (sf *SiaFile) saveHeader() ([]writeaheadlog.Update, error) {
	// Create a list of updates which need to be applied to save the metadata.
	var updates []writeaheadlog.Update

	// Marshal the pubKeyTable.
	pubKeyTable, err := marshalPubKeyTable(sf.pubKeyTable)
	if err != nil {
		return nil, err
	}

	// Update the pubKeyTableOffset. This is not necessarily the final offset
	// but we need to marshal the metadata with this new offset to see if the
	// metadata and the pubKeyTable overlap.
	sf.staticMetadata.PubKeyTableOffset = sf.staticMetadata.ChunkOffset - int64(len(pubKeyTable))

	// Marshal the metadata.
	metadata, err := marshalMetadata(sf.staticMetadata)
	if err != nil {
		return nil, err
	}

	// If the metadata and the pubKeyTable overlap, we need to allocate a new
	// page for them. Afterwards we need to marshal the metadata again since
	// ChunkOffset and PubKeyTableOffset change when allocating a new page.
	for int64(len(metadata))+int64(len(pubKeyTable)) > sf.staticMetadata.ChunkOffset {
		// Create update to move chunkData back by a page.
		chunkUpdate, err := sf.allocateHeaderPage()
		if err != nil {
			return nil, err
		}
		updates = append(updates, chunkUpdate)
		// Update the PubKeyTableOffset.
		sf.staticMetadata.PubKeyTableOffset = sf.staticMetadata.ChunkOffset - int64(len(pubKeyTable))
		// Marshal the metadata again.
		metadata, err = marshalMetadata(sf.staticMetadata)
		if err != nil {
			return nil, err
		}
	}

	// Create updates for the metadata and pubKeyTable.
	updates = append(updates, sf.createInsertUpdate(0, metadata))
	updates = append(updates, sf.createInsertUpdate(sf.staticMetadata.PubKeyTableOffset, pubKeyTable))
	return updates, nil
}

// saveMetadata saves the metadata of the SiaFile but not the publicKeyTable.
// Most of the time updates are only made to the metadata and not to the
// publicKeyTable and the metadata fits within a single disk sector on the
// harddrive. This means that using saveMetadata instead of saveHeader is
// potentially faster for SiaFiles with a header that can not be marshaled
// within a single page.
func (sf *SiaFile) saveMetadata() ([]writeaheadlog.Update, error) {
	// Marshal the pubKeyTable.
	pubKeyTable, err := marshalPubKeyTable(sf.pubKeyTable)
	if err != nil {
		return nil, err
	}
	// Sanity check the length of the pubKeyTable to find out if the length of
	// the table changed. We should never just save the metadata if the table
	// changed as well as it might lead to corruptions.
	if sf.staticMetadata.PubKeyTableOffset+int64(len(pubKeyTable)) != sf.staticMetadata.ChunkOffset {
		panic("never call saveMetadata if the pubKeyTable changed, call saveHeader instead")
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
		return sf.saveHeader()
	}
	// Otherwise we can create and return the updates.
	return []writeaheadlog.Update{sf.createInsertUpdate(0, metadata)}, nil
}
