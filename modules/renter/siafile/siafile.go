package siafile

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

var (
	// ErrPathOverload is an error when a file already exists at that location
	ErrPathOverload = errors.New("a file already exists at that location")
	// ErrUnknownPath is an error when a file cannot be found with the given path
	ErrUnknownPath = errors.New("no file known with that path")
	// ErrUnknownThread is an error when a SiaFile is trying to be closed by a
	// thread that is not in the threadMap
	ErrUnknownThread = errors.New("thread should not be calling Close(), does not have control of the siafile")

	// ShareExtension is the extension to be used
	ShareExtension = ".sia"
)

type (
	// SiaFile is the disk format for files uploaded to the Sia network.  It
	// contains all the necessary information to recover a file from its hosts and
	// allows for easy constant-time updates of the file without having to read or
	// write the whole file.
	SiaFile struct {
		// staticMetadata is the mostly static staticMetadata of a SiaFile. The reserved
		// size of the staticMetadata on disk should always be a multiple of 4kib.
		// The staticMetadata is also the only part of the file that is JSON encoded
		// and can therefore be easily extended.
		staticMetadata metadata

		// pubKeyTable stores the public keys of the hosts this file's pieces are uploaded to.
		// Since multiple pieces from different chunks might be uploaded to the same host, this
		// allows us to deduplicate the rather large public keys.
		pubKeyTable []HostPublicKey

		// staticChunks are the staticChunks the file was split into.
		staticChunks []chunk

		// utility fields. These are not persisted.
		deleted        bool
		deps           modules.Dependencies
		mu             sync.RWMutex
		staticUniqueID string
		wal            *writeaheadlog.WAL // the wal that is used for SiaFiles

		// siaFilePath is the path to the .sia file on disk.
		siaFilePath string
	}

	// chunk represents a single chunk of a file on disk
	chunk struct {
		// ExtensionInfo is some reserved space for each chunk that allows us
		// to indicate if a chunk is special.
		ExtensionInfo [16]byte

		// Pieces are the Pieces of the file the chunk consists of.
		Pieces [][]piece

		// Stuck indicates if the chunk was not repaired as expected by the
		// repair loop
		Stuck bool
	}

	// Chunk is an exported chunk. It contains exported pieces.
	Chunk struct {
		Pieces [][]Piece
	}

	// piece represents a single piece of a chunk on disk
	piece struct {
		HostTableOffset uint32      // offset of the host's key within the pubKeyTable
		MerkleRoot      crypto.Hash // merkle root of the piece
	}

	// Piece is an exported piece. It contains a resolved public key instead of
	// the table offset.
	Piece struct {
		HostPubKey types.SiaPublicKey // public key of the host
		MerkleRoot crypto.Hash        // merkle root of the piece
	}

	// HostPublicKey is an entry in the HostPubKey table.
	HostPublicKey struct {
		PublicKey types.SiaPublicKey // public key of host
		Used      bool               // indicates if we currently use this host
	}
)

// MarshalSia implements the encoding.SiaMarshaler interface.
func (hpk HostPublicKey) MarshalSia(w io.Writer) error {
	e := encoding.NewEncoder(w)
	e.Encode(hpk.PublicKey)
	e.WriteBool(hpk.Used)
	return e.Err()
}

// UnmarshalSia implements the encoding.SiaUnmarshaler interface.
func (hpk *HostPublicKey) UnmarshalSia(r io.Reader) error {
	d := encoding.NewDecoder(r)
	d.Decode(&hpk.PublicKey)
	hpk.Used = d.NextBool()
	return d.Err()
}

// numPieces returns the total number of pieces uploaded for a chunk. This
// means that numPieces can be greater than the number of pieces created by the
// erasure coder.
func (c *chunk) numPieces() (numPieces int) {
	for _, c := range c.Pieces {
		numPieces += len(c)
	}
	return
}

