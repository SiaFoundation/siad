package renter

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/threadgroup"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TODO: (f/u) cooldown testing

var (
	// initialSubscriptionBudget is the initial budget withdrawn for a
	// subscription. After using up 50% of it, the worker refills the budget
	// again to match the initial budget.
	initialSubscriptionBudget = modules.DefaultMaxEphemeralAccountBalance.Div64(10) // 10% of the max

	// subscriptionCooldownResetInterval is the time after which we consider an
	// ongoing subscription to be healthy enough to reset the consecutive
	// failures.
	subscriptionCooldownResetInterval = build.Select(build.Var{
		Testing:  time.Second * 5,
		Dev:      time.Minute,
		Standard: time.Hour,
		Testnet:  time.Hour,
	}).(time.Duration)

	// subscriptionExtensionWindow is the time before the subscription period
	// ends when the workers starts trying to extend the subscription.
	subscriptionExtensionWindow = modules.SubscriptionPeriod / 2 // 50% of period

	// subscriptionLoopInterval is the interval after which the subscription
	// loop checks for work when it's idle. Idle means the staticWakeChan isn't
	// signaling new work.
	subscriptionLoopInterval = time.Second

	// stopSubscriptionGracePeriod is the period of time we wait after signaling
	// the host that we want to stop the subscription. All the incoming
	// bandwidth within this period will be accounted for correctly and help us
	// to keep our EA balance expectations in sync with the host's.
	stopSubscriptionGracePeriod = build.Select(build.Var{
		Testing:  time.Second,
		Dev:      3 * time.Second,
		Standard: 3 * time.Second,
		Testnet:  3 * time.Second,
	}).(time.Duration)

	// priceTableRetryInterval is the interval the subscription loop waits for
	// the maintenance to update the price table before checking again.
	priceTableRetryInterval = time.Second
)

// minSubscriptionVersion is the min version required for a host to support the
// subscription protocol.
const minSubscriptionVersion = "1.5.5"

type (
	// subscriptionInfos contains all of the registry subscription related
	// information of a worker.
	subscriptionInfos struct {
		// subscriptions is the map of subscriptions that the worker is supposed
		// to subscribe to. The worker might not be subscribed to these values
		// at all times due to interruptions but it will try to resubscribe as
		// soon as possible.
		// The worker will also try to unsubscribe from all subscriptions that
		// it currently has which it is not supposed to be subscribed to.
		subscriptions map[modules.RegistryEntryID]*subscription

		// staticWakeChan is a channel to tell the subscription loop that more
		// work is available.
		staticWakeChan chan struct{}

		// stats
		atomicExtensions uint64

		// cooldown
		cooldownUntil       time.Time
		consecutiveFailures uint64

		// utility fields
		mu sync.Mutex
	}

	// subscription is a struct that provides additional information around a
	// subscription.
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
		staticStream io.Closer // stream of the main subscription thread
		staticWorker *worker

		staticPTUpdateChan  chan struct{} // used by notification thread to signal subscription thread to update the price table
		staticPTUpdatedChan chan struct{} // used by subscription thread to signal notification thread that price table update is done

		notificationCost types.Currency
		mu               sync.Mutex
	}
)

// newSubscription creates a new subscription.
func newSubscription(request *modules.RPCRegistrySubscriptionRequest) *subscription {
	return &subscription{
		staticRequest: request,
		subscribed:    make(chan struct{}),
		subscribe:     true,
	}
}

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

