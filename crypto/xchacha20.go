package crypto

import (
	"fmt"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"github.com/aead/chacha20/chacha"
)

// numRounds defines the number of ChaCha rounds. 20 is preferable for
// portability reasons. Although 8 or 12 rounds may be secure, it is not as
// easily found in other programming languages.
const numRounds = 20

// xChaCha20Cipher implements the CipherKey interface using the XChaCha20 stream
// cipher. It embeds the *chacha.Cipher type. All Encryption/Decryption methods
// reset the cipher keystream when they are done. To skip ahead in the keystream
// you can manuallly set the counter, or use DecryptBytesInPlace with a
// blockIndex which will set the counter automatically. Note that this cipher is
// not safe for updating/editing chunks: encrypting different data using the
// same keystream will reveal the XOR of the data encrypted.
type xChaCha20CipherKey struct {
	*chacha.Cipher
	nonce [chacha.XNonceSize]byte
	key   [chacha.KeySize]byte
}

// Key returns the XChaCha20 key.
func (cipher xChaCha20CipherKey) Key() []byte {
	return cipher.key[:]
}

// Nonce returns the XChaCha20 nonce.
func (cipher xChaCha20CipherKey) Nonce() []byte {
	return cipher.nonce[:]
}

// Type returns the type of the XChaCha20 key.
func (cipher xChaCha20CipherKey) Type() CipherType {
	return TypeXChaCha20
}

// generateXChaCha20Key produces a xChaCha20Kcipher that can be used for encrypting
// and decrypting data using XChaCha20.
func generateXChaCha20CipherKey() xChaCha20CipherKey {
	entropy := make([]byte, chacha.KeySize+chacha.XNonceSize)
	fastrand.Read(entropy[:])
	key, err := newXChaCha20CipherKey(entropy)
	if err != nil {
		panic(fmt.Sprintf("Error generating a new XChaCha20 key: %s", err.Error()))
	}
	return key
}

// newXChaCha20Key creates a new xChaCha20CipherKey from a given entropy.
func newXChaCha20CipherKey(entropy []byte) (xChaCha20CipherKey, error) {
	if len(entropy) != chacha.KeySize+chacha.XNonceSize {
		return xChaCha20CipherKey{}, errors.New("Incorrect entropy length for XChaCha20 cipher")
	}

	// Copy entropy into key and nonce values.
	var key [chacha.KeySize]byte
	var nonce [chacha.XNonceSize]byte
	copy(key[:], entropy[:chacha.KeySize])
	copy(nonce[:], entropy[chacha.KeySize:])

	cipher, err := chacha.NewCipher(nonce[:], key[:], numRounds)
	if err != nil {
		return xChaCha20CipherKey{}, err
	}

	return xChaCha20CipherKey{
		cipher,
		nonce,
		key,
	}, nil
}

// DecryptBytes decrypts a ciphertext created by EncryptPiece.
func (cipher xChaCha20CipherKey) DecryptBytes(ciphertext Ciphertext) ([]byte, error) {
	defer cipher.SetCounter(0) // Reset the cipher key stream.

	plaintext := make([]byte, len(ciphertext))
	cipher.XORKeyStream(plaintext, ciphertext)
	return plaintext, nil
}

// DecryptBytesInPlace decrypts a ciphertext created by EncryptBytes.
func (cipher xChaCha20CipherKey) DecryptBytesInPlace(ct Ciphertext, blockIndex uint64) ([]byte, error) {
	defer cipher.SetCounter(0) // Reset the cipher key stream.

	cipher.SetCounter(blockIndex)
	cipher.XORKeyStream(ct, ct)
	return ct, nil
}

// Derive derives a child key for a given combination of chunk and piece index.
func (cipher xChaCha20CipherKey) Derive(chunkIndex, pieceIndex uint64) CipherKey {
	nonceEntropy := HashAll(cipher.nonce, chunkIndex, pieceIndex)
	ck, err := NewSiaKey(TypeXChaCha20, append(cipher.key[:chacha.KeySize], nonceEntropy[:chacha.XNonceSize]...))
	if err != nil {
		panic("this should not be possible when deriving from a valid key")
	}
	return ck
}

// EncryptBytes encrypts arbitrary data using the XChaCha20 key.
func (cipher xChaCha20CipherKey) EncryptBytes(plaintext []byte) Ciphertext {
	defer cipher.SetCounter(0) // Reset the cipher key stream.

	ciphertext := make([]byte, len(plaintext))
	cipher.XORKeyStream(ciphertext, plaintext)
	return ciphertext
}
