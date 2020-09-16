package registry

import (
	"fmt"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
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
}

// TestSetFirst is a unit test for SetFirst.
func TestSetFirst(t *testing.T) {
	var b bitfield
	// Start setting the next bit 1000 times. Randomly decide to set the bit
	// with b.Set to simulate bits that are already in use.
	for i := uint64(0); i < 1000; i++ {
		// Choose randomly how to set it.
		if fastrand.Intn(2) == 0 {
			b.Set(i)
		} else {
			first := b.SetFirst()
			if first != i {
				t.Fatal("first doesn't match expectation", first, i)
			}
		}
		if !b.IsSet(i) {
			t.Fatalf("expected i %v to be set", i)
		}
	}

	// Create a few gaps.
	b.Unset(0)
	b.Unset(100)
	b.Unset(998)

	// Call b.SetFist and confirm the gaps are filled.
	i := b.SetFirst()
	if i != 0 {
		t.Fatal("wrong bit")
	}
	if !b.IsSet(i) {
		t.Fatal("bit wasn't set")
	}
	i = b.SetFirst()
	if i != 100 {
		t.Fatal("wrong bit")
	}
	if !b.IsSet(i) {
		t.Fatal("bit wasn't set")
	}
	i = b.SetFirst()
	if i != 998 {
		t.Fatal("wrong bit")
	}
	if !b.IsSet(i) {
		t.Fatal("bit wasn't set")
	}
}