// New create a new SiaFile.
func New(siaFilePath, siaPath, source string, wal *writeaheadlog.WAL, erasureCode modules.ErasureCoder, masterKey crypto.CipherKey, fileSize uint64, fileMode os.FileMode) (*SiaFile, error) {
	currentTime := time.Now()
	ecType, ecParams := marshalErasureCoder(erasureCode)
	file := &SiaFile{
		staticMetadata: metadata{
			AccessTime:              currentTime,
			ChunkOffset:             defaultReservedMDPages * pageSize,
			ChangeTime:              currentTime,
			CreateTime:              currentTime,
			StaticFileSize:          int64(fileSize),
			LocalPath:               source,
			StaticMasterKey:         masterKey.Key(),
			StaticMasterKeyType:     masterKey.Type(),
			Mode:                    fileMode,
			ModTime:                 currentTime,
			staticErasureCode:       erasureCode,
			StaticErasureCodeType:   ecType,
			StaticErasureCodeParams: ecParams,
			StaticPagesPerChunk:     numChunkPagesRequired(erasureCode.NumPieces()),
			StaticPieceSize:         modules.SectorSize - masterKey.Type().Overhead(),
			SiaPath:                 siaPath,
		},
		deps:           modules.ProdDependencies,
		siaFilePath:    siaFilePath,
		staticUniqueID: hex.EncodeToString(fastrand.Bytes(20)),
		wal:            wal,
	}
	// Init chunks.
	numChunks := fileSize / file.staticChunkSize()
	if fileSize%file.staticChunkSize() != 0 || numChunks == 0 {
		numChunks++
	}
	file.staticChunks = make([]chunk, numChunks)
	for i := range file.staticChunks {
		file.staticChunks[i].Pieces = make([][]piece, erasureCode.NumPieces())
	}
	// Save file.
	return file, file.saveFile()
}

// AddPiece adds an uploaded piece to the file. It also updates the host table
// if the public key of the host is not already known.
func (sf *SiaFile) AddPiece(pk types.SiaPublicKey, chunkIndex, pieceIndex uint64, merkleRoot crypto.Hash) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	// If the file was deleted we can't add a new piece since it would write
	// the file to disk again.
	if sf.deleted {
		return errors.New("can't add piece to deleted file")
	}

	// Get the index of the host in the public key table.
	tableIndex := -1
	for i, hpk := range sf.pubKeyTable {
		if hpk.PublicKey.Algorithm == pk.Algorithm && bytes.Equal(hpk.PublicKey.Key, pk.Key) {
			tableIndex = i
			break
		}
	}
	// If we don't know the host yet, we add it to the table.
	tableChanged := false
	if tableIndex == -1 {
		sf.pubKeyTable = append(sf.pubKeyTable, HostPublicKey{
			PublicKey: pk,
			Used:      true,
		})
		tableIndex = len(sf.pubKeyTable) - 1
		tableChanged = true
	}
	// Check if the chunkIndex is valid.
	if chunkIndex >= uint64(len(sf.staticChunks)) {
		return fmt.Errorf("chunkIndex %v out of bounds (%v)", chunkIndex, len(sf.staticChunks))
	}
	// Check if the pieceIndex is valid.
	if pieceIndex >= uint64(len(sf.staticChunks[chunkIndex].Pieces)) {
		return fmt.Errorf("pieceIndex %v out of bounds (%v)", pieceIndex, len(sf.staticChunks[chunkIndex].Pieces))
	}
	// Add the piece to the chunk.
	sf.staticChunks[chunkIndex].Pieces[pieceIndex] = append(sf.staticChunks[chunkIndex].Pieces[pieceIndex], piece{
		HostTableOffset: uint32(tableIndex),
		MerkleRoot:      merkleRoot,
	})

	// Update the AccessTime, ChangeTime and ModTime.
	sf.staticMetadata.AccessTime = time.Now()
	sf.staticMetadata.ChangeTime = sf.staticMetadata.AccessTime
	sf.staticMetadata.ModTime = sf.staticMetadata.AccessTime

	// Defrag the chunk if necessary.
	chunk := &sf.staticChunks[chunkIndex]
	chunkSize := marshaledChunkSize(chunk.numPieces())
	maxChunkSize := int64(sf.staticMetadata.StaticPagesPerChunk) * pageSize
	if chunkSize > maxChunkSize {
		sf.defragChunk(chunk)
	}

	// If the chunk is still too large after the defrag, we abort.
	chunkSize = marshaledChunkSize(chunk.numPieces())
	if chunkSize > maxChunkSize {
		return fmt.Errorf("chunk doesn't fit into allocated space %v > %v", chunkSize, maxChunkSize)
	}

	// Update the file atomically.
	var updates []writeaheadlog.Update
	var err error
	// Get the updates for the header.
	if tableChanged {
		// If the table changed we update the whole header.
		updates, err = sf.saveHeaderUpdates()
	} else {
		// Otherwise just the metadata.
		updates, err = sf.saveMetadataUpdate()
	}
	if err != nil {
		return err
	}
	// Save the changed chunk to disk.
	chunkUpdate, err := sf.saveChunkUpdate(int(chunkIndex))
	if err != nil {
		return err
	}
	return sf.createAndApplyTransaction(append(updates, chunkUpdate)...)
}

