package siafile

import (
	"os"
	"path/filepath"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

type (
	// metadata is the metadata of a SiaFile and is JSON encoded.
	metadata struct {
		StaticPagesPerChunk int8     `json:"pagesperchunk"` // number of pages reserved for storing a chunk.
		StaticVersion       [16]byte `json:"version"`       // version of the sia file format used
		StaticFileSize      int64    `json:"filesize"`      // total size of the file
		StaticPieceSize     uint64   `json:"piecesize"`     // size of a single piece of the file
		LocalPath           string   `json:"localpath"`     // file to the local copy of the file used for repairing
		SiaPath             string   `json:"siapath"`       // the path of the file on the Sia network

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
		Mode    os.FileMode `json:"mode"`    // unix filemode of the sia file - uint32
		UserID  int         `json:"userid"`  // id of the user who owns the file
		GroupID int         `json:"groupid"` // id of the group that owns the file

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

		// erasure code settings.
		//
		// StaticErasureCodeType specifies the algorithm used for erasure coding
		// chunks. Available types are:
		//   0 - Invalid / Missing Code
		//   1 - Reed Solomon Code
		//
		// erasureCodeParams specifies possible parameters for a certain
		// StaticErasureCodeType. Currently params will be parsed as follows:
		//   Reed Solomon Code - 4 bytes dataPieces / 4 bytes parityPieces
		//
		StaticErasureCodeType   [4]byte              `json:"erasurecodetype"`
		StaticErasureCodeParams [8]byte              `json:"erasurecodeparams"`
		staticErasureCode       modules.ErasureCoder // not persisted, exists for convenience
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
func (sf *SiaFile) ChunkSize() uint64 {
	return sf.staticChunkSize()
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
	// TODO - this code creates directories without metadata files.  Add
	// metadate file creation in repair by folder code when updating renter
	// redundancy
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
	chunksUpdates, err := sf.saveChunks()
	if err != nil {
		return err
	}
	updates = append(updates, chunksUpdates...)
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

// ChunkSize returns the size of a single chunk of the file.
func (sf *SiaFile) staticChunkSize() uint64 {
	return sf.staticMetadata.StaticPieceSize * uint64(sf.staticMetadata.staticErasureCode.MinPieces())
}
