package host

import (
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/siamux/mux"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
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
	rv := modules.NewRegistryValue(tweak, data, rev, modules.RegistryTypeWithoutPubkey).Sign(sk)
	return rv, spk, sk
}

// assertInfo asserts the fields of a single subscriptionInfo.
func assertInfo(info *subscriptionInfo, notificationCost, remainingBudget types.Currency) error {
	info.mu.Lock()
	defer info.mu.Unlock()
	if !info.notificationCost.Equals(notificationCost) {
		return fmt.Errorf("notification cost in info doesn't match pricetable %v != %v", info.notificationCost.HumanString(), notificationCost.HumanString())
	}
	if !info.staticBudget.Remaining().Equals(remainingBudget) {
		return fmt.Errorf("host budget doesn't match expected budget %v != %v", info.staticBudget.Remaining(), remainingBudget)
	}
	if info.staticStream == nil {
		return errors.New("stream not set")
	}
	return nil
}

// assertNumSubscriptions asserts the total number of subscribed to entries on a
// host.
func assertNumSubscriptions(host *Host, n int) error {
	host.staticRegistrySubscriptions.mu.Lock()
	defer host.staticRegistrySubscriptions.mu.Unlock()
	if len(host.staticRegistrySubscriptions.subscriptions) != n {
		return fmt.Errorf("invalid number of subscriptions %v != %v", len(host.staticRegistrySubscriptions.subscriptions), n)
	}
	return nil
}

// assertSubscriptionInfos asserts the number of times a specific entry has been
// subscribed to and returns the corresponding nsubscription infos.
func assertSubscriptionInfos(host *Host, spk types.SiaPublicKey, tweak crypto.Hash, n int) ([]*subscriptionInfo, error) {
	sid := modules.DeriveRegistryEntryID(spk, tweak)
	host.staticRegistrySubscriptions.mu.Lock()
	subInfos, found := host.staticRegistrySubscriptions.subscriptions[sid]
	host.staticRegistrySubscriptions.mu.Unlock()
	if !found {
		return nil, errors.New("subscription not found for id")
	}
	if len(subInfos) != n {
		return nil, fmt.Errorf("wrong number of subscription infos %v != %v", len(subInfos), n)
	}
	var infos []*subscriptionInfo
	for _, info := range subInfos {
		infos = append(infos, info)
		if _, ok := info.subscriptions[sid]; !ok {
			return nil, errors.New("info doesn't contain subscription")
		}
	}
	return infos, nil
}

// readAndAssertRegistryValueNotification reads a notification, checks that the
// notification is valid and compares the entry to the provided one.
func readAndAssertRegistryValueNotification(spk types.SiaPublicKey, rv modules.SignedRegistryValue, r io.Reader) error {
	var snt modules.RPCRegistrySubscriptionNotificationType
	err := modules.RPCRead(r, &snt)
	if err != nil {
		return err
	}
	if snt.Type != modules.SubscriptionResponseRegistryValue {
		return errors.New("notification has wrong type")
	}
	var sneu modules.RPCRegistrySubscriptionNotificationEntryUpdate
	err = modules.RPCRead(r, &sneu)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(rv, sneu.Entry) {
		return errors.New("wrong entry in notification")
	}
	if !sneu.PubKey.Equals(spk) {
		return errors.New("wrong pubkey returned")
	}
	return nil
}

// readAndAssertOkResponse reads an 'OK' notification and makes sure that it has
// the right type.
func readAndAssertOkResponse(r io.Reader) error {
	var snt modules.RPCRegistrySubscriptionNotificationType
	err := modules.RPCRead(r, &snt)
	if err != nil {
		return err
	}
	if snt.Type != modules.SubscriptionResponseSubscriptionSuccess {
		return errors.New("notification has wrong type")
	}
	return nil
}

