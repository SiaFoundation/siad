package registry

import (
	"math"

	"gitlab.com/NebulousLabs/fastrand"
)

type (
	bitfield []uint64
)

// SetFirst finds the first unused gap in the bitfield and sets it.
func (b *bitfield) SetFirst() uint64 {
	// If the bitfield is empty, set 0.
	if len(*b) == 0 {
		b.Set(0)
		return 0
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
					return index
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
	l := b.Len()
	b.Set(l)
	return l
}

// IsSet returns whether the gap at the specified index is set.
func (b bitfield) IsSet(index uint64) bool {
	// Each index covers 8 bytes which 8 bits each. So 64 bits in total.
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
func (b *bitfield) Set(index uint64) {
	// Each index covers 8 bytes which 8 bits each. So 64 bits in total.
	sliceOffset := index / 64
	bitOffset := index % 64

	// Extend bitfield if necessary.
	for sliceOffset >= uint64(len(*b)) {
		*b = append(*b, 0)
	}

	(*b)[sliceOffset] |= 1 << (63 - bitOffset)
}

// Unset unsets a gap in the bitfield.
func (b *bitfield) Unset(index uint64) {
	// Each index covers 8 bytes which 8 bits each. So 64 bits in total.
	sliceOffset := index / 64
	bitOffset := index % 64

	// Extend bitfield if necessary.
	for sliceOffset >= uint64(len(*b)) {
		*b = append(*b, 0)
	}

	(*b)[sliceOffset] &= ^(1 << (63 - bitOffset))
}

// Trim trims unset gaps from the end of the bitfield.
func (b *bitfield) Trim() {
	toRemove := 0
	old := *b
	for i := len(old) - 1; i >= 0; i-- {
		if old[i] != 0 {
			break
		}
		toRemove++
	}
	if toRemove > 0 {
		*b = make([]uint64, len(old)-toRemove)
		copy(*b, old[:len(old)-toRemove])
	}
}
