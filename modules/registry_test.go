package modules

import (
	"math"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestHashRegistryValue tests that signing registry values results in expected
// values.
func TestHashRegistryValue(t *testing.T) {
	expected := "788dddf5232807611557a3dc0fa5f34012c2650526ba91d55411a2b04ba56164"
	dataKey := "HelloWorld"
	tweak := crypto.HashAll(dataKey)
	data := []byte("abc")
	revision := uint64(123456789)

	value := NewRegistryValue(tweak, data, revision)
	hash := value.hash()
	if hash.String() != expected {
		t.Fatalf("expected hash %v, got %v", expected, hash.String())
	}
}

// TestRegistryValueSignature tests signature verification on registry values.
func TestRegistryValueSignature(t *testing.T) {
	signedRV := func() (SignedRegistryValue, crypto.PublicKey) {
		sk, pk := crypto.GenerateKeyPair()
		rv := NewRegistryValue(crypto.Hash{1}, fastrand.Bytes(100), 2).Sign(sk)
		return rv, pk
	}

	// Check signed.
	rv, _ := signedRV()
	if rv.Signature == (crypto.Signature{}) {
		t.Fatal("signing failed")
	}
	// Verify valid
	rv, pk := signedRV()
	if err := rv.Verify(pk); err != nil {
		t.Fatal("verification failed")
	}
	// Verify invalid - no sig
	rv, pk = signedRV()
	rv.Signature = crypto.Signature{}
	if err := rv.Verify(pk); err == nil {
		t.Fatal("verification succeeded")
	}
	// Verify invalid - wrong tweak
	rv, pk = signedRV()
	fastrand.Read(rv.Tweak[:])
	if err := rv.Verify(pk); err == nil {
		t.Fatal("verification succeeded")
	}
	// Verify invalid - wrong data
	rv, pk = signedRV()
	rv.Data = fastrand.Bytes(100)
	if err := rv.Verify(pk); err == nil {
		t.Fatal("verification succeeded")
	}
	// Verify invalid - wrong revision
	rv, pk = signedRV()
	rv.Revision = fastrand.Uint64n(math.MaxUint64)
	if err := rv.Verify(pk); err == nil {
		t.Fatal("verification succeeded")
	}
}
