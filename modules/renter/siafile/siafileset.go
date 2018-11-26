package siafile

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// The SiaFileSet structure helps track the number of threads using a siafile
// and will enable features like caching and lazy loading to improve start up
// times and reduce memory usage. SiaFile methods such as New, Delete, Rename,
// etc should be called through the SiaFileSet to maintain atomic transactions.

type (
	// SiaFileSet is a helper struct responsible for managing the renter's
	// siafiles in memory
	SiaFileSet struct {
		siaFileMap map[string]*SiaFileSetEntry

		// utilities
		mu  sync.Mutex
		wal *writeaheadlog.WAL
	}

	// SiaFileSetEntry contains information about the threads accessing the
	// SiaFile and references to the SiaFile and the SiaFileSet
	SiaFileSetEntry struct {
		siaFile    *SiaFile
		siaFileSet *SiaFileSet
		siaPath    string // This is the siaPath used to open the file

		threadMap map[ThreadType]int

		mu sync.Mutex
	}
)

// NewSiaFileSet initializes and returns a SiaFileSet
func NewSiaFileSet() *SiaFileSet {
	return &SiaFileSet{
		siaFileMap: make(map[string]*SiaFileSetEntry),
	}
}

// Close decrements the threadType count. If the threadType count is 0 then it
// will remove the SiaFileSetEntry from the SiaFileSet map, which will remove it
// from memory
//
// NOTE: Close should use the SiaPath that is stored in the SiaFileSetEntry as
// it was used to open the file, otherwise there is risk of SiaFileSetEntries
// being stuck in the map or not being found in the map if another thread
// renames the SiaPath
func (entry *SiaFileSetEntry) Close(threadType ThreadType) error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if _, ok := entry.threadMap[threadType]; !ok {
		return ErrUnknownThread
	}
	entry.threadMap[threadType]--
	if entry.threadMap[threadType] < 0 {
		build.Critical("threadType count should never be less than zero")
	}
	if entry.threadMap[threadType] == 0 {
		delete(entry.threadMap, threadType)
	}
	if len(entry.threadMap) == 0 {
		entry.siaFileSet.mu.Lock()
		delete(entry.siaFileSet.siaFileMap, entry.siaPath)
		entry.siaFileSet.mu.Unlock()
	}
	return nil
}

// SiaFile returns the SiaFileSetEntry's SiaFile
func (entry *SiaFileSetEntry) SiaFile() *SiaFile {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.siaFile
}

// newSiaFileSetEntry initializes and returns a SiaFileSetEntry
func (sfs *SiaFileSet) newSiaFileSetEntry(sf *SiaFile, siaPath string, threadType ThreadType) *SiaFileSetEntry {
	threads := make(map[ThreadType]int)
	threads[threadType] = 0
	return &SiaFileSetEntry{
		siaPath:    siaPath,
		siaFile:    sf,
		siaFileSet: sfs,
		threadMap:  threads,
	}
}

// All returns all the siafiles in the renter by either returning them from the
// SiaFileSet of reading them from disk. The SiaFileSetEntry is closed after the
// SiaFile is added to the slice. This way the files are removed from memory by
// GC as soon as the calling method returns
//
// Note: This is currently only needed for the Files endpoint. This is an
// expensive call so it should be avoided unless absolutely necessary
func (sfs *SiaFileSet) All(dir string, threadType ThreadType) ([]*SiaFile, error) {
	var siaFiles []*SiaFile
	// Recursively load all files found in renter directory. Errors
	// are not considered fatal and are ignored.
	return siaFiles, filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		// This error is non-nil if filepath.Walk couldn't stat a file or
		// folder.
		if err != nil {
			return nil
		}

		// Skip folders and non-sia files.
		if info.IsDir() || filepath.Ext(path) != ShareExtension {
			return nil
		}

		// Load the Siafile.
		siaPath := strings.TrimSuffix(strings.TrimPrefix(path, dir), ShareExtension)
		entry, err := sfs.Open(siaPath, dir, SiaFileLoadThread)
		if err != nil {
			return nil
		}
		siaFiles = append(siaFiles, entry.siaFile)
		if err := entry.Close(SiaFileLoadThread); err != nil {
			return nil
		}
		return nil
	})
}

// AssignWAL sets the wal for the SiaFileSet
func (sfs *SiaFileSet) AssignWAL(wal *writeaheadlog.WAL) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sfs.wal = wal
}

// Delete deletes the SiaFileSetEntry's SiaFile
func (sfs *SiaFileSet) Delete(entry *SiaFileSetEntry) error {
	// Lock the SiaFileSet
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	// Lock the entry
	entry.mu.Lock()
	defer entry.mu.Unlock()
	// Check that the entry is in SiaFileSet
	_, exists := sfs.siaFileMap[entry.siaPath]
	if !exists {
		return ErrUnknownPath
	}
	// Delete SiaFile
	if err := entry.siaFile.Delete(); err != nil {
		return err
	}
	return nil
}

