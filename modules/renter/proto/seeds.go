package proto

import (
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
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
	contractIdentifier           [32]byte
	contractIdentifierSigningKey [32]byte
	contractSecretKey            [32]byte
	contractSignedIdentifier     [80]byte // 32 bytes identifier, 32 bytes signature, 16 bytes prefix
)

// contractIdentifierSeed derives a contractIdentifierSeed from a renterSeed.
func (rs RenterSeed) contractIdentifierSeed() (seed identifierSeed) {
	s := crypto.HashAll(rs, "identifier_seed")
	copy(seed[:], s[:])
	return
}

// contractSecretKeySeed derives a secretKeySeed from a renterSeed.
func (rs RenterSeed) contractSecretKeySeed() (seed secretKeySeed) {
	s := crypto.HashAll(rs, "secret_key_seed")
	copy(seed[:], s[:])
	return
}

// contractIdentifierSigningSeed derives an identifierSigningSeed from a renterSeed.
func (rs RenterSeed) contractIdentifierSigningSeed() (seed identifierSigningSeed) {
	s := crypto.HashAll(rs, "signing_key_seed")
	copy(seed[:], s[:])
	return
}

// identifier derives an identifier from the identifierSeed.
func (is identifierSeed) identifier(sci types.SiacoinInput) (ci contractIdentifier) {
	s := crypto.HashAll(is, sci)
	copy(ci[:], s[:])
	return
}

// identifierSigningKey derives a signing key from the identifierSigningSeed.
func (iss identifierSigningSeed) identifierSigningKey(sci types.SiacoinInput) (cisk contractIdentifierSigningKey) {
	s := crypto.HashAll(iss, sci)
	copy(cisk[:], s[:])
	return
}

// secretKey derives a secret key for the contract from a secretKeySeed.
func (sks secretKeySeed) secretKey(sci types.SiacoinInput) (csk contractSecretKey) {
	s := crypto.HashAll(sks, sci)
	copy(csk[:], s[:])
	return
}

// EphemeralRenterSeed creates a renterSeed for creating file contracts.
func EphemeralRenterSeed(walletSeed modules.Seed, blockheight types.BlockHeight) RenterSeed {
	var renterSeed RenterSeed
	rs := crypto.HashAll(walletSeed, "renter", blockheight/ephemeralSeedInterval)
	copy(renterSeed[:], rs[:])

	// Sanity check seed length.
	if len(renterSeed) != len(rs) {
		build.Critical("sanity check failed: renterSeed != rs")
	}
	return renterSeed
}

// prefixedSignedIdentifier is a helper function that creates a prefixed and
// signed identifier using a renter key and siacoin input.
func prefixedSignedIdentifier(renterSeed RenterSeed, sci types.SiacoinInput) (contractSignedIdentifier, error) {
	// Get identifier and signing key.
	identifier := renterSeed.contractIdentifierSeed().identifier(sci)
	signingKey := renterSeed.contractIdentifierSigningSeed().identifierSigningKey(sci)
	// Pad the signing key since threefish requires 64 bytes of entropy.
	sk, err := crypto.NewSiaKey(crypto.TypeThreefish, append(signingKey[:], make([]byte, 32)...))
	if err != nil {
		return contractSignedIdentifier{}, err
	}
	// Pad the identifier and sign it.
	signature := sk.EncryptBytes(append(identifier[:], make([]byte, 32)...))
	// Create the signed identifer object.
	var csi contractSignedIdentifier
	copy(csi[:16], modules.PrefixFileContractIdentifier[:])
	copy(csi[16:48], identifier[:])
	copy(csi[48:], signature[:])
	return csi, nil
}
