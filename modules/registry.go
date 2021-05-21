package modules

import (
	"bytes"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
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

	// HostPubKeyHashSize is the expected size of a host's pubkey's hash within
	// a registry entry.
	HostPubKeyHashSize = 20
)

const (
	// RegistryEntryVersionInvalid is a sentinel value for a version byte that
	// wasn't set.
	RegistryEntryVersionInvalid = iota

	// RegistryEntryVersionNoPubKey is the version of an entry without
	// pubkeyhash.
	RegistryEntryVersionNoPubKey

	// RegistryEntryVersionWithPubKey is the version of an entry with
	// pubkeyhash.
	RegistryEntryVersionWithPubKey
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

// NewRegistryValueWithPubKey creates a registry value with a host's pubkey. The
// data is appended to the pubkey together with a version byte. This function
// returns an empty entry if the resulting data exceeds the max. In a testing
// build it will panic in that case. If the data is too short to fill the entry,
// it is padded with zeros.
func NewRegistryValueWithPubKey(tweak crypto.Hash, hpk types.SiaPublicKey, data []byte, rev uint64) RegistryValue {
	// Compute the hash of the pubkey, take the first HostPubKeyHashSize bytes
	// of it and append the data.
	hpkh := crypto.HashObject(hpk)
	d := append(hpkh[:HostPubKeyHashSize], data...)
	if len(d) > RegistryDataSize-1 {
		// Sanity check that the data + a version byte aren't greater than the max
		// allowed entry size.
		build.Critical("NewRegistryValueWithPubKey: too much data provided")
		return RegistryValue{}
	} else if diff := RegistryDataSize - len(d); diff > 0 {
		// If they are smaller than the max, add padding.
		d = append(d, make([]byte, diff)...)
	}
	// Set the version byte.
	d[RegistryDataSize-1] = RegistryEntryVersionWithPubKey
	return NewRegistryValue(tweak, d, rev)
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
	// First, check the revision.
	if entry.Revision > entry2.Revision {
		return ErrLowerRevNum
	} else if entry2.Revision > entry.Revision {
		return nil
	}
	// Then the work.
	if entry.HasMoreWork(entry2) {
		return ErrSameRevNum
	} else if entry2.HasMoreWork(entry) {
		return nil
	}
	// Finally check whether any of them are primary entries.
	entryIsPrimary := entry.IsPrimaryEntry(hpk)
	entry2IsPrimary := entry2.IsPrimaryEntry(hpk)
	if entryIsPrimary && !entry2IsPrimary {
		return ErrSameWork
	} else if entry2IsPrimary && !entryIsPrimary {
		return nil
	}
	return ErrSameEntry
}

// Version returns the version of the entry. For an unknown version or one that
// wasn't set RegistryEntryVersionInvalid is returned.
func (entry RegistryValue) Version() uint8 {
	if len(entry.Data) < RegistryDataSize {
		return RegistryEntryVersionNoPubKey
	}
	switch entry.Data[RegistryDataSize-1] {
	case RegistryEntryVersionNoPubKey:
		return RegistryEntryVersionNoPubKey
	case RegistryEntryVersionWithPubKey:
		return RegistryEntryVersionWithPubKey
	default:
		return RegistryEntryVersionInvalid
	}
}

// IsPrimaryEntry compares the pubkey hash within the registry value to the
// provided one to determine whether the entry is a primary one. This returns
// `false` for entries without keys.
func (entry RegistryValue) IsPrimaryEntry(hpk types.SiaPublicKey) bool {
	if entry.Version() != RegistryEntryVersionWithPubKey {
		return false
	}
	hpkh := crypto.HashObject(hpk)
	return bytes.Equal(entry.HostPubKeyHash(), hpkh[:HostPubKeyHashSize])
}

// HostPubKeyHash tries to parse a pubkey from a registry entry. It returns `nil` if
// no key is found.
func (entry RegistryValue) HostPubKeyHash() []byte {
	if entry.Version() != RegistryEntryVersionWithPubKey {
		return nil // no pubkey
	}
	// Return the first HostPubKeyHashSize bytes for the pubkey hash.
	return entry.Data[:HostPubKeyHashSize]
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
	// For entries with pubkeys, ignore the pubkey at the beginning and the
	// version at the end. That way a legacy entry containing the same data as
	// an entry with version byte will still have the same work.
	if entry.Version() == RegistryEntryVersionWithPubKey {
		data = data[HostPubKeyHashSize : len(data)-1]
	}
	return crypto.HashAll(entry.Tweak, data, entry.Revision)
}
