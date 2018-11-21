package siafile

import (
	"os"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
)

type (
	// Snapshot is a snapshot of a SiaFile. A snapshot is a deep-copy and
	// can be accessed without locking at the cost of being a frozen readonly
	// representation of a siafile which only exists in memory.
	Snapshot struct {
	}
)

// ChunkIndexByOffset will return the chunkIndex that contains the provided
// offset of a file and also the relative offset within the chunk. If the
// offset is out of bounds, chunkIndex will be equal to NumChunk().
func (s *Snapshot) ChunkIndexByOffset(offset uint64) (chunkIndex uint64, off uint64) {
	panic("not implemented yet")
}

// ChunkSize returns the size of a single chunk of the file.
func (s *Snapshot) ChunkSize() uint64 {
	panic("not implemented yet")
}

// ErasureCode returns the erasure coder used by the file.
func (s *Snapshot) ErasureCode() modules.ErasureCoder {
	panic("not implemented yet")
}

// MasterKey returns the masterkey used to encrypt the file.
func (s *Snapshot) MasterKey() crypto.CipherKey {
	panic("not implemented yet")
}

// Mode returns the FileMode of the file.
func (s *Snapshot) Mode() os.FileMode {
	panic("not implemented yet")
}

// NumChunks returns the number of chunks the file consists of. This will
// return the number of chunks the file consists of even if the file is not
// fully uploaded yet.
func (s *Snapshot) NumChunks() uint64 {
	panic("not implemented yet")
}

// Pieces returns all the pieces for a chunk in a slice of slices that contains
// all the pieces for a certain index.
func (s *Snapshot) Pieces(chunkIndex uint64) ([][]Piece, error) {
	panic("not implemented yet")
}

// PieceSize returns the size of a single piece of the file.
func (s *Snapshot) PieceSize() uint64 {
	panic("not implemented yet")
}

// SiaPath returns the SiaPath of the file.
func (s *Snapshot) SiaPath() string {
	panic("not implemented yet")
}

// Size returns the size of the file.
func (s *Snapshot) Size() uint64 {
	panic("not implemented yet")
}

// Snapshot creates a snapshot of the SiaFile.
func (sf *SiaFile) Snapshot() *Snapshot {
	panic("not implemented yet")
}
