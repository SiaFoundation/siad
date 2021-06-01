package modules

import (
	"encoding/hex"
	"math"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/types"
)

// TestHashRegistryValue tests that signing registry values results in expected
// values.
func TestHashRegistryValue(t *testing.T) {
	t.Parallel()

	expected := "788dddf5232807611557a3dc0fa5f34012c2650526ba91d55411a2b04ba56164"
	dataKey := "HelloWorld"
	tweak := crypto.HashAll(dataKey)
	data := []byte("abc")
	revision := uint64(123456789)

	value := NewRegistryValue(tweak, data, revision, RegistryEntryType(RegistryTypeWithoutPubkey))
	hash := value.hash()
	if hash.String() != expected {
		t.Fatalf("expected hash %v, got %v", expected, hash.String())
	}

	// Test again for entries with pubkey.
	expected = "f76214bd0a2aae1783027124c587b741398dd268cce9a3457ac434af620a8f86"
	value.Type = RegistryTypeWithPubkey
	hash = value.hash()
	if hash.String() != expected {
		t.Fatalf("expected hash %v, got %v", expected, hash.String())
	}
}

// TestHasMoreWork is a unit test for the registry entry's HasMoreWork method.
func TestHasMoreWork(t *testing.T) {
	t.Parallel()

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
	rv1 := NewRegistryValue(crypto.Hash{}, rv1Data, 0, RegistryTypeWithoutPubkey)
	rv2 := NewRegistryValue(crypto.Hash{}, rv2Data, 0, RegistryTypeWithoutPubkey)

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

	// Copy rv2 and add a pubkey. This should result in the same amount of work.
	rvWithPubkey := rv2
	rvWithPubkey.Type = RegistryTypeWithPubkey
	var hpk types.SiaPublicKey
	hpkh := crypto.HashObject(hpk)
	rvWithPubkey.Data = append(hpkh[:RegistryPubKeyHashSize], rvWithPubkey.Data...)

	// rvWithPubkey should have more work than rv1
	if !rvWithPubkey.HasMoreWork(rv1) {
		t.Fatal("rvWithPubkey should have more work than rv1")
	}
	// rv1 should have less work than rvWithPubkey
	if rv1.HasMoreWork(rvWithPubkey) {
		t.Fatal("rv1 should have less work than rvWithPubkey")
	}
	// rvWithPubkey should have the same work as rv2.
	if rvWithPubkey.work() != rv2.work() {
		t.Fatal("wrong work")
	}
}

// TestRegistryValueSignature tests signature verification on registry values.
func TestRegistryValueSignature(t *testing.T) {
	t.Parallel()

	signedRV := func(entryType RegistryEntryType) (SignedRegistryValue, crypto.PublicKey) {
		sk, pk := crypto.GenerateKeyPair()
		rv := NewRegistryValue(crypto.Hash{1}, fastrand.Bytes(100), 2, entryType).Sign(sk)
		return rv, pk
	}

	test := func(entryType RegistryEntryType) {
		// Check signed.
		rv, _ := signedRV(entryType)
		if rv.Signature == (crypto.Signature{}) {
			t.Fatal("signing failed")
		}
		// Verify valid
		rv, pk := signedRV(entryType)
		if err := rv.Verify(pk); err != nil {
			t.Fatal("verification failed")
		}
		// Verify invalid - no sig
		rv, pk = signedRV(entryType)
		rv.Signature = crypto.Signature{}
		if err := rv.Verify(pk); err == nil {
			t.Fatal("verification succeeded")
		}
		// Verify invalid - wrong tweak
		rv, pk = signedRV(entryType)
		fastrand.Read(rv.Tweak[:])
		if err := rv.Verify(pk); err == nil {
			t.Fatal("verification succeeded")
		}
		// Verify invalid - wrong data
		rv, pk = signedRV(entryType)
		rv.Data = fastrand.Bytes(100)
		if err := rv.Verify(pk); err == nil {
			t.Fatal("verification succeeded")
		}
		// Verify invalid - wrong revision
		rv, pk = signedRV(entryType)
		rv.Revision = fastrand.Uint64n(math.MaxUint64)
		if err := rv.Verify(pk); err == nil {
			t.Fatal("verification succeeded")
		}
		// Verify invalid - wrong type.
		rv, pk = signedRV(entryType)
		if rv.Type == RegistryEntryType(RegistryTypeWithPubkey) {
			rv.Type = RegistryEntryType(RegistryTypeWithoutPubkey)
		} else if rv.Type == RegistryEntryType(RegistryTypeWithoutPubkey) {
			rv.Type = RegistryEntryType(RegistryTypeWithPubkey)
		} else {
			t.Fatal("unknown type")
		}
		if err := rv.Verify(pk); err == nil {
			t.Fatal("verification succeeded")
		}
	}
	test(RegistryTypeWithPubkey)
	test(RegistryTypeWithoutPubkey)
}

