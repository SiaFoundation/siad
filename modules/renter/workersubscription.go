package renter

import (
	"context"
	"encoding/hex"
	"fmt"
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

// TODO: testing for account deposits and withdrawals
// TODO: testing for updating price table

var (
	// initialSubscriptionBudget is the initial budget withdrawn for a
	// subscription. After using up 50% of it, the worker refills the budget
	// again to match the initial budget.
	initialSubscriptionBudget = modules.DefaultMaxEphemeralAccountBalance.Div64(10) // 10% of the max

	// subscriptionExtensionWindow is the time before the subscription period
	// ends when the workers starts trying to extend the subscription.
	subscriptionExtensionWindow = modules.SubscriptionPeriod / 2 // 50% of period

	// priceTableRetryInterval is the interval the subscription loop waits for
	// the maintenance to update the price table before checking again.
	priceTableRetryInterval = time.Second

	// subscriptionLoopInterval is the interval after which the subscription
	// loop checks for work when it's idle. Idle means the staticWakeChan isn't
	// signaling new work.
	subscriptionLoopInterval = time.Second

	// subscriptionLoopCooldown is the time the loop will sleep after
	// encountering an error.
	// TODO: f/u more sophisticate cooldown
	subscriptionLoopCooldown = 10 * time.Second

	// stopSubscriptionGracePeriod is the period of time we wait after signaling
	// the host that we want to stop the subscription. All the incoming
	// bandwidth within this period will be accounted for correctly and help us
	// to keep our EA balance expectations in sync with the host's.
	stopSubscriptionGracePeriod = build.Select(build.Var{
		Testing:  time.Second,
		Dev:      3 * time.Second,
		Standard: 3 * time.Second,
	}).(time.Duration)
)

type (
	// subscriptionInfos contains all of the registry subscription related
	// information of a worker.
	subscriptionInfos struct {
		// subscriptions is the map of subscriptions that the worker is supposed
		// to subscribe to. The worker might not be subscribed to these values
		// at all times due to interruptions but it will try to resubscribe as
		// soon as possible.
		// The worker will also try to unsubscribe from all subsriptions that it
		// currently has which it is not supposed to be subscribed to.
		subscriptions map[modules.SubscriptionID]*subscription

		// staticWakeChan is a channel to tell the subscription loop that more
		// work is available.
		staticWakeChan chan struct{}

		// stats
		atomicExtensions uint64

		// utility fields
		mu sync.Mutex
	}

	// subscription is a struct that provides additional information around a
	// subscribed to registry entry.
	subscription struct {
		staticRequest *modules.RPCRegistrySubscriptionRequest

		// subscribe indicates whether the subscription should be kept active.
		// If it is 'true', the worker will try to resubscribe if the session is
		// interrupted.
		subscribe bool

		// subscribed is closed as soon as the corresponding entry is subscribed
		// to and indicates that the worker is actively listening for updates.
		// It's also closed when a subscription is deleted from the map due to
		// no longer being necessary.
		subscribed chan struct{}

		// latestRV is kept up-to-date with the latest known value for a
		// subscribed entry and should only be checked if 'subscribed' is
		// closed. It may be 'nil' even though a subscription is active in case
		// the host doesn't know the subscribed entry. If the host does know,
		// the initial value should be set before closing 'subscribed'.
		latestRV *modules.SignedRegistryValue
	}

	// notificationHandler is a helper type that contains some information
	// relevant to notification pricing and updating price tables.
	notificationHandler struct {
		staticWorker       *worker
		staticPTUpdateChan chan modules.RPCPriceTable

		ptUpdateDone     chan struct{}
		notificationCost types.Currency
		mu               sync.Mutex
	}
)

// active returns 'true' if the subscription is currently active. That means the
// subscribed channel was closed after a successful subscription request.
func (sub *subscription) active() bool {
	select {
	case <-sub.subscribed:
		return true
	default:
	}
	return false
}

// managedHandleNotification handles incoming notifications from the host. It
// verifies notifications and updates the worker's internal state accordingly.
func (nh *notificationHandler) managedHandleNotification(stream siamux.Stream, budget *modules.RPCBudget, limit *modules.BudgetLimit) {
	w := nh.staticWorker
	// Close the stream when done.
	defer func() {
		if err := stream.Close(); err != nil {
			w.renter.log.Print("managedHandleNotification: failed to close stream: ", err)
		}
	}()
	subInfo := w.staticSubscriptionInfo

	// The stream should have a sane deadline.
	err := stream.SetDeadline(time.Now().Add(defaultNewStreamTimeout))
	if err != nil {
		w.renter.log.Print("managedHandleNotification: failed to set deadlien on stream: ", err)
		return
	}

	// Read the notification type.
	var snt modules.RPCRegistrySubscriptionNotificationType
	err = modules.RPCRead(stream, &snt)
	if err != nil {
		w.renter.log.Print("managedHandleNotification: failed to read notification type: ", err)
		return
	}

	// Handle the notification.
	switch snt.Type {
	case modules.SubscriptionResponseSubscriptionSuccess:
		// This indicates, that the subscription was extended. We expect a new
		// price table and have to update the notification cost.
		nh.mu.Lock()
		defer nh.mu.Unlock()

		// Update the ptUpdateDone channel after closing the old one.
		defer func() {
			close(nh.ptUpdateDone)
			nh.ptUpdateDone = make(chan struct{})
		}()

		var pt modules.RPCPriceTable
		select {
		case pt = <-nh.staticPTUpdateChan:
		default:
			if build.Release == "testing" {
				build.Critical("no pt on pt update chan")
			}
			w.renter.log.Print("managedHandleNotification: extension 'ok' was received but no new price table is available")
			return
		}
		// Update limit and notification cost.
		limit.UpdateCosts(pt.DownloadBandwidthCost, pt.UploadBandwidthCost)
		nh.notificationCost = pt.SubscriptionNotificationCost
		// Since this stream uses the new costs we set the limit last.
		err = stream.SetLimit(limit)
		if err != nil {
			w.renter.log.Print("managedHandleNotification: extension 'ok' was received but failed to update limit on stream")
			return
		}
		return
	default:
		w.renter.log.Print("managedHandleNotification: unknown notification type")
		return
	case modules.SubscriptionResponseRegistryValue:
	}

	// Add a limit to the stream.
	err = stream.SetLimit(limit)
	if err != nil {
		w.renter.log.Print("managedHandleNotification: failed to set limit on notification stream: ", err)
		return
	}

	// Withdraw notification cost.
	nh.mu.Lock()
	ok := budget.Withdraw(nh.notificationCost)
	nh.mu.Unlock()
	if !ok {
		w.renter.log.Print("managedHandleNotification: failed to withdraw notification cost")
		return
	}

	// Read the update.
	var sneu modules.RPCRegistrySubscriptionNotificationEntryUpdate
	err = modules.RPCRead(stream, &sneu)
	if err != nil {
		w.renter.log.Print("managedHandleNotification: failed to read entry update: ", err)
		return
	}

	// Check if the host was trying to cheat us with an outdated entry.
	latestRev, exists := w.staticRegistryCache.Get(sneu.PubKey, sneu.Entry.Tweak)
	if exists && sneu.Entry.Revision < latestRev {
		// TODO: (f/u) Punish the host by adding a subscription cooldown and
		// closing the subscription session for a while.
		w.renter.log.Printf("managedHandleNotification: %v < %v", sneu.Entry.Revision, latestRev)
		return
	}

	// Verify the signature.
	err = sneu.Entry.Verify(sneu.PubKey.ToPublicKey())
	if err != nil {
		// TODO: (f/u) Punish the host by adding a subscription cooldown and
		// closing the subscription session for a while.
		w.renter.log.Printf("managedHandleNotification: failed to verify signature: %v", err)
		return
	}

	// The entry is valid. Update the cache.
	w.staticRegistryCache.Set(sneu.PubKey, sneu.Entry, false)

	// Check if the host sent us an update we are not subsribed to. This might
	// not seem bad, but the host might want to spam us with valid entries that
	// we are not interested in simply to have us pay for bandwidth.
	subInfo.mu.Lock()
	defer subInfo.mu.Unlock()
	sub, exists := subInfo.subscriptions[modules.RegistrySubscriptionID(sneu.PubKey, sneu.Entry.Tweak)]
	if !exists || (sub.latestRV != nil && sub.latestRV.Revision >= sneu.Entry.Revision) {
		// TODO: (f/u) Punish the host by adding a subscription cooldown and
		// closing the subscription session for a while.
		w.renter.log.Printf("managedHandleNotification: %v >= %v", sub.latestRV.Revision, sneu.Entry.Revision)
		return
	}

	// Update the subscription.
	sub.latestRV = &sneu.Entry
}

// threadedSubscriptionLoop is the main subscription loop. It opens a
// subscription with the host and then calls managedSubscriptionLoop to keep the
// subscription alive. If the subscription dies, threadedSubscriptionLoop will
// start it again.
func (w *worker) threadedSubscriptionLoop() {
	if err := w.staticTG.Add(); err != nil {
		return
	}
	defer w.staticTG.Done()

	// No need to run loop if the host doesn't support it.
	if build.VersionCmp(w.staticCache().staticHostVersion, "1.5.5") < 0 {
		return
	}

	// Disable loop if necessary.
	if w.renter.deps.Disrupt("DisableSubscriptionLoop") {
		return
	}

	// Convenience var.
	subInfo := w.staticSubscriptionInfo

	for {
		// Check for shutdown
		select {
		case <-w.staticTG.StopChan():
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
			case <-w.staticTG.StopChan():
				return // shutdown
			}
		}

		// If the worker is on a cooldown, block until it is over before trying
		// to establish a new subscriptoin session.
		if w.managedOnMaintenanceCooldown() {
			cooldownTime := w.callStatus().MaintenanceCoolDownTime
			w.staticTG.Sleep(cooldownTime)
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

		// Prepare a unique handler for the host to subscribe to.
		var subscriber types.Specifier
		fastrand.Read(subscriber[:])
		subscriberStr := hex.EncodeToString(subscriber[:])

		// Begin the subscription session.
		stream, err := w.managedBeginSubscription(initialBudget, w.staticAccount.staticID, subscriber)
		if err != nil {
			// Mark withdrawal as failed.
			w.staticAccount.managedCommitWithdrawal(initialBudget, false)

			// Log error and wait for some time before trying again.
			w.renter.log.Printf("Worker %v: failed to begin subscription: %v", w.staticHostPubKeyStr, err)
			w.staticTG.Sleep(subscriptionLoopCooldown)
			continue
		}

		// Commit the withdrawal.
		w.staticAccount.managedCommitWithdrawal(initialBudget, true)

		// Run the subscription. The error is checked after closing the handler
		// and the refund.
		errSubscription := w.managedSubscriptionLoop(stream, pt, deadline, budget, initialBudget, subscriberStr)

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
			w.staticTG.Sleep(subscriptionLoopCooldown)
			continue
		}
	}
}

