package types

import (
	"bytes"
	"testing"
)

func TestNewSpecifier(t *testing.T) {
	specifier := NewSpecifier("testing")
	expected := [16]byte{116, 101, 115, 116, 105, 110, 103, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(specifier[:], expected[:]) {
		t.Fatal("received unexpected specifier")
	}
}
