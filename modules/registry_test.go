package modules

import (
	"bytes"
	"encoding/hex"
	"math"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/types"
)

// TestCanUpdateWith is a unit test for CanUpdateWith.
func TestCanUpdateWith(t *testing.T) {
	t.Parallel()

	rvData, err := hex.DecodeString("829675d476f4795e5e3caf6583d1f323a8f065236b9ace5296dfd6b24c876ba7f135")
	if err != nil {
		t.Fatal(err)
	}
	rvMoreWorkData, err := hex.DecodeString("0673b5a673596d840db8f714bbf6751e7d1869fca23e67fa20803597f925ac45e445")
	if err != nil {
		t.Fatal(err)
	}
	_, pk := crypto.GenerateKeyPair()
	spk := types.Ed25519PublicKey(pk)

	// Base value.
	rv := NewRegistryValue(crypto.Hash{}, rvData, 0)

	// Value with more work than base.
	rvMoreWork := NewRegistryValue(crypto.Hash{}, rvMoreWorkData, 0)

	// Value with higher revision than base.
	rvHigherRev := rv
	rvHigherRev.Revision++

	// Value with matching pubkey.
	rvPubKey := NewRegistryValueWithPubKey(rv.Tweak, spk, fastrand.Bytes(10), rv.Revision)
	rvNoPubKey := NewRegistryValue(rv.Tweak, rvPubKey.Data[HostPubKeyHashSize:], rv.Revision)

	// Run multiple testcases.
	tests := []struct {
		old RegistryValue
		new RegistryValue
		err error
	}{
		// Case 0: update base with itself
		{
			old: rv,
			new: rv,
			err: ErrSameEntry,
		},
		// Case 1: update base with higher rev
		{
			old: rv,
			new: rvHigherRev,
			err: nil,
		},
		// Case 2: update higher rev with lower base
		{
			old: rvHigherRev,
			new: rv,
			err: ErrLowerRevNum,
		},
		// Case 3: update base with more work
		{
			old: rv,
			new: rvMoreWork,
			err: nil,
		},
		// Case 4: update more work rev with lower work base
		{
			old: rvMoreWork,
			new: rv,
			err: ErrSameRevNum,
		},
		// Case 5: update base with matching pubkey
		{
			old: rvNoPubKey,
			new: rvPubKey,
			err: nil,
		},
		// Case 6: update rv with pubkey with same entry minus the pubkey
		{
			old: rvPubKey,
			new: rvNoPubKey,
			err: ErrSameWork,
		},
	}
	for i, test := range tests {
		if i < 6 {
			continue
		}
		err = test.old.CanUpdateWith(test.new, spk)
		if test.err != err && !errors.Contains(err, test.err) {
			t.Fatalf("%v: %v != %v", i, err, test.err)
		}
	}
}

// TestHashRegistryValue tests that signing registry values results in expected
// values.
func TestHashRegistryValue(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	// Create the rv's from hardcoded values for which we know the resulting
	// hash.
	rv1Data, err := hex.DecodeString("829675d476f4795e5e3caf6583d1f323a8f065236b9ace5296dfd6b24c876ba7f135")
	if err != nil {
		t.Fatal(err)
	}
	rv2Data, err := hex.DecodeString("0673b5a673596d840db8f714bbf6751e7d1869fca23e67fa20803597f925ac45e445")
	if err != nil {
		t.Fatal(err)
	}

	rv1 := NewRegistryValue(crypto.Hash{}, rv1Data, 0)
	rv2 := NewRegistryValue(crypto.Hash{}, rv2Data, 0)

	// Make sure the hashes match our expectations.
	rv1Hash := "c598bbad313e0003ce9a95b07b46fcb1abf09dd605389287b4aa9a583f38a901"
	rv2Hash := "b4afc3c50a9a087d7a1cb82ca14dac1f456f30742856056ff5269eb3a1b7343c"
	if rv1.hash().String() != rv1Hash {
		t.Fatal("rv1 wrong hash")
	}
	if rv2.hash().String() != rv2Hash {
		t.Fatal("rv2 wrong hash")
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
	// adding a pubkey to rv1 shouldn't change the work but the hash.
	rv1WithPubkey := rv1
	_, pk := crypto.GenerateKeyPair()
	spkh := crypto.HashObject(types.Ed25519PublicKey(pk))
	rv1WithPubkey.Data = append(rv1WithPubkey.Data, spkh[:]...)
	if rv1.work() != rv1WithPubkey.work() {
		t.Fatal("work should match")
	}
	if rv1.hash() == rv1WithPubkey.hash() {
		t.Fatal("hash should change")
	}
}

// TestRegistryValueSignature tests signature verification on registry values.
func TestRegistryValueSignature(t *testing.T) {
	t.Parallel()

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
	rv.Data = fastrand.Bytes(RegistryDataSize)
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

// TestHostPubKeyHash is a unit test for HostPubKeyHash.
func TestHostPubKeyHash(t *testing.T) {
	t.Parallel()

	// value without pubkey.
	rv := NewRegistryValue(crypto.Hash{}, fastrand.Bytes(1), 0)
	hpkh := rv.HostPubKeyHash()
	if hpkh != nil {
		t.Fatal("hpkh should be nil")
	}

	// value with pubkey but invalid version.
	_, pk := crypto.GenerateKeyPair()
	hpk2 := types.Ed25519PublicKey(pk)
	hpkh2 := crypto.HashObject(hpk2)
	rv.Data = make([]byte, RegistryDataSize)
	copy(rv.Data, hpkh2[:HostPubKeyHashSize])
	hpkh = rv.HostPubKeyHash()
	if hpkh != nil {
		t.Fatal("hpkh should be nil")
	}

	// with correct version.
	rv.Data[RegistryDataSize-1] = RegistryEntryVersionWithPubKey
	hpkh = rv.HostPubKeyHash()
	if !bytes.Equal(hpkh, hpkh2[:HostPubKeyHashSize]) {
		t.Fatal("hpkh should be nil")
	}

	// invalid data length shouldn't panic.
	rv = NewRegistryValue(crypto.Hash{}, []byte{}, 0)
	hpkh = rv.HostPubKeyHash()
	if hpkh != nil {
		t.Fatal("hpkh should be nil")
	}
}
