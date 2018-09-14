package crypto

import (
	"bytes"
	"testing"

	"github.com/dchest/threefish"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestThreefishEncryptDecrypt makes sure that the encryption and decryption
// using threefish is not a no-op and that DecryptBytesInPlace happens
// in-place.
func TestThreefishEncryptDecrypt(t *testing.T) {
	// Get a new key.
	key := generateThreefishKey()

	// Get random data to encrypt.
	data := fastrand.Bytes(threefish.BlockSize)

	// Encrypt data.
	ciphertext := key.EncryptBytes(data)

	// Make sure EncryptBytes wasn't a no-op.
	if bytes.Equal(ciphertext, data) {
		t.Fatal("EncryptBytes was a no-op")
	}

	// Plaintext and Ciphertext should have same length.
	if len(data) != len(ciphertext) {
		t.Fatal("Plaintext and Ciphertext don't have same length")
	}

	// Decrypt data.
	plain, err := key.DecryptBytes(ciphertext)
	if err != nil {
		t.Fatal(err)
	}

	// Check that decrypting produced the correct result.
	if !bytes.Equal(plain, data) {
		t.Fatal("Decrypted data doesn't match initial data")
	}

	// Decrypt again in-place.
	plain, err = key.DecryptBytesInPlace(ciphertext)
	if err != nil {
		t.Fatal(err)
	}

	// Check that decrypting produced the correct result.
	if !bytes.Equal(plain, data) {
		t.Fatal("Decrypted data doesn't match initial data")
	}

	// Since decryption was in-place the slices should be the same.
	ct := []byte(ciphertext[:])
	if &plain[cap(plain)-1] != &ct[cap(ct)-1] {
		t.Fatal("Decryption wasn't in-place")
	}
}
