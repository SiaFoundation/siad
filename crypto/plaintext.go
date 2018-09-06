package crypto

type (
	// plainTextCipherKey implements the SiaCipherKey interface but doesn't
	// encrypt or decrypt data.
	plainTextCipherKey struct{}
)

// CipherType returns the type of the twofish key.
func (plainTextCipherKey) CipherType() CipherType {
	return TypePlain
}

// Cipherkey returns the plaintext key which is an empty slice.
func (p plainTextCipherKey) CipherKey() []byte {
	return []byte{}
}

// EncryptBytes is a no-op for the plainTextCipherKey.
func (p plainTextCipherKey) EncryptBytes(piece []byte) Ciphertext {
	return Ciphertext(piece)
}

// DecryptBytes is a no-op for the plainTextCipherKey.
func (p plainTextCipherKey) DecryptBytes(ct Ciphertext) ([]byte, error) {
	return ct[:], nil
}

// DecryptBytesInPlace is a no-op for the plainTextCipherKey.
func (p plainTextCipherKey) DecryptBytesInPlace(ct Ciphertext) ([]byte, error) {
	return ct[:], nil
}

// Overhead return zero for the plainTextCipherKey since it doesn't actually
// encrypt/decrypt anything.
func (p plainTextCipherKey) Overhead() uint64 {
	return 0
}
