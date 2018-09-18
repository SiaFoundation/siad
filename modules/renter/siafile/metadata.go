package siafile

import (
	"math"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

type (
	// metadata is the metadata of a SiaFile and is JSON encoded.
	metadata struct {
		StaticVersion   [16]byte `json:"version"`   // version of the sia file format used
		StaticFileSize  int64    `json:"filesize"`  // total size of the file
		StaticPieceSize uint64   `json:"piecesize"` // size of a single piece of the file
		LocalPath       string   `json:"localpath"` // file to the local copy of the file used for repairing
		SiaPath         string   `json:"siapath"`   // the path of the file on the Sia network

		// fields for encryption
		StaticMasterKey      []byte            `json:"masterkey"` // masterkey used to encrypt pieces
		StaticMasterKeyType  crypto.CipherType `json:"masterkeytype"`
		StaticSharingKey     []byte            `json:"sharingkey"` // key used to encrypt shared pieces
		StaticSharingKeyType crypto.CipherType `json:"sharingkeytype"`

		// The following fields are the usual unix timestamps of files.
		ModTime    time.Time `json:"modtime"`    // time of last content modification
		ChangeTime time.Time `json:"changetime"` // time of last metadata modification
		AccessTime time.Time `json:"accesstime"` // time of last access
		CreateTime time.Time `json:"createtime"` // time of file creation

		// File ownership/permission fields.
		Mode os.FileMode `json:"mode"` // unix filemode of the sia file - uint32
		UID  int         `json:"uid"`  // id of the user who owns the file
		Gid  int         `json:"gid"`  // id of the group that owns the file

		// staticChunkMetadataSize is the amount of space allocated within the
		// siafile for the metadata of a single chunk. It allows us to do
		// random access operations on the file in constant time.
		StaticChunkMetadataSize uint64 `json:"chunkmetadatasize"`

		// The following fields are the offsets for data that is written to disk
		// after the pubKeyTable. We reserve a generous amount of space for the
		// table and extra fields, but we need to remember those offsets in case we
		// need to resize later on.
		//
		// chunkOffset is the offset of the first chunk, forced to be a factor of
		// 4096, default 4kib
		//
		// pubKeyTableOffset is the offset of the publicKeyTable within the
		// file.
		//
		ChunkOffset       int64 `json:"chunkoffset"`
		PubKeyTableOffset int64 `json:"pubkeytableoffset"`
	}
)

// AccessTime returns the AccessTime timestamp of the file.
func (sf *SiaFile) AccessTime() time.Time {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return sf.staticMetadata.AccessTime
}

// ChangeTime returns the ChangeTime timestamp of the file.
func (sf *SiaFile) ChangeTime() time.Time {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return sf.staticMetadata.ChangeTime
}

// CreateTime returns the CreateTime timestamp of the file.
func (sf *SiaFile) CreateTime() time.Time {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return sf.staticMetadata.CreateTime
}

// ChunkSize returns the size of a single chunk of the file.
func (sf *SiaFile) ChunkSize(chunkIndex uint64) uint64 {
	return sf.staticChunkSize(chunkIndex)
}

// Delete removes the file from disk and marks it as deleted. Once the file is
// deleted, certain methods should return an error.
func (sf *SiaFile) Delete() error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	update := sf.createDeleteUpdate()
	err := sf.createAndApplyTransaction(update)
	sf.deleted = true
	return err
}

// Deleted indicates if this file has been deleted by the user.
func (sf *SiaFile) Deleted() bool {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return sf.deleted
}

// Expiration returns the lowest height at which any of the file's contracts
// will expire.
func (sf *SiaFile) Expiration(contracts map[string]modules.RenterContract) types.BlockHeight {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	if len(sf.pubKeyTable) == 0 {
		return 0
	}

	lowest := ^types.BlockHeight(0)
	for _, pk := range sf.pubKeyTable {
		contract, exists := contracts[string(pk.Key)]
		if !exists {
			continue
		}
		if contract.EndHeight < lowest {
			lowest = contract.EndHeight
		}
	}
	return lowest
}

// HostPublicKeys returns all the public keys of hosts the file has ever been
// uploaded to. That means some of those hosts might no longer be in use.
func (sf *SiaFile) HostPublicKeys() []types.SiaPublicKey {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return sf.pubKeyTable
}

// LocalPath returns the path of the local data of the file.
func (sf *SiaFile) LocalPath() string {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return sf.staticMetadata.LocalPath
}

