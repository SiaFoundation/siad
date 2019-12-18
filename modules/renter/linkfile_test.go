package renter

import (
	"testing"
)

// TestLinkfileLayoutEncoding checks that encoding and decoding a linkfile
// layout always results in the same struct.
func TestLinkfileLayoutEncoding(t *testing.T) {
	// Try encoding an decoding a simple example.
	llOriginal := linkfileLayout{
		filesize: 1e6,
		metadataSize: 1e3,
		initialFanoutSize: 187,
		fanoutDataPieces: 8,
		fanoutParityPieces: 3,
	}
	encoded := llOriginal.encode()
	var llRecovered linkfileLayout
	llRecovered.decode(encoded)
	if llOriginal != llRecovered {
		t.Fatal("encoding and decoding of linkfileLayout does not match")
	}

	// TODO: Try a wider range of values with randomness / fuzzing.
}