// Available indicates whether the file is ready to be downloaded.
func (sf *SiaFile) Available(offline map[string]bool) bool {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	// We need to find at least erasureCode.MinPieces different pieces for each
	// chunk for the file to be available.
	for _, chunk := range sf.staticChunks {
		piecesForChunk := 0
		for _, pieceSet := range chunk.Pieces {
			for _, piece := range pieceSet {
				if !offline[string(sf.pubKeyTable[piece.HostTableOffset].PublicKey.Key)] {
					piecesForChunk++
					break // break out since we only count unique pieces
				}
			}
			if piecesForChunk >= sf.staticMetadata.staticErasureCode.MinPieces() {
				break // we already have enough pieces for this chunk.
			}
		}
		if piecesForChunk < sf.staticMetadata.staticErasureCode.MinPieces() {
			return false // this chunk isn't available.
		}
	}
	return true
}

// chunkHealth returns the health of the chunk which is defined as the percent
// of parity pieces remaining.
//
// health = 0 is full redundancy, health <= 1 is recoverable, health > 1 needs
// to be repaired from disk
func (sf *SiaFile) chunkHealth(chunkIndex int, offline map[string]bool) float64 {
	// The max number of good pieces that a chunk can have is NumPieces()
	numPieces := sf.staticMetadata.staticErasureCode.NumPieces()
	minPieces := sf.staticMetadata.staticErasureCode.MinPieces()
	targetPieces := float64(numPieces - minPieces)
	// Iterate over each pieceSet
	var goodPieces int
	for _, pieceSet := range sf.staticChunks[chunkIndex].Pieces {
		// Iterate over each pieceSet and count all the unique
		// goodPieces
		for _, piece := range pieceSet {
			if !offline[string(sf.pubKeyTable[piece.HostTableOffset].PublicKey.Key)] {
				// Once a good piece is found, break out since all pieces in
				// pieceSet are the same
				goodPieces++
				break
			}
		}
	}

	// Sanity Check, if something went wrong, default to minimum health
	if goodPieces > numPieces || goodPieces < 0 {
		build.Critical("unexpected number of goodPieces for chunkHealth")
		goodPieces = 0
	}
	return 1 - (float64(goodPieces-minPieces) / targetPieces)
}

