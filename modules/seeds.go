package modules

import (
	"bytes"

	"github.com/dchest/threefish"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/types"
)

const (
	// FCSignedIdentiferSize is the size of a ContractSignedIdentifier
	FCSignedIdentiferSize = 80 // 32 bytes identifier, 32 bytes signature, 16 bytes prefix
)

// ErrCSIDoesNotMatchSeed is returned when a ContractSignedIdentifier was not
// created with the current seed used by the renter.
var ErrCSIDoesNotMatchSeed = errors.New("ContractSignedIdentifier signature bytes not equal")

var (
	// The following specifiers are used for deriving different seeds from the
	// wallet seed.
	identifierSeedSpecifier = types.NewSpecifier("identifierseed")
	renterSeedSpecifier     = types.NewSpecifier("renter")
	secretKeySeedSpecifier  = types.NewSpecifier("secretkeyseed")
	signingKeySeedSpecifier = types.NewSpecifier("signingkeyseed")

	// ephemeralSeedInterval is the amount of blocks after which we use a new
	// renter seed for creating file contracts.
	ephemeralSeedInterval = build.Select(build.Var{
		Dev:      types.BlockHeight(100),
		Standard: types.BlockHeight(1000),
		Testnet:  types.BlockHeight(1000),
		Testing:  types.BlockHeight(10),
	}).(types.BlockHeight)
)

// Declaration of individual seed types for additional type safety.
type (
	// identifierSeed is the seed used to derive identifiers for file contracts.
	identifierSeed Seed
	// identifierSigningSeed is the seed used to derive a signing key for the
	// identifier.
	identifierSigningSeed Seed
	// secretKeySeed is the seed used to derive the secret key for file
	// contracts.
	secretKeySeed Seed
	// RenterSeed is the master seed of the renter which is used to derive
	// other seeds.
	RenterSeed Seed
	// EphemeralRenterSeed is a renter seed derived from the master renter
	// seed. The master seed should never be used directly.
	EphemeralRenterSeed RenterSeed
)

type (
	// contractIdentifier is an identifier which is stored in the arbitrary data
	// section of each contract.
	contractIdentifier [32]byte
	// contractIdentifierSigningKey is the key used to sign a
	// contractIdentifier to verify that the identifier was created by the
	// renter.
	contractIdentifierSigningKey [64]byte
	// ContractSignedIdentifier is an identifier with a prefix and appended
	// signature, ready to be stored in the arbitrary data section of a
	// transaction.
	ContractSignedIdentifier [FCSignedIdentiferSize]byte
)

// contractIdentifierSeed derives a contractIdentifierSeed from a renterSeed.
func (rs EphemeralRenterSeed) contractIdentifierSeed() (seed identifierSeed) {
	s := crypto.HashAll(rs, identifierSeedSpecifier)
	copy(seed[:], s[:])
	return
}

// contractSecretKeySeed derives a secretKeySeed from a renterSeed.
func (rs EphemeralRenterSeed) contractSecretKeySeed() (seed secretKeySeed) {
	s := crypto.HashAll(rs, secretKeySeedSpecifier)
	copy(seed[:], s[:])
	return
}

