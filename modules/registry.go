package modules

import (
	"bytes"

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

	// RegistryPubKeyHashSize defines the number of bytes taken from the
	// beginning of a PubKey hash that are expected at the beginning of a
	// registry entry with pubkey.
	RegistryPubKeyHashSize = 20
)

const (
	// RegistryTypeInvalid is the type of an entry that didn't have it's type
	// field initialized correctly.
	RegistryTypeInvalid = RegistryEntryType(iota)
	// RegistryTypeWithoutPubkey is the type of an entry that doesn't contain a
	// pubkey. All of the data is considered to be arbitrary.
	RegistryTypeWithoutPubkey
	// RegistryTypeWithPubkey is the type of an entry which is expected to have
	// a RegistryPubKeyHashSize long hash of a host's pubkey at the beginning of
	// its data. The key is used to determine whether an entry is considered a
	// primary or secondary entry on a host.
	RegistryTypeWithPubkey
)

type (
	// RegistryEntryType signals the type of a registry entry.
	RegistryEntryType uint8
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
	// ErrRegistryEntryDataMalformed is returned when a registry entry contains
	// unexpected data. e.g. when a pubkey is expected but the data is too
	// short.
	ErrRegistryEntryDataMalformed = errors.New("entry data is malformed")
	// ErrInvalidRegistryEntryType is returned when an entry with the
	// RegistryTypeInvalid is encountered.
	ErrInvalidRegistryEntryType = errors.New("invalid entry type")
	// ErrUnknownRegistryEntryType is returned when an entry has an unknown
	// entry type.
	ErrUnknownRegistryEntryType = errors.New("unknown entry type")
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
	Type     RegistryEntryType
}

// SignedRegistryValue is a value that can be registered on a host's registry that has
// been signed.
type SignedRegistryValue struct {
	RegistryValue
	Signature crypto.Signature
}

// NewRegistryValue is a convenience method for creating a new RegistryValue
// from arguments.
func NewRegistryValue(tweak crypto.Hash, data []byte, rev uint64, t RegistryEntryType) RegistryValue {
	return RegistryValue{
		Tweak:    tweak,
		Data:     append([]byte{}, data...), // deep copy data to prevent races
		Revision: rev,
		Type:     t,
	}
}

// NewSignedRegistryValue is a convenience method for creating a new
// SignedRegistryValue from arguments.
func NewSignedRegistryValue(tweak crypto.Hash, data []byte, rev uint64, sig crypto.Signature, t RegistryEntryType) SignedRegistryValue {
	return SignedRegistryValue{
		RegistryValue: NewRegistryValue(tweak, data, rev, t),
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
func (entry *RegistryValue) ShouldUpdateWith(entry2 *RegistryValue, hpk types.SiaPublicKey) (bool, error) {
	// Check entries for nil first.
	if entry2 == nil {
		// A nil entry never replaces an existing entry.
		return false, nil
	} else if entry == nil {
		// A non-nil entry always replaces a nil entry.
		return true, nil
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
	// Check if any of them are primary keys?
	rvPrimary := entry.IsPrimaryEntry(hpk)
	rv2Primary := entry2.IsPrimaryEntry(hpk)
	// If the existing entry isn't primary, but the new one is, update.
	if !rvPrimary && rv2Primary {
		return true, nil
	}
	// The entries are equal in every regard.
	return false, ErrSameRevNum
}

// IsPrimaryEntry returns true if an entry is primary. This means that the entry
// was specifically intended for this host. Only an entry containing a partial
// hash of the provided pubkey can be primary.
func (entry RegistryValue) IsPrimaryEntry(hpk types.SiaPublicKey) bool {
	if entry.Type != RegistryTypeWithPubkey {
		return false // if the entry doesn't have a pubkey it can't be a primary entry
	}
	hpkh := crypto.HashObject(hpk)
	return bytes.Equal(hpkh[:RegistryPubKeyHashSize], entry.Data[:RegistryPubKeyHashSize])
}

// IsRegistryEntryExistErr returns true if the provided error is related to the
// host already storing a higher priority registry entry.
func IsRegistryEntryExistErr(err error) bool {
	return errors.Contains(err, ErrLowerRevNum) || errors.Contains(err, ErrInsufficientWork) || errors.Contains(err, ErrSameRevNum)
}

// HasMoreWork returns 'true' if the hash of entry is larger than target's.
func (entry RegistryValue) HasMoreWork(target RegistryValue) bool {
	hEntry := entry.work()
	hTarget := target.work()
	return bytes.Compare(hTarget[:], hEntry[:]) > 0
}

// Verify verifies the signature on the RegistryValue.
func (entry SignedRegistryValue) Verify(pk crypto.PublicKey) error {
	// Check the integrity of the data first.
	switch entry.Type {
	case RegistryTypeInvalid:
		return ErrInvalidRegistryEntryType
	case RegistryTypeWithoutPubkey:
		// nothing to verify
	case RegistryTypeWithPubkey:
		// verify data length
		if len(entry.Data) < RegistryPubKeyHashSize {
			return ErrRegistryEntryDataMalformed
		}
	default:
		return ErrUnknownRegistryEntryType
	}
	// Check the signature.
	hash := entry.hash()
	return crypto.VerifyHash(hash, pk, entry.Signature)
}

// hash hashes the registry value.
func (entry RegistryValue) hash() crypto.Hash {
	// Handle legacy values without pubkey.
	if entry.Type == RegistryTypeWithoutPubkey {
		return crypto.HashAll(entry.Tweak, entry.Data, entry.Revision)
	}
	// More recent values have the type signed as well.
	return crypto.HashAll(entry.Tweak, entry.Data, entry.Revision, entry.Type)
}

// work returns the work of the registry value.
func (entry RegistryValue) work() crypto.Hash {
	data := entry.Data
	// For entries with pubkeys, ignore the pubkey at the beginning and the
	// version at the end. That way a legacy entry containing the same data as
	// an entry with version byte will still have the same work.
	if entry.Type == RegistryTypeWithPubkey {
		data = data[RegistryPubKeyHashSize:]
	}
	// This is the same as the hash() but without the version.
	return crypto.HashAll(entry.Tweak, data, entry.Revision)
}
