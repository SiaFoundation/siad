package renter

import (
	"testing"
)

// TestLinkfileLayoutEncoding checks that encoding and decoding a linkfile
// layout always results in the same struct.
func TestLinkfileLayoutEncoding(t *testing.T) {
	// Try encoding an decoding a simple example.
	llOriginal := linkfileLayout{
		version:                 LinkfileVersion,
		filesize:                1e6,
		metadataSize:            14e3,
		intraSectorDataPieces:   8,
		intraSectorParityPieces: 3,
		fanoutHeaderSize:        75e3,
		fanoutExtensionSize:     9e9,
		fanoutDataPieces:        10,
		fanoutParityPieces:      20,
	}
	encoded := llOriginal.encode()
	var llRecovered linkfileLayout
	llRecovered.decode(encoded)
	if llOriginal != llRecovered {
		t.Fatal("encoding and decoding of linkfileLayout does not match")
	}

	// TODO: Try a wider range of values with randomness / fuzzing.
}