// TestRPCSubscribe is a set of tests related to the registry subscription rpc.
func TestRPCSubscribe(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a blank host tester
	rhp, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rhp.Close()
		if err != nil {
			t.Error(err)
		}
	}()

	// Add some space to the host's registry.
	is := rhp.staticHT.host.InternalSettings()
	is.RegistrySize += (modules.RegistryEntrySize * 100)
	err = rhp.staticHT.host.SetInternalSettings(is)
	if err != nil {
		t.Fatal(err)
	}

	// Test the standard flow.
	t.Run("Basic", func(t *testing.T) {
		t.Run("ByKey", func(t *testing.T) {
			testRPCSubscribeBasic(t, rhp, false)
		})
		t.Run("ByRID", func(t *testing.T) {
			testRPCSubscribeBasic(t, rhp, true)
		})
	})
	t.Run("SubscribeBeforeAvailable", func(t *testing.T) {
		testRPCSubscribeBeforeAvailable(t, rhp)
	})
	t.Run("Timeout", func(t *testing.T) {
		testRPCSubscribeSessionTimeout(t, rhp)
	})
	t.Run("ExtendTimeout", func(t *testing.T) {
		testRPCSubscribeExtendTimeout(t, rhp)
	})
	t.Run("Concurrent", func(t *testing.T) {
		testRPCSubscribeConcurrent(t, rhp)
	})
}

