package siafile

import (
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// CombinedChunkIndex is a helper method which translates a chunk's index to the
// corresponding combined chunk index dependng on the number of combined chunks.
func CombinedChunkIndex(numChunks, chunkIndex uint64, numCombinedChunks int) int {
	if numCombinedChunks == 1 && chunkIndex == numChunks-1 {
		return 0
	}
	if numCombinedChunks == 2 && chunkIndex == numChunks-2 {
		return 0
	}
	if numCombinedChunks == 2 && chunkIndex == numChunks-1 {
		return 1
	}
	return -1
}

// Merge merges two PartialsSiafiles into one, returning a map which translates
// chunk indices in newFile to indices in sf.
func (sf *SiaFile) Merge(newFile *SiaFile) (map[uint64]uint64, error) {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	return sf.merge(newFile)
}

// addCombinedChunk adds a new combined chunk to a combined Siafile. This can't
// be called on a regular SiaFile.
func (sf *SiaFile) addCombinedChunk() ([]writeaheadlog.Update, error) {
	if sf.deleted {
		return nil, errors.New("can't add combined chunk to deleted file")
	}
	if filepath.Ext(sf.siaFilePath) != modules.PartialsSiaFileExtension {
		return nil, errors.New("can only call addCombinedChunk on combined SiaFiles")
	}
	// Create updates to add a chunk and return index of that new chunk.
	updates, err := sf.growNumChunks(uint64(sf.numChunks) + 1)
	return updates, err
}

// merge merges two PartialsSiafiles into one, returning a map which translates
// chunk indices in newFile to indices in sf.
func (sf *SiaFile) merge(newFile *SiaFile) (map[uint64]uint64, error) {
	if sf.deleted {
		return nil, errors.New("can't merge into deleted file")
	}
	if filepath.Ext(sf.siaFilePath) != modules.PartialsSiaFileExtension {
		return nil, errors.New("can only call merge on PartialsSiaFile")
	}
	if filepath.Ext(newFile.SiaFilePath()) != modules.PartialsSiaFileExtension {
		return nil, errors.New("can only merge PartialsSiafiles into a PartialsSiaFile")
	}
	newFile.mu.Lock()
	defer newFile.mu.Unlock()
	if newFile.deleted {
		return nil, errors.New("can't merge deleted file")
	}
	var newChunks []chunk
	indexMap := make(map[uint64]uint64)
	ncb := sf.numChunks
	err := newFile.iterateChunksReadonly(func(chunk chunk) error {
		newIndex := sf.numChunks
		indexMap[uint64(chunk.Index)] = uint64(newIndex)
		chunk.Index = newIndex
		newChunks = append(newChunks, chunk)
		return nil
	})
	if err != nil {
		sf.numChunks = ncb
		return nil, err
	}
	return indexMap, sf.saveFile(newChunks)
}
