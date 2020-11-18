package registry

import (
	"fmt"
	"testing"

	"gitlab.com/NebulousLabs/errors"
)

// TestBitfield tests setting and unsettings values in the bitfield.
func TestBitfield(t *testing.T) {
	nBits := uint64(64 * 200)
	b, err := newBitfield(nBits)
	if err != nil {
		t.Fatal(err)
	}

	// Declare a helper to check all indices.
	areSet := func(bf bitfield, set []uint64) error {
		for i := uint64(0); i < bf.Len(); i++ {
			isSet := bf.IsSet(i)
			if len(set) > 0 && set[0] == i {
				set = set[1:]
				if !isSet {
					return fmt.Errorf("%v should be set but wasn't", i)
				}
			} else if isSet {
				return fmt.Errorf("%v shouldn't be set but was", i)
			}
		}
		return nil
	}

	// Length is nBits.
	if b.Len() != nBits {
		t.Fatal("length should be nBits", nBits, b.Len())
	}

	// Set bit 0 and 63.
	err = b.Set(0)
	if err != nil {
		t.Fatal(err)
	}
	err = b.Set(63)
	if err != nil {
		t.Fatal(err)
	}
	if err := areSet(b, []uint64{0, 63}); err != nil {
		t.Fatal(err)
	}

	// Set bit 64.
	err = b.Set(64)
	if err != nil {
		t.Fatal(err)
	}
	if err := areSet(b, []uint64{0, 63, 64}); err != nil {
		t.Fatal(err)
	}

	// Unset bit 64.
	err = b.Unset(64)
	if err != nil {
		t.Fatal(err)
	}
	if err := areSet(b, []uint64{0, 63}); err != nil {
		t.Fatal(err)
	}

	// Unset all bits.
	for i := uint64(0); i < b.Len(); i++ {
		err = b.Unset(i)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := uint64(0); i < b.Len(); i++ {
		if b.IsSet(i) {
			t.Fatal("bit shouldn't be set", i)
		}
	}

	// Set all bits.
	for i := uint64(0); i < b.Len(); i++ {
		err = b.Set(i)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := uint64(0); i < b.Len(); i++ {
		if !b.IsSet(i) {
			t.Fatal("bit should be set", i)
		}
	}

	// Unset all bits again.
	for i := uint64(0); i < b.Len(); i++ {
		err = b.Unset(i)
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := uint64(0); i < b.Len(); i++ {
		if b.IsSet(i) {
			t.Fatal("bit shouldn't be set", i)
		}
	}
}

// TestSetRandom is a unit test for SetRandom.
func TestSetRandom(t *testing.T) {
	// Start setting the next bit n times.
	setMap := make(map[uint64]struct{})
	n := 640
	b, err := newBitfield(uint64(n))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		first, err := b.SetRandom()
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := setMap[first]; exists {
			t.Fatal("SetRandom set an already set field")
		}
		if !b.IsSet(first) {
			t.Fatalf("expected first %v to be set", first)
		}
		setMap[first] = struct{}{}
	}

	// Every index from 0 to n-1 should be set.
	for i := 0; i < n; i++ {
		if _, exists := setMap[uint64(i)]; !exists {
			t.Fatalf("index %v wasn't set", i)
		}
	}

	// Create a few gaps.
	err = b.Unset(0)
	if err != nil {
		t.Fatal(err)
	}
	err = b.Unset(63)
	if err != nil {
		t.Fatal(err)
	}
	err = b.Unset(64)
	if err != nil {
		t.Fatal(err)
	}
	err = b.Unset(65)
	if err != nil {
		t.Fatal(err)
	}
	err = b.Unset(127)
	if err != nil {
		t.Fatal(err)
	}

	// Call b.SetFist and confirm the gaps are filled.
	setMap = make(map[uint64]struct{})
	for i := 0; i < 5; i++ {
		first, err := b.SetRandom()
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := setMap[first]; exists {
			t.Fatal("SetFirst set an already set field")
		}
		if !b.IsSet(first) {
			t.Fatalf("expected first %v to be set", first)
		}
		setMap[first] = struct{}{}
	}
	if len(setMap) != 5 {
		t.Fatal("expected 5 indices to be set")
	}
	if !b.IsSet(0) {
		t.Fatal("bit wasn't set")
	}
	if !b.IsSet(63) {
		t.Fatal("bit wasn't set")
	}
	if !b.IsSet(64) {
		t.Fatal("bit wasn't set")
	}
	if !b.IsSet(65) {
		t.Fatal("bit wasn't set")
	}
	if !b.IsSet(127) {
		t.Fatal("bit wasn't set")
	}

	// EdgeCase: SetRandom on empty bitfield.
	b, err = newBitfield(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetRandom(); !errors.Contains(err, ErrNoFreeBit) {
		t.Fatal(err)
	}
}

// TestNewBitfield makes sure a bitfield is initialized with the right length.
func TestNewBitfield(t *testing.T) {
	b, err := newBitfield(0)
	if err != nil {
		t.Fatal(err)
	}
	if b.Len() != 0 {
		t.Fatal("wrong length")
	}
	b, err = newBitfield(1)
	if err == nil {
		t.Fatal("should have failed")
	}
	b, err = newBitfield(63)
	if err == nil {
		t.Fatal("should have failed")
	}
	b, err = newBitfield(64)
	if err != nil {
		t.Fatal(err)
	}
	if b.Len() != 64 {
		t.Fatal("wrong length")
	}
}