// testRPCSubscribeBasic tests subscribing to an entry and unsubscribing without
// hitting any edge cases.
func testRPCSubscribeBasic(t *testing.T, rhp *renterHostPair, useRID bool) {
	// Prepare a listener for the worker.
	notificationReader, notificationWriter := io.Pipe()
	var sub types.Specifier
	fastrand.Read(sub[:])
	var notificationUploaded, notificationDownloaded uint64
	var numNotifications uint64
	var notificationMu sync.Mutex
	err := rhp.staticRenterMux.NewListener(hex.EncodeToString(sub[:]), func(stream siamux.Stream) {
		notificationMu.Lock()
		defer notificationMu.Unlock()
		defer func() {
			if err := stream.Close(); err != nil {
				t.Error(err)
			}
		}()
		numNotifications++

		// Copy the output to the pipe.
		io.Copy(notificationWriter, stream)

		// Collect used bandwidth.
		notificationDownloaded += stream.Limit().Downloaded()
		notificationUploaded += stream.Limit().Uploaded()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = rhp.staticRenterMux.CloseListener(hex.EncodeToString(sub[:]))
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Create a registry value.
	expiry := types.BlockHeight(1000)
	rv, spk, sk := randomRegistryValue()
	tweak := rv.Tweak

	// Set it on the host.
	host := rhp.staticHT.host
	_, err = host.RegistryUpdate(rv, spk, expiry)
	if err != nil {
		t.Fatal(err)
	}

	// fund the account.
	currentBalance := host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	expectedBalance := modules.DefaultHostExternalSettings().MaxEphemeralAccountBalance
	_, err = rhp.managedFundEphemeralAccount(rhp.pt.FundAccountCost.Add(expectedBalance).Sub(currentBalance), false)
	if err != nil {
		t.Fatal(err)
	}

	// check the account balance.
	if !host.staticAccountManager.callAccountBalance(rhp.staticAccountID).Equals(expectedBalance) {
		t.Fatal("invalid balance", expectedBalance, host.staticAccountManager.callAccountBalance(rhp.staticAccountID))
	}

	// begin the subscription loop.
	initialBudget := expectedBalance.Div64(2)
	stream, err := rhp.BeginSubscription(initialBudget, sub)
	if err != nil {
		t.Fatal(err)
	}
	pt := rhp.managedPriceTable()

	// Prepare a function to compute expected budget.
	l := stream.Limit()
	expectedBudget := func(costs types.Currency) types.Currency {
		notificationMu.Lock()
		defer notificationMu.Unlock()
		upCost := pt.UploadBandwidthCost.Mul64(l.Uploaded() + notificationUploaded)
		downCost := pt.DownloadBandwidthCost.Mul64(l.Downloaded() + notificationDownloaded)
		return initialBudget.Sub(upCost).Sub(downCost).Sub(costs)
	}

	// Helper functions for subscribing and unsubscribing.
	subscribe := func(stream siamux.Stream, pt *modules.RPCPriceTable, pubkey types.SiaPublicKey, tweak crypto.Hash) (*modules.SignedRegistryValue, error) {
		if !useRID {
			return rhp.SubcribeToRV(stream, pt, spk, tweak)
		} else {
			return rhp.SubcribeToRVByRID(stream, pt, modules.DeriveRegistryEntryID(spk, tweak))
		}
	}
	unsubscribe := func(stream siamux.Stream, pt *modules.RPCPriceTable, pubkey types.SiaPublicKey, tweak crypto.Hash) error {
		if !useRID {
			return rhp.UnsubcribeFromRV(stream, pt, spk, tweak)
		} else {
			return rhp.UnsubcribeFromRVByRID(stream, pt, modules.DeriveRegistryEntryID(spk, tweak))
		}
	}

	// subsribe to the previously created entry.
	rvInitial, err := subscribe(stream, pt, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rv, *rvInitial) {
		t.Fatal("initial value doesn't match", rv, rvInitial)
	}

	runningCost := modules.MDMSubscribeCost(pt, 1, 1)

	// Make sure that the host got the subscription.
	err = assertNumSubscriptions(host, 1)
	if err != nil {
		t.Fatal(err)
	}
	infos, err := assertSubscriptionInfos(host, spk, tweak, 1)
	if err != nil {
		t.Fatal(err)
	}
	info := infos[0]

	// The info should have the right fields set.
	err = assertInfo(info, pt.SubscriptionNotificationCost, expectedBudget(runningCost))
	if err != nil {
		t.Fatal(err)
	}

	// Update the entry on the host.
	rv.Revision++
	rv = rv.Sign(sk)
	_, err = host.RegistryUpdate(rv, spk, expiry)
	if err != nil {
		t.Fatal(err)
	}

	// Read the notification and make sure it's the right one.
	err = readAndAssertRegistryValueNotification(spk, rv, notificationReader)
	if err != nil {
		t.Fatal(err)
	}
	runningCost = runningCost.Add(pt.SubscriptionNotificationCost)

	// Check the info again.
	err = assertInfo(info, pt.SubscriptionNotificationCost, expectedBudget(runningCost))
	if err != nil {
		t.Fatal(err)
	}

	// Fund the subscription.
	fundAmt := types.NewCurrency64(42)
	err = rhp.FundSubscription(stream, fundAmt)
	if err != nil {
		t.Fatal(err)
	}
	runningCost = runningCost.Sub(fundAmt)

	// Check the info.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		return assertInfo(info, pt.SubscriptionNotificationCost, expectedBudget(runningCost))
	})
	if err != nil {
		t.Fatal(err)
	}

	// Extend the subscription.
	err = modules.RPCExtendSubscription(stream, pt)
	if err != nil {
		t.Fatal(err)
	}
	runningCost = runningCost.Add(modules.MDMSubscriptionMemoryCost(pt, 1))

	// Read the "OK" response.
	err = readAndAssertOkResponse(notificationReader)
	if err != nil {
		t.Fatal(err)
	}

	// Check the info.
	err = assertInfo(info, pt.SubscriptionNotificationCost, expectedBudget(runningCost))
	if err != nil {
		t.Fatal(err)
	}

	// Unsubscribe.
	err = unsubscribe(stream, pt, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(100, 100*time.Millisecond, func() error {
		return assertNumSubscriptions(host, 0)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Update the entry on the host.
	rv.Revision++
	rv = rv.Sign(sk)
	_, err = host.RegistryUpdate(rv, spk, expiry)
	if err != nil {
		t.Fatal(err)
	}

	// Check the info.
	err = assertInfo(info, pt.SubscriptionNotificationCost, expectedBudget(runningCost))
	if err != nil {
		t.Fatal(err)
	}

	// Close the subscription.
	if err := rhp.StopSubscription(stream); err != nil {
		t.Fatal(err)
	}

	// Check the balance.
	// 1. subtract the initial budget.
	// 2. add the remaining budget.
	// 3. subtract fundAmt.
	expectedBalance = expectedBalance.Sub(initialBudget)
	expectedBalance = expectedBalance.Add(expectedBudget(runningCost))
	expectedBalance = expectedBalance.Sub(fundAmt)
	if !host.staticAccountManager.callAccountBalance(rhp.staticAccountID).Equals(expectedBalance) {
		t.Fatalf("invalid balance %v != %v", expectedBalance, host.staticAccountManager.callAccountBalance(rhp.staticAccountID))
	}

	// Check total subscriptions.
	err = assertNumSubscriptions(host, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Check number of notifications.
	notificationMu.Lock()
	if expected := numNotifications; expected != 2 {
		t.Error("wrong number of notifications", expected, 2)
	}
	notificationMu.Unlock()
}

// testRPCSubscribeBeforeAvailable tests subscribing to a value that is not yet
// available.
func testRPCSubscribeBeforeAvailable(t *testing.T, rhp *renterHostPair) {
	// Prepare a listener for the worker.
	notificationReader, notificationWriter := io.Pipe()
	var sub types.Specifier
	fastrand.Read(sub[:])
	var notificationUploaded, notificationDownloaded uint64
	var numNotifications uint64
	var notificationMu sync.Mutex
	err := rhp.staticRenterMux.NewListener(hex.EncodeToString(sub[:]), func(stream siamux.Stream) {
		notificationMu.Lock()
		defer notificationMu.Unlock()
		defer func() {
			if err := stream.Close(); err != nil {
				t.Error(err)
			}
		}()
		numNotifications++

		// Copy the output to the pipe.
		io.Copy(notificationWriter, stream)

		// Collect used bandwidth.
		notificationDownloaded += stream.Limit().Downloaded()
		notificationUploaded += stream.Limit().Uploaded()
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err = rhp.staticRenterMux.CloseListener(hex.EncodeToString(sub[:]))
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Create a registry value.
	expiry := types.BlockHeight(1000)
	rv, spk, sk := randomRegistryValue()
	tweak := rv.Tweak
	host := rhp.staticHT.host

	// fund the account.
	currentBalance := host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	expectedBalance := modules.DefaultHostExternalSettings().MaxEphemeralAccountBalance
	_, err = rhp.managedFundEphemeralAccount(rhp.pt.FundAccountCost.Add(expectedBalance).Sub(currentBalance), false)
	if err != nil {
		t.Fatal(err)
	}

	// check the account balance.
	if !host.staticAccountManager.callAccountBalance(rhp.staticAccountID).Equals(expectedBalance) {
		t.Fatal("invalid balance", expectedBalance, host.staticAccountManager.callAccountBalance(rhp.staticAccountID))
	}

	// begin the subscription loop.
	initialBudget := expectedBalance.Div64(2)
	stream, err := rhp.BeginSubscription(initialBudget, sub)
	if err != nil {
		t.Fatal(err)
	}
	pt := rhp.managedPriceTable()

	// Prepare a function to compute expected budget.
	l := stream.Limit()
	expectedBudget := func(costs types.Currency) types.Currency {
		notificationMu.Lock()
		defer notificationMu.Unlock()
		upCost := pt.UploadBandwidthCost.Mul64(l.Uploaded() + notificationUploaded)
		downCost := pt.DownloadBandwidthCost.Mul64(l.Downloaded() + notificationDownloaded)
		return initialBudget.Sub(upCost).Sub(downCost).Sub(costs)
	}

	// subsribe to the previously created entry.
	rvInitial, err := rhp.SubcribeToRV(stream, pt, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}
	if rvInitial != nil {
		t.Fatal("no initial value should be returned")
	}

	runningCost := modules.MDMSubscribeCost(pt, 0, 1)

	// Make sure that the host got the subscription.
	err = assertNumSubscriptions(host, 1)
	if err != nil {
		t.Fatal(err)
	}
	infos, err := assertSubscriptionInfos(host, spk, tweak, 1)
	if err != nil {
		t.Fatal(err)
	}
	info := infos[0]

	// The info should have the right fields set.
	err = assertInfo(info, pt.SubscriptionNotificationCost, expectedBudget(runningCost))
	if err != nil {
		t.Fatal(err)
	}

	// Update the entry on the host.
	rv.Revision++
	rv = rv.Sign(sk)
	_, err = host.RegistryUpdate(rv, spk, expiry)
	if err != nil {
		t.Fatal(err)
	}

	// Read the notification and make sure it's the right one.
	err = readAndAssertRegistryValueNotification(spk, rv, notificationReader)
	if err != nil {
		t.Fatal(err)
	}
	runningCost = runningCost.Add(pt.SubscriptionNotificationCost)

	// Check the info again.
	err = assertInfo(info, pt.SubscriptionNotificationCost, expectedBudget(runningCost))
	if err != nil {
		t.Fatal(err)
	}

	// Fund the subscription.
	fundAmt := types.NewCurrency64(42)
	err = rhp.FundSubscription(stream, fundAmt)
	if err != nil {
		t.Fatal(err)
	}
	runningCost = runningCost.Sub(fundAmt)

	// Check the info.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		return assertInfo(info, pt.SubscriptionNotificationCost, expectedBudget(runningCost))
	})
	if err != nil {
		t.Fatal(err)
	}

	// Extend the subscription.
	err = modules.RPCExtendSubscription(stream, pt)
	if err != nil {
		t.Fatal(err)
	}
	runningCost = runningCost.Add(modules.MDMSubscriptionMemoryCost(pt, 1))

	// Read the "OK" response.
	err = readAndAssertOkResponse(notificationReader)
	if err != nil {
		t.Fatal(err)
	}

	// Check the info.
	err = assertInfo(info, pt.SubscriptionNotificationCost, expectedBudget(runningCost))
	if err != nil {
		t.Fatal(err)
	}

	// Unsubscribe.
	err = rhp.UnsubcribeFromRV(stream, pt, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(100, 100*time.Millisecond, func() error {
		return assertNumSubscriptions(host, 0)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Update the entry on the host.
	rv.Revision++
	rv = rv.Sign(sk)
	_, err = host.RegistryUpdate(rv, spk, expiry)
	if err != nil {
		t.Fatal(err)
	}

	// Check the info.
	err = assertInfo(info, pt.SubscriptionNotificationCost, expectedBudget(runningCost))
	if err != nil {
		t.Fatal(err)
	}

	// Close the subscription.
	if err := rhp.StopSubscription(stream); err != nil {
		t.Fatal(err)
	}

	// Check the balance.
	// 1. subtract the initial budget.
	// 2. add the remaining budget.
	// 3. subtract fundAmt.
	expectedBalance = expectedBalance.Sub(initialBudget)
	expectedBalance = expectedBalance.Add(expectedBudget(runningCost))
	expectedBalance = expectedBalance.Sub(fundAmt)
	if !host.staticAccountManager.callAccountBalance(rhp.staticAccountID).Equals(expectedBalance) {
		t.Fatalf("invalid balance %v != %v", expectedBalance, host.staticAccountManager.callAccountBalance(rhp.staticAccountID))
	}

	// Check total subscriptions.
	err = assertNumSubscriptions(host, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Check number of notifications.
	notificationMu.Lock()
	if expected := numNotifications; expected != 2 {
		t.Error("wrong number of notifications", expected, 2)
	}
	notificationMu.Unlock()
}

// testRPCSubscribeSessionTimeout makes sure that a session that is not extended
// times out.
func testRPCSubscribeSessionTimeout(t *testing.T, rhp *renterHostPair) {
	var sub types.Specifier
	fastrand.Read(sub[:])

	// Create a registry value.
	rv, spk, _ := randomRegistryValue()
	tweak := rv.Tweak
	host := rhp.staticHT.host

	// fund the account.
	currentBalance := host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	expectedBalance := modules.DefaultHostExternalSettings().MaxEphemeralAccountBalance
	_, err := rhp.managedFundEphemeralAccount(rhp.pt.FundAccountCost.Add(expectedBalance).Sub(currentBalance), false)
	if err != nil {
		t.Fatal(err)
	}

	// check the account balance.
	if !host.staticAccountManager.callAccountBalance(rhp.staticAccountID).Equals(expectedBalance) {
		t.Fatal("invalid balance", expectedBalance, host.staticAccountManager.callAccountBalance(rhp.staticAccountID))
	}

	// begin the subscription loop.
	initialBudget := expectedBalance.Div64(2)
	stream, err := rhp.BeginSubscription(initialBudget, sub)
	if err != nil {
		t.Fatal(err)
	}
	pt := rhp.managedPriceTable()

	// Sleep for the duration of the period plus a bit to avoid NDFs.
	time.Sleep(modules.SubscriptionPeriod + time.Millisecond*100)

	// Send a request. This should return an error.
	_, err = rhp.SubcribeToRV(stream, pt, spk, tweak)
	if err == nil || !strings.Contains(err.Error(), io.ErrClosedPipe.Error()) {
		t.Fatal(err)
	}

	// Check balance after error.
	l := stream.Limit()
	upCost := pt.UploadBandwidthCost.Mul64(l.Uploaded())
	downCost := pt.DownloadBandwidthCost.Mul64(l.Downloaded())
	cost := upCost.Add(downCost)

	currentBalance = host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	expectedBalance = expectedBalance.Sub(cost)
	if !currentBalance.Equals(expectedBalance) {
		t.Fatal("wrong balance after timeout", currentBalance, expectedBalance)
	}

	// Check total subscriptions.
	err = assertNumSubscriptions(host, 0)
	if err != nil {
		t.Fatal(err)
	}
}

// testRPCSubscribeExtendTimeout makes sure a session doesn't time out as long
// as it's being extended.
func testRPCSubscribeExtendTimeout(t *testing.T, rhp *renterHostPair) {
	var sub types.Specifier
	fastrand.Read(sub[:])

	// Register a listener for notifications.
	notificationReader, notificationWriter := io.Pipe()
	var notificationUploaded, notificationDownloaded uint64
	var notificationMu sync.Mutex
	err := rhp.staticRenterMux.NewListener(hex.EncodeToString(sub[:]), func(stream siamux.Stream) {
		notificationMu.Lock()
		defer notificationMu.Unlock()
		defer func() {
			if err := stream.Close(); err != nil {
				t.Error(err)
			}
		}()

		// Copy the output to the pipe.
		io.Copy(notificationWriter, stream)

		// Collect used bandwidth.
		notificationDownloaded += stream.Limit().Downloaded()
		notificationUploaded += stream.Limit().Uploaded()
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a registry value.
	rv, spk, _ := randomRegistryValue()
	tweak := rv.Tweak
	host := rhp.staticHT.host

	// fund the account.
	currentBalance := host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	expectedBalance := modules.DefaultHostExternalSettings().MaxEphemeralAccountBalance
	_, err = rhp.managedFundEphemeralAccount(rhp.pt.FundAccountCost.Add(expectedBalance).Sub(currentBalance), false)
	if err != nil {
		t.Fatal(err)
	}

	// check the account balance.
	if !host.staticAccountManager.callAccountBalance(rhp.staticAccountID).Equals(expectedBalance) {
		t.Fatal("invalid balance", expectedBalance, host.staticAccountManager.callAccountBalance(rhp.staticAccountID))
	}

	// begin the subscription loop.
	initialBudget := expectedBalance.Div64(2)
	stream, err := rhp.BeginSubscription(initialBudget, sub)
	if err != nil {
		t.Fatal(err)
	}
	pt := rhp.managedPriceTable()

	// Subscribe to a value.
	_, err = rhp.SubcribeToRV(stream, pt, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}

	// Sleep for half the period and extend it three times.
	n := 3
	for i := 0; i < n; i++ {
		time.Sleep(modules.SubscriptionPeriod / 2)
		err = modules.RPCExtendSubscription(stream, pt)
		if err != nil {
			t.Fatal(err)
		}
		err = readAndAssertOkResponse(notificationReader)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Send a request. This should still work even though we just slept 1.5
	// times the period because we extended it 3 times the period.
	_, err = rhp.SubcribeToRV(stream, pt, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}

	// Close connection to get the refund.
	if err := rhp.StopSubscription(stream); err != nil {
		t.Fatal(err)
	}

	// Check balance afterwards.
	l := stream.Limit()
	err = build.Retry(10, time.Second, func() error {
		upCost := pt.UploadBandwidthCost.Mul64(l.Uploaded() + notificationUploaded)
		downCost := pt.DownloadBandwidthCost.Mul64(l.Downloaded() + notificationDownloaded)
		bandwidthCost := upCost.Add(downCost)
		cost := bandwidthCost.Add(modules.MDMSubscribeCost(pt, 0, 1).Mul64(2))
		cost = cost.Add(modules.MDMSubscriptionMemoryCost(pt, 1).Mul64(uint64(n)))

		currentBalance = host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
		expected := expectedBalance.Sub(cost)
		if !currentBalance.Equals(expected) {
			return fmt.Errorf("wrong balance %v != %v", currentBalance, expected)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Check total subscriptions.
	err = assertNumSubscriptions(host, 0)
	if err != nil {
		t.Fatal(err)
	}
}

// testRPCSubscribeConcurrent tests that extending a subscription with an
// ongoing stream of incoming updates works and that both parties assume the
// same consumed bandwidth.
func testRPCSubscribeConcurrent(t *testing.T, rhp *renterHostPair) {
	var sub types.Specifier
	fastrand.Read(sub[:])

	// Register a listener for notifications.
	var notificationMu sync.Mutex
	numNotifications := 0
	var wg sync.WaitGroup
	var limits []mux.BandwidthLimit
	err := rhp.staticRenterMux.NewListener(hex.EncodeToString(sub[:]), func(stream siamux.Stream) {
		wg.Add(1)
		defer wg.Done()
		notificationMu.Lock()
		defer notificationMu.Unlock()
		defer func() {
			if err := stream.Close(); err != nil {
				t.Error(err)
			}
		}()

		// Read the notification type.
		var snt modules.RPCRegistrySubscriptionNotificationType
		err := modules.RPCRead(stream, &snt)
		if err != nil {
			t.Fatal(err)
		}

		// Handle the response.
		switch snt.Type {
		case modules.SubscriptionResponseSubscriptionSuccess:
		case modules.SubscriptionResponseRegistryValue:
			// Read the update.
			var sneu modules.RPCRegistrySubscriptionNotificationEntryUpdate
			err = modules.RPCRead(stream, &sneu)
			if err != nil {
				t.Fatal(err)
			}
			numNotifications++
		default:
			t.Fatal("invalid notification type", snt.Type)
		}

		// Read until the stream is closed by the peer.
		_, err = stream.Read(make([]byte, 1))
		if err == nil || !strings.Contains(err.Error(), io.ErrClosedPipe.Error()) {
			t.Fatal(err)
		}

		// Close the stream.
		if err = stream.Close(); err != nil {
			t.Fatal(err)
		}

		// Collect stats.
		limits = append(limits, stream.Limit())
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a registry value.
	rv, spk, sk := randomRegistryValue()
	tweak := rv.Tweak
	host := rhp.staticHT.host

	// Start a goroutine that continuously sets it to a new value on the host.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	cancelTicker := make(chan struct{})
	var updateWG sync.WaitGroup
	updateWG.Add(1)
	go func(rv modules.SignedRegistryValue) {
		defer updateWG.Done()
		for {
			select {
			case <-ticker.C:
			case <-cancelTicker:
				return
			}
			host := rhp.staticHT.host
			rv.Revision++
			rv = rv.Sign(sk)
			_, err := host.RegistryUpdate(rv, spk, math.MaxUint64)
			if err != nil {
				t.Error(err)
				return
			}
		}
	}(rv)

	// fund the account.
	currentBalance := host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	expectedBalance := modules.DefaultHostExternalSettings().MaxEphemeralAccountBalance
	_, err = rhp.managedFundEphemeralAccount(rhp.pt.FundAccountCost.Add(expectedBalance).Sub(currentBalance), false)
	if err != nil {
		t.Fatal(err)
	}

	// check the account balance.
	if !host.staticAccountManager.callAccountBalance(rhp.staticAccountID).Equals(expectedBalance) {
		t.Fatal("invalid balance", expectedBalance, host.staticAccountManager.callAccountBalance(rhp.staticAccountID))
	}

	// begin the subscription loop.
	initialBudget := expectedBalance.Div64(2)
	stream, err := rhp.BeginSubscription(initialBudget, sub)
	if err != nil {
		t.Fatal(err)
	}
	pt := rhp.managedPriceTable()

	// Subscribe to a value.
	_, err = rhp.SubcribeToRV(stream, pt, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}

	// Sleep for half the period and extend it three times.
	n := 3
	for i := 0; i < n; i++ {
		time.Sleep(modules.SubscriptionPeriod / 2)
		err = modules.RPCExtendSubscription(stream, pt)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Stop session.
	if err := rhp.StopSubscription(stream); err != nil {
		t.Fatal(err)
	}

	// When checking the prices we give the host a grace period after stopping
	// the subscription before closing the listener. That way we don't miss any
	// streams which were send at the same time as the subscription was stopped.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// Compute used bandwidth from limits.
		var nu, nd uint64
		notificationMu.Lock()
		for _, limit := range limits {
			nu += limit.Uploaded()
			nd += limit.Downloaded()
		}
		nn := numNotifications
		notificationMu.Unlock()

		// Check balance afterwards.
		l := stream.Limit()
		lu, ld := l.Uploaded(), l.Downloaded()
		upCost := pt.UploadBandwidthCost.Mul64(lu + nu)
		downCost := pt.DownloadBandwidthCost.Mul64(ld + nd)
		bandwidthCost := upCost.Add(downCost)
		cost := bandwidthCost.Add(modules.MDMSubscribeCost(pt, 1, 1))
		cost = cost.Add(modules.MDMSubscriptionMemoryCost(pt, 1).Mul64(uint64(n)))
		cost = cost.Add(pt.SubscriptionNotificationCost.Mul64(uint64(nn)))

		currentBalance = host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
		expected := expectedBalance.Sub(cost)
		if !currentBalance.Equals(expected) {
			return fmt.Errorf("wrong balance %v != %v", currentBalance, expected)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Stop the ticker and wait for the goroutine to finish.
	close(cancelTicker)
	updateWG.Wait()

	// Close listener.
	err = rhp.staticRenterMux.CloseListener(hex.EncodeToString(sub[:]))
	if err != nil {
		t.Fatal(err)
	}

	// Wait for last notification to finish.
	wg.Wait()

	// Check total subscriptions.
	err = assertNumSubscriptions(host, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Compute used bandwidth from limits. Nothing should have changed.
	var nu, nd uint64
	notificationMu.Lock()
	for _, limit := range limits {
		nu += limit.Uploaded()
		nd += limit.Downloaded()
	}
	nn := numNotifications
	notificationMu.Unlock()

	// Check balance afterwards.
	l := stream.Limit()
	lu, ld := l.Uploaded(), l.Downloaded()
	upCost := pt.UploadBandwidthCost.Mul64(lu + nu)
	downCost := pt.DownloadBandwidthCost.Mul64(ld + nd)
	bandwidthCost := upCost.Add(downCost)
	cost := bandwidthCost.Add(modules.MDMSubscribeCost(pt, 1, 1))
	cost = cost.Add(modules.MDMSubscriptionMemoryCost(pt, 1).Mul64(uint64(n)))
	cost = cost.Add(pt.SubscriptionNotificationCost.Mul64(uint64(nn)))

	currentBalance = host.staticAccountManager.callAccountBalance(rhp.staticAccountID)
	expected := expectedBalance.Sub(cost)
	if !currentBalance.Equals(expected) {
		t.Fatalf("wrong balance %v != %v", currentBalance, expected)
	}
}