// managedHandleRegistryEntry is called by managedHandleNotification to handle a
// notification about an updated registry entry.
func (nh *notificationHandler) managedHandleRegistryEntry(stream siamux.Stream, budget *modules.RPCBudget, limit *modules.BudgetLimit) (err error) {
	w := nh.staticWorker
	subInfo := w.staticSubscriptionInfo

	// Add a limit to the stream.
	err = stream.SetLimit(limit)
	if err != nil {
		return errors.AddContext(err, "failed to set limit on notification stream")
	}

	// Withdraw notification cost.
	nh.mu.Lock()
	ok := budget.Withdraw(nh.notificationCost)
	nh.mu.Unlock()
	if !ok {
		return errors.New("failed to withdraw notification cost")
	}

	// Read the update.
	var sneu modules.RPCRegistrySubscriptionNotificationEntryUpdate
	err = modules.RPCRead(stream, &sneu)
	if err != nil {
		return errors.AddContext(err, "failed to read entry update")
	}

	// Starting here we close the main subscription stream if an error happens
	// because it will be the host trying to cheat us.
	defer func() {
		if err != nil {
			err = errors.Compose(err, nh.staticStream.Close())
		}
	}()

	// Check if the host was trying to cheat us with an outdated entry.
	latestRev, exists := w.staticRegistryCache.Get(sneu.PubKey, sneu.Entry.Tweak)
	if exists && sneu.Entry.Revision < latestRev {
		return fmt.Errorf("host provided outdated entry %v < %v", sneu.Entry.Revision, latestRev)
	}

	// Verify the signature.
	err = sneu.Entry.Verify(sneu.PubKey.ToPublicKey())
	if err != nil {
		return errors.AddContext(err, "failed to verify signature")
	}

	// The entry is valid. Update the cache.
	w.staticRegistryCache.Set(sneu.PubKey, sneu.Entry, false)

	// Check if the host sent us an update we are not subsribed to. This might
	// not seem bad, but the host might want to spam us with valid entries that
	// we are not interested in simply to have us pay for bandwidth.
	subInfo.mu.Lock()
	defer subInfo.mu.Unlock()
	sub, exists := subInfo.subscriptions[modules.DeriveRegistryEntryID(sneu.PubKey, sneu.Entry.Tweak)]
	if !exists || (sub.latestRV != nil && sub.latestRV.Revision >= sneu.Entry.Revision) {
		if exists && sub.latestRV != nil {
			return fmt.Errorf("host sent an outdated revision %v >= %v", sub.latestRV.Revision, sneu.Entry.Revision)
		}
		return fmt.Errorf("subscription not found")
	}

	// Update the subscription.
	sub.latestRV = &sneu.Entry
	return nil
}

// managedHandleSubscriptionSuccess is called by managedHandleNotification to
// handle a subscription success notification.
func (nh *notificationHandler) managedHandleSubscriptionSuccess(stream siamux.Stream, limit *modules.BudgetLimit) error {
	// Tell the subscription thread to update the limits using the new price
	// table.
	select {
	case <-nh.staticWorker.staticTG.StopChan():
		return nil // shutdown
	case nh.staticPTUpdateChan <- struct{}{}:
	}
	// Wait for the subscription thread to be done updating the limits.
	select {
	case <-nh.staticWorker.staticTG.StopChan():
		return nil // shutdown
	case <-nh.staticPTUpdatedChan:
	}
	// Since this stream uses the new costs we set the limit after updating
	// the costs.
	err := stream.SetLimit(limit)
	if err != nil {
		return errors.AddContext(err, "extension 'ok' was received but failed to update limit on stream")
	}
	return nil
}

// managedHandleNotification handles incoming notifications from the host. It
// verifies notifications and updates the worker's internal state accordingly.
// Since it's registered as a handle which is called in a separate goroutine it
// doesn't return an error.
func (nh *notificationHandler) managedHandleNotification(stream siamux.Stream, budget *modules.RPCBudget, limit *modules.BudgetLimit) {
	w := nh.staticWorker
	// Close the stream when done.
	defer func() {
		if err := stream.Close(); err != nil {
			w.renter.log.Print("managedHandleNotification: failed to close stream: ", err)
		}
	}()

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
		if err := nh.managedHandleSubscriptionSuccess(stream, limit); err != nil {
			w.renter.log.Print("managedHAndleSubscriptionSuccess:", err)
		}
		return
	case modules.SubscriptionResponseRegistryValue:
		if err := nh.managedHandleRegistryEntry(stream, budget, limit); err != nil {
			w.renter.log.Print("managedHandleRegistryEntry:", err)
		}
		return
	default:
	}

	// TODO: (f/u) Punish the host by adding a subscription cooldown.
	w.renter.log.Print("managedHandleNotification: unknown notification type")
	if err := nh.staticStream.Close(); err != nil {
		w.renter.log.Debugln("managedHandleNotification: failed to close subscription:", err)
	}
}

// managedClearSubscription replaces the channels of all subscriptions of the
// subInfo. This unblocks anyone waiting for a subscription to be established.
func (subInfo *subscriptionInfos) managedClearSubscriptions() {
	subInfo.mu.Lock()
	defer subInfo.mu.Unlock()
	for _, sub := range subInfo.subscriptions {
		// Replace channels.
		select {
		case <-sub.subscribed:
		default:
			close(sub.subscribed)
		}
		sub.subscribed = make(chan struct{})
	}
}

// managedIncrementCooldown increments the subscription cooldown.
func (subInfo *subscriptionInfos) managedIncrementCooldown() {
	subInfo.mu.Lock()
	defer subInfo.mu.Unlock()

	// If the last cooldown ended a while ago, we reset the consecutive
	// failures.
	if time.Now().Sub(subInfo.cooldownUntil) > subscriptionCooldownResetInterval {
		subInfo.consecutiveFailures = 0
	}

	// Increment the cooldown.
	subInfo.cooldownUntil = cooldownUntil(subInfo.consecutiveFailures)
	subInfo.consecutiveFailures++
}