// NewFromFileData creates a new SiaFile from a FileData object that was
// previously created from a legacy file.
func (sfs *SiaFileSet) NewFromFileData(fd FileData, threadType ThreadType) (*SiaFileSetEntry, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	// legacy masterKeys are always twofish keys
	mk, err := crypto.NewSiaKey(crypto.TypeTwofish, fd.MasterKey[:])
	if err != nil {
		return nil, errors.AddContext(err, "failed to restore master key")
	}
	currentTime := time.Now()
	ecType, ecParams := marshalErasureCoder(fd.ErasureCode)
	file := &SiaFile{
		staticMetadata: metadata{
			AccessTime:              currentTime,
			ChunkOffset:             defaultReservedMDPages * pageSize,
			ChangeTime:              currentTime,
			CreateTime:              currentTime,
			StaticFileSize:          int64(fd.FileSize),
			LocalPath:               fd.RepairPath,
			StaticMasterKey:         mk.Key(),
			StaticMasterKeyType:     mk.Type(),
			Mode:                    fd.Mode,
			ModTime:                 currentTime,
			staticErasureCode:       fd.ErasureCode,
			StaticErasureCodeType:   ecType,
			StaticErasureCodeParams: ecParams,
			StaticPagesPerChunk:     numChunkPagesRequired(fd.ErasureCode.NumPieces()),
			StaticPieceSize:         fd.PieceSize,
			SiaPath:                 fd.Name,
		},
		deleted:        fd.Deleted,
		staticUniqueID: fd.UID,
	}
	file.staticChunks = make([]chunk, len(fd.Chunks))
	for i := range file.staticChunks {
		file.staticChunks[i].Pieces = make([][]piece, file.staticMetadata.staticErasureCode.NumPieces())
	}

	// Populate the pubKeyTable of the file and add the pieces.
	pubKeyMap := make(map[string]uint32)
	for chunkIndex, chunk := range fd.Chunks {
		for pieceIndex, pieceSet := range chunk.Pieces {
			for _, p := range pieceSet {
				// Check if we already added that public key.
				tableOffset, exists := pubKeyMap[string(p.HostPubKey.Key)]
				if !exists {
					tableOffset = uint32(len(file.pubKeyTable))
					pubKeyMap[string(p.HostPubKey.Key)] = tableOffset
					file.pubKeyTable = append(file.pubKeyTable, HostPublicKey{
						PublicKey: p.HostPubKey,
						Used:      true,
					})
				}
				// Add the piece to the SiaFile.
				file.staticChunks[chunkIndex].Pieces[pieceIndex] = append(file.staticChunks[chunkIndex].Pieces[pieceIndex], piece{
					HostTableOffset: tableOffset,
					MerkleRoot:      p.MerkleRoot,
				})
			}
		}
	}
	entry := sfs.newSiaFileSetEntry(file, fd.Name, threadType)
	entry.threadMap[SiaFileNewThread]++
	sfs.siaFileMap[fd.Name] = entry
	return entry, file.saveFile()
}

// NewSiaFile create a new SiaFile, adds it to the SiaFileSet, increments the
// threadCount, and returns the SiaFileSetEntry. Since this method returns the
// siafile, wherever NewSiaFile is called should then Return the SiaFile to the
// SiaFileSet to avoid the file being stuck in memory due the threadCount never
// being decremented after creating the SiaFile
func (sfs *SiaFileSet) NewSiaFile(siaFilePath, siaPath, source string, erasureCode modules.ErasureCoder, masterKey crypto.CipherKey, fileSize uint64, fileMode os.FileMode, threadType ThreadType) (*SiaFileSetEntry, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	sf, err := New(siaFilePath, siaPath, source, sfs.wal, erasureCode, masterKey, fileSize, fileMode)
	if err != nil {
		return nil, err
	}
	entry := sfs.newSiaFileSetEntry(sf, siaPath, threadType)
	entry.threadMap[threadType]++
	sfs.siaFileMap[siaPath] = entry
	return entry, nil
}

// Open returns the siafile from the SiaFileSet for the corresponding key and
// increments the threadCount. If the siafile is not in memory it will load it
// from disk
func (sfs *SiaFileSet) Open(siaPath, filesDir string, threadType ThreadType) (*SiaFileSetEntry, error) {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	// Check for file in Memory
	var entry *SiaFileSetEntry
	entry, exists := sfs.siaFileMap[siaPath]
	if !exists {
		// Try and Load File from disk
		sf, err := LoadSiaFile(filepath.Join(filesDir, siaPath+ShareExtension), sfs.wal)
		if err != nil {
			return nil, err
		}
		entry = sfs.newSiaFileSetEntry(sf, siaPath, threadType)
		sfs.siaFileMap[siaPath] = entry
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if _, ok := entry.threadMap[threadType]; !ok {
		entry.threadMap[threadType] = 0
	}
	entry.threadMap[threadType]++
	return entry, nil
}

// Rename renames the SiaFile of the SiaFileSetEntry, it does not create a new
// entry for the new name nor does it delete the entry with with old name
func (sfs *SiaFileSet) Rename(entry *SiaFileSetEntry, newSiaPath, newSiaFilePath string) error {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	entry.mu.Lock()
	defer entry.mu.Unlock()
	_, exists := sfs.siaFileMap[entry.siaPath]
	if !exists {
		return ErrUnknownPath
	}
	_, exists = sfs.siaFileMap[newSiaPath]
	if exists {
		return ErrPathOverload
	}

	return entry.siaFile.Rename(newSiaPath, newSiaFilePath)
}

// SiaFileMap returns the SiaFileMap of the SiaFileSet
func (sfs *SiaFileSet) SiaFileMap() map[string]*SiaFileSetEntry {
	sfs.mu.Lock()
	defer sfs.mu.Unlock()
	return sfs.siaFileMap
}
