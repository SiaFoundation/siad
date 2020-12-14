package host

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

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
		testRPCSubscribeBasic(t, rhp)
	})
}

// testRPCSubscribeBasic tests subscribing to an entry and unsubscribing without
// hitting any edge cases.
func testRPCSubscribeBasic(t *testing.T, rhp *renterHostPair) {
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
	expiry := types.BlockHeight(1000)
	rv := modules.NewRegistryValue(tweak, data, rev).Sign(sk)

	// Set it on the host.
	host := rhp.staticHT.host
	_, err := host.RegistryUpdate(rv, spk, expiry)
	if err != nil {
		t.Fatal(err)
	}

	// fund the account.
	_, err = rhp.managedFundEphemeralAccount(rhp.pt.FundAccountCost.Add(modules.DefaultHostExternalSettings().MaxEphemeralAccountBalance), false)
	if err != nil {
		t.Fatal(err)
	}

	// check the account balance.
	expectedBalance := modules.DefaultHostExternalSettings().MaxEphemeralAccountBalance
	if !host.staticAccountManager.callAccountBalance(rhp.staticAccountID).Equals(expectedBalance) {
		t.Fatal("invalid balance", expectedBalance, host.staticAccountManager.callAccountBalance(rhp.staticAccountID))
	}

	// begin the subscription loop.
	stream, err := rhp.BeginSubscription()
	if err != nil {
		t.Fatal(err)
	}

	// subsribe to the previously created entry.
	pt := rhp.managedPriceTable()
	rvInitial, err := rhp.SubcribeToRV(stream, pt, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rv, rvInitial) {
		t.Fatal("initial value doesn't match")
	}

	// Make sure that the host got the subscription.
	sid := deriveSubscriptionID(spk, tweak)
	err = build.Retry(100, 100*time.Millisecond, func() error {
		host.staticRegistrySubscriptions.mu.Lock()
		defer host.staticRegistrySubscriptions.mu.Unlock()

		if len(host.staticRegistrySubscriptions.subscriptions) != 1 {
			return fmt.Errorf("invalid number of subscriptions %v != %v", len(host.staticRegistrySubscriptions.subscriptions), 1)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	host.staticRegistrySubscriptions.mu.Lock()
	subInfos, found := host.staticRegistrySubscriptions.subscriptions[sid]
	if !found {
		host.staticRegistrySubscriptions.mu.Unlock()
		t.Fatal("subscription not found for id")
	}
	if len(subInfos) != 1 {
		host.staticRegistrySubscriptions.mu.Unlock()
		t.Fatal("wrong number of subscription infos", len(subInfos), 1)
	}
	var info *subscriptionInfo
	for _, subInfo := range subInfos {
		info = subInfo
		break
	}
	host.staticRegistrySubscriptions.mu.Unlock()

	// The info should have the right fields set.
	info.mu.Lock()
	if info.notificationsLeft != modules.InitialNumNotifications {
		t.Error("wrong number of notifications left", info.notificationsLeft)
	}
	if info.staticStream == nil {
		t.Error("stream not set")
	}
	info.mu.Unlock()

	// Update the entry on the host.
	rv.Revision++
	rv = rv.Sign(sk)
	_, err = host.RegistryUpdate(rv, spk, expiry)
	if err != nil {
		t.Fatal(err)
	}

	// Read the notification.
	var notification modules.RPCRegistrySubscriptionNotification
	err = modules.RPCRead(stream, &notification)
	if err != nil {
		t.Fatal(err)
	}

	// Make sure it's the right one.
	if notification.Type != modules.SubscriptionResponseRegistryValue {
		t.Fatal("notification has wrong type")
	}
	if !reflect.DeepEqual(rv, notification.Entry) {
		t.Fatal("wrong entry in notification")
	}

	// The info should now have fewer notifications left.
	info.mu.Lock()
	if info.notificationsLeft != modules.InitialNumNotifications-1 {
		t.Error("wrong number of notifications left", info.notificationsLeft)
	}
	info.mu.Unlock()

	// Unsubscribe.
	err = rhp.UnsubcribeFromRV(stream, pt, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(100, 100*time.Millisecond, func() error {
		host.staticRegistrySubscriptions.mu.Lock()
		defer host.staticRegistrySubscriptions.mu.Unlock()

		if len(host.staticRegistrySubscriptions.subscriptions) != 0 {
			return fmt.Errorf("invalid number of subscriptions %v != %v", len(host.staticRegistrySubscriptions.subscriptions), 0)
		}
		return nil
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

	// Shouldn't receive a notification.
	err = stream.SetReadDeadline(time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	err = modules.RPCRead(stream, &notification)
	if err == nil || !strings.Contains(err.Error(), "stream timed out") {
		t.Fatal(err)
	}

	// Close the subscription.
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}

	// Helper to calculate the cost of n notifications.
	nCost := func(numNotifications uint64) types.Currency {
		return (pt.SubscriptionNotificationBaseCost.Add(modules.MDMReadRegistryCost(pt)).Mul64(numNotifications))
	}

	// Check the balance.
	// 1. subtract the base cost and 100 notifications for opening the loop
	// 2. subtract the base cost and memory cost for 1 subscription
	// 3. subtract the bast cost for unsubscribing 1 time
	// 4. add the InitialNumNotifications-1 unused notifications as a refund
	err = build.Retry(100, 100*time.Millisecond, func() error {
		expectedBalance := expectedBalance.Sub(pt.SubscriptionBaseCost.Add(nCost(modules.InitialNumNotifications)))
		expectedBalance = expectedBalance.Sub(pt.SubscriptionBaseCost.Add(subscriptionMemoryCost(pt, 1)).Add(modules.SubscriptionNotificationsCost(pt, 1)))
		expectedBalance = expectedBalance.Sub(pt.SubscriptionBaseCost)
		expectedBalance = expectedBalance.Add(nCost(modules.InitialNumNotifications - 1))
		if !host.staticAccountManager.callAccountBalance(rhp.staticAccountID).Equals(expectedBalance) {
			return fmt.Errorf("invalid balance %v != %v", expectedBalance, host.staticAccountManager.callAccountBalance(rhp.staticAccountID))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
