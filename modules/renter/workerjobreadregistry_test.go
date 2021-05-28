package renter

import (
	"context"
	"reflect"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
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
	rv := modules.NewRegistryValue(tweak, data, rev, modules.RegistryTypeWithoutPubkey).Sign(sk)

	// Run the UpdateRegistry job.
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
	if !reflect.DeepEqual(*lookedUpRV, rv) {
		t.Log(lookedUpRV)
		t.Log(rv)
		t.Fatal("entries don't match")
	}
}

// TestReadRegistryInvalidCached checks that a host can't provide an older
// revision for an entry if we have seen a more recent one from it in the past
// already.
func TestReadRegistryInvalidCached(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	deps := dependencies.NewDependencyRegistryUpdateNoOp()
	deps.Disable()
	wt, err := newWorkerTesterCustomDependency(t.Name(), modules.ProdDependencies, deps)
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
	rev := fastrand.Uint64n(1000) + 1
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	rv := modules.NewRegistryValue(tweak, data, rev, modules.RegistryTypeWithoutPubkey).Sign(sk)

	// Run the UpdateRegistry job.
	err = wt.UpdateRegistry(context.Background(), spk, rv)
	if err != nil {
		t.Fatal(err)
	}

	// Run the UpdateRegistry job again. This time it's a no-op. The renter
	// won't know and increment the revision in the cache.
	rv.Revision++
	rv = rv.Sign(sk)
	deps.Enable()
	err = wt.UpdateRegistry(context.Background(), spk, rv)
	deps.Disable()
	if err != nil {
		t.Fatal(err)
	}

	// Read the value. This should result in an error due to the host providing a
	// lower revision number than expected.
	_, err = wt.ReadRegistry(context.Background(), spk, rv.Tweak)
	if !errors.Contains(err, errHostLowerRevisionThanCache) {
		t.Fatal(err)
	}

	// Make sure there is a recent error and cooldown.
	wt.staticJobReadRegistryQueue.mu.Lock()
	if !errors.Contains(wt.staticJobReadRegistryQueue.recentErr, errHostLowerRevisionThanCache) {
		t.Fatal("wrong recent error", wt.staticJobReadRegistryQueue.recentErr)
	}
	if wt.staticJobReadRegistryQueue.cooldownUntil == (time.Time{}) {
		t.Fatal("cooldownUntil is not set")
	}
	wt.staticJobReadRegistryQueue.mu.Unlock()
}

// TestReadRegistryCacheUpdated tests the registry cache functionality as used
// by ReadRegistry jobs.
func TestReadRegistryCachedUpdated(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	deps := dependencies.NewDependencyRegistryUpdateNoOp()
	deps.Disable()
	wt, err := newWorkerTesterCustomDependency(t.Name(), modules.ProdDependencies, deps)
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
	rev := fastrand.Uint64n(1000) + 1
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	rv := modules.NewRegistryValue(tweak, data, rev, modules.RegistryTypeWithoutPubkey).Sign(sk)

	// Run the UpdateRegistry job.
	err = wt.UpdateRegistry(context.Background(), spk, rv)
	if err != nil {
		t.Fatal(err)
	}

	// Make sure the value is in the cache.
	rev, cached := wt.staticRegistryCache.Get(spk, tweak)
	if !cached || rev != rv.Revision {
		t.Fatal("invalid cached value")
	}

	// Delete the value from the cache.
	wt.staticRegistryCache.Delete(spk, rv)
	_, cached = wt.staticRegistryCache.Get(spk, tweak)
	if cached {
		t.Fatal("value wasn't removed")
	}

	// Read the registry value.
	readRV, err := wt.ReadRegistry(context.Background(), spk, rv.Tweak)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rv, *readRV) {
		t.Fatal("read value doesn't match set value")
	}

	// Revision should be cached again.
	rev, cached = wt.staticRegistryCache.Get(spk, tweak)
	if !cached || rev != rv.Revision {
		t.Fatal("invalid cached value")
	}

	// Update the revision again.
	rv2 := rv
	rv2.Revision++
	rv2 = rv2.Sign(sk)
	err = wt.UpdateRegistry(context.Background(), spk, rv2)
	if err != nil {
		t.Fatal(err)
	}

	// Make sure the value is in the cache.
	rev, cached = wt.staticRegistryCache.Get(spk, tweak)
	if !cached || rev != rv2.Revision {
		t.Fatal("invalid cached value")
	}

	// Set the cache to the earlier revision of rv.
	wt.staticRegistryCache.Set(spk, rv, true)
	rev, cached = wt.staticRegistryCache.Get(spk, tweak)
	if !cached || rev != rv.Revision {
		t.Fatal("invalid cached value")
	}

	// Read the registry value. Should be rv2.
	readRV, err = wt.ReadRegistry(context.Background(), spk, rv2.Tweak)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rv2, *readRV) {
		t.Fatal("read value doesn't match set value")
	}

	// Revision from rv2 should be cached again.
	rev, cached = wt.staticRegistryCache.Get(spk, tweak)
	if !cached || rev != rv2.Revision {
		t.Fatal("invalid cached value")
	}
}
