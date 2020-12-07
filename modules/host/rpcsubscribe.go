package host

import (
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
)

type (
	// registrySubscriptions is a helper type that holds all current
	// subscriptions.
	registrySubscriptions struct {
		mu sync.Mutex

		// subscriptions is a mapping of subscriptions to subscription infos.
		// It's a map of maps since the same entry can be subscribed to by
		// multiple peers and we want to be able to look up subscriptions in
		// constant time.
		subscriptions map[subscriptionID]map[subscriptionInfoID]*subscriptionInfo
	}
	// subscriptionInfo holds the information required to respond to a
	// subscriber and to correctly charge it.
	subscriptionInfo struct {
		id                subscriptionInfoID
		minExpectedRevNum uint64
		notificationsLeft uint64
		mu                sync.Mutex

		staticStream siamux.Stream
	}

	// subscriptionID is a hash derived from the public key and tweak that a
	// renter would like to subscribe to.
	subscriptionID     crypto.Hash
	subscriptionInfoID types.Specifier
)

var (
	// ErrSubscriptionRequestLimitReached is returned if too many subscribe or
	// unsubscribe requests are sent at once.
	ErrSubscriptionRequestLimitReached = errors.New("number of requests exceeds limit")
)

// maxSubscriptionRequests is the maximum number of subscription requests the
// host will accept at once.
const maxSubscriptionRequests = 10000

// createSubscriptionID is a helper to derive a subscription id.
func createSubscriptionID(pubKey types.SiaPublicKey, tweak crypto.Hash) subscriptionID {
	return subscriptionID(crypto.HashAll(pubKey, tweak))
}

// newRegistrySubscriptions creates a new registrySubscriptions instance.
func newRegistrySubscriptions() *registrySubscriptions {
	return &registrySubscriptions{
		subscriptions: make(map[subscriptionID]map[subscriptionInfoID]*subscriptionInfo),
	}
}

// subscriptionMemoryCost is the cost of storing the given number of
// subscriptions in memory.
func subscriptionMemoryCost(pt *modules.RPCPriceTable, newSubscriptions uint64) types.Currency {
	memory := newSubscriptions * modules.SubscriptionEntrySize
	return pt.SubscriptionMemoryCost.Mul64(memory)
}

// newSubscriptionInfo creates a new subscriptionInfo object.
func newSubscriptionInfo(stream siamux.Stream) *subscriptionInfo {
	info := &subscriptionInfo{
		staticStream:      stream,
		notificationsLeft: modules.InitialNumNotifications,
	}
	fastrand.Read(info.id[:])
	return info
}

// AddSubscription adds one or multiple subscriptions.
func (rs *registrySubscriptions) AddSubscriptions(info *subscriptionInfo, entryIDs ...subscriptionID) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, entryID := range entryIDs {
		if _, exists := rs.subscriptions[entryID]; !exists {
			rs.subscriptions[entryID] = make(map[subscriptionInfoID]*subscriptionInfo)
		}
		rs.subscriptions[entryID][info.id] = info
	}
}

// RemoveSubscriptions removes one or multiple subscriptions.
func (rs *registrySubscriptions) RemoveSubscriptions(info *subscriptionInfo, entryIDs ...subscriptionID) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, entryID := range entryIDs {
		infos, found := rs.subscriptions[entryID]
		if !found {
			continue
		}
		delete(infos, info.id)

		if len(infos) == 0 {
			delete(rs.subscriptions, entryID)
		}
	}
}

// managedHandleSubscribeRequest handles a new subscription.
func (h *Host) managedHandleSubscribeRequest(info *subscriptionInfo, pt *modules.RPCPriceTable, pd modules.PaymentDetails) (types.Currency, error) {
	stream := info.staticStream

	// Read a number indicating how many requests to expect.
	var numSubs uint64
	err := modules.RPCRead(stream, &numSubs)
	if err != nil {
		return types.ZeroCurrency, errors.New("failed to read number of requests to expect")
	}

	// Check that this number isn't exceeding a safe limit.
	if numSubs > maxSubscriptionRequests {
		return types.ZeroCurrency, ErrSubscriptionRequestLimitReached
	}

	// Check payment first.
	requestCost := pt.SubscriptionBaseCost.Mul64(numSubs)
	memoryCost := subscriptionMemoryCost(pt, numSubs)
	cost := requestCost.Add(memoryCost)
	if pd.Amount().Cmp(cost) < 0 {
		return types.ZeroCurrency, modules.ErrInsufficientPaymentForRPC
	}
	refund := pd.Amount().Sub(cost)

	// Read the requests and apply them.
	ids := make([]subscriptionID, 0, numSubs)
	for i := uint64(0); i < numSubs; i++ {
		var rsr modules.RPCRegistrySubscriptionRequest
		err = modules.RPCRead(stream, &rsr)
		if err != nil {
			return refund, errors.AddContext(err, "failed to read subscription request")
		}
		ids = append(ids, createSubscriptionID(rsr.PubKey, rsr.Tweak))
	}
	// Add the subscriptions.
	h.staticRegistrySubscriptions.AddSubscriptions(info, ids...)
	return refund, nil
}

