package renter

import (
	"context"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/Sia/build"
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

	subscriptionLoopInterval = time.Second
)

type (
	subscriptionInfos struct {
		// subscriptions is the map of subscriptions that the worker is supposed
		// to subscribe to. The worker might not be subscribed to these values
		// at all times due to interruptions but it will try to resubscribe as
		// soon as possible.
		// The worker will also try to unsubscribe from all subsriptions that it
		// currently has which it is not supposed to be subscribed to.
		subscriptions map[modules.SubscriptionID]*subscription

		staticWakeChan chan struct{}

		// utility fields
		mu sync.Mutex
	}

	subscription struct {
		staticRequest *modules.RPCRegistrySubscriptionRequest

		active    bool
		subscribe bool

		subscribed chan struct{}
		latestRV   *modules.SignedRegistryValue
	}
)

// managedHandleNotification handles incoming notifications from the host. It
// verifies notifications and updates the worker's internal state accordingly.
func (w *worker) managedHandleNotification(stream siamux.Stream) {
	// Close the stream when done.
	defer func() {
		if err := stream.Close(); err != nil {
			w.renter.log.Print("managedHandleNotification: failed to close stream: ", err)
		}
	}()
	subInfo := w.staticSubscriptionInfo

	// Read the notification type.
	var snt modules.RPCRegistrySubscriptionNotificationType
	err := modules.RPCRead(stream, &snt)
	if err != nil {
		w.renter.log.Print("managedHandleNotification: failed to read notification type: ", err)
		return
	}

	// Handle the notification.
	switch snt.Type {
	case modules.SubscriptionResponseSubscriptionSuccess:
		// TODO: same race as mentioned in the other comment. Once we receive
		// the this notification, we know that future notifications will
		// potentially use a different pricetable.
	default:
		w.renter.log.Print("managedHandleNotification: unknown notification type")
		return
	case modules.SubscriptionResponseRegistryValue:
	}

	// Read the update.
	var sneu modules.RPCRegistrySubscriptionNotificationEntryUpdate
	err = modules.RPCRead(stream, &sneu)
	if err != nil {
		w.renter.log.Print("managedHandleNotification: failed to read entry update: ", err)
		return
	}

	// Verify the update. Start with the checks that don't require locking the
	// subsription.

	// Check if the host was trying to cheat us with an outdated entry.
	latestRev, exists := w.staticRegistryCache.Get(sneu.PubKey, sneu.Entry.Tweak)
	if exists && sneu.Entry.Revision <= latestRev {
		// TODO: (f/u) Punish the host by adding a subscription cooldown and
		// closing the subscription session for a while.
	}

	// Verify the signature.
	err = sneu.Entry.Verify(sneu.PubKey.ToPublicKey())
	if err != nil {
		// TODO: (f/u) Punish the host by adding a subscription cooldown and
		// closing the subscription session for a while.
	}

	// The entry is valid. Update the cache.
	w.staticRegistryCache.Set(sneu.PubKey, sneu.Entry, false)

	// Check if the host sent us an update we are not subsribed to. This might
	// not seem bad, but the host might want to spam us with valid entries that
	// we are not interested in simply to have us pay for bandwidth.
	subInfo.mu.Lock()
	defer subInfo.mu.Unlock()
	sub, exists := subInfo.subscriptions[modules.RegistrySubscriptionID(sneu.PubKey, sneu.Entry.Tweak)]
	if !exists || (!sub.subscribe && !sub.active) {
		// TODO: (f/u) Punish the host by adding a subscription cooldown and
		// closing the subscription session for a while.
	}

	// Update the subscription.
	sub.latestRV = &sneu.Entry
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

	// Convenience var.
	subInfo := w.staticSubscriptionInfo

	for {
		// Check for shutdown
		select {
		case <-w.tg.StopChan():
			return // shutdown
		default:
		}

		// Nothing to do if there are no subscriptions.
		subInfo.mu.Lock()
		nSubs := len(subInfo.subscriptions)
		subInfo.mu.Unlock()
		if nSubs == 0 {
			select {
			case <-subInfo.staticWakeChan:
				// Wait for work
			case <-w.tg.StopChan():
				return // shutdown
			}
		}

		// Prepare a unique handler for the host to subscribe to.
		var subscriber types.Specifier
		fastrand.Read(subscriber[:])
		subscriberStr := hex.EncodeToString(subscriber[:])

		// If the worker is on a cooldown, block until it is over.
		if w.managedOnMaintenanceCooldown() {
			cooldownTime := w.callStatus().MaintenanceCoolDownTime
			w.tg.Sleep(cooldownTime)
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
			w.tg.Sleep(time.Second * 10)
			continue
		}

		// Commit the withdrawal.
		w.staticAccount.managedCommitWithdrawal(initialBudget, true)

		// Register the handler. This can happen after beginning the subscription
		// since we are not expecting any notifications yet.
		err = w.renter.staticMux.NewListener(subscriberStr, w.managedHandleNotification)
		if err != nil {
			w.renter.log.Printf("Worker %v: failed to register host listener: %v", w.staticHostPubKeyStr, err)
			w.tg.Sleep(time.Second * 10)
			continue
		}

		// Run the subscription. The error is checked after closing the handler
		// and the refund.
		errSubscription := w.managedSubscriptionLoop(stream, pt, deadline, budget, initialBudget)

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
			w.tg.Sleep(time.Second * 10)
		}
	}
}