// TestIsPrimaryKey is a unit test for the IsPrimaryKey method.
func TestIsPrimaryKey(t *testing.T) {
	t.Parallel()

	// Create primary entry.
	_, pk := crypto.GenerateKeyPair()
	hpk := types.Ed25519PublicKey(pk)
	hpkh := crypto.HashObject(hpk)
	rv := NewRegistryValue(crypto.Hash{}, hpkh[:RegistryPubKeyHashSize], 0, RegistryTypeWithPubkey)
	primary := rv.IsPrimaryEntry(hpk)
	if !primary {
		t.Fatal("should be primary")
	}

	// Try a different hostkey. Shouldn't be primary anymore.
	primary = rv.IsPrimaryEntry(types.SiaPublicKey{})
	if primary {
		t.Fatal("shouldn't be primary")
	}

	// Change the type to something else without pubkey. Shouldn't be primary
	// anymore.
	rvWrongType := rv
	rvWrongType.Type = RegistryTypeWithoutPubkey
	primary = rv.IsPrimaryEntry(hpk)
	if !primary {
		t.Fatal("shouldn't be primary")
	}
}

// TestShouldUpdateWith is a unit test for ShouldUpdateWith.
func TestShouldUpdateWith(t *testing.T) {
	t.Parallel()

	_, pk := crypto.GenerateKeyPair()
	hpk := types.Ed25519PublicKey(pk)
	hpkh := crypto.HashObject(hpk)

	tests := []struct {
		existing *RegistryValue
		new      *RegistryValue
		result   bool
		err      error
	}{
		{
			existing: nil,
			new:      &RegistryValue{},
			result:   true,
			err:      nil,
		},
		{
			existing: &RegistryValue{},
			new:      nil,
			result:   false,
			err:      nil,
		},
		{
			existing: nil,
			new:      nil,
			result:   false,
			err:      nil,
		},
		{
			existing: &RegistryValue{Revision: 0},
			new:      &RegistryValue{Revision: 1},
			result:   true,
			err:      nil,
		},
		{
			existing: &RegistryValue{Revision: 1},
			new:      &RegistryValue{Revision: 0},
			result:   false,
			err:      ErrLowerRevNum,
		},
		{
			existing: &RegistryValue{Revision: 0, Tweak: crypto.Hash{1, 2, 3}},
			new:      &RegistryValue{Revision: 0, Tweak: crypto.Hash{3, 2, 1}},
			result:   true,
			err:      nil,
		},
		{
			existing: &RegistryValue{Revision: 0, Tweak: crypto.Hash{3, 2, 1}},
			new:      &RegistryValue{Revision: 0, Tweak: crypto.Hash{1, 2, 3}},
			result:   false,
			err:      ErrInsufficientWork,
		},
		{
			existing: &RegistryValue{Revision: 1},
			new:      &RegistryValue{Revision: 1},
			result:   false,
			err:      ErrSameRevNum,
		},
		{
			existing: &RegistryValue{Data: hpkh[:RegistryPubKeyHashSize], Type: RegistryTypeWithPubkey},
			new:      &RegistryValue{Type: RegistryTypeWithoutPubkey},
			result:   false,
			err:      ErrSameRevNum,
		},
		{
			existing: &RegistryValue{Type: RegistryTypeWithoutPubkey},
			new:      &RegistryValue{Data: hpkh[:RegistryPubKeyHashSize], Type: RegistryTypeWithPubkey},
			result:   true,
			err:      nil,
		},
		{
			existing: &RegistryValue{Data: hpkh[:RegistryPubKeyHashSize], Type: RegistryTypeWithPubkey},
			new:      &RegistryValue{Data: hpkh[:RegistryPubKeyHashSize], Type: RegistryTypeWithPubkey},
			result:   false,
			err:      ErrSameRevNum,
		},
	}

	for i, test := range tests {
		result, err := test.existing.ShouldUpdateWith(test.new, hpk)
		if result != test.result || err != test.err {
			t.Errorf("%v: wrong result/error expected %v and %v but was %v and %v", i, test.result, test.err, result, err)
		}
	}
}
