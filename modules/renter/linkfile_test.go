package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"

	"gitlab.com/NebulousLabs/fastrand"
)

// TestLinkfileLayoutEncoding checks that encoding and decoding a linkfile
// layout always results in the same struct.
func TestLinkfileLayoutEncoding(t *testing.T) {
	// Try encoding an decoding a simple example.
	llOriginal := linkfileLayout{
		version:            LinkfileVersion,
		filesize:           1e6,
		metadataSize:       14e3,
		fanoutSize:         75e3,
		fanoutDataPieces:   10,
		fanoutParityPieces: 20,
		cipherType:         crypto.TypePlain,
	}
	rand := fastrand.Bytes(64)
	copy(llOriginal.cipherKey[:], rand)
	encoded := llOriginal.encode()
	var llRecovered linkfileLayout
	llRecovered.decode(encoded)
	if llOriginal != llRecovered {
		t.Fatal("encoding and decoding of linkfileLayout does not match")
	}
}
