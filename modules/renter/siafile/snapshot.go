package siafile

import (
	"os"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

type (
	// Snapshot is a snapshot of a SiaFile. A snapshot is a deep-copy and
	// can be accessed without locking at the cost of being a frozen readonly
	// representation of a siafile which only exists in memory.
	Snapshot struct {
		staticChunks      []Chunk
		staticFileSize    int64
		staticPieceSize   uint64
		staticErasureCode modules.ErasureCoder
		staticMasterKey   crypto.CipherKey
		staticMode        os.FileMode
		staticPubKeyTable []HostPublicKey
		staticSiaPath     modules.SiaPath
	}
)

// SnapshotReader is a helper type that allows reading a raw SiaFile from disk
// while keeping the file in memory locked.
type SnapshotReader struct {
	f  *os.File
	sf *SiaFile
}

// Close closes the underlying file.
func (sfr *SnapshotReader) Close() error {
	sfr.sf.mu.RUnlock()
	return sfr.f.Close()
}

// Read calls Read on the underlying file.
func (sfr *SnapshotReader) Read(b []byte) (int, error) {
	return sfr.f.Read(b)
}

// Stat returns the FileInfo of the underlying file.
func (sfr *SnapshotReader) Stat() (os.FileInfo, error) {
	return sfr.f.Stat()
}

// SnapshotReader creates a io.ReadCloser that can be used to read the raw
// Siafile from disk.
func (sf *SiaFile) SnapshotReader() (*SnapshotReader, error) {
	// Lock the file.
	sf.mu.RLock()
	if sf.deleted {
		sf.mu.RUnlock()
		return nil, errors.New("can't copy deleted SiaFile")
	}
	// Open file.
	f, err := os.Open(sf.siaFilePath)
	if err != nil {
		sf.mu.RUnlock()
		return nil, err
	}
	return &SnapshotReader{
		sf: sf,
		f:  f,
	}, nil
}

// ChunkIndexByOffset will return the chunkIndex that contains the provided
// offset of a file and also the relative offset within the chunk. If the
// offset is out of bounds, chunkIndex will be equal to NumChunk().
func (s *Snapshot) ChunkIndexByOffset(offset uint64) (chunkIndex uint64, off uint64) {
	chunkIndex = offset / s.ChunkSize()
	off = offset % s.ChunkSize()
	return
}

// ChunkSize returns the size of a single chunk of the file.
func (s *Snapshot) ChunkSize() uint64 {
	return s.staticPieceSize * uint64(s.staticErasureCode.MinPieces())
}

// ErasureCode returns the erasure coder used by the file.
func (s *Snapshot) ErasureCode() modules.ErasureCoder {
	return s.staticErasureCode
}

// MasterKey returns the masterkey used to encrypt the file.
func (s *Snapshot) MasterKey() crypto.CipherKey {
	return s.staticMasterKey
}

// Mode returns the FileMode of the file.
func (s *Snapshot) Mode() os.FileMode {
	return s.staticMode
}

// NumChunks returns the number of chunks the file consists of. This will
// return the number of chunks the file consists of even if the file is not
// fully uploaded yet.
func (s *Snapshot) NumChunks() uint64 {
	return uint64(len(s.staticChunks))
}

// Pieces returns all the pieces for a chunk in a slice of slices that contains
// all the pieces for a certain index.
func (s *Snapshot) Pieces(chunkIndex uint64) ([][]Piece, error) {
	// Return the pieces. Since the snapshot is meant to be used read-only, we
	// don't have to return a deep-copy here.
	return s.staticChunks[chunkIndex].Pieces, nil
}

// PieceSize returns the size of a single piece of the file.
func (s *Snapshot) PieceSize() uint64 {
	return s.staticPieceSize
}

// SiaPath returns the SiaPath of the file.
func (s *Snapshot) SiaPath() modules.SiaPath {
	return s.staticSiaPath
}

// Size returns the size of the file.
func (s *Snapshot) Size() uint64 {
	return uint64(s.staticFileSize)
}

// Snapshot creates a snapshot of the SiaFile.
func (sf *siaFileSetEntry) Snapshot() *Snapshot {
	mk := sf.MasterKey()
	sf.mu.RLock()

	// Copy PubKeyTable.
	pkt := make([]HostPublicKey, len(sf.pubKeyTable))
	copy(pkt, sf.pubKeyTable)

	chunks := make([]Chunk, 0, len(sf.staticChunks))
	// Figure out how much memory we need to allocate for the piece sets and
	// pieces.
	var numPieceSets, numPieces int
	for chunkIndex := range sf.staticChunks {
		numPieceSets += len(sf.staticChunks[chunkIndex].Pieces)
		for pieceIndex := range sf.staticChunks[chunkIndex].Pieces {
			numPieces += len(sf.staticChunks[chunkIndex].Pieces[pieceIndex])
		}
	}
	// Allocate all the piece sets and pieces at once.
	allPieceSets := make([][]Piece, numPieceSets)
	allPieces := make([]Piece, numPieces)

	// Copy chunks.
	for chunkIndex := range sf.staticChunks {
		pieces := allPieceSets[:len(sf.staticChunks[chunkIndex].Pieces)]
		allPieceSets = allPieceSets[len(sf.staticChunks[chunkIndex].Pieces):]
		for pieceIndex := range pieces {
			pieces[pieceIndex] = allPieces[:len(sf.staticChunks[chunkIndex].Pieces[pieceIndex])]
			allPieces = allPieces[len(sf.staticChunks[chunkIndex].Pieces[pieceIndex]):]
			for i, piece := range sf.staticChunks[chunkIndex].Pieces[pieceIndex] {
				pieces[pieceIndex][i] = Piece{
					HostPubKey: sf.pubKeyTable[piece.HostTableOffset].PublicKey,
					MerkleRoot: piece.MerkleRoot,
				}
			}
		}
		chunks = append(chunks, Chunk{
			Pieces: pieces,
		})
	}
	sf.mu.RUnlock()

	sf.siaFileSet.mu.Lock()
	sp := sf.siaFileSet.siaPath(sf)
	sf.siaFileSet.mu.Unlock()

	return &Snapshot{
		staticChunks:      chunks,
		staticFileSize:    sf.staticMetadata.StaticFileSize,
		staticPieceSize:   sf.staticMetadata.StaticPieceSize,
		staticErasureCode: sf.staticMetadata.staticErasureCode,
		staticMasterKey:   mk,
		staticMode:        sf.staticMetadata.Mode,
		staticPubKeyTable: pkt,
		staticSiaPath:     sp,
	}
}
