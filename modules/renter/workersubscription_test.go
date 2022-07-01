package renter

import (
	"context"
	"encoding/hex"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/threadgroup"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
)

// dummyCloser is a helper type that counts the number of times Close is called.
type dummyCloser struct {
	atomicClosed uint64
}

// Close implements io.Closer.
func (dc *dummyCloser) Close() error {
	atomic.AddUint64(&dc.atomicClosed, 1)
	return nil
}

// staticCount returns the number of times Close was called.
func (dc *dummyCloser) staticCount() uint64 {
	return atomic.LoadUint64(&dc.atomicClosed)
}

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
	rv := modules.NewRegistryValue(tweak, data, rev, modules.RegistryTypeWithoutPubkey).Sign(sk)
	return rv, spk, sk
}

// TestSubscriptionHelpersWithWorker tests the subscription helper methods
// against the worker tester. They are already unit-tested against a host in
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
	err = wt.managedFundSubscription(stream, pt, initialSubscriptionBudget.Div64(2))
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
		staticExpiryTime: time.Now().Add(modules.SubscriptionPeriod).Add(priceTableRetryInterval).Add(time.Second),
		staticUpdateTime: time.Now().Add(time.Hour), // 1 hour from now
	}
	fastrand.Read(wptValid.staticPriceTable.UID[:])
	wt.staticSetPriceTable(wptValid)

	// Wait for the goroutine to finish.
	select {
	case <-time.After(10 * priceTableRetryInterval):
		t.Fatal("goroutine won't stop")
	case <-done:
	}

	// Make sure the correct price table was returned.
	if !reflect.DeepEqual(*pt, wptValid.staticPriceTable) {
		t.Fatal("invalid price table returned")
	}
}

