package crypto

type (
	// plainTextCipherKey implements the SiaCipherKey interface but doesn't
	// encrypt or decrypt data.
	plainTextCipherKey struct{}
)

// Type returns the type of the twofish key.
func (plainTextCipherKey) Type() CipherType {
	return TypePlain
}

// Cipherkey returns the plaintext key which is an empty slice.
func (p plainTextCipherKey) Key() []byte {
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
