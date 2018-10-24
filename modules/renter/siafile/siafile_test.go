package siafile

import (
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
)

// randomChunk is a helper method for testing that creates a random chunk.
func randomChunk() chunk {
	numPieces := 30
	chunk := chunk{}
	chunk.Pieces = make([][]piece, numPieces)
	fastrand.Read(chunk.ExtensionInfo[:])

	// Add 0-3 pieces for each pieceIndex within the file.
	for pieceIndex := range chunk.Pieces {
		n := fastrand.Intn(4) // [0;3]
		// Create and add n pieces at pieceIndex i.
		for i := 0; i < n; i++ {
			var piece piece
			piece.HostTableOffset = uint32(fastrand.Intn(100))
			fastrand.Read(piece.MerkleRoot[:])
			chunk.Pieces[pieceIndex] = append(chunk.Pieces[pieceIndex], piece)
		}
	}
	return chunk
}

// randomPiece is a helper method for testing that creates a random piece.
func randomPiece() piece {
	var piece piece
	piece.HostTableOffset = uint32(fastrand.Intn(100))
	fastrand.Read(piece.MerkleRoot[:])
	return piece
}

// TestNumPieces tests the chunk's numPieces method.
func TestNumPieces(t *testing.T) {
	// create a random chunk.
	chunk := randomChunk()

	// get the number of pieces of the chunk.
	totalPieces := 0
	for _, pieceSet := range chunk.Pieces {
		totalPieces += len(pieceSet)
	}

	// compare it to the one reported by numPieces.
	if totalPieces != chunk.numPieces() {
		t.Fatalf("Expected %v pieces but was %v", totalPieces, chunk.numPieces())
	}
}
