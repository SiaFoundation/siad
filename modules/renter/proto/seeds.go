package proto

import (
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// Declaration of individual seed types for additional type safety.
type (
	// identifierSeed is the seed used to derive identifiers for file contracts.
	identifierSeed modules.Seed
	// identifierSigningSeed is the seed used to derive a signing key for the
	// identifier.
	identifierSigningSeed modules.Seed
	// secretKeySeed is the seed used to derive the secret key for file
	// contracts.
	secretKeySeed modules.Seed
	// RenterSeed is the master seed of the renter which is used to derive
	// other seeds.
	RenterSeed modules.Seed
)

type (
	// contractIdentifier is an identifer which is stored in the arbitrary data
	// section of each contract.
	contractIdentifier [32]byte
	// contractIdentifierSigningKey is the key used to sign a
	// contractIdentifier to verify that the identifier was created by the
	// renter.
	contractIdentifierSigningKey [64]byte
	// ContractSignedIdentifier is an identifer with a prefix and appended
	// signature, ready to be stored in the arbitrary data section of a
	// transaction.
	ContractSignedIdentifier [80]byte // 32 bytes identifier, 32 bytes signature, 16 bytes prefix
)

// contractIdentifierSeed derives a contractIdentifierSeed from a renterSeed.
func (rs RenterSeed) contractIdentifierSeed() (seed identifierSeed) {
	s := crypto.HashAll(rs, identifierSeedSpecifier)
	copy(seed[:], s[:])
	return
}

// contractSecretKeySeed derives a secretKeySeed from a renterSeed.
func (rs RenterSeed) contractSecretKeySeed() (seed secretKeySeed) {
	s := crypto.HashAll(rs, secretKeySeedSpecifier)
	copy(seed[:], s[:])
	return
}

// contractIdentifierSigningSeed derives an identifierSigningSeed from a renterSeed.
func (rs RenterSeed) contractIdentifierSigningSeed() (seed identifierSigningSeed) {
	s := crypto.HashAll(rs, signingKeySeedSpecifier)
	copy(seed[:], s[:])
	return
}

// identifier derives an identifier from the identifierSeed.
func (is identifierSeed) identifier(txn types.Transaction) (ci contractIdentifier) {
	s := crypto.HashAll(is, txn.SiacoinInputs[0].ParentID)
	copy(ci[:], s[:])
	return
}

// identifierSigningKey derives a signing key from the identifierSigningSeed.
func (iss identifierSigningSeed) identifierSigningKey(txn types.Transaction) (cisk contractIdentifierSigningKey) {
	s1 := crypto.HashAll(iss, txn.SiacoinInputs[0].ParentID, 0)
	s2 := crypto.HashAll(iss, txn.SiacoinInputs[0].ParentID, 1)
	copy(cisk[:32], s1[:])
	copy(cisk[32:], s2[:])
	return
}

// GenerateKeyPair generates a secret and a public key for a contract to be used
// in its unlock conditions.
func GenerateKeyPair(renterSeed RenterSeed, txn types.Transaction) (sk crypto.SecretKey, pk crypto.PublicKey) {
	// Get the secret key seed and wipe it afterwards.
	csks := renterSeed.contractSecretKeySeed()
	defer fastrand.Read(csks[:])

	// Combine the seed with the first SiacoinInput's parentID to create unique entropy for a
	// txn.
	entropy := crypto.HashAll(csks, txn.SiacoinInputs[0].ParentID)
	defer fastrand.Read(entropy[:])

	// Use the enropy to generate the keypair.
	return crypto.GenerateKeyPairDeterministic([crypto.EntropySize]byte(entropy))
}

// EphemeralRenterSeed creates a renterSeed for creating file contracts.
// NOTE: The seed returned by this function should be wiped once it's no longer
// in use.
func EphemeralRenterSeed(walletSeed modules.Seed, blockheight types.BlockHeight) RenterSeed {
	var renterSeed RenterSeed
	rs := crypto.HashAll(walletSeed, renterSeedSpecifier, blockheight/ephemeralSeedInterval)
	copy(renterSeed[:], rs[:])

	// Sanity check seed length.
	if len(renterSeed) != len(rs) {
		build.Critical("sanity check failed: renterSeed != rs")
	}
	return renterSeed
}

// PrefixedSignedIdentifier is a helper function that creates a prefixed and
// signed identifier using a renter key and siacoin input's parent.
// NOTE: Always use PrefixedSignedIdentifier when creating identifiers for
// filecontracts. It wipes all the secrets required for creating the identifier
// from memory safely.
func PrefixedSignedIdentifier(renterSeed RenterSeed, txn types.Transaction) (ContractSignedIdentifier, error) {
	// Get the seeds and wipe them after we are done using them.
	cis := renterSeed.contractIdentifierSeed()
	defer fastrand.Read(cis[:])
	ciss := renterSeed.contractIdentifierSigningSeed()
	defer fastrand.Read(ciss[:])
	// Get identifier and signing key. Wipe the signing key after we are done
	// using it. The identifier is public anyway.
	identifier := cis.identifier(txn)
	signingKey := ciss.identifierSigningKey(txn)
	defer fastrand.Read(signingKey[:])
	// Create the cipher for signing the identifier.
	sk, err := crypto.NewSiaKey(crypto.TypeThreefish, signingKey[:])
	if err != nil {
		return ContractSignedIdentifier{}, err
	}
	// Pad the identifier and sign it.
	signature := sk.EncryptBytes(append(identifier[:], make([]byte, 32)...))
	// Create the signed identifer object.
	var csi ContractSignedIdentifier
	// TODO change this to use the PrefixFileContractIdentifier in the future
	// once 1.4.0 has been released for long enough that nodes should support
	// it.
	copy(csi[:16], modules.PrefixNonSia[:])
	copy(csi[16:48], identifier[:])
	copy(csi[48:], signature[:])
	return csi, nil
}