// managedOnCooldown returns whether the subscription cooldown is active and its
// remaining time.
func (subInfo *subscriptionInfos) managedOnCooldown() (time.Duration, bool) {
	subInfo.mu.Lock()
	defer subInfo.mu.Unlock()
	return time.Until(subInfo.cooldownUntil), time.Now().Before(subInfo.cooldownUntil)
}

// managedSubscriptionDiff returns the difference between the desired
// subscriptions and the active subscriptions. It also returns a slice of
// channels which need to be closed when the corresponding desired subscription
// was established.
func (subInfo *subscriptionInfos) managedSubscriptionDiff() (toSubscribe, toUnsubscribe []modules.RPCRegistrySubscriptionRequest, subChans []chan struct{}) {
	subInfo.mu.Lock()
	defer subInfo.mu.Unlock()
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
	return
}

// managedExtendSubscriptionPeriod extends the ongoing subscription with a host
// and adjusts the deadline on the stream.
func (w *worker) managedExtendSubscriptionPeriod(stream siamux.Stream, budget *modules.RPCBudget, limit *modules.BudgetLimit, oldDeadline time.Time, oldPT *modules.RPCPriceTable, nh *notificationHandler) (*modules.RPCPriceTable, time.Time, error) {
	subInfo := w.staticSubscriptionInfo

	// Get a pricetable that is valid until the new deadline.
	newDeadline := oldDeadline.Add(modules.SubscriptionPeriod)
	newPT := w.managedPriceTableForSubscription(time.Until(newDeadline))

	// Try extending the subscription.
	err := modules.RPCExtendSubscription(stream, newPT)
	if err != nil {
		return nil, time.Time{}, errors.AddContext(err, "failed to extend subscription")
	}

	// Wait for "ok".
	select {
	case <-w.staticTG.StopChan():
		return nil, time.Time{}, threadgroup.ErrStopped
	case <-time.After(time.Until(newDeadline)):
		return nil, time.Time{}, errors.New("never received the 'ok' response for extending the subscription")
	case <-nh.staticPTUpdateChan:
	}

	// Update limit and notification cost.
	limit.UpdateCosts(newPT.DownloadBandwidthCost, newPT.UploadBandwidthCost)
	nh.mu.Lock()
	nh.notificationCost = newPT.SubscriptionNotificationCost
	nh.mu.Unlock()

	// Tell the notification goroutine that the limit was updated.
	select {
	case <-w.staticTG.StopChan():
		return nil, time.Time{}, threadgroup.ErrStopped
	case <-time.After(time.Until(newDeadline)):
		return nil, time.Time{}, errors.New("notification thread never read the update signal")
	case nh.staticPTUpdatedChan <- struct{}{}:
	}

	// Count the number of active subscriptions.
	var nSubs uint64
	for _, sub := range subInfo.subscriptions {
		if sub.active() {
			nSubs++
		}
	}

	// Withdraw from budget.
	if !budget.Withdraw(modules.MDMSubscriptionMemoryCost(newPT, nSubs)) {
		return nil, time.Time{}, errors.New("failed to withdraw subscription extension cost from budget")
	}

	// Set the stream deadline to the new subscription deadline.
	err = stream.SetDeadline(newDeadline)
	if err != nil {
		return nil, time.Time{}, errors.AddContext(err, "failed to set stream deadlien to subscription deadline")
	}

	// Increment stats for extending the subscription.
	atomic.AddUint64(&subInfo.atomicExtensions, 1)
	return newPT, newDeadline, nil
}

// managedRefillSubscription refills the subscription up until expectedBudget.
func (w *worker) managedRefillSubscription(stream siamux.Stream, pt *modules.RPCPriceTable, expectedBudget types.Currency, budget *modules.RPCBudget) error {
	fundAmt := expectedBudget.Sub(budget.Remaining())

	// Track the withdrawal.
	w.staticAccount.managedTrackWithdrawal(fundAmt)

	// Fund the subscription.
	err := w.managedFundSubscription(stream, pt, fundAmt)
	if err != nil {
		w.staticAccount.managedCommitWithdrawal(categorySubscription, fundAmt, types.ZeroCurrency, false)
		return errors.AddContext(err, "failed to fund subscription")
	}

	// Success. Add the funds to the budget and signal to the account
	// that the withdrawal was successful.
	budget.Deposit(fundAmt)
	w.staticAccount.managedCommitWithdrawal(categorySubscription, fundAmt, types.ZeroCurrency, true)
	return nil
}