// managedSubscriptionLoop handles an existing subscription session. It will add
// subscriptions, remove subscriptions, fund the subscription and extend it
// indefinitely.
func (w *worker) managedSubscriptionLoop(stream siamux.Stream, pt *modules.RPCPriceTable, deadline time.Time, budget *modules.RPCBudget, expectedBudget types.Currency) (err error) {
	// Register some cleanup.
	subInfo := w.staticSubscriptionInfo
	defer func() {
		// Close the stream gracefully.
		err = errors.Compose(err, modules.RPCStopSubscription(stream))

		// Clear the active subsriptions at the end of this method.
		subInfo.mu.Lock()
		for _, sub := range subInfo.subscriptions {
			sub.subscribed = make(chan struct{})
			sub.active = false
		}
		subInfo.mu.Unlock()
	}()

	// Set the stream deadline to the subscription deadline.
	err = stream.SetDeadline(deadline)
	if err != nil {
		return errors.AddContext(err, "failed to set stream deadlien to subscription deadline")
	}

	for {
		// If the budget is half empty, fund it.
		if budget.Remaining().Cmp(expectedBudget.Div64(2)) < 0 {
			fundAmt := expectedBudget.Sub(budget.Remaining())

			// Track the withdrawal.
			w.staticAccount.managedTrackWithdrawal(fundAmt)

			// Fund the subscription.
			err = w.managedFundSubscription(stream, fundAmt)
			if err != nil {
				w.staticAccount.managedCommitWithdrawal(fundAmt, false)
				return errors.AddContext(err, "failed to fund subscription")
			}

			// Success. Add the funds to the budget and signal to the account
			// that the withdrawal was successful.
			budget.Deposit(fundAmt)
			w.staticAccount.managedCommitWithdrawal(fundAmt, true)
		}

		// If the subscription period is halfway over, extend it.
		if time.Until(deadline) < modules.SubscriptionPeriod/2 {
			// Get a pricetable that is valid until the new deadline.
			deadline = deadline.Add(modules.SubscriptionPeriod)
			pt = w.managedPriceTableForSubscription(time.Until(deadline))

			// Try extending the subscription.
			// TODO: (req) there is a race here with incoming notifications.
			// Need some special locking.
			err = modules.RPCExtendSubscription(stream, pt)
			if err != nil {
				return errors.AddContext(err, "failed to extend subscription")
			}

			// Set the stream deadline to the new subscription deadline.
			err = stream.SetDeadline(deadline)
			if err != nil {
				return errors.AddContext(err, "failed to set stream deadlien to subscription deadline")
			}
		}

		// Create a diff between the active subscriptions and the desired
		// ones.
		subInfo.mu.Lock()
		var toUnsubscribe []modules.RPCRegistrySubscriptionRequest
		var toSubscribe []modules.RPCRegistrySubscriptionRequest
		var subChans []chan struct{}
		for sid, sub := range subInfo.subscriptions {
			if !sub.subscribe && !sub.active {
				// Delete the subscription. We are neither supposed to subscribe
				// to it nor are we subscribed to it.
				delete(subInfo.subscriptions, sid)
				// Also close the channel if necessary since there is a chance
				// that this sub was never active before and therefore the
				// channel could still be open.
				select {
				case <-sub.subscribed:
				default:
					close(sub.subscribed)
				}
			} else if sub.active && !sub.subscribe {
				// Delete the subscription and remember to actually unsubscribe
				// later.
				toUnsubscribe = append(toUnsubscribe, *sub.staticRequest)
				delete(subInfo.subscriptions, sid)
			} else if !sub.active && sub.subscribe {
				// Mark the subscription as active and remember to subscribe
				// later. Also remember to close the channel once subscribed.
				toSubscribe = append(toSubscribe, *sub.staticRequest)
				subChans = append(subChans, sub.subscribed)
				sub.active = true
			}
		}
		subInfo.mu.Unlock()

		// Subscribe/Unsubscribe appropriately. We do so within an inline
		// function to be able to close all channels we need to close.
		err = func() error {
			defer func() {
				for _, c := range subChans {
					close(c)
				}
			}()

			// Unsubscribe from unnecessary subscriptions.
			if len(toUnsubscribe) > 0 {
				err = modules.RPCUnsubscribeFromRVs(stream, toUnsubscribe)
				if err != nil {
					return errors.AddContext(err, "failed to unsubscribe from registry values")
				}
			}

			// Subscribe to any missing values.
			if len(toSubscribe) > 0 {
				rvs, err := modules.RPCSubscribeToRVs(stream, toSubscribe)
				if err != nil {
					return errors.AddContext(err, "failed to subscribe to registry values")
				}
				// Update the subscriptions with the received values.
				subInfo.mu.Lock()
				for _, rv := range rvs {
					subInfo.subscriptions[modules.RegistrySubscriptionID(rv.PubKey, rv.Entry.Tweak)].latestRV = &rv.Entry
				}
				subInfo.mu.Unlock()
			}
			return nil
		}()
		if err != nil {
			return err
		}

		// Wait until some time passed or until there is new work.
		t := time.NewTimer(subscriptionLoopInterval)
		select {
		case <-w.tg.StopChan():
			return threadgroup.ErrStopped // shutdown
		case <-t.C:
			// continue right away since the timer is drained.
			continue
		case <-subInfo.staticWakeChan:
		}

		// We didn't receive from the timer's channel. Stop it and drain it.
		if !t.Stop() {
			<-t.C
		}
	}
}