// ChunkIndexByOffset will return the chunkIndex that contains the provided
// offset of a file and also the relative offset within the chunk. If the
// offset is out of bounds, chunkIndex will be equal to NumChunk().
func (sf *SiaFile) ChunkIndexByOffset(offset uint64) (chunkIndex uint64, off uint64) {
	chunkIndex = offset / sf.staticChunkSize()
	off = offset % sf.staticChunkSize()
	return
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

// ErasureCode returns the erasure coder used by the file.
func (sf *SiaFile) ErasureCode() modules.ErasureCoder {
	return sf.staticMetadata.staticErasureCode
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
		contract, exists := contracts[string(pk.PublicKey.Key)]
		if !exists {
			continue
		}
		if contract.EndHeight < lowest {
			lowest = contract.EndHeight
		}
	}
	return lowest
}

// Health calculates the health of the file to be used in determining repair
// priority. Health of the file is the lowest health of any of the chunks and is
// defined as the percent of parity pieces remaining.
//
// health = 0 is full redundancy, health <= 1 is recoverable, health > 1 needs
// to be repaired from disk
func (sf *SiaFile) Health(offline map[string]bool) float64 {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	numPieces := float64(sf.staticMetadata.staticErasureCode.NumPieces())
	minPieces := float64(sf.staticMetadata.staticErasureCode.MinPieces())
	worstHealth := 1 - ((0 - minPieces) / (numPieces - minPieces))
	health := float64(0)
	for chunkIndex := range sf.staticChunks {
		chunkHealth := sf.chunkHealth(chunkIndex, offline)
		if chunkHealth > health {
			health = chunkHealth
		}
	}

	// Sanity check, if something went wrong default to worst health
	if health > worstHealth {
		if build.DEBUG {
			build.Critical("WARN: health out of bounds. Max value, Min value, health found", worstHealth, 0, health)
		}
		health = worstHealth
	}
	return health
}

// HostPublicKeys returns all the public keys of hosts the file has ever been
// uploaded to. That means some of those hosts might no longer be in use.
func (sf *SiaFile) HostPublicKeys() (spks []types.SiaPublicKey) {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	// Only return the keys, not the whole entry.
	keys := make([]types.SiaPublicKey, 0, len(sf.pubKeyTable))
	for _, key := range sf.pubKeyTable {
		keys = append(keys, key.PublicKey)
	}
	return keys
}

// IsStuck checks if a siafile is stuck. A siafile is stuck if it has any stuck
// chunks
func (sf *SiaFile) IsStuck() bool {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	numStuckChunks := uint64(0)
	for _, chunk := range sf.staticChunks {
		if chunk.Stuck {
			numStuckChunks++
		}
	}
	sf.staticMetadata.NumStuckChunks = numStuckChunks
	return sf.staticMetadata.NumStuckChunks > 0
}

// NumChunks returns the number of chunks the file consists of. This will
// return the number of chunks the file consists of even if the file is not
// fully uploaded yet.
func (sf *SiaFile) NumChunks() uint64 {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	return uint64(len(sf.staticChunks))
}

// Pieces returns all the pieces for a chunk in a slice of slices that contains
// all the pieces for a certain index.
func (sf *SiaFile) Pieces(chunkIndex uint64) ([][]Piece, error) {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	if chunkIndex >= uint64(len(sf.staticChunks)) {
		err := fmt.Errorf("index %v out of bounds (%v)", chunkIndex, len(sf.staticChunks))
		build.Critical(err)
		return nil, err
	}
	// Return a deep-copy to avoid race conditions.
	pieces := make([][]Piece, len(sf.staticChunks[chunkIndex].Pieces))
	for pieceIndex := range pieces {
		pieces[pieceIndex] = make([]Piece, len(sf.staticChunks[chunkIndex].Pieces[pieceIndex]))
		for i, piece := range sf.staticChunks[chunkIndex].Pieces[pieceIndex] {
			pieces[pieceIndex][i] = Piece{
				HostPubKey: sf.pubKeyTable[piece.HostTableOffset].PublicKey,
				MerkleRoot: piece.MerkleRoot,
			}
		}
	}
	return pieces, nil
}

