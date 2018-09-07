package crypto

import (
	"bytes"
	"compress/gzip"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
)

// TestTwofishEncryption checks that encryption and decryption works correctly.
func TestTwofishEncryption(t *testing.T) {
	// Get a key for encryption.
	key := generateTwofishKey()

	// Encrypt and decrypt a zero plaintext, and compare the decrypted to the
	// original.
	plaintext := make([]byte, 600)
	ciphertext := key.EncryptBytes(plaintext)
	decryptedPlaintext, err := key.DecryptBytes(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plaintext, decryptedPlaintext) {
		t.Fatal("Encrypted and decrypted zero plaintext do not match")
	}

	// Try again with a nonzero plaintext.
	plaintext = fastrand.Bytes(600)
	ciphertext = key.EncryptBytes(plaintext)
	decryptedPlaintext, err = key.DecryptBytes(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(plaintext, decryptedPlaintext) {
		t.Fatal("Encrypted and decrypted zero plaintext do not match")
	}

	// Try to decrypt using a different key
	key2 := generateTwofishKey()
	_, err = key2.DecryptBytes(ciphertext)
	if err == nil {
		t.Fatal("Expecting failed authentication err", err)
	}

	// Try to decrypt using bad ciphertexts.
	ciphertext[0]++
	_, err = key.DecryptBytes(ciphertext)
	if err == nil {
		t.Fatal("Expecting failed authentication err", err)
	}
	_, err = key.DecryptBytes(ciphertext[:10])
	if err != ErrInsufficientLen {
		t.Error("Expecting ErrInsufficientLen:", err)
	}

	// Try to trigger a panic or error with nil values.
	key.EncryptBytes(nil)
	_, err = key.DecryptBytes(nil)
	if err != ErrInsufficientLen {
		t.Error("Expecting ErrInsufficientLen:", err)
	}
}

// TestTwofishEntropy encrypts and then decrypts a zero plaintext, checking
// that the ciphertext is high entropy.
func TestTwofishEntropy(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Encrypt a larger zero plaintext and make sure that the outcome is high
	// entropy. Entropy is measured by compressing the ciphertext with gzip.
	// 10 * 1000 bytes was chosen to minimize the impact of gzip overhead.
	const cipherSize = 10e3
	key := generateTwofishKey()
	plaintext := make([]byte, cipherSize)
	ciphertext := key.EncryptBytes(plaintext)

	// Gzip the ciphertext
	var b bytes.Buffer
	zip := gzip.NewWriter(&b)
	_, err := zip.Write(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	zip.Close()
	if b.Len() < cipherSize {
		t.Error("supposedly high entropy ciphertext has been compressed!")
	}
}

// TestTwofishNewCipherAssumption tests that the length of a TwofishKey is 16,
// 24, or 32 as these are the only cases where twofish.NewCipher(key[:])
// doesn't return an error.
func TestTwofishNewCipherAssumption(t *testing.T) {
	// Generate key.
	key := generateTwofishKey()
	// Test key length.
	keyLen := len(key)
	if keyLen != 16 && keyLen != 24 && keyLen != 32 {
		t.Errorf("TwofishKey must have length 16, 24, or 32, but generated key has length %d\n", keyLen)
	}
}
