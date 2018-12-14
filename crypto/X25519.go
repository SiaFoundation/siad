package crypto

import (
	"gitlab.com/NebulousLabs/fastrand"
	"golang.org/x/crypto/blake2b"
	"golang.org/x/crypto/curve25519"
)

type (
	// An X25519SecretKey is the secret half of an X25519 key pair.
	X25519SecretKey [32]byte

	// An X25519PublicKey is the public half of an X25519 key pair.
	X25519PublicKey [32]byte
)

// GenerateX25519KeyPair generates an ephemeral key pair for use in ECDH.
func GenerateX25519KeyPair() (xsk X25519SecretKey, xpk X25519PublicKey) {
	fastrand.Read(xsk[:])
	curve25519.ScalarBaseMult((*[32]byte)(&xpk), (*[32]byte)(&xsk))
	return
}

// DeriveSharedSecret derives 32 bytes of entropy from a secret key and public
// key. Derivation is via ScalarMult of the private and public keys, followed
// by a 256-bit unkeyed blake2b hash.
func DeriveSharedSecret(xsk X25519SecretKey, xpk X25519PublicKey) (secret [32]byte) {
	var dst [32]byte
	curve25519.ScalarMult(&dst, (*[32]byte)(&xsk), (*[32]byte)(&xpk))
	return blake2b.Sum256(dst[:])
}
