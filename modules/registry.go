package modules

import "gitlab.com/NebulousLabs/Sia/crypto"

const (
	// KeySize is the size of a registered key.
	KeySize = crypto.PublicKeySize

	// TweakSize is the size of the tweak which can be used to register multiple
	// values for the same pubkey.
	TweakSize = crypto.HashSize

	// RegistryDataSize is the amount of arbitrary data in bytes a renter can
	// register in the registry.
	RegistryDataSize = 114
)

// RoundRegistrySize is a helper to correctly round up the size of a registry to
// the closest valid one.
func RoundRegistrySize(size uint64) uint64 {
	// TODO: this will be changed once the MR is rebased on top of the open
	// registry MDM MR.
	smallestRegUnit := uint64(256 * 64)
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
		Data:     data,
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
