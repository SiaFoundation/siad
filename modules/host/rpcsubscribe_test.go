package host

import (
	"encoding/hex"
	"fmt"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
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

func assertNumSubscriptions(host *Host, n int) error {
	host.staticRegistrySubscriptions.mu.Lock()
	defer host.staticRegistrySubscriptions.mu.Unlock()
	if len(host.staticRegistrySubscriptions.subscriptions) != n {
		return fmt.Errorf("invalid number of subscriptions %v != %v", len(host.staticRegistrySubscriptions.subscriptions), n)
	}
	return nil
}

func assertSubscriptionInfos(host *Host, spk types.SiaPublicKey, tweak crypto.Hash, n int) ([]*subscriptionInfo, error) {
	sid := deriveSubscriptionID(spk, tweak)
	host.staticRegistrySubscriptions.mu.Lock()
	subInfos, found := host.staticRegistrySubscriptions.subscriptions[sid]
	if !found {
		host.staticRegistrySubscriptions.mu.Unlock()
		return nil, errors.New("subscription not found for id")
	}
	if len(subInfos) != 1 {
		host.staticRegistrySubscriptions.mu.Unlock()
		return nil, fmt.Errorf("wrong number of subscription infos %v != %v", len(subInfos), n)
	}
	var infos []*subscriptionInfo
	for _, info := range subInfos {
		infos = append(infos, info)
	}
	host.staticRegistrySubscriptions.mu.Unlock()
	return infos, nil
}

func readAndAssertRegistryValueNotification(rv modules.SignedRegistryValue, r io.Reader) error {
	var snt modules.RPCRegistrySubscriptionNotificationType
	err := modules.RPCRead(r, &snt)
	if err != nil {
		return (err)
	}
	if snt.Type != modules.SubscriptionResponseRegistryValue {
		return errors.New("notification has wrong type")
	}
	var sneu modules.RPCRegistrySubscriptionNotificationEntryUpdate
	err = modules.RPCRead(r, &sneu)
	if err != nil {
		return (err)
	}
	if !reflect.DeepEqual(rv, sneu.Entry) {
		return errors.New("wrong entry in notification")
	}
	return nil
}

func readAndAssertOkResponse(r io.Reader) error {
	var snt modules.RPCRegistrySubscriptionNotificationType
	err := modules.RPCRead(r, &snt)
	if err != nil {
		return (err)
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
		testRPCSubscribeBasic(t, rhp)
	})
}

// testRPCSubscribeBasic tests subscribing to an entry and unsubscribing without
// hitting any edge cases.
func testRPCSubscribeBasic(t *testing.T, rhp *renterHostPair) {
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

	// subsribe to the previously created entry.
	rvInitial, err := rhp.SubcribeToRV(stream, pt, spk, tweak)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rv, rvInitial) {
		t.Fatal("initial value doesn't match")
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
	err = readAndAssertRegistryValueNotification(rv, notificationReader)
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
	err = rhp.ExtendSubscription(stream, pt)
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
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}

	// Check the balance.
	// 1. subtract the initial budget.
	// 2. add the remaining budget.
	// 3. subtract fundAmt.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		expectedBalance := expectedBalance.Sub(initialBudget)
		expectedBalance = expectedBalance.Add(expectedBudget(runningCost))
		expectedBalance = expectedBalance.Sub(fundAmt)
		if !host.staticAccountManager.callAccountBalance(rhp.staticAccountID).Equals(expectedBalance) {
			return fmt.Errorf("invalid balance %v != %v", expectedBalance, host.staticAccountManager.callAccountBalance(rhp.staticAccountID))
		}
		return nil
	})
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
