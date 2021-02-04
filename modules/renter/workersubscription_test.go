package renter

import (
	"context"
	"reflect"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// randomRegistryValue is a helper to create a signed registry value for
// testing.
func randomRegistryValue() (modules.SignedRegistryValue, types.SiaPublicKey, crypto.SecretKey) {
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
	return rv, spk, sk
}

// TestSubscriptionHelpersWithWorker tests the subscription helper methods against the
// worker tester. They are already unit-tested against a host in
// rpcsubscribe_test.go but better safe than sorry.
func TestSubscriptionHelpersWithWorker(t *testing.T) {
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Random subscriber.
	var subscriber types.Specifier
	fastrand.Read(subscriber[:])

	// Random registry value.
	srv1, spk1, _ := randomRegistryValue()
	srv2, spk2, _ := randomRegistryValue()
	srv3, spk3, _ := randomRegistryValue()

	// Get price table.
	pt := &wt.staticPriceTable().staticPriceTable

	// Update the host with the first and third one.
	err = wt.UpdateRegistry(context.Background(), spk1, srv1)
	if err != nil {
		t.Fatal(err)
	}
	err = wt.UpdateRegistry(context.Background(), spk3, srv3)
	if err != nil {
		t.Fatal(err)
	}

	// Begin subscription. Compute deadline.
	deadline := time.Now().Add(modules.SubscriptionPeriod)
	stream, err := wt.managedBeginSubscription(initialSubscriptionBudget, wt.staticAccount.staticID, subscriber)
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe to all three values.
	initialValues, err := modules.RPCSubscribeToRVs(stream, []modules.RPCRegistrySubscriptionRequest{
		{
			PubKey: spk1,
			Tweak:  srv1.Tweak,
		},
		{
			PubKey: spk2,
			Tweak:  srv2.Tweak,
		},
		{
			PubKey: spk3,
			Tweak:  srv3.Tweak,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Expect 2 initial values.
	if len(initialValues) != 2 {
		t.Fatal("wrong number of values", len(initialValues))
	}
	if !reflect.DeepEqual(initialValues[0], srv1) {
		t.Fatal("wrong value")
	}
	if !reflect.DeepEqual(initialValues[1], srv3) {
		t.Fatal("wrong value")
	}

	// Fund the budget a bit.
	err = wt.managedFundSubscription(stream, initialSubscriptionBudget.Div64(2))
	if err != nil {
		t.Fatal(err)
	}

	// Extend the subscription.
	err = modules.RPCExtendSubscription(stream, pt)
	if err != nil {
		t.Fatal(err)
	}

	// Unsubscribe from the values again.
	err = modules.RPCUnsubscribeFromRVs(stream, []modules.RPCRegistrySubscriptionRequest{
		{
			PubKey: spk1,
			Tweak:  srv1.Tweak,
		},
		{
			PubKey: spk2,
			Tweak:  srv2.Tweak,
		},
		{
			PubKey: spk3,
			Tweak:  srv3.Tweak,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Sleep until the first deadline + half way through the second period.
	time.Sleep(time.Until(deadline.Add(modules.SubscriptionPeriod / 2)))

	// Graceful shutdown.
	err = modules.RPCStopSubscription(stream)
	if err != nil {
		t.Fatal(err)
	}
}