// Redundancy returns the redundancy of the least redundant chunk. A file
// becomes available when this redundancy is >= 1. Assumes that every piece is
// unique within a file contract. -1 is returned if the file has size 0. It
// takes two arguments, a map of offline contracts for this file and a map that
// indicates if a contract is goodForRenew.
func (sf *SiaFile) Redundancy(offlineMap map[string]bool, goodForRenewMap map[string]bool) float64 {
	sf.mu.RLock()
	defer sf.mu.RUnlock()
	if sf.staticMetadata.StaticFileSize == 0 {
		// TODO change this once tiny files are supported.
		if len(sf.staticChunks) != 1 {
			// should never happen
			return -1
		}
		ec := sf.staticMetadata.staticErasureCode
		return float64(ec.NumPieces()) / float64(ec.MinPieces())
	}

	minRedundancy := math.MaxFloat64
	minRedundancyNoRenew := math.MaxFloat64
	for _, chunk := range sf.staticChunks {
		// Loop over chunks and remember how many unique pieces of the chunk
		// were goodForRenew and how many were not.
		numPiecesRenew := uint64(0)
		numPiecesNoRenew := uint64(0)
		for _, pieceSet := range chunk.Pieces {
			// Remember if we encountered a goodForRenew piece or a
			// !goodForRenew piece that was at least online.
			foundGoodForRenew := false
			foundOnline := false
			for _, piece := range pieceSet {
				offline, exists1 := offlineMap[string(sf.pubKeyTable[piece.HostTableOffset].PublicKey.Key)]
				goodForRenew, exists2 := goodForRenewMap[string(sf.pubKeyTable[piece.HostTableOffset].PublicKey.Key)]
				if exists1 != exists2 {
					build.Critical("contract can't be in one map but not in the other")
				}
				if !exists1 || offline {
					continue
				}
				// If we found a goodForRenew piece we can stop.
				if goodForRenew {
					foundGoodForRenew = true
					break
				}
				// Otherwise we continue since there might be other hosts with
				// the same piece that are goodForRenew. We still remember that
				// we found an online piece though.
				foundOnline = true
			}
			if foundGoodForRenew {
				numPiecesRenew++
				numPiecesNoRenew++
			} else if foundOnline {
				numPiecesNoRenew++
			}
		}
		redundancy := float64(numPiecesRenew) / float64(sf.staticMetadata.staticErasureCode.MinPieces())
		if redundancy < minRedundancy {
			minRedundancy = redundancy
		}
		redundancyNoRenew := float64(numPiecesNoRenew) / float64(sf.staticMetadata.staticErasureCode.MinPieces())
		if redundancyNoRenew < minRedundancyNoRenew {
			minRedundancyNoRenew = redundancyNoRenew
		}
	}

	// If the redundancy is smaller than 1x we return the redundancy that
	// includes contracts that are not good for renewal. The reason for this is
	// a better user experience. If the renter operates correctly, redundancy
	// should never go above numPieces / minPieces and redundancyNoRenew should
	// never go below 1.
	if minRedundancy < 1 && minRedundancyNoRenew >= 1 {
		return 1
	} else if minRedundancy < 1 {
		return minRedundancyNoRenew
	}
	return minRedundancy
}

