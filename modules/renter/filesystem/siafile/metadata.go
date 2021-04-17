package siafile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

type (
	// PartialChunkInfo contains all the essential information about a partial
	// chunk relevant to SiaFiles. A SiaFile with a partial chunk may contain 1 or
	// 2 PartialChunkInfos since the partial chunk might be split between 2
	// combined chunks.
	PartialChunkInfo struct {
		ID     modules.CombinedChunkID `json:"id"`     // ID of the combined chunk
		Index  uint64                  `json:"index"`  // Index of the combined chunk within partialsSiaFile
		Offset uint64                  `json:"offset"` // Offset of partial chunk within combined chunk
		Length uint64                  `json:"length"` // Length of partial chunk within combined chunk
		Status uint8                   `json:"status"` // Status of combined chunk
	}

	// SiafileUID is a unique identifier for siafile which is used to track
	// siafiles even after renaming them.
	SiafileUID string

	// Metadata is the metadata of a SiaFile and is JSON encoded.
	// Note: Methods which update the metadata and can potentially fail after
	// doing so and before persisting the change should use backup() and
	// restore() to restore the metadata before returning the error. Also
	// changes to Metadata require backup() and restore() to be updated as well.
	Metadata struct {
		UniqueID SiafileUID `json:"uniqueid"` // unique identifier for file

		StaticPagesPerChunk uint8    `json:"pagesperchunk"` // number of pages reserved for storing a chunk.
		StaticVersion       [16]byte `json:"version"`       // version of the sia file format used
		FileSize            int64    `json:"filesize"`      // total size of the file
		StaticPieceSize     uint64   `json:"piecesize"`     // size of a single piece of the file
		LocalPath           string   `json:"localpath"`     // file to the local copy of the file used for repairing

		// Fields for encryption
		StaticMasterKey      []byte            `json:"masterkey"` // masterkey used to encrypt pieces
		StaticMasterKeyType  crypto.CipherType `json:"masterkeytype"`
		StaticSharingKey     []byte            `json:"sharingkey"` // key used to encrypt shared pieces
		StaticSharingKeyType crypto.CipherType `json:"sharingkeytype"`

		// Fields for partial uploads
		DisablePartialChunk bool               `json:"disablepartialchunk"` // determines whether the file should be treated like legacy files
		PartialChunks       []PartialChunkInfo `json:"partialchunks"`       // information about the partial chunk.
		HasPartialChunk     bool               `json:"haspartialchunk"`     // indicates whether this file is supposed to have a partial chunk or not

		// The following fields are the usual unix timestamps of files.
		ModTime    time.Time `json:"modtime"`    // time of last content modification
		ChangeTime time.Time `json:"changetime"` // time of last metadata modification
		AccessTime time.Time `json:"accesstime"` // time of last access
		CreateTime time.Time `json:"createtime"` // time of file creation

		// Cached fields. These fields are cached fields and are only meant to be used
		// to create FileInfos for file related API endpoints. There is no guarantee
		// that these fields are up-to-date. Neither in memory nor on disk. Updates to
		// these fields aren't persisted immediately. Instead they will only be
		// persisted whenever another method persists the metadata or when the SiaFile
		// is closed.
		//
		// CachedRedundancy is the redundancy of the file on the network and is
		// updated within the 'Redundancy' method which is periodically called by the
		// repair code.
		//
		// CachedUserRedundancy is the redundancy of the file on the network as
		// visible to the user and is updated within the 'Redundancy' method which is
		// periodically called by the repair code.
		//
		// CachedHealth is the health of the file on the network and is also
		// periodically updated by the health check loop whenever 'Health' is called.
		//
		// CachedStuckHealth is the health of the stuck chunks of the file. It is
		// updated by the health check loop. CachedExpiration is the lowest height at
		// which any of the file's contracts will expire. Also updated periodically by
		// the health check loop whenever 'Health' is called.
		//
		// CachedUploadedBytes is the number of bytes of the file that have been
		// uploaded to the network so far. Is updated every time a piece is added to
		// the siafile.
		//
		// CachedUploadProgress is the upload progress of the file and is updated
		// every time a piece is added to the siafile.
		CachedRedundancy     float64           `json:"cachedredundancy"`
		CachedRepairBytes    uint64            `json:"cachedrepairbytes"`
		CachedUserRedundancy float64           `json:"cacheduserredundancy"`
		CachedHealth         float64           `json:"cachedhealth"`
		CachedNumStuckChunks uint64            `json:"cachednumstuckchunks"`
		CachedStuckBytes     uint64            `json:"cachedstuckbytes"`
		CachedStuckHealth    float64           `json:"cachedstuckhealth"`
		CachedExpiration     types.BlockHeight `json:"cachedexpiration"`
		CachedUploadedBytes  uint64            `json:"cacheduploadedbytes"`
		CachedUploadProgress float64           `json:"cacheduploadprogress"`

		// Repair loop fields
		//
		// Health is the worst health of the file's unstuck chunks and
		// represents the percent of redundancy missing
		//
		// LastHealthCheckTime is the timestamp of the last time the SiaFile's
		// health was checked by Health()
		//
		// NumStuckChunks is the number of all the SiaFile's chunks that have
		// been marked as stuck by the repair loop. This doesn't include a potential
		// partial chunk at the end of the file though. Use 'numStuckChunks()' for
		// that instead.
		//
		// Redundancy is the cached value of the last time the file's redundancy
		// was checked
		//
		// StuckHealth is the worst health of any of the file's stuck chunks
		Health              float64   `json:"health"`
		LastHealthCheckTime time.Time `json:"lasthealthchecktime"`
		NumStuckChunks      uint64    `json:"numstuckchunks"`
		Redundancy          float64   `json:"redundancy"`
		RepairBytes         uint64    `json:"repairbytes"`
		StuckHealth         float64   `json:"stuckhealth"`
		StuckBytes          uint64    `json:"stuckbytes"`

		// File ownership/permission fields.
		Mode    os.FileMode `json:"mode"`    // unix filemode of the sia file - uint32
		UserID  int32       `json:"userid"`  // id of the user who owns the file
		GroupID int32       `json:"groupid"` // id of the group that owns the file

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
		OnDisk              bool
		Redundancy          float64
		RepairBytes         uint64
		Size                uint64
		StuckBytes          uint64
		StuckHealth         float64
		UID                 SiafileUID
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

// PartialChunks returns the partial chunk infos of the siafile.
func (sf *SiaFile) PartialChunks() []PartialChunkInfo {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return sf.staticMetadata.PartialChunks
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

// HasPartialChunk returns whether this file is supposed to have a partial chunk
// or not.
func (sf *SiaFile) HasPartialChunk() bool {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return sf.staticMetadata.HasPartialChunk
}

// LastHealthCheckTime returns the LastHealthCheckTime timestamp of the file
func (sf *SiaFile) LastHealthCheckTime() time.Time {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
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
	return sf.staticMasterKey()
}

// Metadata returns the SiaFile's metadata, resolving any fields related to
// partial chunks.
func (sf *SiaFile) Metadata() Metadata {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	md := sf.staticMetadata
	md.NumStuckChunks = sf.numStuckChunks()
	return md
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
	return sf.numStuckChunks()
}

// PieceSize returns the size of a single piece of the file.
func (sf *SiaFile) PieceSize() uint64 {
	return sf.staticMetadata.StaticPieceSize
}

// Rename changes the name of the file to a new one. To guarantee that renaming
// the file is atomic across all operating systems, we create a wal transaction
// that moves over all the chunks one-by-one and deletes the src file.
func (sf *SiaFile) Rename(newSiaFilePath string) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	return sf.rename(newSiaFilePath)
}

// backup creates a deep-copy of a Metadata.
func (md Metadata) backup() (b Metadata) {
	// Copy the static fields first. They are shallow copies since they are not
	// allowed to change.
	b.StaticPagesPerChunk = md.StaticPagesPerChunk
	b.StaticVersion = md.StaticVersion
	b.StaticPieceSize = md.StaticPieceSize
	b.StaticMasterKey = md.StaticMasterKey
	b.StaticMasterKeyType = md.StaticMasterKeyType
	b.StaticSharingKey = md.StaticSharingKey
	b.StaticSharingKeyType = md.StaticSharingKeyType
	b.StaticErasureCodeType = md.StaticErasureCodeType
	b.StaticErasureCodeParams = md.StaticErasureCodeParams
	b.staticErasureCode = md.staticErasureCode

	// Deep copy the remaining fields. For the sake of completion and safety we
	// also copy the native types one-by-one even though they could be cloned
	// together with the static types by a simple assignment like b = md
	b.UniqueID = md.UniqueID
	b.FileSize = md.FileSize
	b.LocalPath = md.LocalPath
	b.DisablePartialChunk = md.DisablePartialChunk
	b.HasPartialChunk = md.HasPartialChunk
	b.ModTime = md.ModTime
	b.ChangeTime = md.ChangeTime
	b.AccessTime = md.AccessTime
	b.CreateTime = md.CreateTime
	b.CachedRepairBytes = md.CachedRepairBytes
	b.CachedStuckBytes = md.CachedStuckBytes
	b.CachedRedundancy = md.CachedRedundancy
	b.CachedUserRedundancy = md.CachedUserRedundancy
	b.CachedHealth = md.CachedHealth
	b.CachedNumStuckChunks = md.CachedNumStuckChunks
	b.CachedStuckHealth = md.CachedStuckHealth
	b.CachedExpiration = md.CachedExpiration
	b.CachedUploadedBytes = md.CachedUploadedBytes
	b.CachedUploadProgress = md.CachedUploadProgress
	b.Health = md.Health
	b.LastHealthCheckTime = md.LastHealthCheckTime
	b.NumStuckChunks = md.NumStuckChunks
	b.RepairBytes = md.RepairBytes
	b.StuckBytes = md.StuckBytes
	b.Redundancy = md.Redundancy
	b.StuckHealth = md.StuckHealth
	b.Mode = md.Mode
	b.UserID = md.UserID
	b.GroupID = md.GroupID
	b.ChunkOffset = md.ChunkOffset
	b.PubKeyTableOffset = md.PubKeyTableOffset
	// Special handling for slice since reflect.DeepEqual is false when
	// comparing empty slice to nil.
	if md.PartialChunks == nil {
		b.PartialChunks = nil
	} else {
		// Being extra explicit about capacity and length here.
		b.PartialChunks = make([]PartialChunkInfo, len(md.PartialChunks), cap(md.PartialChunks))
		copy(b.PartialChunks, md.PartialChunks)
	}
	// If the backup was successful it should match the original.
	if build.Release == "testing" && !md.equals(b) {
		fmt.Println("md:\n", md)
		fmt.Println("b:\n", b)
		build.Critical("backup: copy doesn't match original")
	}
	return
}

// restore restores the metadata from a backup created with the backup() method.
func (md *Metadata) restore(b Metadata) {
	md.UniqueID = b.UniqueID
	md.FileSize = b.FileSize
	md.LocalPath = b.LocalPath
	md.DisablePartialChunk = b.DisablePartialChunk
	md.PartialChunks = b.PartialChunks
	md.HasPartialChunk = b.HasPartialChunk
	md.ModTime = b.ModTime
	md.ChangeTime = b.ChangeTime
	md.AccessTime = b.AccessTime
	md.CreateTime = b.CreateTime
	md.CachedRepairBytes = b.CachedRepairBytes
	md.CachedStuckBytes = b.CachedStuckBytes
	md.CachedRedundancy = b.CachedRedundancy
	md.CachedUserRedundancy = b.CachedUserRedundancy
	md.CachedHealth = b.CachedHealth
	md.CachedNumStuckChunks = b.CachedNumStuckChunks
	md.CachedStuckHealth = b.CachedStuckHealth
	md.CachedExpiration = b.CachedExpiration
	md.CachedUploadedBytes = b.CachedUploadedBytes
	md.CachedUploadProgress = b.CachedUploadProgress
	md.Health = b.Health
	md.LastHealthCheckTime = b.LastHealthCheckTime
	md.NumStuckChunks = b.NumStuckChunks
	md.RepairBytes = b.RepairBytes
	md.StuckBytes = b.StuckBytes
	md.Redundancy = b.Redundancy
	md.StuckHealth = b.StuckHealth
	md.Mode = b.Mode
	md.UserID = b.UserID
	md.GroupID = b.GroupID
	md.ChunkOffset = b.ChunkOffset
	md.PubKeyTableOffset = b.PubKeyTableOffset
	// If the backup was successful it should match the backup.
	if build.Release == "testing" && !md.equals(b) {
		fmt.Println("md:\n", md)
		fmt.Println("b:\n", b)
		build.Critical("restore: copy doesn't match original")
	}
}

// equal compares the two structs for equality by serializing them and comparing
// the serialized representations.
//
// WARNING: Do not use in production!
func (md *Metadata) equals(b Metadata) bool {
	if build.Release != "testing" {
		build.Critical("Metadata.equals used in non-testing code!")
	}
	mdBytes, err := json.Marshal(md)
	if err != nil {
		build.Critical(fmt.Sprintf("failed to marshal: %s. Problematic entity: %+v\n", err.Error(), md))
	}
	bBytes, err := json.Marshal(b)
	if err != nil {
		build.Critical(fmt.Sprintf("failed to marshal: %s. Problematic entity: %+v\n", err.Error(), b))
	}
	return bytes.Equal(mdBytes, bBytes)
}

// rename changes the name of the file to a new one. To guarantee that renaming
// the file is atomic across all operating systems, we create a wal transaction
// that moves over all the chunks one-by-one and deletes the src file.
func (sf *SiaFile) rename(newSiaFilePath string) (err error) {
	if sf.deleted {
		return errors.New("can't rename deleted siafile")
	}
	// backup the changed metadata before changing it. Revert the change on
	// error.
	oldPath := sf.siaFilePath
	defer func(backup Metadata) {
		if err != nil {
			sf.staticMetadata.restore(backup)
			sf.siaFilePath = oldPath
		}
	}(sf.staticMetadata.backup())
	// Check if file exists at new location.
	if _, err := os.Stat(newSiaFilePath); err == nil {
		return ErrPathOverload
	}
	// Create path to renamed location.
	dir, _ := filepath.Split(newSiaFilePath)
	err = os.MkdirAll(dir, 0700)
	if err != nil {
		return err
	}
	// Create the delete update before changing the path to the new one.
	updates := []writeaheadlog.Update{sf.createDeleteUpdate()}
	// Load all the chunks.
	chunks := make([]chunk, 0, sf.numChunks)
	err = sf.iterateChunksReadonly(func(chunk chunk) error {
		if _, ok := sf.isIncludedPartialChunk(uint64(chunk.Index)); ok {
			return nil // Ignore partial chunk
		}
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		return err
	}
	// Rename file in memory.
	sf.siaFilePath = newSiaFilePath
	// Update the ChangeTime because the metadata changed.
	sf.staticMetadata.ChangeTime = time.Now()
	// Write the header to the new location.
	headerUpdate, err := sf.saveHeaderUpdates()
	if err != nil {
		return err
	}
	updates = append(updates, headerUpdate...)
	// Write the chunks to the new location.
	for _, chunk := range chunks {
		updates = append(updates, sf.saveChunkUpdate(chunk))
	}
	// Apply updates.
	return createAndApplyTransaction(sf.wal, updates...)
}

// SetMode sets the filemode of the sia file.
func (sf *SiaFile) SetMode(mode os.FileMode) (err error) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	// backup the changed metadata before changing it. Revert the change on
	// error.
	defer func(backup Metadata) {
		if err != nil {
			sf.staticMetadata.restore(backup)
		}
	}(sf.staticMetadata.backup())
	sf.staticMetadata.Mode = mode
	sf.staticMetadata.ChangeTime = time.Now()

	// Save changes to metadata to disk.
	updates, err := sf.saveMetadataUpdates()
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(updates...)
}

// SetLastHealthCheckTime sets the LastHealthCheckTime in memory to the current
// time but does not update and write to disk.
//
// NOTE: This call should be used in conjunction with a method that saves the
// SiaFile metadata
func (sf *SiaFile) SetLastHealthCheckTime() {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	sf.staticMetadata.LastHealthCheckTime = time.Now()
}

// SetLocalPath changes the local path of the file which is used to repair
// the file from disk.
func (sf *SiaFile) SetLocalPath(path string) (err error) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	// backup the changed metadata before changing it. Revert the change on
	// error.
	defer func(backup Metadata) {
		if err != nil {
			sf.staticMetadata.restore(backup)
		}
	}(sf.staticMetadata.backup())

	sf.staticMetadata.LocalPath = path

	// Save changes to metadata to disk.
	updates, err := sf.saveMetadataUpdates()
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(updates...)
}

// Size returns the file's size.
func (sf *SiaFile) Size() uint64 {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return uint64(sf.staticMetadata.FileSize)
}

// UpdateUniqueID creates a new random uid for the SiaFile.
func (sf *SiaFile) UpdateUniqueID() {
	sf.staticMetadata.UniqueID = uniqueID()
}

// UpdateAccessTime updates the AccessTime timestamp to the current time.
func (sf *SiaFile) UpdateAccessTime() (err error) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	// backup the changed metadata before changing it. Revert the change on
	// error.
	defer func(backup Metadata) {
		if err != nil {
			sf.staticMetadata.restore(backup)
		}
	}(sf.staticMetadata.backup())
	sf.staticMetadata.AccessTime = time.Now()

	// Save changes to metadata to disk.
	updates, err := sf.saveMetadataUpdates()
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(updates...)
}

