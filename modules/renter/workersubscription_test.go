package renter

import (
	"context"
	"encoding/hex"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/threadgroup"
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
	if !reflect.DeepEqual(initialValues[0].Entry, srv1) {
		t.Fatal("wrong value")
	}
	if !reflect.DeepEqual(initialValues[1].Entry, srv3) {
		t.Fatal("wrong value")
	}
	if !initialValues[0].PubKey.Equals(spk1) {
		t.Fatal("wrong pubkey")
	}
	if !initialValues[1].PubKey.Equals(spk3) {
		t.Fatal("wrong pubkey")
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

// TestPriceTableForSubscription is a unit test for
// managedPriceTableForSubscription.
func TestPriceTableForSubscription(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a worker that's not running its worker loop.
	wt, err := newWorkerTesterCustomDependency(t.Name(), &dependencies.DependencyDisableWorker{}, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a unique price table for testing.
	wptInvalid := &workerPriceTable{
		staticExpiryTime: time.Now().Add(modules.SubscriptionPeriod).Add(-time.Microsecond), // Won't cover the period
		staticUpdateTime: time.Now().Add(time.Hour),                                         // 1 hour from now
	}
	fastrand.Read(wptInvalid.staticPriceTable.UID[:])
	wt.staticSetPriceTable(wptInvalid)

	// Try to fetch a price table in a different goroutine. This should block
	// since we don't have a valid price table.
	var pt *modules.RPCPriceTable
	done := make(chan struct{})
	go func() {
		defer close(done)
		pt = wt.managedPriceTableForSubscription(modules.SubscriptionPeriod)
	}()

	// Wait for 1 second. The goroutine shouldn't finish.
	select {
	case <-done:
		t.Fatal("goroutine finished even though it should block")
	case <-time.After(time.Second):
	}

	// The price table should be updated to be renewed.
	updatedPT := wt.staticPriceTable()
	if !updatedPT.staticUpdateTime.IsZero() {
		t.Fatal("update time of price table should be zero")
	}
	if updatedPT.staticPriceTable.UID != wptInvalid.staticPriceTable.UID {
		t.Fatal("UIDs don't match")
	}

	// Create a new, valid price table and set it to simulate a price table
	// update.
	wptValid := &workerPriceTable{
		staticExpiryTime: time.Now().Add(modules.SubscriptionPeriod).Add(100 * time.Millisecond), // Won't cover the period
		staticUpdateTime: time.Now().Add(time.Hour),                                              // 1 hour from now
	}
	fastrand.Read(wptValid.staticPriceTable.UID[:])
	wt.staticSetPriceTable(wptValid)

	// Wait for the goroutine to finish.
	select {
	case <-time.After(5 * priceTableRetryInterval):
		t.Fatal("goroutine won't stop")
	case <-done:
	}

	// Make sure the correct price table was returned.
	if !reflect.DeepEqual(*pt, wptValid.staticPriceTable) {
		t.Fatal("invalid price table returned")
	}
}

// TestSubscriptionLoop is a unit test for managedSubscriptionLoop.
func TestSubscriptionLoop(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a worker that's not running its worker loop.
	wt, err := newWorkerTesterCustomDependency(t.Name(), &dependencies.DependencyDisableWorker{}, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Ignore threadgroup stopped error since we are manually closing the
		// threadgroup of the worker.
		if err := wt.Close(); err != nil && !errors.Contains(err, threadgroup.ErrStopped) {
			t.Fatal(err)
		}
	}()

	// Prepare a unique handler for the host to subscribe to.
	var subscriber types.Specifier
	fastrand.Read(subscriber[:])
	subscriberStr := hex.EncodeToString(subscriber[:])

	// Register the handler. This can happen after beginning the subscription
	// since we are not expecting any notifications yet.
	err = wt.renter.staticMux.NewListener(subscriberStr, wt.managedHandleNotification)
	if err != nil {
		t.Fatal(err)
	}

	// Get a price table and refill the account manually.
	wt.staticUpdatePriceTable()
	wt.managedRefillAccount()

	// The fresh price table should be valid for the subscription.
	wpt := wt.staticPriceTable()
	if !priceTableValidFor(wpt, modules.SubscriptionPeriod) {
		t.Fatal("price table not valid for long enough")
	}

	// Compute the expected deadline.
	deadline := time.Now().Add(modules.SubscriptionPeriod)

	// Set the initial budget to half the budget that the loop should maintain.
	expectedBudget := initialSubscriptionBudget
	initialBudget := expectedBudget.Div64(2)
	budget := modules.NewBudget(initialBudget)

	// Begin the subscription.
	stream, err := wt.managedBeginSubscription(initialBudget, wt.staticAccount.staticID, subscriber)
	if err != nil {
		t.Fatal(err)
	}

	// Set the bandwidth limiter on the stream.
	pt := &wpt.staticPriceTable
	limit := modules.NewBudgetLimit(budget, pt.DownloadBandwidthCost, pt.UploadBandwidthCost)
	err = stream.SetLimit(limit)
	if err != nil {
		t.Fatal(err)
	}

	// Remember bandwidth before subscription.
	downloadBefore := limit.Downloaded()
	uploadBefore := limit.Uploaded()

	// Run the subscription loop in a separate goroutine.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := wt.managedSubscriptionLoop(stream, pt, deadline, budget, expectedBudget)
		if err != nil && !errors.Contains(err, threadgroup.ErrStopped) {
			t.Error(err)
			return
		}
	}()

	// Make sure the budget is funded.
	err = build.Retry(10, time.Second, func() error {
		// Compute bandwidth cost before subscribing.
		downloadBeforeCost := pt.DownloadBandwidthCost.Mul64(downloadBefore)
		uploadBeforeCost := pt.UploadBandwidthCost.Mul64(uploadBefore)
		bandwidthBeforeCost := downloadBeforeCost.Add(uploadBeforeCost)

		balanceBeforeFund := initialBudget.Sub(bandwidthBeforeCost)
		fundAmt := expectedBudget.Sub(balanceBeforeFund)

		// Compute the total bandwidth cost.
		downloadCost := pt.DownloadBandwidthCost.Mul64(limit.Downloaded())
		uploadCost := pt.UploadBandwidthCost.Mul64(limit.Uploaded())
		bandwidthCost := downloadCost.Add(uploadCost)

		// The remaining budget should be the initial budget plus the amount of money
		// funded minus the total bandwidth cost.
		remainingBudget := initialBudget.Add(fundAmt).Sub(bandwidthCost)
		if !remainingBudget.Equals(budget.Remaining()) {
			return fmt.Errorf("wrong remaining budget %v != %v", remainingBudget, budget.Remaining())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Start a goroutine that updates the price table whenever necessary.
	wg.Add(1)
	stopTicker := make(chan struct{})
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(100 * time.Millisecond)
		for {
			select {
			case <-stopTicker:
				return
			case <-ticker.C:
			}
			if time.Now().Before(wpt.staticUpdateTime) {
				continue
			}
			wt.staticUpdatePriceTable()
		}
	}()

	// Sleep for a subscription period. This makes sure that the subscription is
	// automatically extended.
	time.Sleep(modules.SubscriptionPeriod)

	// The subscription maps should be empty.
	subInfo := wt.staticSubscriptionInfo
	subInfo.mu.Lock()
	if len(subInfo.subscriptions) != 0 {
		subInfo.mu.Unlock()
		t.Fatal("maps contain subscriptions")
	}
	// Add 2 random rvs to subscription map.
	srv1, spk1, _ := randomRegistryValue()
	subInfo.subscriptions[modules.RegistrySubscriptionID(spk1, srv1.Tweak)] = &subscription{
		staticRequest: &modules.RPCRegistrySubscriptionRequest{
			PubKey: spk1,
			Tweak:  srv1.Tweak,
		},
		subscribed: make(chan struct{}),
		subscribe:  true,
	}

	srv2, spk2, _ := randomRegistryValue()
	subInfo.subscriptions[modules.RegistrySubscriptionID(spk2, srv2.Tweak)] = &subscription{
		staticRequest: &modules.RPCRegistrySubscriptionRequest{
			PubKey: spk2,
			Tweak:  srv2.Tweak,
		},
		subscribed: make(chan struct{}),
		subscribe:  true,
	}
	subInfo.mu.Unlock()

	// Wake up worker.
	select {
	case subInfo.staticWakeChan <- struct{}{}:
	default:
	}

	// Wait for the values to be subscribed to.
	nActive := 0
	for _, sub := range subInfo.subscriptions {
		select {
		case <-time.After(5 * time.Second):
			t.Fatal("timeout")
		case <-sub.subscribed:
		}
		subInfo.mu.Lock()
		// The subscription should be active.
		if !sub.active {
			t.Fatal("subscription should be active")
		}
		// The latest value should be nil since it doesn't exist on the host.
		if sub.latestRV != nil {
			t.Fatal("latest value should be nil")
		}
		subInfo.mu.Unlock()
		nActive++
	}

	// There should be 2 active subscriptions.
	if nActive != 2 {
		t.Fatalf("wrong number of active subscriptions: %v", nActive)
	}

	// Remove the second subscription.
	subInfo.mu.Lock()
	subInfo.subscriptions[modules.RegistrySubscriptionID(spk2, srv2.Tweak)].subscribe = false

	// Add a third subscription which should be removed automatically since
	// "subscribe" is set to false from the beginning. Make sure the channel is
	// closed.
	srv3, spk3, _ := randomRegistryValue()
	sub3 := &subscription{
		staticRequest: &modules.RPCRegistrySubscriptionRequest{
			PubKey: spk3,
			Tweak:  srv3.Tweak,
		},
		subscribed: make(chan struct{}),
		subscribe:  false,
	}
	subInfo.subscriptions[modules.RegistrySubscriptionID(spk2, srv2.Tweak)] = sub3
	subInfo.mu.Unlock()

	// After a bit of time we should be successfully unsubscribed.
	err = build.Retry(10, time.Second, func() error {
		subInfo.mu.Lock()
		defer subInfo.mu.Unlock()
		nActive := 0
		for _, sub := range subInfo.subscriptions {
			if sub.active {
				nActive++
			}
		}
		if nActive != 1 {
			return fmt.Errorf("one subscription should be active %v", nActive)
		}
		if len(subInfo.subscriptions) != 1 {
			return fmt.Errorf("there should only be one subscription in the map %v", len(subInfo.subscriptions))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Stop goroutines and wait for them to finish.
	close(stopTicker)
	err = wt.tg.Stop()
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	// Subscription info should be reset.
	subInfo.mu.Lock()
	for _, sub := range subInfo.subscriptions {
		if sub.active {
			t.Fatal("no subscription should be active")
		}
		select {
		case <-sub.subscribed:
			t.Fatal("channels should be reset")
		default:
		}
	}
	// The channel of sub3 should be closed.
	select {
	case <-sub3.subscribed:
	default:
		t.Fatal("sub3 channel not closed")
	}
	subInfo.mu.Unlock()
}
