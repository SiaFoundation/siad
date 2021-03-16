package modules

import (
	"encoding/hex"
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

// TestHasMoreWork is a unit test for the registry entry's HasMoreWork method.
func TestHasMoreWork(t *testing.T) {
	// Create the rv's from hardcoded values for which we know the resulting
	// hash.
	rv1Data, err := hex.DecodeString("9c0e0775d2176f1f9984")
	if err != nil {
		t.Fatal(err)
	}
	rv2Data, err := hex.DecodeString("d609d8de783665bfb437")
	if err != nil {
		t.Fatal(err)
	}
	rv1 := NewRegistryValue(crypto.Hash{}, rv1Data, 0)
	rv2 := NewRegistryValue(crypto.Hash{}, rv2Data, 0)

	// Make sure the hashes match our expectations.
	rv1Hash := "659f49276a066a4b2434c9ffb953efee63d255e69c5541fb1785b54ebc10fbad"
	rv2Hash := "0c46015835772a5aa99ca8999fa8b876bb2293cd16ca2fbff8c858a64813eb51"
	if rv1.hash().String() != rv1Hash {
		t.Fatal("rv1 wrong hash")
	}
	if rv2.hash().String() != rv2Hash {
		t.Fatal("rv1 wrong hash")
	}

	// rv2 should have more work than rv1
	if !rv2.HasMoreWork(rv1) {
		t.Fatal("rv2 should have more work than rv1")
	}
	// rv1 should have less work than rv2
	if rv1.HasMoreWork(rv2) {
		t.Fatal("rv1 should have less work than rv2")
	}
	// rv1 should not have more work than itself.
	if rv1.HasMoreWork(rv1) {
		t.Fatal("rv1 shouldn't have more work than itself")
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