// numStuckChunks returns the number of stuck chunks recorded in the file's
// metadata.
func (sf *SiaFile) numStuckChunks() uint64 {
	numStuckChunks := sf.staticMetadata.NumStuckChunks
	for _, cc := range sf.staticMetadata.PartialChunks {
		stuck, err := sf.partialsSiaFile.StuckChunkByIndex(cc.Index)
		if err != nil {
			build.Critical("failed to get 'stuck' status of partial chunk")
		}
		if stuck {
			numStuckChunks++
		}
	}
	return numStuckChunks
}

// staticChunkSize returns the size of a single chunk of the file.
func (sf *SiaFile) staticChunkSize() uint64 {
	return sf.staticMetadata.StaticPieceSize * uint64(sf.staticMetadata.staticErasureCode.MinPieces())
}

// staticMasterKey returns the masterkey used to encrypt the file.
func (sf *SiaFile) staticMasterKey() crypto.CipherKey {
	sk, err := crypto.NewSiaKey(sf.staticMetadata.StaticMasterKeyType, sf.staticMetadata.StaticMasterKey)
	if err != nil {
		// This should never happen since the constructor of the SiaFile takes
		// a CipherKey as an argument which guarantees that it is already a
		// valid key.
		panic(errors.AddContext(err, "failed to create masterkey of siafile"))
	}
	return sk
}

// uniqueID creates a random unique SiafileUID.
func uniqueID() SiafileUID {
	return SiafileUID(persist.UID())
}
