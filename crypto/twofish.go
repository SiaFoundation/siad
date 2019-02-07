package crypto

import (
	"crypto/cipher"
	"fmt"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"golang.org/x/crypto/twofish"
)

const (
	// twofishOverhead is the number of bytes added by EncryptBytes.
	twofishOverhead = 28
)

var (
	// ErrInsufficientLen is an error when supplied ciphertext is not
	// long enough to contain a nonce.
	ErrInsufficientLen = errors.New("supplied ciphertext is not long enough to contain a nonce")
)

type (
	// twofishKey is a key used for encrypting and decrypting data.
	twofishKey [EntropySize]byte
)

// generateTwofishKey produces a twofishKey that can be used for encrypting and
// decrypting data using Twofish-GCM.
func generateTwofishKey() (key twofishKey) {
	fastrand.Read(key[:])
	return
}

// newCipher creates a new Twofish cipher from the key.
func (key twofishKey) newCipher() cipher.Block {
	cipher, err := twofish.NewCipher(key[:])
	if err != nil {
		panic("NewCipher only returns an error if len(key) != 16, 24, or 32.")
	}
	return cipher
}

// newTwofishKey creates a new twofishKey from a given entropy.
func newTwofishKey(entropy []byte) (key twofishKey, err error) {
	// check key length
	if len(entropy) != len(key) {
		err = fmt.Errorf("twofish key should have size %v but was %v",
			EntropySize, len(entropy))
		return
	}
	// create key
	copy(key[:], entropy)
	return
}

// DecryptBytes decrypts a ciphertext created by EncryptPiece. The nonce is
// expected to be the first 12 bytes of the ciphertext.
func (key twofishKey) DecryptBytes(ct Ciphertext) ([]byte, error) {
	// Create the cipher.
	aead, err := cipher.NewGCM(key.newCipher())
	if err != nil {
		return nil, errors.AddContext(err, "NewGCM should only return an error if twofishCipher.BlockSize != 16")
	}
	return DecryptWithNonce(ct, aead)
}

// DecryptBytesInPlace decrypts the ciphertext created by EncryptBytes. The
// nonce is expected to be the first 12 bytes of the ciphertext.
// DecryptBytesInPlace reuses the memory of ct to be able to operate in-place.
// This means that ct can't be reused after calling DecryptBytesInPlace.
func (key twofishKey) DecryptBytesInPlace(ct Ciphertext, blockIndex uint64) ([]byte, error) {
	if blockIndex != 0 {
		return nil, errors.New("twofish doesn't support a blockIndex != 0")
	}
	// Create the cipher.
	aead, err := cipher.NewGCM(key.newCipher())
	if err != nil {
		return nil, errors.AddContext(err, "NewGCM should only return an error if twofishCipher.BlockSize != 16")
	}

	// Check for a nonce.
	if len(ct) < aead.NonceSize() {
		return nil, ErrInsufficientLen
	}

	// Decrypt the data.
	nonce := ct[:aead.NonceSize()]
	ciphertext := ct[aead.NonceSize():]
	return aead.Open(ciphertext[:0], nonce, ciphertext, nil)
}

// Derive derives a child key for a given combination of chunk and piece index.
func (key twofishKey) Derive(chunkIndex, pieceIndex uint64) CipherKey {
	entropy := HashAll(key, chunkIndex, pieceIndex)
	ck, err := NewSiaKey(TypeTwofish, entropy[:])
	if err != nil {
		panic("this should not be possible when deriving from a valid key")
	}
	return ck
}

// EncryptBytes encrypts arbitrary data using the TwofishKey, prepending a 12
// byte nonce to the ciphertext in the process.  GCM and prepends the nonce (12
// bytes) to the ciphertext.
func (key twofishKey) EncryptBytes(piece []byte) Ciphertext {
	// Create the cipher.
	aead, err := cipher.NewGCM(key.newCipher())
	if err != nil {
		panic("NewGCM only returns an error if twofishCipher.BlockSize != 16")
	}
	return EncryptWithNonce(piece, aead)
}

// Key returns the twofish key.
func (key twofishKey) Key() []byte {
	return key[:]
}

// Type returns the type of the twofish key.
func (twofishKey) Type() CipherType {
	return TypeTwofish
}