// managedSubscriptionCleanup cleans up a subscription by signalling the host
// that we would like to stop the subscription and resetting the subscription
// related fields in the subscription info.
func (w *worker) managedSubscriptionCleanup(stream siamux.Stream, subscriber string) (err error) {
	subInfo := w.staticSubscriptionInfo

	// Close the stream gracefully.
	err = modules.RPCStopSubscription(stream)

	// After signalling to shut down the subscription, we wait for a short
	// grace period to allow for incoming streams which were already read by
	// the siamux but did not have the handler called upon them yet. This
	// makes sure that our bandwidth expectations don't drift apart from the
	// host's. We want to always wait for this even upon shutdown to make
	// sure we refund our account correctly.
	time.Sleep(stopSubscriptionGracePeriod)

	// Close the handler.
	err = errors.Compose(err, w.renter.staticMux.CloseListener(subscriber))

	// Clear the active subscriptions at the end of this method.
	subInfo.managedClearSubscriptions()
	return err
}

// managedUnsubscribeFromRVs unsubscribes the worker from multiple ongoing
// subscriptions.
func (w *worker) managedUnsubscribeFromRVs(stream siamux.Stream, toUnsubscribe []modules.RPCRegistrySubscriptionRequest) error {
	subInfo := w.staticSubscriptionInfo
	// Unsubscribe.
	err := modules.RPCUnsubscribeFromRVs(stream, toUnsubscribe)
	if err != nil {
		return errors.AddContext(err, "failed to unsubscribe from registry values")
	}
	// Reset the subscription's channel to signal that it's no longer
	// active.
	subInfo.mu.Lock()
	defer subInfo.mu.Unlock()
	for _, req := range toUnsubscribe {
		sid := modules.DeriveRegistryEntryID(req.PubKey, req.Tweak)
		sub, exists := subInfo.subscriptions[sid]
		if !exists {
			err = errors.New("managedSubscriptionLoop: missing subscription - subscriptions should only be deleted in this thread so this shouldn't be the case")
			build.Critical(err)
			return err
		}
		sub.subscribed = make(chan struct{})
	}
	return nil
}