// managedHandleUnsubscribeRequest handles a request to unsubscribe.
func (h *Host) managedHandleUnsubscribeRequest(info *subscriptionInfo, pt *modules.RPCPriceTable, pd modules.PaymentDetails) (types.Currency, error) {
	stream := info.staticStream

	// Read a number indicating how many requests to expect.
	var numUnsubs uint64
	err := modules.RPCRead(stream, &numUnsubs)
	if err != nil {
		return types.ZeroCurrency, errors.New("failed to read number of requests to expect")
	}

	// Check that this number isn't exceeding a safe limit.
	if numUnsubs > maxSubscriptionRequests {
		return types.ZeroCurrency, ErrSubscriptionRequestLimitReached
	}

	// Check payment first.
	cost := pt.SubscriptionBaseCost.Mul64(numUnsubs)
	if pd.Amount().Cmp(cost) < 0 {
		return types.ZeroCurrency, modules.ErrInsufficientPaymentForRPC
	}
	refund := pd.Amount().Sub(cost)

	// Read the requests.
	ids := make([]subscriptionID, 0, numUnsubs)
	for i := uint64(0); i < numUnsubs; i++ {
		var rsr modules.RPCRegistrySubscriptionRequest
		err = modules.RPCRead(stream, &rsr)
		if err != nil {
			return refund, errors.AddContext(err, "failed to read subscription request")
		}
		ids = append(ids, createSubscriptionID(rsr.PubKey, rsr.Tweak))
	}

	// Remove the subscription.
	h.staticRegistrySubscriptions.RemoveSubscriptions(info, ids...)
	return refund, nil
}

// managedHandleExtendSubscriptionRequest handles a request to extend the subscription.
func (h *Host) managedHandleExtendSubscriptionRequest(stream siamux.Stream, subs map[subscriptionID]struct{}, oldDeadline time.Time, pt *modules.RPCPriceTable, pd modules.PaymentDetails) (types.Currency, time.Time, error) {
	// Get new deadline.
	newDeadline := oldDeadline.Add(modules.SubscriptionPeriod)

	// Check payment first.
	memoryCost := subscriptionMemoryCost(pt, uint64(len(subs)))
	cost := pt.SubscriptionBaseCost.Add(memoryCost)
	if pd.Amount().Cmp(cost) < 0 {
		return types.ZeroCurrency, time.Time{}, modules.ErrInsufficientPaymentForRPC
	}
	refund := pd.Amount().Sub(cost)

	// Set deadline.
	err := stream.SetReadDeadline(newDeadline)
	if err != nil {
		return refund, time.Time{}, errors.AddContext(err, "failed to extend stream deadline")
	}
	return refund, newDeadline, nil
}

// managedHandlePrepayNotifications handles a request to pay for more notifications.
func (h *Host) managedHandlePrepayNotifications(stream siamux.Stream, info *subscriptionInfo, pt *modules.RPCPriceTable, pd modules.PaymentDetails) (types.Currency, error) {
	// Read the number of notifications the caller would like to pay for.
	var numNotifications uint64
	err := modules.RPCRead(stream, &numNotifications)
	if err != nil {
		return types.ZeroCurrency, errors.New("failed to read number of notifications to expect")
	}
	// Check payment first.
	cost := pt.SubscriptionBaseCost.Add(modules.MDMReadRegistryCost(pt).Mul64(numNotifications))
	if pd.Amount().Cmp(cost) < 0 {
		return types.ZeroCurrency, modules.ErrInsufficientPaymentForRPC
	}
	refund := pd.Amount().Sub(cost)

	// Update notifications.
	info.mu.Lock()
	info.notificationsLeft += numNotifications
	info.mu.Unlock()
	return refund, nil
}