// MasterKey returns the masterkey used to encrypt the file.
func (sf *SiaFile) MasterKey() crypto.CipherKey {
	sk, err := crypto.NewSiaKey(sf.staticMetadata.StaticMasterKeyType, sf.staticMetadata.StaticMasterKey)
	if err != nil {
		// This should never happen since the constructor of the SiaFile takes
		// a CipherKey as an argument which guarantees that it is already a
		// valid key.
		panic(errors.AddContext(err, "failed to create masterkey of siafile"))
	}
	return sk
}

// Mode returns the FileMode of the SiaFile.
func (sf *SiaFile) Mode() os.FileMode {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return sf.staticMetadata.Mode
}

// ModTime returns the ModTime timestamp of the file.
func (sf *SiaFile) ModTime() time.Time {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return sf.staticMetadata.ModTime
}

// PieceSize returns the size of a single piece of the file.
func (sf *SiaFile) PieceSize() uint64 {
	return sf.staticMetadata.StaticPieceSize
}

// Rename changes the name of the file to a new one.
func (sf *SiaFile) Rename(newSiaPath, newSiaFilePath string) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	// Create path to renamed location.
	dir, _ := filepath.Split(newSiaFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	// Create the delete update before changing the path to the new one.
	updates := []writeaheadlog.Update{sf.createDeleteUpdate()}
	// Rename file in memory.
	sf.siaFilePath = newSiaFilePath
	sf.staticMetadata.SiaPath = newSiaPath
	// Update the ChangeTime because the metadata changed.
	sf.staticMetadata.ChangeTime = time.Now()
	// Write the header to the new location.
	headerUpdate, err := sf.saveHeader()
	if err != nil {
		return err
	}
	updates = append(updates, headerUpdate...)
	// Write the chunks to the new location.
	chunksUpdate, err := sf.saveChunks()
	if err != nil {
		return err
	}
	updates = append(updates, chunksUpdate)
	// Apply updates.
	return sf.createAndApplyTransaction(updates...)
}

// SetMode sets the filemode of the sia file.
func (sf *SiaFile) SetMode(mode os.FileMode) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	sf.staticMetadata.Mode = mode
	sf.staticMetadata.ChangeTime = time.Now()

	// Save changes to metadata to disk.
	updates, err := sf.saveHeader()
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(updates...)
}

// SetLocalPath changes the local path of the file which is used to repair
// the file from disk.
func (sf *SiaFile) SetLocalPath(path string) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	sf.staticMetadata.LocalPath = path

	// Save changes to metadata to disk.
	updates, err := sf.saveHeader()
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(updates...)
}

// SiaPath returns the file's sia path.
func (sf *SiaFile) SiaPath() string {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return sf.staticMetadata.SiaPath
}

// Size returns the file's size.
func (sf *SiaFile) Size() uint64 {
	return uint64(sf.staticMetadata.StaticFileSize)
}

// UpdateAccessTime updates the AccessTime timestamp to the current time.
func (sf *SiaFile) UpdateAccessTime() error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	sf.staticMetadata.AccessTime = time.Now()

	// Save changes to metadata to disk.
	updates, err := sf.saveHeader()
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(updates...)
}

// UploadedBytes indicates how many bytes of the file have been uploaded via
// current file contracts. Note that this includes padding and redundancy, so
// uploadedBytes can return a value much larger than the file's original filesize.
func (sf *SiaFile) UploadedBytes() uint64 {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	var uploaded uint64
	for _, chunk := range sf.staticChunks {
		for _, pieceSet := range chunk.Pieces {
			// Note: we need to multiply by SectorSize here instead of
			// f.pieceSize because the actual bytes uploaded include overhead
			// from TwoFish encryption
			uploaded += uint64(len(pieceSet)) * modules.SectorSize
		}
	}
	return uploaded
}

// UploadProgress indicates what percentage of the file (plus redundancy) has
// been uploaded. Note that a file may be Available long before UploadProgress
// reaches 100%, and UploadProgress may report a value greater than 100%.
func (sf *SiaFile) UploadProgress() float64 {
	// TODO change this once tiny files are supported.
	if sf.Size() == 0 {
		return 100
	}
	uploaded := sf.UploadedBytes()
	var desired uint64
	for i := uint64(0); i < sf.NumChunks(); i++ {
		desired += modules.SectorSize * uint64(sf.ErasureCode(i).NumPieces())
	}
	return math.Min(100*(float64(uploaded)/float64(desired)), 100)
}

// ChunkSize returns the size of a single chunk of the file.
func (sf *SiaFile) staticChunkSize(chunkIndex uint64) uint64 {
	return sf.staticMetadata.StaticPieceSize * uint64(sf.staticChunks[chunkIndex].staticErasureCode.MinPieces())
}
