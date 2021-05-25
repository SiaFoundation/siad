package modules

import (
	"bytes"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
)

const (
	// KeySize is the size of a registered key.
	KeySize = crypto.PublicKeySize

	// TweakSize is the size of the tweak which can be used to register multiple
	// values for the same pubkey.
	TweakSize = crypto.HashSize

	// RegistryDataSize is the amount of arbitrary data in bytes a renter can
	// register in the registry. It's RegistryEntrySize - all the fields besides
	// the data that get persisted.
	RegistryDataSize = 113

	// RegistryEntrySize is the size of a marshaled registry value on disk.
	RegistryEntrySize = 256

	// FileIDVersion is the current version we expect in a FileID.
	FileIDVersion = 1
)

var (
	// ErrInsufficientWork is returned when the revision numbers of two entries
	// match but the new entry doesn't have enough pow to replace the existing
	// one.
	ErrInsufficientWork = errors.New("entry doesn't have enough pow to replace existing entry")
	// ErrLowerRevNum is returned when the revision number of the data to
	// register isn't greater than the known revision number.
	ErrLowerRevNum = errors.New("provided revision number is invalid")
	// ErrSameRevNum is returned if the revision number of the data to register
	// is already registered.
	ErrSameRevNum = errors.New("provided revision number is already registered")
)

// RoundRegistrySize is a helper to correctly round up the size of a registry to
// the closest valid one.
func RoundRegistrySize(size uint64) uint64 {
	smallestRegUnit := uint64(RegistryEntrySize * 64)
	nUnits := size / smallestRegUnit
	if size%smallestRegUnit != 0 {
		nUnits++
	}
	return nUnits * smallestRegUnit
}

// RegistryValue is a value that can be registered on a host's registry.
type RegistryValue struct {
	Tweak    crypto.Hash
	Data     []byte
	Revision uint64
}

// SignedRegistryValue is a value that can be registered on a host's registry that has
// been signed.
type SignedRegistryValue struct {
	RegistryValue
	Signature crypto.Signature
}

// NewRegistryValue is a convenience method for creating a new RegistryValue
// from arguments.
func NewRegistryValue(tweak crypto.Hash, data []byte, rev uint64) RegistryValue {
	return RegistryValue{
		Tweak:    tweak,
		Data:     append([]byte{}, data...), // deep copy data to prevent races
		Revision: rev,
	}
}

// NewSignedRegistryValue is a convenience method for creating a new
// SignedRegistryValue from arguments.
func NewSignedRegistryValue(tweak crypto.Hash, data []byte, rev uint64, sig crypto.Signature) SignedRegistryValue {
	return SignedRegistryValue{
		RegistryValue: NewRegistryValue(tweak, data, rev),
		Signature:     sig,
	}
}

// Sign adds a signature to the RegistryValue.
func (entry RegistryValue) Sign(sk crypto.SecretKey) SignedRegistryValue {
	hash := entry.hash()
	return SignedRegistryValue{
		RegistryValue: entry,
		Signature:     crypto.SignHash(hash, sk),
	}
}

// ShouldUpdateWith returns true if entry2 would replace entry1 in a host's
// registry. It returns false if it shouldn't. An error might be returned in
// that case to specify the reason.
func (entry *RegistryValue) ShouldUpdateWith(entry2 *RegistryValue) (bool, error) {
	// Check entries for nil first.
	if entry == nil && entry2 != nil {
		return true, nil
	} else if entry != nil && entry2 == nil {
		return false, nil
	}
	// Both are not nil. Check revision numbers.
	if entry.Revision > entry2.Revision {
		return false, ErrLowerRevNum
	} else if entry.Revision < entry2.Revision {
		return true, nil
	}
	// Both have the same revision number. Check work.
	if entry.HasMoreWork(*entry2) {
		return false, ErrInsufficientWork
	} else if entry2.HasMoreWork(*entry) {
		return true, nil
	}
	// Both have the same work. Entries appear to be equal.
	return false, ErrSameRevNum
}

// IsRegistryEntryExistErr returns true if the provided error is related to the
// host already storing a higher priority registry entry.
func IsRegistryEntryExistErr(err error) bool {
	if errors.Contains(err, ErrLowerRevNum) || errors.Contains(err, ErrInsufficientWork) || errors.Contains(err, ErrSameRevNum) {
		return true
	}
	return false
}

// HasMoreWork returns 'true' if the hash of entry is larger than target's.
func (entry RegistryValue) HasMoreWork(target RegistryValue) bool {
	hEntry := entry.hash()
	hTarget := target.hash()
	return bytes.Compare(hTarget[:], hEntry[:]) > 0
}

// Verify verifies the signature on the RegistryValue.
func (entry SignedRegistryValue) Verify(pk crypto.PublicKey) error {
	hash := entry.hash()
	return crypto.VerifyHash(hash, pk, entry.Signature)
}

// hash hashes the registry value.
func (entry RegistryValue) hash() crypto.Hash {
	return crypto.HashAll(entry.Tweak, entry.Data, entry.Revision)
}
