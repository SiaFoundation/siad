package renter

import (
	"encoding/hex"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/threadgroup"
)

// TODO: track spending in account

var (
	initialSubscriptionBudget = modules.DefaultMaxEphemeralAccountBalance.Div64(10)

	subscriptionExtensionWindow = modules.SubscriptionPeriod / 2

	priceTableRetryInterval = time.Second
)

type (
	subscriptionInfo struct {
		// subscriptions is the map of subscriptions that the worker is supposed
		// to subscribe to. The worker might not be subscribed to these values
		// at all times due to interruptions but it will try to resubscribe as
		// soon as possible.
		// The worker will also try to unsubscribe from all subsriptions that it
		// currently has which are not in this map.
		subscriptions map[modules.SubscriptionID]struct{}

		// activeSubscriptions are the subscriptions that the worker is
		// currently subscribed to. This map is ideally equal to subscriptions
		// but might temporarily diverge.
		activeSubscriptions map[modules.SubscriptionID]struct{}

		// utility fields
		mu sync.Mutex
	}
)

func priceTableValidFor(pt *workerPriceTable, duration time.Duration) bool {
	minExpiry := time.Now().Add(duration)
	return minExpiry.Before(pt.staticExpiryTime)
}

func (w *worker) managedHandleNotification(stream siamux.Stream) {
	panic("not implemented")
}

func (w *worker) threadedSubscriptionLoop() {
	if err := w.renter.tg.Add(); err != nil {
		return
	}
	defer w.renter.tg.Done()

	// No need to run loop if the host doesn't support it.
	if build.VersionCmp(w.staticCache().staticHostVersion, "1.5.5") < 0 {
		return
	}

	for {
		// Check for shutdown
		select {
		case <-w.killChan:
			return // shutdown
		default:
		}

		// Prepare a unique handler for the host to subscribe to.
		var subscriber types.Specifier
		fastrand.Read(subscriber[:])
		subscriberStr := hex.EncodeToString(subscriber[:])

		// If the worker is on a cooldown, block until it is over.
		if w.managedOnMaintenanceCooldown() {
			cooldownTime := w.callStatus().MaintenanceCoolDownTime
			w.renter.tg.Sleep(cooldownTime)
			continue // try again
		}

		// Get a valid price table.
		pt := w.managedPriceTableForSubscription(modules.SubscriptionPeriod)

		// Compute the initial deadline.
		deadline := time.Now().Add(modules.SubscriptionPeriod)

		// Set the initial budget.
		initialBudget := initialSubscriptionBudget
		budget := modules.NewBudget(initialBudget)

		// Track the withdrawal.
		w.staticAccount.managedTrackWithdrawal(initialBudget)

		// Begin the subscription session.
		stream, err := w.managedBeginSubscription(initialBudget, w.staticAccount.staticID, subscriber)
		if err != nil {
			// Mark withdrawal as failed.
			w.staticAccount.managedCommitWithdrawal(initialBudget, false)

			// Log error and wait for some time before trying again.
			w.renter.log.Printf("Worker %v: failed to begin subscription: %v", w.staticHostPubKeyStr, err)
			w.renter.tg.Sleep(time.Second * 10)
			continue
		}

		// Commit the withdrawal.
		w.staticAccount.managedCommitWithdrawal(initialBudget, true)

		// Register the handler. This can happen after beginning the subscription
		// since we are not expecting any notifications yet.
		err = w.renter.staticMux.NewListener(subscriberStr, w.managedHandleNotification)
		if err != nil {
			w.renter.log.Printf("Worker %v: failed to register host listener: %v", w.staticHostPubKeyStr, err)
			w.renter.tg.Sleep(time.Second * 10)
			continue
		}

		// Run the subscription. The error is checked after closing the handler
		// and the refund.
		errSubscription := w.managedSubscriptionLoop(stream, pt, deadline, budget)

		// Close the handler.
		err = w.renter.staticMux.CloseListener(subscriberStr)
		if err != nil {
			w.renter.log.Printf("Worker %v: failed to close host listener: %v", w.staticHostPubKeyStr, err)
		}

		// Deposit refund. This happens in any case.
		refund := budget.Remaining()
		w.staticAccount.managedTrackDeposit(refund)
		w.staticAccount.managedCommitDeposit(refund, true)

		// Check the error.
		if errors.Contains(errSubscription, threadgroup.ErrStopped) {
			return // shutdown
		}
		if err != nil {
			w.renter.log.Printf("Worker %v: subscription got interrupted: %v", w.staticHostPubKeyStr, errSubscription)
			w.renter.tg.Sleep(time.Second * 10)
		}
	}
}

func (w *worker) managedSubscriptionLoop(stream siamux.Stream, pt *modules.RPCPriceTable, deadline time.Time, budget *modules.RPCBudget) error {
	// Clear the active subsriptions at the end of this method.
	subInfo := w.staticSubscriptionInfo
	defer func() {
		subInfo.mu.Lock()
		subInfo.activeSubscriptions = make(map[modules.SubscriptionID]struct{})
		subInfo.mu.Unlock()
	}()

	for {
		panic("not implemented yet")
	}
}

