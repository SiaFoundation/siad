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
		StaticPagesPerChunk uint8           `json:"pagesperchunk"` // number of pages reserved for storing a chunk.
		StaticVersion       [16]byte        `json:"version"`       // version of the sia file format used
		StaticFileSize      int64           `json:"filesize"`      // total size of the file
		StaticPieceSize     uint64          `json:"piecesize"`     // size of a single piece of the file
		LocalPath           string          `json:"localpath"`     // file to the local copy of the file used for repairing
		SiaPath             modules.SiaPath `json:"siapath"`       // the path of the file on the Sia network

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

		// Repair loop fields
		//
		// Health is the worst health of the file's unstuck chunks and
		// represents the percent of redundancy missing
		//
		// LastHealthCheckTime is the timestamp of the last time the SiaFile's
		// health was checked by Health()
		//
		// NumStuckChunks is the number of all the SiaFile's chunks that have
		// been marked as stuck by the repair loop
		//
		// RecentRepairTime is the timestamp of the last time the file was added
		// to the repair heap for repair
		//
		// Redundancy is the cached value of the last time the file's redundancy
		// was checked
		//
		// StuckHealth is the worst health of any of the file's stuck chunks
		//
		Health              float64   `json:"health"`
		LastHealthCheckTime time.Time `json:"lasthealthchecktime"`
		NumStuckChunks      uint64    `json:"numstuckchunks"`
		RecentRepairTime    time.Time `json:"recentrepairtime"`
		Redundancy          float64   `json:"redundancy"`
		StuckHealth         float64   `json:"stuckhealth"`

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

	// BubbledMetadata is the metadata of a siafile that gets bubbled
	BubbledMetadata struct {
		Health              float64
		LastHealthCheckTime time.Time
		ModTime             time.Time
		NumStuckChunks      uint64
		RecentRepairTime    time.Time
		Redundancy          float64
		Size                uint64
		StuckHealth         float64
	}

	// CachedHealthMetadata is a healper struct that contains the siafile health
	// metadata fields that are cached
	CachedHealthMetadata struct {
		Health      float64
		Redundancy  float64
		StuckHealth float64
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

// LastHealthCheckTime returns the LastHealthCheckTime timestamp of the file
func (sf *SiaFile) LastHealthCheckTime() time.Time {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	return sf.staticMetadata.LastHealthCheckTime
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

// NumStuckChunks returns the Number of Stuck Chunks recorded in the file's
// metadata
func (sf *SiaFile) NumStuckChunks() uint64 {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return sf.staticMetadata.NumStuckChunks
}

// PieceSize returns the size of a single piece of the file.
func (sf *SiaFile) PieceSize() uint64 {
	return sf.staticMetadata.StaticPieceSize
}

// RecentRepairTime returns  the RecentRepairTime timestamp of the file
func (sf *SiaFile) RecentRepairTime() time.Time {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	return sf.staticMetadata.RecentRepairTime
}

// Rename changes the name of the file to a new one.
func (sf *SiaFile) Rename(newSiaPath modules.SiaPath, newSiaFilePath string) error {
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
	headerUpdate, err := sf.saveHeaderUpdates()
	if err != nil {
		return err
	}
	updates = append(updates, headerUpdate...)
	// Write the chunks to the new location.
	chunksUpdates, err := sf.saveChunksUpdates()
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
	updates, err := sf.saveMetadataUpdates()
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
	updates, err := sf.saveMetadataUpdates()
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(updates...)
}

// SiaPath returns the file's sia path.
func (sf *SiaFile) SiaPath() modules.SiaPath {
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
	updates, err := sf.saveMetadataUpdates()
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(updates...)
}

// UpdateLastHealthCheckTime updates the LastHealthCheckTime timestamp to the
// current time.
func (sf *SiaFile) UpdateLastHealthCheckTime() error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	sf.staticMetadata.LastHealthCheckTime = time.Now()
	// Save changes to metadata to disk.
	updates, err := sf.saveMetadataUpdates()
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(updates...)
}

// UpdateCachedHealthMetadata updates the siafile metadata fields that are the
// cached health values
func (sf *SiaFile) UpdateCachedHealthMetadata(metadata CachedHealthMetadata) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	// Update the number of stuck chunks
	var numStuckChunks uint64
	for _, chunk := range sf.staticChunks {
		if chunk.Stuck {
			numStuckChunks++
		}
	}
	sf.staticMetadata.Health = metadata.Health
	sf.staticMetadata.NumStuckChunks = numStuckChunks
	sf.staticMetadata.Redundancy = metadata.Redundancy
	sf.staticMetadata.StuckHealth = metadata.StuckHealth
	// Save changes to metadata to disk.
	updates, err := sf.saveMetadataUpdates()
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(updates...)
}

// UpdateRecentRepairTime updates the RecentRepairTime timestamp to the current
// time.
func (sf *SiaFile) UpdateRecentRepairTime() error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	sf.staticMetadata.RecentRepairTime = time.Now()

	// Save changes to metadata to disk.
	updates, err := sf.saveMetadataUpdates()
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(updates...)
}

// staticChunkSize returns the size of a single chunk of the file.
func (sf *SiaFile) staticChunkSize() uint64 {
	return sf.staticMetadata.StaticPieceSize * uint64(sf.staticMetadata.staticErasureCode.MinPieces())
}
