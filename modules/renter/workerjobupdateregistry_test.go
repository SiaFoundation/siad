package renter

import (
	"context"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestUpdateRegistryJob tests the various cases of running an UpdateRegistry
// job on a host.
func TestUpdateRegistryJob(t *testing.T) {
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a registry value.
	sk, pk := crypto.GenerateKeyPair()
	var tweak crypto.Hash
	fastrand.Read(tweak[:])
	data := fastrand.Bytes(modules.RegistryDataSize)
	rev := fastrand.Uint64n(1000)
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	rv := modules.NewRegistryValue(tweak, data, rev).Sign(sk)

	// Run the UpdateRegistryJob.
	err = wt.UpdateRegistry(context.Background(), spk, rv)
	if err != nil {
		t.Fatal(err)
	}

	// Manually try to read the entry from the host.
	lookedUpRV, err := lookupRegistry(wt.worker, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}

	// The entries should match.
	if !reflect.DeepEqual(lookedUpRV, rv) {
		t.Fatal("entries don't match")
	}

	// Run the UpdateRegistryJob again. This time it's a no-op and should
	// succeed.
	err = wt.UpdateRegistry(context.Background(), spk, rv)
	if err != nil {
		t.Fatal(err)
	}

	// Manually try to read the entry from the host.
	lookedUpRV, err = lookupRegistry(wt.worker, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}

	// The entries should match.
	if !reflect.DeepEqual(lookedUpRV, rv) {
		t.Fatal("entries don't match")
	}

	// Increment the revision number and do it one more time.
	rv.Revision++
	rv = rv.Sign(sk)
	err = wt.UpdateRegistry(context.Background(), spk, rv)
	if err != nil {
		t.Fatal(err)
	}

	// Manually try to read the entry from the host.
	lookedUpRV, err = lookupRegistry(wt.worker, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}

	// The entries should match.
	if !reflect.DeepEqual(lookedUpRV, rv) {
		t.Fatal("entries don't match")
	}
}
