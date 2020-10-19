package modules

import (
	"bytes"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/encoding"
)

const (
	// KeySize is the size of a registered key.
	KeySize = crypto.PublicKeySize

	// TweakSize is the size of the tweak which can be used to register multiple
	// values for the same pubkey.
	TweakSize = crypto.HashSize

	// RegistryDataSize is the amount of arbitrary data in bytes a renter can
	// register in the registry.
	RegistryDataSize = 113

	// RegistryEntrySize is the size of a marshaled registry value on disk.
	RegistryEntrySize = 256

	// FileIDVersion is the current version we expect in a FileID.
	FileIDVersion = 1
)

// FileID is the id associated with a registry entry.
type FileID struct {
	Version uint8 `json:"version"`

	ApplicationID string `json:"applicationid"`
	FileType      uint8  `json:"filetype"`
	FileName      string `json:"filename"`
}

// Tweak creates the tweak from a FileID object.
func (fid FileID) Tweak() (crypto.Hash, error) {
	b := bytes.NewBuffer(nil)
	enc := encoding.NewEncoder(b)
	_ = enc.Encode(fid.Version)
	_ = enc.Encode(fid.ApplicationID)
	_ = enc.Encode(fid.FileType)
	_ = enc.Encode(fid.FileName)
	if err := enc.Err(); err != nil {
		return crypto.Hash{}, err
	}
	return crypto.HashBytes(b.Bytes()), nil
}

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
	hash := crypto.HashAll(entry.Tweak, entry.Data, entry.Revision)
	return SignedRegistryValue{
		RegistryValue: entry,
		Signature:     crypto.SignHash(hash, sk),
	}
}

// Verify verifies the signature on the RegistryValue.
func (entry SignedRegistryValue) Verify(pk crypto.PublicKey) error {
	hash := crypto.HashAll(entry.Tweak, entry.Data, entry.Revision)
	return crypto.VerifyHash(hash, pk, entry.Signature)
}
