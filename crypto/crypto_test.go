package crypto

import (
	"bytes"
	"testing"
)

// TestCipherTypeStringConversion tests the conversion from a CipherType into a
// string and vice versa.
func TestCipherTypeStringConversion(t *testing.T) {
	var ct CipherType
	if err := ct.FromString(TypeDefaultRenter.String()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(TypeDefaultRenter[:], ct[:]) {
		t.Fatal("Failed to parse CipherType from string")
	}
}
