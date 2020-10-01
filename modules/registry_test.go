package modules

import (
	"math"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestRegistryValueSignature tests signature verification on registry values.
func TestRegistryValueSignature(t *testing.T) {
	signedRV := func() (RegistryValue, crypto.PublicKey) {
		sk, pk := crypto.GenerateKeyPair()
		rv := RegistryValue{
			Tweak:    crypto.Hash{1},
			Data:     fastrand.Bytes(100),
			Revision: 2,
		}
		rv.Sign(sk)
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
