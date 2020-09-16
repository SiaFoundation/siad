package registry

import "math"

type (
	bitfield []uint64
)

// SetFirst finds the first unused gap in the bitfield and sets it.
func (b *bitfield) SetFirst() uint64 {
	// Search for a gap.
	for i := 0; i < len(*b); i++ {
		if (*b)[i] == math.MaxUint64 {
			continue
		}
		// Index with gap found. Search for gap.
		for j := uint64(0); j < 64; j++ {
			index := uint64(i)*64 + j
			if !b.IsSet(index) {
				b.Set(index)
				return index
			}
		}
	}
	// No gap found.
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
