package crypto

type (
	// plainTextCipherKey implements the SiaCipherKey interface but doesn't
	// encrypt or decrypt data.
	plainTextCipherKey struct{}
)

// Type returns the type of the plaintext 'key'.
func (plainTextCipherKey) Type() CipherType {
	return TypePlain
}

// Cipherkey returns the plaintext key which is an empty slice.
func (plainTextCipherKey) Key() []byte {
	return []byte{}
}

// DecryptBytes is a no-op for the plainTextCipherKey.
func (plainTextCipherKey) DecryptBytes(ct Ciphertext) ([]byte, error) {
	return ct[:], nil
}

// DecryptBytesInPlace is a no-op for the plainTextCipherKey.
func (plainTextCipherKey) DecryptBytesInPlace(ct Ciphertext, off uint64) ([]byte, error) {
	return ct[off:], nil
}

// Derive for a plainTextCipherKey simply returns itself since there is no key
// to derive from.
func (p plainTextCipherKey) Derive(_, _ uint64) CipherKey {
	return p
}

// EncryptBytes is a no-op for the plainTextCipherKey.
func (plainTextCipherKey) EncryptBytes(piece []byte) Ciphertext {
	return Ciphertext(piece)
}
