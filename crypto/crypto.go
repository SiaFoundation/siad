package crypto

import (
	"crypto/cipher"
	"errors"

	"gitlab.com/NebulousLabs/fastrand"
)

var (
	// TypeDefaultRenter is the default CipherType that is used for
	// encrypting pieces of uploaded data.
	TypeDefaultRenter = TypeThreefish
	// TypeDefaultWallet is the default CipherType that is used for
	// wallet operations like encrypting the wallet files.
	TypeDefaultWallet = TypeTwofish

	// TypePlain means no encryption is used.
	TypePlain = CipherType{0, 0, 0, 0, 0, 0, 0, 1}
	// TypeTwofish is the type for the Twofish-GCM encryption.
	TypeTwofish = CipherType{0, 0, 0, 0, 0, 0, 0, 2}
	// TypeThreefish is the type for the Threefish encryption.
	TypeThreefish = CipherType{0, 0, 0, 0, 0, 0, 0, 3}
)

var (
	// ErrInvalidCipherType is returned upon encountering an unknown cipher
	// type.
	ErrInvalidCipherType = errors.New("provided cipher type is invalid")
)

type (
	// CipherType is an identifier for the individual ciphers provided by this
	// package.
	CipherType [8]byte

	// Ciphertext is an encrypted []byte.
	Ciphertext []byte

	// CipherKey is a key with Sia specific encryption/decryption methods.
	CipherKey interface {
		// Key returns the underlying key.
		Key() []byte

		// Type returns the type of the key.
		Type() CipherType

		// EncryptBytes encrypts the given plaintext and returns the
		// ciphertext.
		EncryptBytes([]byte) Ciphertext

		// DecryptBytes decrypts the given ciphertext and returns the
		// plaintext.
		DecryptBytes(Ciphertext) ([]byte, error)

		// DecryptBytesInPlace decrypts the given ciphertext and returns the
		// plaintext. It will reuse the memory of the ciphertext which means
		// that it's not save to use it after calling DecryptBytesInPlace.
		DecryptBytesInPlace(Ciphertext, uint64) ([]byte, error)

		// Derive derives a child cipherkey given a provided chunk index and
		// piece index.
		Derive(chunkIndex, pieceIndex uint64) CipherKey
	}
)

// String creates a string representation of a CipherType that can be converted
// into a type with FromString.
func (ct CipherType) String() string {
	switch ct {
	case TypePlain:
		return "plaintext"
	case TypeTwofish:
		return "twofish-gcm"
	case TypeThreefish:
		return "threefish512"
	default:
		panic(ErrInvalidCipherType)
	}
}

// FromString reads a CipherType from a string.
func (ct *CipherType) FromString(s string) error {
	switch s {
	case "plaintext":
		*ct = TypePlain
	case "twofish-gcm":
		*ct = TypeTwofish
	case "threefish512":
		*ct = TypeThreefish
	default:
		return ErrInvalidCipherType
	}
	return nil
}

// Overhead reports the overhead produced by a CipherType in bytes.
func (ct CipherType) Overhead() uint64 {
	switch ct {
	case TypePlain, TypeThreefish:
		return 0
	case TypeTwofish:
		return twofishOverhead
	default:
		panic(ErrInvalidCipherType)
	}
}

// NewWalletKey is a helper method which is meant to be used only if the type
// and entropy are guaranteed to be valid. In the wallet this is always the
// case since we always use hashes as the entropy and we don't read the key
// from file.
func NewWalletKey(entropy Hash) CipherKey {
	sk, err := NewSiaKey(TypeDefaultWallet, entropy[:])
	if err != nil {
		panic(err)
	}
	return sk
}

// NewSiaKey creates a new SiaKey from the provided type and entropy.
func NewSiaKey(ct CipherType, entropy []byte) (CipherKey, error) {
	switch ct {
	case TypePlain:
		return plainTextCipherKey{}, nil
	case TypeTwofish:
		return newTwofishKey(entropy)
	case TypeThreefish:
		return newThreefishKey(entropy)
	default:
		return nil, ErrInvalidCipherType
	}
}

// GenerateSiaKey creates a new SiaKey from the provided type and
// entropy.
func GenerateSiaKey(ct CipherType) CipherKey {
	switch ct {
	case TypePlain:
		return plainTextCipherKey{}
	case TypeTwofish:
		return generateTwofishKey()
	case TypeThreefish:
		return generateThreefishKey()
	default:
		panic(ErrInvalidCipherType)
	}
}

// IsValidCipherType returns true if ct is a known CipherType and false
// otherwise.
func IsValidCipherType(ct CipherType) bool {
	switch ct {
	case TypePlain, TypeTwofish, TypeThreefish:
		return true
	default:
		return false
	}
}

// RandomCipherType is a helper function for testing. It's located in the
// crypto package to centralize all the types within one file to make future
// changes to them easy.
func RandomCipherType() CipherType {
	types := []CipherType{TypePlain, TypeTwofish}
	return types[fastrand.Intn(len(types))]
}

// EncryptWithNonce encrypts plaintext with aead and prepends a random nonce.
func EncryptWithNonce(plaintext []byte, aead cipher.AEAD) []byte {
	nonce := fastrand.Bytes(aead.NonceSize())
	return aead.Seal(nonce, nonce, plaintext, nil)
}

// DecryptWithNonce decrypts ciphertext with aead, using a prepended nonce.
func DecryptWithNonce(ciphertext []byte, aead cipher.AEAD) ([]byte, error) {
	if len(ciphertext) < aead.NonceSize() {
		return nil, ErrInsufficientLen
	}
	nonce, ciphertext := ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():]
	return aead.Open(nil, nonce, ciphertext, nil)
}