// managedSubscribeToRVs subscribes the workers to multiple registry values.
func (w *worker) managedSubscribeToRVs(stream siamux.Stream, toSubscribe []modules.RPCRegistrySubscriptionRequest, subChans []chan struct{}, budget *modules.RPCBudget, pt *modules.RPCPriceTable) error {
	subInfo := w.staticSubscriptionInfo
	// Subscribe.
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
	defer subInfo.mu.Unlock()
	for _, rv := range rvs {
		subInfo.subscriptions[modules.DeriveRegistryEntryID(rv.PubKey, rv.Entry.Tweak)].latestRV = &rv.Entry
	}
	// Close the channels to signal that the subscription is done.
	for _, c := range subChans {
		close(c)
	}
	return nil
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
		staticStream:        stream,
		staticWorker:        w,
		staticPTUpdateChan:  make(chan struct{}),
		staticPTUpdatedChan: make(chan struct{}),
		notificationCost:    pt.SubscriptionNotificationCost,
	}
	err = w.renter.staticMux.NewListenerSerial(subscriber, func(stream siamux.Stream) {
		nh.managedHandleNotification(stream, budget, limit)
	})
	if err != nil {
		return errors.AddContext(err, "failed to register listener")
	}

	// Register some cleanup.
	defer func() {
		err = errors.Compose(err, w.managedSubscriptionCleanup(stream, subscriber))
	}()

	// Set the stream deadline to the subscription deadline.
	err = stream.SetDeadline(deadline)
	if err != nil {
		return errors.AddContext(err, "failed to set stream deadlien to subscription deadline")
	}

	for {
		// If the budget is half empty, fund it.
		if budget.Remaining().Cmp(expectedBudget.Div64(2)) < 0 {
			err = w.managedRefillSubscription(stream, pt, expectedBudget, budget)
			if err != nil {
				return err
			}
		}

		// If the subscription period is halfway over, extend it.
		if time.Until(deadline) < modules.SubscriptionPeriod/2 {
			pt, deadline, err = w.managedExtendSubscriptionPeriod(stream, budget, limit, deadline, pt, nh)
			if err != nil {
				return err
			}
		}

		// Create a diff between the active subscriptions and the desired
		// ones.
		subInfo := w.staticSubscriptionInfo
		toSubscribe, toUnsubscribe, subChans := subInfo.managedSubscriptionDiff()

		// Unsubscribe from unnecessary subscriptions.
		if len(toUnsubscribe) > 0 {
			err = w.managedUnsubscribeFromRVs(stream, toUnsubscribe)
			if err != nil {
				return err
			}
		}

		// Subscribe to any missing values.
		if len(toSubscribe) > 0 {
			err = w.managedSubscribeToRVs(stream, toSubscribe, subChans, budget, pt)
			if err != nil {
				return err
			}
		}

		// Wait until some time passed or until there is new work.
		ctx, cancel := context.WithTimeout(context.Background(), subscriptionLoopInterval)
		select {
		case <-w.staticTG.StopChan():
			cancel()
			return threadgroup.ErrStopped // shutdown
		case <-ctx.Done():
			// continue right away since the timer is drained.
			cancel()
			continue
		case <-subInfo.staticWakeChan:
			cancel()
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
		w.renter.log.Printf("managedPriceTableForSubscription: pt not ready yet for worker %v", w.staticHostPubKeyStr)

		// Trigger an update by setting the update time to now and calling
		// 'staticWake'.
		newPT := *pt
		newPT.staticUpdateTime = time.Time{}
		oldPT := (*workerPriceTable)(atomic.SwapPointer(&w.atomicPriceTable, unsafe.Pointer(&newPT)))
		w.staticWake()

		// The old table's UID should be the same. Otherwise we just swapped out
		// a new table and need to try again. This condition can be false when
		// pricetable got updated between now and when we fetched it at the
		// beginning of this iteration.
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
	return stream, modules.RPCBeginSubscription(stream, w.staticHostPubKey, &w.staticPriceTable().staticPriceTable, w.staticAccount.staticID, w.staticAccount.staticSecretKey, initialBudget, w.staticCache().staticBlockHeight, subscriber)
}

// managedFundSubscription pays the host to increase the subscription budget.
func (w *worker) managedFundSubscription(stream siamux.Stream, pt *modules.RPCPriceTable, fundAmt types.Currency) error {
	return modules.RPCFundSubscription(stream, w.staticHostPubKey, w.staticAccount.staticID, w.staticAccount.staticSecretKey, pt.HostBlockHeight, fundAmt)
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
	if build.VersionCmp(w.staticCache().staticHostVersion, minSubscriptionVersion) < 0 {
		return
	}

	// Disable loop if necessary.
	if w.renter.deps.Disrupt("DisableSubscriptionLoop") {
		return
	}

	// Convenience var.
	subInfo := w.staticSubscriptionInfo

	for {
		// Clear potential subscriptions before establishing a new loop.
		subInfo.managedClearSubscriptions()

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
		if cooldownTime, onCooldown := subInfo.managedOnCooldown(); onCooldown {
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
			w.staticAccount.managedCommitWithdrawal(categorySubscription, initialBudget, types.ZeroCurrency, false)

			// Log error and increment cooldown.
			w.renter.log.Printf("Worker %v: failed to begin subscription: %v", w.staticHostPubKeyStr, err)
			subInfo.managedIncrementCooldown()
			continue
		}

		// Run the subscription. The error is checked after closing the handler
		// and the refund.
		errSubscription := w.managedSubscriptionLoop(stream, pt, deadline, budget, initialBudget, subscriberStr)

		// Commit the withdrawal now we know the refund.
		refund := budget.Remaining()
		withdrawal := initialBudget.Sub(refund)
		w.staticAccount.managedCommitWithdrawal(categorySubscription, withdrawal, refund, true)

		// Check the error.
		if errors.Contains(errSubscription, threadgroup.ErrStopped) {
			return // shutdown
		}
		if err != nil {
			w.renter.log.Printf("Worker %v: subscription got interrupted: %v", w.staticHostPubKeyStr, errSubscription)
			subInfo.managedIncrementCooldown()
			continue
		}
	}
}

// Unsubscribe marks the provided entries as not subscribed to and notifies the
// worker of the change.
func (w *worker) Unsubscribe(requests ...modules.RPCRegistrySubscriptionRequest) {
	subInfo := w.staticSubscriptionInfo

	subInfo.mu.Lock()
	defer subInfo.mu.Unlock()
	for _, req := range requests {
		sid := modules.DeriveRegistryEntryID(req.PubKey, req.Tweak)
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
		sid := modules.DeriveRegistryEntryID(req.PubKey, req.Tweak)
		sub, exists := subInfo.subscriptions[sid]
		if !exists {
			sub = newSubscription(&requests[i])
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
	subInfo.mu.Lock()
	defer subInfo.mu.Unlock()
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