// TestSubscriptionLoop is a unit test for managedSubscriptionLoop. This
// includes making sure that the loop will extend the subscription if necessary
// and fund the budget if it runs low. It also tests that a subscription which
// is added to the subscription map will be subscribed to or unsubscribed from.
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

	// Get a price table and refill the account manually.
	wt.staticUpdatePriceTable()
	wt.managedRefillAccount()

	// Get the EA balance before running the test.
	balance := wt.staticAccount.availableBalance()

	// The fresh price table should be valid for the subscription.
	wpt := wt.staticPriceTable()
	if !wpt.staticValidFor(modules.SubscriptionPeriod) {
		t.Fatal("price table not valid for long enough")
	}
	pt := &wpt.staticPriceTable

	// Compute the expected deadline.
	deadline := time.Now().Add(modules.SubscriptionPeriod)

	// Set the initial budget to half the budget that the loop should maintain.
	expectedBudget := initialSubscriptionBudget
	initialBudget := expectedBudget.Div64(2)
	budget := modules.NewBudget(initialBudget)

	// Prepare a unique handler for the host to subscribe to.
	var subscriber types.Specifier
	fastrand.Read(subscriber[:])
	subscriberStr := hex.EncodeToString(subscriber[:])

	// Begin the subscription.
	stream, err := wt.managedBeginSubscription(initialBudget, wt.staticAccount.staticID, subscriber)
	if err != nil {
		t.Fatal(err)
	}

	// Get the stream's bandwidth limit.
	limit := stream.Limit()

	// Remember bandwidth before subscription.
	downloadBefore := limit.Downloaded()
	uploadBefore := limit.Uploaded()

	// Run the subscription loop in a separate goroutine.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := wt.managedSubscriptionLoop(stream, pt, deadline, budget, expectedBudget, subscriberStr)
		if err != nil && !errors.Contains(err, threadgroup.ErrStopped) {
			t.Error(err)
			return
		}
	}()

	// Make sure the budget is funded.
	var fundAmt types.Currency
	err = build.Retry(10, time.Second, func() error {
		// Fetch latest limit. managedSubscriptionLoop changes it.
		limit := stream.Limit()

		// Compute bandwidth cost before subscribing.
		downloadBeforeCost := pt.DownloadBandwidthCost.Mul64(downloadBefore)
		uploadBeforeCost := pt.UploadBandwidthCost.Mul64(uploadBefore)
		bandwidthBeforeCost := downloadBeforeCost.Add(uploadBeforeCost)

		balanceBeforeFund := initialBudget.Sub(bandwidthBeforeCost)
		fundAmt = expectedBudget.Sub(balanceBeforeFund)

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

	// Get the EA balance after funding the account.
	wt.staticAccount.mu.Lock()
	balanceAfter := wt.staticAccount.availableBalance()
	wt.staticAccount.mu.Unlock()

	// The spending should be half the balance that the loop wants to maintain
	// due to a single refill happening.
	spending := balance.Sub(balanceAfter)
	if !spending.Equals(fundAmt) {
		t.Fatal("fundAmt wasn't subtracted from the EA")
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

	// The subscription maps should be empty.
	subInfo := wt.staticSubscriptionInfo
	subInfo.mu.Lock()
	if len(subInfo.subscriptions) != 0 {
		subInfo.mu.Unlock()
		t.Fatal("maps contain subscriptions")
	}
	// Add 2 random rvs to subscription map.
	srv1, spk1, _ := randomRegistryValue()
	subInfo.subscriptions[modules.DeriveRegistryEntryID(spk1, srv1.Tweak)] = &subscription{
		staticRequest: &modules.RPCRegistrySubscriptionRequest{
			PubKey: spk1,
			Tweak:  srv1.Tweak,
		},
		subscribed: make(chan struct{}),
		subscribe:  true,
	}

	srv2, spk2, _ := randomRegistryValue()
	subInfo.subscriptions[modules.DeriveRegistryEntryID(spk2, srv2.Tweak)] = &subscription{
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
		case <-time.After(10 * time.Second):
			t.Fatal("timeout")
		case <-sub.subscribed:
		}
		subInfo.mu.Lock()
		// The subscription should be active.
		if !sub.active() {
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

	// Wait for period to be extended while having active subscriptions. This
	// time it will be extended with active subscriptions.
	nExtensions := atomic.LoadUint64(&subInfo.atomicExtensions)
	err = build.Retry(10, 10*time.Second, func() error {
		n := atomic.LoadUint64(&subInfo.atomicExtensions)
		if n <= nExtensions {
			return fmt.Errorf("still waiting %v <= %v", n, nExtensions)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Remove the second subscription.
	subInfo.mu.Lock()
	subInfo.subscriptions[modules.DeriveRegistryEntryID(spk2, srv2.Tweak)].subscribe = false

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
	subInfo.subscriptions[modules.DeriveRegistryEntryID(spk2, srv2.Tweak)] = sub3
	subInfo.mu.Unlock()

	// After a bit of time we should be successfully unsubscribed.
	err = build.Retry(10, time.Second, func() error {
		subInfo.mu.Lock()
		defer subInfo.mu.Unlock()
		nActive := 0
		for _, sub := range subInfo.subscriptions {
			if sub.active() {
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
	err = wt.staticTG.Stop()
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	// Subscription info should be reset.
	subInfo.mu.Lock()
	for _, sub := range subInfo.subscriptions {
		if sub.active() {
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

	// Check that the budget was withdrawn from correctly.
	err = build.Retry(10, time.Second, func() error {
		limit := stream.Limit()

		// Compute bandwidth cost.
		downloadCost := pt.DownloadBandwidthCost.Mul64(limit.Downloaded())
		uploadCost := pt.UploadBandwidthCost.Mul64(limit.Uploaded())
		bandwidthCost := downloadCost.Add(uploadCost)

		// Compute subscription cost. Subscribed to 2 entries of which 0
		// existed.
		subscriptionCost := modules.MDMSubscribeCost(pt, 0, 2)

		// Compute notification cost. There should not have been any.
		notificationCost := types.ZeroCurrency

		// Compute extension cost. We extended twice with 2 active subscriptions.
		nExtensions := atomic.LoadUint64(&subInfo.atomicExtensions)
		extensionCost := modules.MDMSubscriptionMemoryCost(pt, 2).Mul64(nExtensions)

		// Compute the total cost.
		totalCost := bandwidthCost.Add(subscriptionCost).Add(notificationCost).Add(extensionCost)

		// Compute the remaining budget. Consider the fundAmt from before.
		remainingBudget := initialBudget.Sub(totalCost).Add(fundAmt)
		if !remainingBudget.Equals(budget.Remaining()) {
			return fmt.Errorf("wrong remaining budget %v != %v", remainingBudget, budget.Remaining())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestSubscriptionNotifications is a unit test that is focused on subscribing
// and unsubscribing. It verifies that subscribed values are received correctly
// and that they also update the worker's cache.
func TestSubscriptionNotifications(t *testing.T) {
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

	// Get a price table and refill the account manually.
	wt.staticUpdatePriceTable()
	wt.managedRefillAccount()

	// Prepare a helper to update an entry on a deactivated worker without
	// affecting the cache.
	update := func(spk types.SiaPublicKey, rv modules.SignedRegistryValue) error {
		c := make(chan *jobUpdateRegistryResponse, 1)
		j := wt.newJobUpdateRegistry(context.Background(), c, spk, rv)
		_, err = j.managedUpdateRegistry()
		return err
	}

	// Create 2 entries and set one of them on the host.
	rv1, spk1, sk1 := randomRegistryValue()
	rv2, spk2, sk2 := randomRegistryValue()
	err = update(spk1, rv1)
	if err != nil {
		t.Fatal(err)
	}
	// Prepare 2 updates for the same entries for later.
	rv1a := rv1
	rv2a := rv2
	rv1a.Revision++
	rv2a.Revision++
	rv1a = rv1a.Sign(sk1)
	rv2a = rv2a.Sign(sk2)

	// The fresh price table should be valid for the subscription.
	wpt := wt.staticPriceTable()
	if !wpt.staticValidFor(modules.SubscriptionPeriod) {
		t.Fatal("price table not valid for long enough")
	}

	// Compute the expected deadline.
	deadline := time.Now().Add(modules.SubscriptionPeriod)

	// Set the initial budget.
	expectedBudget := initialSubscriptionBudget
	initialBudget := expectedBudget
	budget := modules.NewBudget(initialBudget)
	pt := &wpt.staticPriceTable

	// Begin the subscription.
	stream, err := wt.managedBeginSubscription(initialBudget, wt.staticAccount.staticID, subscriber)
	if err != nil {
		t.Fatal(err)
	}

	// Run the subscription loop in a separate goroutine.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := wt.managedSubscriptionLoop(stream, pt, deadline, budget, expectedBudget, subscriberStr)
		if err != nil && !errors.Contains(err, threadgroup.ErrStopped) {
			t.Error(err)
			return
		}
	}()

	// Subscribe to both entries.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	rvs, err := wt.Subscribe(ctx, []modules.RPCRegistrySubscriptionRequest{
		{
			PubKey: spk1,
			Tweak:  rv1.Tweak,
		},
		{
			PubKey: spk2,
			Tweak:  rv2.Tweak,
		},
	}...)
	if err != nil {
		t.Fatal(err)
	}

	// Only 1 value should be returned since the host only knows about 1 entry yet.
	if len(rvs) != 1 {
		t.Fatalf("expected len to be 1 but was %v", len(rvs))
	}
	if !reflect.DeepEqual(rvs[0].Entry, rv1) {
		t.Fatal("wrong entry was returned")
	}
	if !rvs[0].PubKey.Equals(spk1) {
		t.Fatal("wrong pubkey was returned")
	}

	// The worker should have updated the cache.
	cache := wt.staticRegistryCache
	cachedRev, exists := cache.Get(spk1, rv1.Tweak)
	if !exists || cachedRev != rv1.Revision {
		t.Fatal("cache wasn't updated correctyl")
	}
	_, exists = cache.Get(spk2, rv2.Tweak)
	if exists {
		t.Fatal("cache shouldn't be updated for rv2")
	}

	// The workers internal state should reflect the subscription.
	subInfo := wt.staticSubscriptionInfo
	subInfo.mu.Lock()
	if len(subInfo.subscriptions) != 2 {
		t.Fatal("should have 2 subscriptions")
	}
	subInfo.mu.Unlock()

	// Unsubscribe from rv1.
	wt.Unsubscribe([]modules.RPCRegistrySubscriptionRequest{
		{
			PubKey: spk1,
			Tweak:  rv1.Tweak,
		},
	}...)

	// The worker should eventually only have 1 subscription.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		subInfo.mu.Lock()
		defer subInfo.mu.Unlock()
		if len(subInfo.subscriptions) != 1 {
			return errors.New("should have 1 subscriptions")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Update h
	err = update(spk1, rv1a)
	if err != nil {
		t.Fatal(err)
	}
	err = update(spk2, rv2a)
	if err != nil {
		t.Fatal(err)
	}

	// The worker should receive the notification for the subscribed entry and
	// update the cache.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// rv1 should still be the same
		cachedRev, exists := cache.Get(spk1, rv1.Tweak)
		if !exists {
			return errors.New("rv1: cached entry doesn't exist")
		}
		if cachedRev != rv1.Revision {
			return fmt.Errorf("rv1: wrong cached value %v != %v", cachedRev, rv1.Revision)
		}
		// rv2 should be updated to rv2a
		cachedRev, exists = cache.Get(spk2, rv2.Tweak)
		if !exists {
			return errors.New("rv2: cached entry doesn't exist")
		}
		if cachedRev != rv2a.Revision {
			return fmt.Errorf("rv2: wrong cached value %v != %v", cachedRev, rv2a.Revision)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// The worker should also have updated the subscription.
	subInfo.mu.Lock()
	sub, exists := subInfo.subscriptions[modules.DeriveRegistryEntryID(spk2, rv2a.Tweak)]
	if !exists {
		t.Fatal("rv2's subscription doesn't exist")
	}
	if !reflect.DeepEqual(*sub.latestRV, rv2a) {
		t.Log(sub.latestRV)
		t.Log(rv2a)
		t.Fatal("latestRV wasn't updated")
	}
	subInfo.mu.Unlock()

	// Stop the loop by shutting down the worker.
	err = wt.staticTG.Stop()
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()

	// Check that the subscriptions are cleared.
	subInfo.mu.Lock()
	for _, sub := range subInfo.subscriptions {
		if sub.active() {
			t.Fatal("no subscription should be active")
		}
	}
	subInfo.mu.Unlock()

	// Check that the budget was withdrawn from correctly.
	err = build.Retry(10, time.Second, func() error {
		limit := stream.Limit()

		// Compute bandwidth cost.
		downloadCost := pt.DownloadBandwidthCost.Mul64(limit.Downloaded())
		uploadCost := pt.UploadBandwidthCost.Mul64(limit.Uploaded())
		bandwidthCost := downloadCost.Add(uploadCost)

		// Compute subscription cost. Subscribed to 2 entries of which 1
		// existed.
		subscriptionCost := modules.MDMSubscribeCost(pt, 1, 2)

		// Compute notification cost.
		notificationCost := pt.SubscriptionNotificationCost

		// Compute extension cost. Should be zero since this test never extends.
		extensionCost := types.ZeroCurrency

		// Compute the total cost.
		totalCost := bandwidthCost.Add(subscriptionCost).Add(notificationCost).Add(extensionCost)

		// Compute the remaining budget
		remainingBudget := initialBudget.Sub(totalCost)
		if !remainingBudget.Equals(budget.Remaining()) {
			return fmt.Errorf("wrong remaining budget %v != %v", remainingBudget, budget.Remaining())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestHandleNotification is a unit test for managedHandleNotification.
func TestHandleNotification(t *testing.T) {
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

	// Declare some costs.
	downloadBandwidthCost := types.NewCurrency64(1)
	uploadBandwidthCost := types.NewCurrency64(1)
	notificationCost := types.NewCurrency64(1)

	// Create a handler for testing.
	closer := &dummyCloser{}
	nh := &notificationHandler{
		staticStream:        closer,
		staticWorker:        wt.worker,
		staticPTUpdateChan:  make(chan struct{}),
		staticPTUpdatedChan: make(chan struct{}),
		notificationCost:    notificationCost,
	}

	// Register it.
	budget := modules.NewBudget(types.SiacoinPrecision.Mul64(1000))
	limit := modules.NewBudgetLimit(budget, downloadBandwidthCost, uploadBandwidthCost)
	err = wt.renter.staticMux.NewListener(subscriberStr, func(stream siamux.Stream) {
		nh.managedHandleNotification(stream, budget, limit)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Declare a helper function that creates a stream which triggers the
	// notification handler when written to.
	hostStream := func() siamux.Stream {
		muxAddr := wt.renter.staticMux.Address()
		pk := wt.renter.staticMux.PublicKey()
		stream, err := wt.renter.staticMux.NewStream(subscriberStr, muxAddr.String(), pk)
		if err != nil {
			t.Fatal(err)
		}
		return stream
	}

	// helper to send a new "subscription success" notification.
	sendSuccessNotification := func() {
		stream := hostStream()
		defer stream.Close()
		err := modules.RPCWrite(stream, modules.RPCRegistrySubscriptionNotificationType{
			Type: modules.SubscriptionResponseSubscriptionSuccess,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// helper to send a new "registry value" notification.
	sendRegistryValue := func(spk types.SiaPublicKey, srv modules.SignedRegistryValue) {
		stream := hostStream()
		defer stream.Close()
		err := modules.RPCWrite(stream, modules.RPCRegistrySubscriptionNotificationType{
			Type: modules.SubscriptionResponseRegistryValue,
		})
		if err != nil {
			t.Fatal(err)
		}
		err = modules.RPCWrite(stream, modules.RPCRegistrySubscriptionNotificationEntryUpdate{
			Entry:  srv,
			PubKey: spk,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	// helper to compare bandwidth and cost for every test.
	testNotification := func(run func(), downloadCost, uploadCost, additionalCost types.Currency) {
		// Capture used bandwidth and budget before test.
		budgetBefore := budget.Remaining()
		downloadBefore := limit.Downloaded()
		uploadBefore := limit.Uploaded()

		// Run the test.
		run()

		// Compute how much bandwidth and budget was used.
		usedBudget := budgetBefore.Sub(budget.Remaining())
		usedDownload := limit.Downloaded() - downloadBefore
		usedUpload := limit.Uploaded() - uploadBefore

		// Assert result.
		dc := downloadCost.Mul64(usedDownload)
		uc := uploadCost.Mul64(usedUpload)
		if !dc.Add(uc).Add(additionalCost).Equals(usedBudget) {
			t.Log("usedDownload", usedDownload)
			t.Log("usedUpload", usedUpload)
			t.Fatalf("%v + %v + %v != %v", dc, uc, additionalCost, usedBudget)
		}
	}

	// Vars for testing.
	subInfo := wt.staticSubscriptionInfo

	// Test - valid update
	testNotification(func() {
		// Subscribe to a random registry value.
		rv, spk, _ := randomRegistryValue()
		sid := modules.DeriveRegistryEntryID(spk, rv.Tweak)
		subInfo.subscriptions[sid] = newSubscription(&modules.RPCRegistrySubscriptionRequest{
			PubKey: spk,
			Tweak:  rv.Tweak,
		})
		// Send the notification.
		sendRegistryValue(spk, rv)
		// The worker cache and subscription should be updated.
		err := build.Retry(100, 100*time.Millisecond, func() error {
			// Check worker cache.
			if revNum, found := wt.staticRegistryCache.Get(spk, rv.Tweak); !found || revNum != rv.Revision {
				return fmt.Errorf("cache wasn't updated %v != %v %v", revNum, rv.Revision, found)
			}
			// Check subscription.
			subInfo.mu.Lock()
			sub := subInfo.subscriptions[sid]
			if !reflect.DeepEqual(*sub.latestRV, rv) {
				subInfo.mu.Unlock()
				return errors.New("latestRV doesn't match rv")
			}
			subInfo.mu.Unlock()
			// Check if stream was closed.
			if closer.staticCount() != 0 {
				return errors.New("stream shouldn't have been closed")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}, downloadBandwidthCost, uploadBandwidthCost, notificationCost)

	// Test - outdated entry
	testNotification(func() {
		// Subscribe to a random registry value.
		rv, spk, sk := randomRegistryValue()
		sid := modules.DeriveRegistryEntryID(spk, rv.Tweak)
		subInfo.subscriptions[sid] = newSubscription(&modules.RPCRegistrySubscriptionRequest{
			PubKey: spk,
			Tweak:  rv.Tweak,
		})
		rv2 := rv
		rv2.Revision++
		rv2 = rv2.Sign(sk)
		// Send the notification for rv2 first which has a higher revision
		// number than rv.
		sendRegistryValue(spk, rv2)
		// Then send rv.
		sendRegistryValue(spk, rv)
		// Check fields.
		err := build.Retry(100, 100*time.Millisecond, func() error {
			// Check worker cache. Should be set to rv2.
			if revNum, found := wt.staticRegistryCache.Get(spk, rv.Tweak); !found || revNum != rv2.Revision {
				return fmt.Errorf("cache wasn't updated %v != %v %v", revNum, rv2.Revision, found)
			}
			// Check subscription. Should be set to rv2.
			subInfo.mu.Lock()
			sub := subInfo.subscriptions[sid]
			if !reflect.DeepEqual(*sub.latestRV, rv2) {
				subInfo.mu.Unlock()
				return errors.New("latestRV doesn't match rv")
			}
			subInfo.mu.Unlock()
			// Stream should have been closed.
			if closer.staticCount() != 1 {
				return fmt.Errorf("stream should have been closed: %v", closer.staticCount())
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}, downloadBandwidthCost, uploadBandwidthCost, notificationCost.Mul64(2))

	// Test - same entry twice.
	testNotification(func() {
		// Subscribe to a random registry value.
		rv, spk, _ := randomRegistryValue()
		sid := modules.DeriveRegistryEntryID(spk, rv.Tweak)
		subInfo.subscriptions[sid] = newSubscription(&modules.RPCRegistrySubscriptionRequest{
			PubKey: spk,
			Tweak:  rv.Tweak,
		})
		// Send the notification twice.
		sendRegistryValue(spk, rv)
		sendRegistryValue(spk, rv)
		// Check fields.
		err := build.Retry(100, 100*time.Millisecond, func() error {
			// Check worker cache. Should be set to rv.
			if revNum, found := wt.staticRegistryCache.Get(spk, rv.Tweak); !found || revNum != rv.Revision {
				return fmt.Errorf("cache wasn't updated %v != %v %v", revNum, rv.Revision, found)
			}
			// Check subscription. Should be set to rv.
			subInfo.mu.Lock()
			sub := subInfo.subscriptions[sid]
			if !reflect.DeepEqual(*sub.latestRV, rv) {
				subInfo.mu.Unlock()
				return errors.New("latestRV doesn't match rv")
			}
			subInfo.mu.Unlock()
			// Stream should have been closed.
			if closer.staticCount() != 2 {
				return fmt.Errorf("stream should have been closed: %v", closer.staticCount())
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}, downloadBandwidthCost, uploadBandwidthCost, notificationCost.Mul64(2))

	// Test - not subscribed to entry
	testNotification(func() {
		// Create a random registry entry.
		rv, spk, _ := randomRegistryValue()
		sid := modules.DeriveRegistryEntryID(spk, rv.Tweak)
		// Send rv.
		sendRegistryValue(spk, rv)
		// Check fields.
		err := build.Retry(100, 100*time.Millisecond, func() error {
			// Check worker cache. Should be set to rv.
			if revNum, found := wt.staticRegistryCache.Get(spk, rv.Tweak); !found || revNum != rv.Revision {
				return fmt.Errorf("cache wasn't updated %v != %v %v", revNum, rv.Revision, found)
			}
			// Check subscription. Should be set to rv.
			subInfo.mu.Lock()
			_, exist := subInfo.subscriptions[sid]
			if exist {
				subInfo.mu.Unlock()
				return errors.New("subscription shouldn't exist")
			}
			subInfo.mu.Unlock()
			// Stream should have been closed.
			if closer.staticCount() != 3 {
				return fmt.Errorf("stream should have been closed: %v", closer.staticCount())
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}, downloadBandwidthCost, uploadBandwidthCost, notificationCost)

	// Test - subscription extended
	testNotification(func() {
		// Push a new price table.
		// It contains double of the previous specified costs.
		pt := modules.RPCPriceTable{
			SubscriptionNotificationCost: notificationCost.Mul64(2),
			DownloadBandwidthCost:        downloadBandwidthCost.Mul64(2),
			UploadBandwidthCost:          uploadBandwidthCost.Mul64(2),
		}

		// Send success notification.
		sendSuccessNotification()

		// Wait for the update signal.
		<-nh.staticPTUpdateChan

		// Update the limit.
		limit.UpdateCosts(pt.DownloadBandwidthCost, pt.UploadBandwidthCost)
		nh.mu.Lock()
		nh.notificationCost = pt.SubscriptionNotificationCost
		nh.mu.Unlock()

		// Signal update done.
		nh.staticPTUpdatedChan <- struct{}{}

		// The notification cost should be updated on the handler.
		if !nh.notificationCost.Equals(pt.SubscriptionNotificationCost) {
			t.Fatal("notification cost wasn't updated")
		}

		// Stream should not have been closed.
		if closer.staticCount() != 3 {
			t.Fatal("stream shouldn't have been closed")
		}
	}, downloadBandwidthCost.Mul64(2), uploadBandwidthCost.Mul64(2), types.ZeroCurrency)

	// Test invalid notification type.
	testNotification(func() {
		// Send the invalid notification.
		stream := hostStream()
		defer stream.Close()
		err := modules.RPCWrite(stream, modules.RPCRegistrySubscriptionNotificationType{})
		if err != nil {
			t.Fatal(err)
		}
		err = build.Retry(100, 100*time.Millisecond, func() error {
			if closer.staticCount() != 4 {
				return fmt.Errorf("stream should have been closed: %v", closer.staticCount())
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}, downloadBandwidthCost.Mul64(2), uploadBandwidthCost.Mul64(2), types.ZeroCurrency)
}

// TestThreadedSubscriptionLoop tests threadedSubscriptionLoop.
func TestThreadedSubscriptionLoop(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a worker.
	wt, err := newWorkerTester(t.Name())
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

	// Set a random entry on the host.
	rv, spk, sk := randomRegistryValue()
	err = wt.UpdateRegistry(context.Background(), spk, rv)
	if err != nil {
		t.Fatal(err)
	}

	// Subscribe to that entry.
	req := modules.RPCRegistrySubscriptionRequest{
		PubKey: spk,
		Tweak:  rv.Tweak,
	}
	resps, err := wt.Subscribe(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resps) != 1 {
		t.Fatal("invalid length", len(resps))
	}
	if !spk.Equals(resps[0].PubKey) {
		t.Fatal("pubkeys don't match")
	}
	if !reflect.DeepEqual(resps[0].Entry, rv) {
		t.Fatal("entries don't match")
	}

	// Check that the subscription info has 1 subscription.
	subInfo := wt.staticSubscriptionInfo
	subInfo.mu.Lock()
	if len(subInfo.subscriptions) != 1 {
		t.Error("subInfo has wrong length", len(subInfo.subscriptions))
	}
	subInfo.mu.Unlock()

	// Do it again. This should return the same value. Since we are subscribed
	// already this will not establish a new subscription.
	resps, err = wt.Subscribe(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(resps) != 1 {
		t.Fatal("invalid length", len(resps))
	}
	if !spk.Equals(resps[0].PubKey) {
		t.Fatal("pubkeys don't match")
	}
	if !reflect.DeepEqual(resps[0].Entry, rv) {
		t.Fatal("entries don't match")
	}

	// Update the entry on the host.
	rv.Revision++
	rv = rv.Sign(sk)
	err = wt.UpdateRegistry(context.Background(), spk, rv)
	if err != nil {
		t.Fatal(err)
	}

	// Do it again. This should return the new value.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		resps, err = wt.Subscribe(context.Background(), req)
		if err != nil {
			return err
		}
		if len(resps) != 1 {
			return fmt.Errorf("invalid length %v", len(resps))
		}
		if !spk.Equals(resps[0].PubKey) {
			return errors.New("pubkeys don't match")
		}
		if !reflect.DeepEqual(resps[0].Entry, rv) {
			return errors.New("entries don't match")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Unsubscribe from the entry.
	wt.Unsubscribe(req)

	// There should be 0 subscriptions.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		subInfo.mu.Lock()
		defer subInfo.mu.Unlock()
		if len(subInfo.subscriptions) != 0 {
			return fmt.Errorf("subInfo has wrong length %v", len(subInfo.subscriptions))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get the account balance before interrupting the loop.
	wt.staticAccount.mu.Lock()
	balance := wt.staticAccount.availableBalance()
	wt.staticAccount.mu.Unlock()

	// Stop the loop by shutting down the worker.
	err = wt.staticTG.Stop()
	if err != nil {
		t.Fatal(err)
	}

	err = build.Retry(100, 100*time.Millisecond, func() error {
		// Get the EA balance again and check the spending.
		wt.staticAccount.mu.Lock()
		balanceAfter := wt.staticAccount.availableBalance()
		wt.staticAccount.mu.Unlock()
		refund := balanceAfter.Sub(balance)
		spending := initialSubscriptionBudget.Sub(refund)

		// Compute the cost. Since we don't have access to the bandwidth, we use
		// hardcoded values.
		pt := wt.staticPriceTable().staticPriceTable
		downloadCost := pt.DownloadBandwidthCost.Mul64(4380)
		uploadCost := pt.UploadBandwidthCost.Mul64(7300)
		bandwidthCost := downloadCost.Add(uploadCost)
		subCost := modules.MDMSubscribeCost(&pt, 1, 1)
		notificationCost := pt.SubscriptionNotificationCost
		cost := bandwidthCost.Add(notificationCost).Add(subCost)
		if !cost.Equals(spending) {
			return fmt.Errorf("cost doesn't equal spending %v != %v", cost, spending)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestSubscribeUnsubscribeByRID tests the subscription helper methods against
// the worker tester. They are already unit-tested against a host in
// rpcsubscribe_test.go but better safe than sorry.
func TestSubscribeUnsubscribeByRID(t *testing.T) {
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
	initialValues, err := modules.RPCSubscribeToRVsByRID(stream, []modules.RPCRegistrySubscriptionByRIDRequest{
		{
			EntryID: modules.DeriveRegistryEntryID(spk1, srv1.Tweak),
		},
		{
			EntryID: modules.DeriveRegistryEntryID(spk2, srv2.Tweak),
		},
		{
			EntryID: modules.DeriveRegistryEntryID(spk3, srv3.Tweak),
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
	err = wt.managedFundSubscription(stream, pt, initialSubscriptionBudget.Div64(2))
	if err != nil {
		t.Fatal(err)
	}

	// Extend the subscription.
	err = modules.RPCExtendSubscription(stream, pt)
	if err != nil {
		t.Fatal(err)
	}

	// Unsubscribe from the values again.
	err = modules.RPCUnsubscribeFromRVsByRID(stream, []modules.RPCRegistrySubscriptionByRIDRequest{
		{
			EntryID: modules.DeriveRegistryEntryID(spk1, srv1.Tweak),
		},
		{
			EntryID: modules.DeriveRegistryEntryID(spk2, srv2.Tweak),
		},
		{
			EntryID: modules.DeriveRegistryEntryID(spk3, srv3.Tweak),
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
