package modules

import (
	"bytes"
	"fmt"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/types"
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

const (
	// RegistryEntryDataSize is the expected size of a registry update without a
	// pubkey.
	RegistryEntryDataSize = 34

	// RegistryEntryDataSizeWithPubKey is the expected size of a registry update
	// with a pubkey.
	RegistryEntryDataSizeWithPubKey = RegistryEntryDataSize + crypto.PublicKeySize
)

var (
	// ErrLowerRevNum is returned when the revision number of the data to
	// register isn't greater than the known revision number.
	ErrLowerRevNum = errors.New("provided revision number is invalid")
	// ErrSameRevNum is returned if the revision number of the data to register
	// is already registered and the work is insufficient to overwrite the
	// revision.
	ErrSameRevNum = errors.New("provided revision number is already registered")
	// ErrSameWork is returned when an entry can't be updated because both
	// revision and work are the same and the new entry can't be prioritized
	// based on its pubkey.
	ErrSameWork = errors.New("provided registry update has same amount of work as previous one")
	// ErrSameEntry is returned if the entry can't be updated and is considered
	// equal to the existing entry based on its revision number, work and
	// pubkey.
	ErrSameEntry = errors.New("provided registry update and old update are either both primary updates or both secondary updates")
	// ErrUnexpectedEntryLength is returned when an operation fails due to the
	// registry entry containing an unexpted amount of data.
	ErrUnexpectedEntryLength = fmt.Errorf("unexpected entry length, expect %v or %v", RegistryEntryDataSize, RegistryEntryDataSizeWithPubKey)
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
	rv := RegistryValue{
		Data:     append([]byte{}, data...), // deep copy data to prevent races
		Tweak:    tweak,
		Revision: rev,
	}
	return rv
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

// IsLowerPrioEntryErr indicates whether an error was caused by a registry entry
// not having sufficient priority to be overwritten.
func IsLowerPrioEntryErr(err error) bool {
	return errors.Contains(err, ErrLowerRevNum) || errors.Contains(err,
		ErrSameRevNum) || errors.Contains(err, ErrSameWork) || errors.Contains(err,
		ErrSameEntry)
}

// HasMoreWork returns 'true' if the hash of entry is larger than target's.
func (entry RegistryValue) HasMoreWork(target RegistryValue) bool {
	hEntry := entry.work()
	hTarget := target.work()
	return bytes.Compare(hTarget[:], hEntry[:]) > 0
}

// CanUpdateWith checks whether entry can be overwritten by entry2 based on its
// revision numbers, work and pubkey.
func (entry RegistryValue) CanUpdateWith(entry2 RegistryValue, hpk types.SiaPublicKey) error {
	if entry.Revision > entry2.Revision {
		return ErrLowerRevNum
	} else if entry2.Revision > entry.Revision {
		return nil
	}
	if entry.HasMoreWork(entry2) {
		return ErrSameRevNum
	} else if entry2.HasMoreWork(entry) {
		return nil
	}
	entryHPK, err := entry.ParsePubKey()
	if err != nil {
		return err
	}
	entry2HPK, err := entry2.ParsePubKey()
	if err != nil {
		return err
	}
	entryIsOnHost := entryHPK != nil && entryHPK.Equals(hpk)
	entry2IsOnHost := entry2HPK != nil && entry2HPK.Equals(hpk)
	if entryIsOnHost && !entry2IsOnHost {
		return ErrSameWork
	} else if entry2IsOnHost && !entryIsOnHost {
		return nil
	}
	return ErrSameEntry
}

// ValidateData validates the payload of an entry.
func (entry RegistryValue) ValidateData() error {
	if len(entry.Data) != RegistryEntryDataSize && len(entry.Data) != RegistryEntryDataSizeWithPubKey {
		return ErrUnexpectedEntryLength
	}
	return nil
}

// ParsePubKey tries to parse a pubkey from a registry entry. It returns `nil`
// if no key is found.
func (entry RegistryValue) ParsePubKey() (*types.SiaPublicKey, error) {
	switch len(entry.Data) {
	case RegistryEntryDataSize:
		return nil, nil
	case RegistryEntryDataSizeWithPubKey:
		var pk crypto.PublicKey
		copy(pk[:], entry.Data[RegistryEntryDataSize:])
		spk := types.Ed25519PublicKey(pk)
		return &spk, nil
	default:
	}
	return nil, ErrUnexpectedEntryLength
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

// work returns the work of the registry value.
func (entry RegistryValue) work() crypto.Hash {
	data := entry.Data
	// For entries with pubkeys, ignore the pubkey.
	if len(data) == RegistryEntryDataSizeWithPubKey {
		data = data[:RegistryEntryDataSize]
	}
	return crypto.HashAll(entry.Tweak, data, entry.Revision)
}
