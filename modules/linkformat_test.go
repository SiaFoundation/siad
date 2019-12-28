package modules

import (
	"math"
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

	// Try another set of values, this time using the max allowed value for each
	// numeric type.
	ld = LinkData{
		MerkleRoot:   crypto.HashObject("2"),
		Version:      15,
		DataPieces:   15,
		ParityPieces: 11,
		HeaderSize:   (1 << 51),
		FileSize:     math.MaxUint64,
	}
	str = ld.String()
	err = ldDecoded.LoadString(str)
	if err != nil {
		t.Fatal(err)
	}
	if ldDecoded != ld {
		t.Error("encoded data and decoded data do not match")
		t.Log(ld)
		t.Log(ldDecoded)
	}

	// Try loading a string that is too large.
	str = str + "some extra bytes"
	err = ld.LoadString(str)
	if err == nil {
		t.Error("expecting error when bad string is decoded into a LinkData")
	}
	str = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	err = ld.LoadString(str)
	if err == nil {
		t.Error("expecting error when bad string is decoded into a LinkData")
	}

	// Try loading a string that is definitely too small.
	str = "too small"
	err = ld.LoadString(str)
	if err == nil {
		t.Error("expecting error when bad string is decoded into a LinkData")
	}
}
