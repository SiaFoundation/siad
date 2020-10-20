package registry

import (
	"fmt"
	"math"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

type (
	// bitfield is an efficient way to mark specific slots on disk as used,
	// unused or fetch a new free slot.
	bitfield []uint64
)

// ErrNoFreeBit is returned by SetRandom if no free bit was found.
var ErrNoFreeBit = errors.New("no free bit available in bitfield")

// newBitfield creates a new bitfield which can set n bits.
// NOTE: The bitfield will round up to the nearest multiple of 64.
func newBitfield(nBits uint64) (bitfield, error) {
	if nBits%64 != 0 {
		return nil, errors.New("invalid size for bitfield - needs to be multiple of 64")
	}
	return make([]uint64, nBits/64), nil
}

// SetRandom finds an unset bitfield and sets it.
func (b *bitfield) SetRandom() (uint64, error) {
	// If the bitfield is empty there is nothing we can do.
	if len(*b) == 0 {
		return 0, ErrNoFreeBit
	}
	// Search for a gap. Start at a random position.
	initialPos := fastrand.Intn(len(*b))
	i := initialPos
	for {
		// If the position contains unset bits, find one and set it.
		if (*b)[i] != math.MaxUint64 {
			for j := uint64(0); j < 64; j++ {
				index := uint64(i)*64 + j
				if !b.IsSet(index) {
					b.Set(index)
					return index, nil
				}
			}
			panic("b[i] != math.MaxUint64 but no bit found. This is impossible.")
		}
		// Increment the position we are looking at.
		i++
		// When we reached the end, start at the beginning again.
		if i == len(*b) {
			i = 0
		}
		// If we are back at the initialPos, there is no gap.
		if i == initialPos {
			break
		}
	}
	// No gap found. Extend the bitfield.
	return 0, ErrNoFreeBit
}

// IsSet returns whether the gap at the specified index is set.
func (b bitfield) IsSet(index uint64) bool {
	// Each index covers 8 bytes which are 8 bits each. So 64 bits in total.
	sliceOffset := index / 64
	bitOffset := index % 64

	// Check out-of-bounds.
	if sliceOffset >= uint64(len(b)) {
		return false
	}
	return b[sliceOffset]>>(63-bitOffset)&1 == 1
}

// Len returns the length of the bitfield.
func (b bitfield) Len() uint64 {
	return uint64(len(b)) * 64
}

// Set sets a gap in the bitfield.
func (b *bitfield) Set(index uint64) error {
	// Each index covers 8 bytes which are 8 bits each. So 64 bits in total.
	sliceOffset := index / 64
	bitOffset := index % 64

	// Check out-of-bounds.
	if sliceOffset >= uint64(len(*b)) {
		return fmt.Errorf("Set: out-of-bounds %v >= %v", sliceOffset, len(*b))
	}

	(*b)[sliceOffset] |= 1 << (63 - bitOffset)
	return nil
}

// Unset unsets a gap in the bitfield.
func (b *bitfield) Unset(index uint64) error {
	// Each index covers 8 bytes which are 8 bits each. So 64 bits in total.
	sliceOffset := index / 64
	bitOffset := index % 64

	// Check out-of-bounds.
	if sliceOffset >= uint64(len(*b)) {
		return fmt.Errorf("Unset: out-of-bounds %v >= %v", sliceOffset, len(*b))
	}

	(*b)[sliceOffset] &= ^(1 << (63 - bitOffset))
	return nil
}
