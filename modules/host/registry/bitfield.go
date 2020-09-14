package registry

type (
	bitfield []uint64
)

func (b bitfield) Len() uint64 {
	return uint64(len(b)) * 64
}

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
