package modules

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
)

// TestLinkFormat checks that the linkformat is correctly encoding to and
// decoding from a string.
func TestLinkFormat(t *testing.T) {
	ld := LinkData{
		MerkleRoot:   crypto.HashObject("1"),
		Version:      1,
		DataPieces:   8,
		ParityPieces: 3,
		HeaderSize:   173,
		FileSize:     18471849,
	}
	str := ld.String()

	var ldDecoded LinkData
	err := ldDecoded.LoadString(str)
	if err != nil {
		t.Fatal(err)
	}
	if ldDecoded != ld {
		t.Error("encoded data and decoded data do not match")
		t.Log(ld)
		t.Log(ldDecoded)
	}
}