// UID returns a unique identifier for this file.
func (sf *SiaFile) UID() string {
	return sf.staticUniqueID
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

// UpdateUsedHosts updates the 'Used' flag for the entries in the pubKeyTable
// of the SiaFile. The keys of all used hosts should be passed to the method
// and the SiaFile will update the flag for hosts it knows of to 'true' and set
// hosts which were not passed in to 'false'.
func (sf *SiaFile) UpdateUsedHosts(used []types.SiaPublicKey) error {
	sf.mu.Lock()
	defer sf.mu.Unlock()
	// Create a map of the used keys for faster lookups.
	usedMap := make(map[string]struct{})
	for _, key := range used {
		usedMap[string(key.Key)] = struct{}{}
	}
	// Mark the entries in the table. If the entry exists 'Used' is true.
	// Otherwise it's 'false'.
	var unusedHosts uint
	for i, entry := range sf.pubKeyTable {
		_, used := usedMap[string(entry.PublicKey.Key)]
		sf.pubKeyTable[i].Used = used
		if !used {
			unusedHosts++
		}
	}
	// Prune the pubKeyTable if necessary.
	pruned := false
	if unusedHosts > pubKeyTablePruneThreshold {
		sf.pruneHosts()
		pruned = true
	}
	// Save the header to disk.
	updates, err := sf.saveHeaderUpdates()
	if err != nil {
		return err
	}
	// If we pruned the hosts we also need to save the body.
	if pruned {
		chunkUpdates, err := sf.saveChunksUpdates()
		if err != nil {
			return err
		}
		updates = append(updates, chunkUpdates...)
	}
	return sf.createAndApplyTransaction(updates...)
}

// UploadProgress indicates what percentage of the file (plus redundancy) has
// been uploaded. Note that a file may be Available long before UploadProgress
// reaches 100%, and UploadProgress may report a value greater than 100%.
func (sf *SiaFile) UploadProgress() float64 {
	if sf.Size() == 0 {
		return 100
	}
	uploaded := sf.UploadedBytes()
	desired := sf.NumChunks() * modules.SectorSize * uint64(sf.ErasureCode().NumPieces())
	return math.Min(100*(float64(uploaded)/float64(desired)), 100)
}

// defragChunk removes pieces which belong to bad hosts and if that wasn't
// enough to reduce the chunkSize below the maximum size, it will remove
// redundant pieces.
func (sf *SiaFile) defragChunk(chunk *chunk) {
	// Calculate how many pieces every pieceSet can contain.
	maxChunkSize := int64(sf.staticMetadata.StaticPagesPerChunk) * pageSize
	maxPieces := (maxChunkSize - marshaledChunkOverhead) / marshaledPieceSize
	maxPiecesPerSet := maxPieces / int64(len(chunk.Pieces))

	// Filter out pieces with unused hosts since we don't have contracts with
	// those anymore.
	for i, pieceSet := range chunk.Pieces {
		var newPieceSet []piece
		for _, piece := range pieceSet {
			if int64(len(newPieceSet)) == maxPiecesPerSet {
				break
			}
			if sf.pubKeyTable[piece.HostTableOffset].Used {
				newPieceSet = append(newPieceSet, piece)
			}
		}
		chunk.Pieces[i] = newPieceSet
	}
}

// pruneHosts prunes the unused hostkeys from the file, updates the
// HostTableOffset of the pieces and removes pieces which do no longer have a
// host.
func (sf *SiaFile) pruneHosts() {
	var prunedTable []HostPublicKey
	// Create a map to track how the indices of the hostkeys changed when being
	// pruned.
	offsetMap := make(map[uint32]uint32)
	for i := uint32(0); i < uint32(len(sf.pubKeyTable)); i++ {
		if sf.pubKeyTable[i].Used {
			prunedTable = append(prunedTable, sf.pubKeyTable[i])
			offsetMap[i] = uint32(len(prunedTable) - 1)
		}
	}
	sf.pubKeyTable = prunedTable
	// With this map we loop over all the chunks and pieces and update the ones
	// who got a new offset and remove the ones that no longer have one.
	for chunkIndex := range sf.staticChunks {
		for pieceIndex, pieceSet := range sf.staticChunks[chunkIndex].Pieces {
			var newPieceSet []piece
			for i, piece := range pieceSet {
				newOffset, exists := offsetMap[piece.HostTableOffset]
				if exists {
					pieceSet[i].HostTableOffset = newOffset
					newPieceSet = append(newPieceSet, pieceSet[i])

				}
			}
			sf.staticChunks[chunkIndex].Pieces[pieceIndex] = newPieceSet
		}
	}
}
