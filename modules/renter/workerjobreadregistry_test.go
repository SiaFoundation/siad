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

// TestReadRegistryJob tests running a ReadRegistry job on a host.
func TestReadRegistryJob(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

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

	// Create a ReadRegistry job to read the entry.
	lookedUpRV, err := wt.ReadRegistry(context.Background(), spk, rv.Tweak)
	if err != nil {
		t.Fatal(err)
	}

	// The entries should match.
	if !reflect.DeepEqual(lookedUpRV, rv) {
		t.Fatal("entries don't match")
	}
}