// contractIdentifierSigningSeed derives an identifierSigningSeed from a renterSeed.
func (rs EphemeralRenterSeed) contractIdentifierSigningSeed() (seed identifierSigningSeed) {
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

// EphemeralRenterSeed creates a renterSeed for creating file contracts.
// NOTE: The seed returned by this function should be wiped once it's no longer
// in use.
func (rs RenterSeed) EphemeralRenterSeed(windowStart types.BlockHeight) EphemeralRenterSeed {
	var ephemeralSeed EphemeralRenterSeed
	ers := crypto.HashAll(rs, windowStart/ephemeralSeedInterval)
	copy(ephemeralSeed[:], ers[:])

	// Sanity check seed length.
	if len(ephemeralSeed) != len(rs) {
		build.Critical("sanity check failed: ephemeralSeed != rs")
	}
	return ephemeralSeed
}

// GenerateContractKeyPair generates a secret and a public key for a contract to
// be used in its unlock conditions.
func GenerateContractKeyPair(renterSeed EphemeralRenterSeed, txn types.Transaction) (sk crypto.SecretKey, pk crypto.PublicKey) {
	return GenerateContractKeyPairWithOutputID(renterSeed, txn.SiacoinInputs[0].ParentID)
}

// GenerateContractKeyPairWithOutputID generates a secret and a public key for a
// contract to be used in its unlock conditions.
func GenerateContractKeyPairWithOutputID(renterSeed EphemeralRenterSeed, inputParentID types.SiacoinOutputID) (sk crypto.SecretKey, pk crypto.PublicKey) {
	// Get the secret key seed and wipe it afterwards.
	csks := renterSeed.contractSecretKeySeed()
	defer fastrand.Read(csks[:])

	// Combine the seed with the first SiacoinInput's parentID to create unique entropy for a
	// txn.
	entropy := crypto.HashAll(csks, inputParentID)
	defer fastrand.Read(entropy[:])

	// Use the enropy to generate the keypair.
	return crypto.GenerateKeyPairDeterministic([crypto.EntropySize]byte(entropy))
}

// DeriveRenterSeed creates a renterSeed for creating file contracts.
// NOTE: The seed returned by this function should be wiped once it's no longer
// in use.
func DeriveRenterSeed(walletSeed Seed) RenterSeed {
	var renterSeed RenterSeed
	rs := crypto.HashAll(walletSeed, renterSeedSpecifier)
	copy(renterSeed[:], rs[:])

	// Sanity check seed length.
	if len(renterSeed) != len(rs) {
		build.Critical("sanity check failed: renterSeed != rs")
	}
	return renterSeed
}

// PrefixedSignedIdentifier is a helper function that creates a prefixed and
// signed identifier using a renter key and the first siacoin input of a
// transaction.
// NOTE: Always use PrefixedSignedIdentifier when creating identifiers for
// filecontracts. It wipes all the secrets required for creating the identifier
// from memory safely.
func PrefixedSignedIdentifier(renterSeed EphemeralRenterSeed, txn types.Transaction, hostKey types.SiaPublicKey) (ContractSignedIdentifier, crypto.Ciphertext) {
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
		// This should never happen. If it happens the contract won't be
		// recoverable.
		build.Critical("failed to create threefish key, this should never happen")
		return ContractSignedIdentifier{}, crypto.Ciphertext{}
	}
	// Pad the identifier and sign it but then only use 32 bytes of the
	// signature.
	signature := sk.EncryptBytes(append(identifier[:], make([]byte, 32)...))[:32]
	// Encrypt the hostKey.
	marshaledKey := encoding.Marshal(hostKey)
	padding := threefish.BlockSize - len(marshaledKey)%threefish.BlockSize
	encryptedKey := sk.EncryptBytes(append(marshaledKey, make([]byte, padding)...))
	// Create the signed identifier object.
	var csi ContractSignedIdentifier
	copy(csi[:16], PrefixNonSia[:])
	copy(csi[16:48], identifier[:])
	copy(csi[48:80], signature[:])
	return csi, encryptedKey
}

// IsValid checks the signature against a seed and contract to determine if it
// was created using the specified seed.
func (csi ContractSignedIdentifier) IsValid(renterSeed EphemeralRenterSeed, txn types.Transaction, hostKey crypto.Ciphertext) (types.SiaPublicKey, bool, error) {
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
	// Create the cipher for verifying the signature and decrypting the hostKey.
	sk, err := crypto.NewSiaKey(crypto.TypeThreefish, signingKey[:])
	if err != nil {
		build.Critical("Unable to generate New Sia Key", err)
		return types.SiaPublicKey{}, false, errors.AddContext(err, "error getting new Sia PublicKey")
	}
	// Pad the identifier and sign it but then only use 32 bytes of the
	// signature.
	signature := sk.EncryptBytes(append(identifier[:], make([]byte, 32)...))[:32]
	// Compare the signatures.
	if !bytes.Equal(signature, csi[48:80]) {
		return types.SiaPublicKey{}, false, ErrCSIDoesNotMatchSeed
	}
	// Decrypt the hostKey.
	hk, err := sk.DecryptBytes(hostKey)
	if err != nil {
		return types.SiaPublicKey{}, false, errors.AddContext(err, "error decrypting bytes")
	}
	// Decode the hostKey.
	var spk types.SiaPublicKey
	if err := encoding.Unmarshal(hk, &spk); err != nil {
		return types.SiaPublicKey{}, false, errors.AddContext(err, "error unmarshalling")
	}
	return spk, true, nil
}