// managedPriceTableForSubscription will fetch a price table that is valid for
// the provided duration. If the current price table of the worker isn't valid
// for that long, it will change its update time to trigger an update.
func (w *worker) managedPriceTableForSubscription(duration time.Duration) *modules.RPCPriceTable {
	for {
		// Get most recent price table.
		pt := w.staticPriceTable()

		// If the price table is valid, return it.
		if pt.staticValidFor(duration) {
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
		newPT := *pt
		newPT.staticUpdateTime = time.Time{}
		oldPT := (*workerPriceTable)(atomic.SwapPointer(&w.atomicPriceTable, unsafe.Pointer(&newPT)))

		// The old table's UID should be the same. Otherwise we just swapped out
		// a new table and need to try again.
		if oldPT.staticPriceTable.UID != pt.staticPriceTable.UID {
			w.staticSetPriceTable(oldPT) // set back to the old one
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
	return modules.RPCBeginSubscription(stream, w.staticAccount, w.staticHostPubKey, &w.staticPriceTable().staticPriceTable, initialBudget, w.staticAccount.staticID, w.staticCache().staticBlockHeight, subscriber)
}

// FundSubscription pays the host to increase the subscription budget.
func (w *worker) managedFundSubscription(stream siamux.Stream, fundAmt types.Currency) error {
	return modules.RPCFundSubscription(stream, w.staticHostPubKey, w.staticAccount, w.staticAccount.staticID, w.staticCache().staticBlockHeight, fundAmt)
}

// Unsubscribe marks the provided entries as not subscribed to and notifies the worker of the change. It will then handle
func (w *worker) Unsubscribe(requests ...modules.RPCRegistrySubscriptionRequest) {
	subInfo := w.staticSubscriptionInfo

	subInfo.mu.Lock()
	defer subInfo.mu.Unlock()
	for _, req := range requests {
		sid := modules.RegistrySubscriptionID(req.PubKey, req.Tweak)
		sub, exists := subInfo.subscriptions[sid]
		if !exists || !sub.subscribe {
			continue // nothing to do
		}
		// Mark the sub as no longer subscribed.
		sub.subscribe = false
	}

	// Notify the subscription loop of the changes.
	select {
	case subInfo.staticWakeChan <- struct{}{}:
	default:
	}
}

func (w *worker) Subscribe(ctx context.Context, requests ...modules.RPCRegistrySubscriptionRequest) ([]modules.RPCRegistrySubscriptionNotificationEntryUpdate, error) {
	subInfo := w.staticSubscriptionInfo

	// Add one subscription for every request that we are not yet subscribed to.
	subInfo.mu.Lock()
	var subs []*subscription
	var subChans []chan struct{}
	for i, req := range requests {
		sid := modules.RegistrySubscriptionID(req.PubKey, req.Tweak)
		sub, exists := subInfo.subscriptions[sid]
		if !exists {
			sub = &subscription{
				staticRequest: &requests[i],
				subscribed:    make(chan struct{}),
				subscribe:     true,
			}
			subInfo.subscriptions[sid] = sub
		}
		subs = append(subs, sub)
		subChans = append(subChans, sub.subscribed)
	}
	subInfo.mu.Unlock()

	// Notify the subscription loop of the changes.
	select {
	case subInfo.staticWakeChan <- struct{}{}:
	default:
	}

	// Wait for all subscriptions to complete.
	for _, c := range subChans {
		select {
		case <-c:
		case <-w.tg.StopChan():
			return nil, threadgroup.ErrStopped // shutdown
		case <-ctx.Done():
			return nil, errors.New("subscription timed out")
		}
	}

	// Collect the values.
	var notifications []modules.RPCRegistrySubscriptionNotificationEntryUpdate
	for _, sub := range subs {
		if sub.latestRV == nil {
			// The value was subscribed to, but it doesn't exist on the host
			// yet.
			continue
		}
		notifications = append(notifications, modules.RPCRegistrySubscriptionNotificationEntryUpdate{
			Entry:  *sub.latestRV,
			PubKey: sub.staticRequest.PubKey,
		})
	}
	return notifications, nil
}
