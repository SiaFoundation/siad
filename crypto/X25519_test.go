package crypto

import (
	"testing"
)

// TestDeriveSharedSecret tests that the same shared secret can be derived
// using either secret+public combination of two keypairs.
func TestDeriveSharedSecret(t *testing.T) {
	sk1, pk1 := GenerateX25519KeyPair()
	sk2, pk2 := GenerateX25519KeyPair()
	if DeriveSharedSecret(sk1, pk2) != DeriveSharedSecret(sk2, pk1) {
		t.Fatal("shared secret does not match")
	}
	if DeriveSharedSecret(sk1, pk1) == DeriveSharedSecret(sk2, pk2) {
		t.Fatal("shared secret should not match")
	}
}
