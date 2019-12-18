package renter

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
)

// TestLinkFormat checks that the linkformat is correctly encoding to and
// decoding from a string.
func TestLinkFormat(t *testing.T) {
	ld := LinkData{
		Version:      1,
		MerkleRoot:   crypto.HashObject("1"),
		HeaderSize:   21e3,
		FileSize:     1e6,
		DataPieces:   1,
		ParityPieces: 1,
	}
	str := ld.String()

	var ldDecoded LinkData
	err := ldDecoded.LoadString(str)
	if err != nil {
		t.Fatal(err)
	}
	if ldDecoded != ld {
		t.Error("encoded data and decoded data do not match")
	}

	// TODO: Loop to test a bunch of different random inputs to make sure things
	// are compatible across all sorts of inputs.
}
