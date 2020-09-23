package registry

import (
	"fmt"
	"testing"
)

// TestBitfield tests setting and unsettings values in the bitfield.
func TestBitfield(t *testing.T) {
	var b bitfield

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

	// Initial length is 0.
	if b.Len() != 0 {
		t.Fatalf("length should be 0 but was %v", b.Len())
	}

	// Set bit 0 and 63. The length is now 64.
	b.Set(0)
	b.Set(63)
	if b.Len() != 64 {
		t.Fatalf("length should be 64 but was %v", b.Len())
	}
	if err := areSet(b, []uint64{0, 63}); err != nil {
		t.Fatal(err)
	}

	// Set bit 64. The length is now 128.
	b.Set(64)
	if b.Len() != 128 {
		t.Fatalf("length should be 128 but was %v", b.Len())
	}
	if err := areSet(b, []uint64{0, 63, 64}); err != nil {
		t.Fatal(err)
	}

	// Unset bit 64. The length is still 128.
	b.Unset(64)
	if b.Len() != 128 {
		t.Fatalf("length should be 128 but was %v", b.Len())
	}
	if err := areSet(b, []uint64{0, 63}); err != nil {
		t.Fatal(err)
	}

	// Trim the bitfield. The length is 64.
	b.Trim()
	if b.Len() != 64 {
		t.Fatalf("length should be 64 but was %v", b.Len())
	}

	// Unset all bits.
	for i := uint64(0); i < b.Len(); i++ {
		b.Unset(i)
	}
	for i := uint64(0); i < b.Len(); i++ {
		if b.IsSet(i) {
			t.Fatal("bit shouldn't be set", i)
		}
	}

	// Set all bits.
	for i := uint64(0); i < b.Len(); i++ {
		b.Set(i)
	}
	for i := uint64(0); i < b.Len(); i++ {
		if !b.IsSet(i) {
			t.Fatal("bit should be set", i)
		}
	}

	// Unset all bits again.
	for i := uint64(0); i < b.Len(); i++ {
		b.Unset(i)
	}
	for i := uint64(0); i < b.Len(); i++ {
		if b.IsSet(i) {
			t.Fatal("bit shouldn't be set", i)
		}
	}
}

// TestSetFirst is a unit test for SetFirst.
func TestSetFirst(t *testing.T) {
	var b bitfield
	// Start setting the next bit 128 times.
	setMap := make(map[uint64]struct{})
	n := 640
	for i := 0; i < n; i++ {
		first := b.SetFirst()
		if _, exists := setMap[first]; exists {
			t.Fatal("SetFirst set an already set field")
		}
		if !b.IsSet(first) {
			t.Fatalf("expected first %v to be set", first)
		}
		setMap[first] = struct{}{}
	}
	if len(setMap) != int(n) {
		t.Fatal("expected n indices to be set")
	}

	// Every index from 0 to n-1 should be set.
	for i := 0; i < n; i++ {
		if _, exists := setMap[uint64(i)]; !exists {
			t.Fatalf("index %v wasn't set", i)
		}
	}

	// Create a few gaps.
	b.Unset(0)
	b.Unset(63)
	b.Unset(64)
	b.Unset(65)
	b.Unset(127)

	// Call b.SetFist and confirm the gaps are filled.
	setMap = make(map[uint64]struct{})
	for i := 0; i < 5; i++ {
		first := b.SetFirst()
		println("first", first)
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
}
