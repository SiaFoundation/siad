package crypto

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/dchest/threefish"
	"gitlab.com/NebulousLabs/fastrand"
)

const (
	// threefishOverhead is the number of bytes added by EncryptBytes.
	threefishOverhead = 0
)

type (
	// threefishKey is a key used for encrypting and decrypting data.
	threefishKey [threefish.KeySize]byte
)

// generateThreefishKey produces a threefishKey that can be used for encrypting
// and decrypting data using Threefish.
func generateThreefishKey() (key threefishKey) {
	fastrand.Read(key[:])
	return
}

// newCipher creates a new Threefish cipher from the key.
func (key threefishKey) newCipher() *threefish.Threefish {
	cipher, err := threefish.NewCipher(key[:], make([]byte, threefish.TweakSize))
	if err != nil {
		panic("NewCipher only returns an error if len(key) != 64")
	}
	return cipher
}

// newThreefishKey creates a new threefishKey from a given entropy.
func newThreefishKey(entropy []byte) (key threefishKey, err error) {
	// check key length
	if len(entropy) != threefish.KeySize {
		err = fmt.Errorf("threefish key should have size %v but was %v",
			threefish.KeySize, len(entropy))
		return
	}
	// create key
	copy(key[:], entropy)
	return
}

// DecryptBytes decrypts a ciphertext created by EncryptPiece. The tweak is
// expected to be incremented by 1 for every 64 bytes of data.
func (key threefishKey) DecryptBytes(ct Ciphertext) ([]byte, error) {
	// Check if input has correct length.
	if len(ct)%threefish.BlockSize != 0 {
		return nil, fmt.Errorf("supplied ciphertext is not a multiple of %v", threefish.BlockSize)
	}

	// Create the cipher
	cipher := key.newCipher()

	// Create the initial tweak and plaintext slice.
	tweak := make([]byte, threefish.TweakSize)
	plaintext := make([]byte, len(ct))

	// Decrypt the ciphertext one block at a time while incrementing the tweak.
	cbuf := bytes.NewBuffer(ct)
	pbuf := bytes.NewBuffer(plaintext)
	for cbuf.Len() > 0 {
		// Decrypt the block.
		cipher.Decrypt(pbuf.Next(threefish.BlockSize), cbuf.Next(threefish.BlockSize))

		// Increment the tweak by 1.
		tweakNum := binary.LittleEndian.Uint64(tweak)
		binary.LittleEndian.PutUint64(tweak, tweakNum+1)
		if err := cipher.SetTweak(tweak); err != nil {
			panic(err)
		}
	}
	return plaintext, nil
}

// DecryptBytesInPlace decrypts a ciphertext created by EncryptPiece. The
// blockIndex is expected to be incremented by 1 for every 64 bytes of data as
// it represents the offset within a larger piece of data, allowing for partial
// decryption. e.g. If the provided ciphertext starts at offset 64 of the
// original ciphertext produced by EncryptPiece, it can be decrypted by setting
// blockIndex to 1.
// DecryptBytesInPlace reuses the memory of ct to be able to operate in-place.
// This means that ct can't be reused after calling DecryptBytesInPlace.
func (key threefishKey) DecryptBytesInPlace(ct Ciphertext, blockIndex uint64) ([]byte, error) {
	// Check if input has correct length.
	if len(ct)%threefish.BlockSize != 0 {
		return nil, fmt.Errorf("supplied ciphertext is not a multiple of %v", threefish.BlockSize)
	}

	// Create the cipher
	cipher := key.newCipher()

	// Create the initial tweak.
	tweak := make([]byte, threefish.TweakSize)
	binary.LittleEndian.PutUint64(tweak, blockIndex)
	if err := cipher.SetTweak(tweak); err != nil {
		panic(err)
	}

	// Decrypt the ciphertext one block at a time while incrementing the tweak.
	buf := bytes.NewBuffer(ct)
	dst := ct
	for block := buf.Next(threefish.BlockSize); len(block) > 0; block = buf.Next(threefish.BlockSize) {
		// Decrypt the block.
		cipher.Decrypt(dst, block)

		// Increment the tweak by 1.
		tweakNum := binary.LittleEndian.Uint64(tweak)
		binary.LittleEndian.PutUint64(tweak, tweakNum+1)
		if err := cipher.SetTweak(tweak); err != nil {
			panic(err)
		}

		// Adjust the dst.
		dst = dst[threefish.BlockSize:]
	}
	return ct[:], nil
}

// Derive derives a child key for a given combination of chunk and piece index.
func (key threefishKey) Derive(chunkIndex, pieceIndex uint64) CipherKey {
	entropy1 := HashAll(key[:], chunkIndex, pieceIndex, 0)
	entropy2 := HashAll(key[:], chunkIndex, pieceIndex, 1)
	ck, err := NewSiaKey(TypeThreefish, append(entropy1[:], entropy2[:]...))
	if err != nil {
		panic("this should not be possible when deriving from a valid key")
	}
	return ck
}

// EncryptBytes encrypts arbitrary data using the ThreefishKey and using a
// different tweak for every 64 byte block.
func (key threefishKey) EncryptBytes(piece []byte) Ciphertext {
	// Sanity check piece length.
	if len(piece)%threefish.BlockSize != 0 {
		panic("piece must be multiple of threefish.BlockSize")
	}

	// Create the cipher.
	cipher := key.newCipher()

	// Create the initial tweak and ciphertext slice.
	tweak := make([]byte, threefish.TweakSize)
	ciphertext := make([]byte, len(piece))

	// Encrypt the piece one block at a time while incrementing the tweak.
	buf := bytes.NewBuffer(piece)
	dst := ciphertext
	for block := buf.Next(threefish.BlockSize); len(block) > 0; block = buf.Next(threefish.BlockSize) {
		// Encrypt the block.
		cipher.Encrypt(dst, block)

		// Increment the tweak by 1.
		tweakNum := binary.LittleEndian.Uint64(tweak)
		binary.LittleEndian.PutUint64(tweak, tweakNum+1)
		if err := cipher.SetTweak(tweak); err != nil {
			panic(err)
		}

		// Adjust the dst.
		dst = dst[threefish.BlockSize:]
	}
	return ciphertext
}

// Key returns the threefish key.
func (key threefishKey) Key() []byte {
	return key[:]
}

// Type returns the type of the threefish key.
func (threefishKey) Type() CipherType {
	return TypeThreefish
}