func (w *worker) managedPriceTableForSubscription(duration time.Duration) *modules.RPCPriceTable {
	for {
		// Get most recent price table.
		pt := w.staticPriceTable()

		// If the price table is valid, return it.
		if priceTableValidFor(pt, duration) {
			return &pt.staticPriceTable
		}

		// NOTE: The price table is not valid for the subsription. This
		// theoretically should not happen a lot.
		// The reason why it shouldn't happen often is that a price table is
		// valid for rpcPriceGuaranteePeriod. That period is 10 minutes in
		// production and gets renewed every 5 minutes. So we should always have
		// a price table that is at least valid for another 5 minutes. The
		// SubscriptionPeriod also happens to be 5 minutes but we renew 2.5
		// minutes before it ends.

		// Trigger an update by setting the update time to now.
		pt.staticUpdateTime = time.Now()
		old := (*workerPriceTable)(atomic.SwapPointer(&w.atomicPriceTable, unsafe.Pointer(pt)))

		// The old table's UID should be the same. Otherwise we just swapped out
		// a new table and need to try again.
		if old.staticPriceTable.UID != pt.staticPriceTable.UID {
			w.staticSetPriceTable(old) // set back to the old one
			continue
		}

		// Wait a bit before checking again.
		select {
		case _ = <-w.renter.tg.StopChan():
			return nil // shutdown
		case <-time.After(priceTableRetryInterval):
		}
	}
}

// managedBeginSubscription begins a subscription on a new stream and returns
// it.
func (w *worker) managedBeginSubscription(initialBudget types.Currency, fundAcc modules.AccountID, subscriber types.Specifier) (_ siamux.Stream, err error) {
	stream, err := w.staticNewStream()
	if err != nil {
		return nil, errors.AddContext(err, "managedBeginSubscription: failed to create stream")
	}
	defer func() {
		if err != nil {
			err = errors.Compose(err, stream.Close())
		}
	}()

	// initiate the RPC
	err = modules.RPCWrite(stream, modules.RPCRegistrySubscription)
	if err != nil {
		return nil, err
	}

	// Write the pricetable uid.
	err = modules.RPCWrite(stream, w.staticPriceTable().staticPriceTable.UID)
	if err != nil {
		return nil, err
	}

	// Provide payment
	err = w.staticAccount.ProvidePayment(stream, w.staticHostPubKey, modules.RPCRegistrySubscription, initialBudget, w.staticAccount.staticID, w.staticCache().staticBlockHeight)
	if err != nil {
		return nil, err
	}

	// Send the subscriber.
	return stream, modules.RPCWrite(stream, subscriber)
}

// stopSubscription gracefully stops a subscription session.
func stopSubscription(stream siamux.Stream) error {
	err := modules.RPCWrite(stream, modules.SubscriptionRequestStop)
	if err != nil {
		return errors.AddContext(err, "StopSubscription failed to send specifier")
	}
	_, err = stream.Read(make([]byte, 1))
	if err == nil || !strings.Contains(err.Error(), io.ErrClosedPipe.Error()) {
		return errors.AddContext(err, "StopSubscription failed to wait for closed stream")
	}
	return stream.Close()
}

// subscribeToRV subscribes to the given publickey/tweak pair.
func subcribeToRV(stream siamux.Stream, pubkey types.SiaPublicKey, tweak crypto.Hash, pt *modules.RPCPriceTable) (*modules.SignedRegistryValue, error) {
	// Send the type of the request.
	err := modules.RPCWrite(stream, modules.SubscriptionRequestSubscribe)
	if err != nil {
		return nil, err
	}
	// Send the request.
	err = modules.RPCWrite(stream, []modules.RPCRegistrySubscriptionRequest{{
		PubKey: pubkey,
		Tweak:  tweak,
	}})
	if err != nil {
		return nil, err
	}
	// Read response.
	var rvs []modules.SignedRegistryValue
	err = modules.RPCRead(stream, &rvs)
	if err != nil {
		return nil, err
	}
	var rv *modules.SignedRegistryValue
	if len(rvs) > 1 {
		build.Critical("more responses than subscribed to values")
	} else if len(rvs) == 1 {
		rv = &rvs[0]
		err = rv.Verify(pubkey.ToPublicKey())
	}
	return rv, err
}

// unsubscribeFromRV unsubscribes from the given publickey/tweak pair.
func unsubcribeFromRV(stream siamux.Stream, pt *modules.RPCPriceTable, pubkey types.SiaPublicKey, tweak crypto.Hash) error {
	// Send the type of the request.
	err := modules.RPCWrite(stream, modules.SubscriptionRequestUnsubscribe)
	if err != nil {
		return err
	}
	// Send the request.
	err = modules.RPCWrite(stream, []modules.RPCRegistrySubscriptionRequest{{
		PubKey: pubkey,
		Tweak:  tweak,
	}})
	if err != nil {
		return err
	}
	return nil
}

// FundSubscription pays the host to increase the subscription budget.
func (w *worker) managedFundSubscription(stream siamux.Stream, fundAmt types.Currency) error {
	// Send the type of the request.
	err := modules.RPCWrite(stream, modules.SubscriptionRequestPrepay)
	if err != nil {
		return err
	}

	// Provide payment
	err = w.staticAccount.ProvidePayment(stream, w.staticHostPubKey, modules.RPCRegistrySubscription, fundAmt, w.staticAccount.staticID, w.staticCache().staticBlockHeight)
	if err != nil {
		return err
	}
	return nil
}

// extendSubscription extends the subscription with the given price table.
func extendSubscription(stream siamux.Stream, pt *modules.RPCPriceTable) error {
	// Send the type of the request.
	err := modules.RPCWrite(stream, modules.SubscriptionRequestExtend)
	if err != nil {
		return err
	}

	// Write the pricetable uid.
	err = modules.RPCWrite(stream, pt.UID)
	if err != nil {
		return err
	}
	return nil
}