// managedSubscriptionLoop handles an existing subscription session. It will add
// subscriptions, remove subscriptions, fund the subscription and extend it
// indefinitely.
func (w *worker) managedSubscriptionLoop(stream siamux.Stream, pt *modules.RPCPriceTable, deadline time.Time, budget *modules.RPCBudget, expectedBudget types.Currency, subscriber string) (err error) {
	// Set the bandwidth limiter on the stream.
	limit := modules.NewBudgetLimit(budget, pt.DownloadBandwidthCost, pt.UploadBandwidthCost)
	err = stream.SetLimit(limit)
	if err != nil {
		return errors.AddContext(err, "failed to set bandwidth limiter on the stream")
	}

	// Register the handler. This can happen after beginning the subscription
	// since we are not expecting any notifications yet.
	nh := &notificationHandler{
		staticWorker:       w,
		staticPTUpdateChan: make(chan modules.RPCPriceTable, 1),
		ptUpdateDone:       make(chan struct{}),
		notificationCost:   pt.SubscriptionNotificationCost,
	}
	err = w.renter.staticMux.NewListener(subscriber, func(stream siamux.Stream) {
		nh.managedHandleNotification(stream, budget, limit)
	})
	if err != nil {
		return errors.AddContext(err, "failed to register listener")
	}

	// Register some cleanup.
	subInfo := w.staticSubscriptionInfo
	defer func() {
		// Close the stream gracefully.
		err = errors.Compose(err, modules.RPCStopSubscription(stream))

		// After signalling to shut down the subscription, we wait for a short
		// grace period to allow for incoming streams which were already read by
		// the siamux but did not have the handler called upon them yet. This
		// makes sure that our bandwidth expectations don't drift apart from the
		// host's. We want to always wait for this even upon shutdown to make
		// sure we refund our account correctly.
		time.Sleep(stopSubscriptionGracePeriod)

		// Close the handler.
		err = errors.Compose(err, w.renter.staticMux.CloseListener(subscriber))

		// Clear the active subsriptions at the end of this method.
		subInfo.mu.Lock()
		for _, sub := range subInfo.subscriptions {
			// Replace closed channels.
			select {
			case <-sub.subscribed:
				sub.subscribed = make(chan struct{})
			default:
			}
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

			// Tell the notification handler about the new pt. The channel is
			// buffered so this doesn't block. Also fetch the handler't
			// notification channel.
			nh.mu.Lock()
			updateDone := nh.ptUpdateDone
			nh.mu.Unlock()
			nh.staticPTUpdateChan <- *pt

			// Try extending the subscription.
			err = modules.RPCExtendSubscription(stream, pt)
			if err != nil {
				return errors.AddContext(err, "failed to extend subscription")
			}

			// Wait for "ok".
			select {
			case <-w.staticTG.StopChan():
				return threadgroup.ErrStopped
			case <-time.After(time.Until(deadline)):
				return errors.New("never received the 'ok' response for extending the subscription")
			case <-updateDone:
			}

			// Count the number of active subscriptions.
			var nSubs uint64
			for _, sub := range subInfo.subscriptions {
				if sub.active() {
					nSubs++
				}
			}
			// Withdraw from budget.
			if !budget.Withdraw(modules.MDMSubscriptionMemoryCost(pt, nSubs)) {
				return errors.New("failed to withdraw subscription extension cost from budget")
			}
			// Set the stream deadline to the new subscription deadline.
			err = stream.SetDeadline(deadline)
			if err != nil {
				return errors.AddContext(err, "failed to set stream deadlien to subscription deadline")
			}
			// Increment stats for extending the subscription.
			atomic.AddUint64(&subInfo.atomicExtensions, 1)
		}

		// Create a diff between the active subscriptions and the desired
		// ones.
		subInfo.mu.Lock()
		var toUnsubscribe []modules.RPCRegistrySubscriptionRequest
		var toSubscribe []modules.RPCRegistrySubscriptionRequest
		var subChans []chan struct{}
		for sid, sub := range subInfo.subscriptions {
			if !sub.subscribe && !sub.active() {
				// Delete the subscription. We are neither supposed to subscribe
				// to it nor are we subscribed to it.
				delete(subInfo.subscriptions, sid)
				// Close its channel.
				close(sub.subscribed)
			} else if sub.active() && !sub.subscribe {
				// Unsubscribe from the entry.
				toUnsubscribe = append(toUnsubscribe, *sub.staticRequest)
			} else if !sub.active() && sub.subscribe {
				// Subscribe and remember the channel to close it later.
				toSubscribe = append(toSubscribe, *sub.staticRequest)
				subChans = append(subChans, sub.subscribed)
			}
		}
		subInfo.mu.Unlock()

		// Unsubscribe from unnecessary subscriptions.
		if len(toUnsubscribe) > 0 {
			err = modules.RPCUnsubscribeFromRVs(stream, toUnsubscribe)
			if err != nil {
				return errors.AddContext(err, "failed to unsubscribe from registry values")
			}
			// Reset the subscription's channel to signal that it's no longer
			// active.
			subInfo.mu.Lock()
			for _, req := range toUnsubscribe {
				sid := modules.RegistrySubscriptionID(req.PubKey, req.Tweak)
				sub, exists := subInfo.subscriptions[sid]
				if !exists {
					build.Critical("managedSubscriptionLoop: missing subscription - subscriptions should only be deleted in this thread so this shouldn't be the case")
				}
				sub.subscribed = make(chan struct{})
			}
			subInfo.mu.Unlock()
		}

		// Subscribe to any missing values.
		if len(toSubscribe) > 0 {
			rvs, err := modules.RPCSubscribeToRVs(stream, toSubscribe)
			if err != nil {
				return errors.AddContext(err, "failed to subscribe to registry values")
			}
			// Check that the initial values are not outdated and update the cache.
			for _, rv := range rvs {
				cachedRevision, exists := w.staticRegistryCache.Get(rv.PubKey, rv.Entry.Tweak)
				if exists && rv.Entry.Revision < cachedRevision {
					return fmt.Errorf("host returned an entry with revision %v which is smaller than cached revision %v for the same entry", rv.Entry.Revision, cachedRevision)
				}
				w.staticRegistryCache.Set(rv.PubKey, rv.Entry, false)
			}
			// Withdraw from budget.
			if !budget.Withdraw(modules.MDMSubscribeCost(pt, uint64(len(rvs)), uint64(len(toSubscribe)))) {
				return errors.New("failed to withdraw subscription payment from budget")
			}
			// Update the subscriptions with the received values.
			subInfo.mu.Lock()
			for _, rv := range rvs {
				subInfo.subscriptions[modules.RegistrySubscriptionID(rv.PubKey, rv.Entry.Tweak)].latestRV = &rv.Entry
			}
			// Close the channels to signal that the subscription is done.
			for _, c := range subChans {
				close(c)
			}
			subInfo.mu.Unlock()
		}

		// Wait until some time passed or until there is new work.
		t := time.NewTimer(subscriptionLoopInterval)
		select {
		case <-w.staticTG.StopChan():
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
	return nil, modules.RPCBeginSubscription(stream, w.staticAccount, w.staticHostPubKey, &w.staticPriceTable().staticPriceTable, initialBudget, w.staticAccount.staticID, w.staticCache().staticBlockHeight, subscriber)
}

// FundSubscription pays the host to increase the subscription budget.
func (w *worker) managedFundSubscription(stream siamux.Stream, fundAmt types.Currency) error {
	return modules.RPCFundSubscription(stream, w.staticHostPubKey, w.staticAccount, w.staticAccount.staticID, w.staticCache().staticBlockHeight, fundAmt)
}

// Unsubscribe marks the provided entries as not subscribed to and notifies the
// worker of the change. It will then handle
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

// Subscribe marks the provided entries as subscribed and waits for the
// subscription to be done, returning potential initial values returend by the
// host.
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
		case <-w.staticTG.StopChan():
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
