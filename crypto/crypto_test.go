package crypto

import (
	"bytes"
	"testing"

	"golang.org/x/crypto/chacha20poly1305"
)

// TestCipherTypeStringConversion tests the conversion from a CipherType into a
// string and vice versa.
func TestCipherTypeStringConversion(t *testing.T) {
	var ct CipherType
	if err := ct.FromString(TypeDefaultRenter.String()); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(TypeDefaultRenter[:], ct[:]) {
		t.Fatal("Failed to parse CipherType from string")
	}
}

// BenchmarkChaCha20Poly1305 benchmarks the speed of ChaCha20-Poly1305 AEAD
// encryption and decryption.
func BenchmarkChaCha20Poly1305(b *testing.B) {
	b.Run("encrypt-1KiB", func(b *testing.B) {
		aead, _ := chacha20poly1305.New(make([]byte, chacha20poly1305.KeySize))
		nonce := make([]byte, aead.NonceSize())
		buf := make([]byte, 1024)
		dst := make([]byte, len(buf)+aead.Overhead())

		b.ResetTimer()
		b.SetBytes(int64(len(buf)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			aead.Seal(dst[:0], nonce, buf, nil)
		}
	})

	b.Run("decrypt-1KiB", func(b *testing.B) {
		aead, _ := chacha20poly1305.New(make([]byte, chacha20poly1305.KeySize))
		nonce := make([]byte, aead.NonceSize())
		buf := aead.Seal(nil, nonce, make([]byte, 1024), nil)
		dst := make([]byte, len(buf))

		b.ResetTimer()
		b.SetBytes(int64(len(buf)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			aead.Open(dst[:0], nonce, buf, nil)
		}
	})

	b.Run("encrypt-4MiB", func(b *testing.B) {
		aead, _ := chacha20poly1305.New(make([]byte, chacha20poly1305.KeySize))
		nonce := make([]byte, aead.NonceSize())
		buf := make([]byte, 1<<22)
		dst := make([]byte, len(buf)+aead.Overhead())

		b.ResetTimer()
		b.SetBytes(int64(len(buf)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			aead.Seal(dst[:0], nonce, buf, nil)
		}
	})

	b.Run("decrypt-4MiB", func(b *testing.B) {
		aead, _ := chacha20poly1305.New(make([]byte, chacha20poly1305.KeySize))
		nonce := make([]byte, aead.NonceSize())
		buf := aead.Seal(nil, nonce, make([]byte, 1<<22), nil)
		dst := make([]byte, len(buf))

		b.ResetTimer()
		b.SetBytes(int64(len(buf)))
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			aead.Open(dst[:0], nonce, buf, nil)
		}
	})
}
