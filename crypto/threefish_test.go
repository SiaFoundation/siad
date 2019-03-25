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
	plain, err = key.DecryptBytesInPlace(ciphertext, 0)
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

// TestThreefishDecryptSubslice check that we can decrypt any subslice of a
// threefish ciphertext as long as its length is a multiple of
// threefish.BlockSize.
func TestThreefishDecryptAtOffset(t *testing.T) {
	// Get a new key.
	key := generateThreefishKey()

	// Get random data to encrypt.
	numBlocks := 100
	data := fastrand.Bytes(100 * threefish.BlockSize)

	// Encrypt the data.
	ciphertext := key.EncryptBytes(data)

	// Test decrypting 100 random sub slices of the ciphertext.
	for i := 0; i < 100; i++ {
		// Get two random indices which define the subslice of the ciphertext
		// we use for testing.
		fromBlock := fastrand.Intn(numBlocks)
		toBlock := fastrand.Intn(numBlocks-fromBlock) + fromBlock
		from := fromBlock * threefish.BlockSize
		to := toBlock * threefish.BlockSize
		// Create subslice of the ciphertext.
		c := make(Ciphertext, to-from)
		copy(c, ciphertext[from:to])
		// Decrypt the subslice.
		d, err := key.DecryptBytesInPlace(c, uint64(fromBlock))
		if err != nil {
			t.Fatal(err)
		}
		// Compare it to the same subslice of the original data.
		if !bytes.Equal(d, data[from:to]) {
			t.Fatal("Decrypted data doesn't match original data")
		}
	}
}