// threadedNotifySubscribers handles notifying all subscribers for a certain
// key/tweak combination.
func (h *Host) threadedNotifySubscribers(pubKey types.SiaPublicKey, rv modules.SignedRegistryValue) {
	err := h.tg.Add()
	if err != nil {
		return
	}
	defer h.tg.Done()

	// Look up subscribers.
	h.staticRegistrySubscriptions.mu.Lock()
	defer h.staticRegistrySubscriptions.mu.Unlock()

	id := createSubscriptionID(pubKey, rv.Tweak)
	infos, found := h.staticRegistrySubscriptions.subscriptions[id]
	if !found {
		return
	}
	for _, info := range infos {
		go func(info *subscriptionInfo) {
			// Lock the info while notifying the subscriber.
			info.mu.Lock()
			defer info.mu.Unlock()

			// Check if we have already updated the subscriber with a higher
			// revision number for that entry than the minExpectedRevNum. This
			// might happen due to a race and should be avoided. Otherwise the
			// subscriber might think that we are trying to cheat them.
			if rv.Revision < info.minExpectedRevNum {
				return
			}
			info.minExpectedRevNum = rv.Revision + 1

			// No notification if caller ran out of prepaid notifications.
			if info.notificationsLeft == 0 {
				return
			}
			info.notificationsLeft--

			// Notify the caller.
			err := modules.RPCWrite(info.staticStream, modules.RPCRegistrySubscriptionNotification{
				Type:  modules.SubscriptionResponseRegistryValue,
				Entry: rv,
			})
			if err != nil {
				h.log.Debug("failed to notify a subscriber", err)
				return
			}
		}(info)
	}
}

// managedRPCRegistrySubscribe handles the RegistrySubscribe rpc.
func (h *Host) managedRPCRegistrySubscribe(stream siamux.Stream) (err error) {
	// read the price table
	pt, err := h.staticReadPriceTableID(stream)
	if err != nil {
		return errors.AddContext(err, "failed to read price table")
	}

	// Process payment.
	pd, err := h.ProcessPayment(stream)
	if err != nil {
		return errors.AddContext(err, "failed to process payment")
	}

	// Check payment.
	cost := pt.SubscriptionBaseCost.Add(modules.MDMReadRegistryCost(pt).Mul64(modules.InitialNumNotifications))
	if pd.Amount().Cmp(cost) < 0 {
		return modules.ErrInsufficientPaymentForRPC
	}

	// Refund excessive amount.
	if pd.Amount().Cmp(cost) > 0 {
		err = h.staticAccountManager.callRefund(pd.AccountID(), pd.Amount().Sub(pt.SubscriptionBaseCost))
		if err != nil {
			return errors.AddContext(err, "failed to refund excessive initial subscription payment")
		}
	}

	// Set the stream deadline.
	deadline := time.Now().Add(modules.SubscriptionPeriod)
	err = stream.SetReadDeadline(deadline)
	if err != nil {
		return errors.AddContext(err, "failed to set intitial subscription deadline")
	}

	// Keep count of the unique subscriptions to be able to charge accordingly.
	subscriptions := make(map[subscriptionID]struct{})
	info := newSubscriptionInfo(stream)

	// Clean up the subscriptions at the end.
	defer func() {
		entryIDs := make([]subscriptionID, 0, len(subscriptions))
		for entryID := range subscriptions {
			entryIDs = append(entryIDs, entryID)
		}
		h.staticRegistrySubscriptions.RemoveSubscriptions(info, entryIDs...)

		// Refund the unused notifications.
		info.mu.Lock()
		defer info.mu.Unlock()
		if info.notificationsLeft > 0 {
			refund := modules.MDMReadRegistryCost(pt).Mul64(info.notificationsLeft)
			err = errors.Compose(err, h.staticAccountManager.callRefund(pd.AccountID(), refund))
			info.notificationsLeft = 0
		}
	}()

	// The subscription RPC is a request/response loop that continues for as
	// long as the renter keeps paying for it.
	for {
		// Read subscription request.
		var requestType uint8
		err = modules.RPCRead(stream, &requestType)
		if err != nil {
			return errors.AddContext(err, "failed to read request type")
		}

		// Read the price table
		pt, err = h.staticReadPriceTableID(stream)
		if err != nil {
			return errors.AddContext(err, "failed to read price table")
		}

		// Process payment.
		pd, err := h.ProcessPayment(stream)
		if err != nil {
			return errors.AddContext(err, "failed to process payment")
		}

		// Handle requests.
		var refund types.Currency
		switch requestType {
		case modules.SubscriptionRequestSubscribe:
			refund, err = h.managedHandleSubscribeRequest(info, pt, pd)
		case modules.SubscriptionRequestUnsubscribe:
			refund, err = h.managedHandleUnsubscribeRequest(info, pt, pd)
		case modules.SubscriptionRequestExtend:
			refund, deadline, err = h.managedHandleExtendSubscriptionRequest(stream, subscriptions, deadline, pt, pd)
		case modules.SubscriptionRequestPrepay:
			refund, err = h.managedHandlePrepayNotifications(stream, info, pt, pd)
		default:
			return errors.New("unknown request type")
		}
		// Refund excessive payment before checking the error.
		if !refund.IsZero() {
			err = errors.Compose(err, h.staticAccountManager.callRefund(pd.AccountID(), refund))
		}
		// Check the errors.
		if err != nil {
			return errors.AddContext(err, "failed to handle request")
		}
	}
}
