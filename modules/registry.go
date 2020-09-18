package modules

import "gitlab.com/NebulousLabs/Sia/crypto"

const (
	// KeySize is the size of a registered key.
	KeySize = crypto.PublicKeySize

	// TweakSize is the size of the tweak which can be used to register multiple
	// values for the same pubkey.
	TweakSize = crypto.HashSize

	// RegistryDataSize is the amount of arbitrary data a renter can register in
	// the registry.
	RegistryDataSize = 109
)

// RegistryValue is a value that can be registered on a host's registry.
type RegistryValue struct {
	Tweak    crypto.Hash
	Data     []byte
	Revision uint64

	// Signature that covers the above fields.
	Signature crypto.Signature
}

// Sign adds a signature to the RegistryValue.
func (entry *RegistryValue) Sign(sk crypto.SecretKey) {
	hash := crypto.HashAll(entry.Tweak, entry.Data, entry.Revision)
	entry.Signature = crypto.SignHash(hash, sk)
}

// Verify verifies the signature on the RegistryValue.
func (entry RegistryValue) Verify(pk crypto.PublicKey) error {
	hash := crypto.HashAll(entry.Tweak, entry.Data, entry.Revision)
	return crypto.VerifyHash(hash, pk, entry.Signature)
}
