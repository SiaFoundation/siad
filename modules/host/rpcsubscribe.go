package host

import (
	"bytes"
	"io"
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
		notificationCost  types.Currency
		minExpectedRevNum uint64
		mu                sync.Mutex

		staticBudget *modules.RPCBudget
		staticID     subscriptionInfoID
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

// deriveSubscriptionID is a helper to derive a subscription id.
func deriveSubscriptionID(pubKey types.SiaPublicKey, tweak crypto.Hash) subscriptionID {
	return subscriptionID(crypto.HashAll(pubKey, tweak))
}

// newRegistrySubscriptions creates a new registrySubscriptions instance.
func newRegistrySubscriptions() *registrySubscriptions {
	return &registrySubscriptions{
		subscriptions: make(map[subscriptionID]map[subscriptionInfoID]*subscriptionInfo),
	}
}

// newSubscriptionInfo creates a new subscriptionInfo object.
func newSubscriptionInfo(stream siamux.Stream, budget *modules.RPCBudget, notificationsCost types.Currency) *subscriptionInfo {
	info := &subscriptionInfo{
		notificationCost: notificationsCost,
		staticStream:     stream,
		staticBudget:     budget,
	}
	fastrand.Read(info.staticID[:])
	return info
}

// AddSubscriptions adds one or multiple subscriptions.
func (rs *registrySubscriptions) AddSubscriptions(info *subscriptionInfo, entryIDs ...subscriptionID) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, entryID := range entryIDs {
		if _, exists := rs.subscriptions[entryID]; !exists {
			rs.subscriptions[entryID] = make(map[subscriptionInfoID]*subscriptionInfo)
		}
		rs.subscriptions[entryID][info.staticID] = info
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
		delete(infos, info.staticID)

		if len(infos) == 0 {
			delete(rs.subscriptions, entryID)
		}
	}
}

// managedHandleSubscribeRequest handles a new subscription.
func (h *Host) managedHandleSubscribeRequest(info *subscriptionInfo, pt *modules.RPCPriceTable) error {
	stream := info.staticStream

	// Read the requests.
	buf := new(bytes.Buffer)
	var rsrs []modules.RPCRegistrySubscriptionRequest
	err := modules.RPCRead(stream, &rsrs)
	if err != nil {
		return errors.AddContext(err, "failed to read subscription request")
	}

	// Compute the subscription cost.
	cost := modules.MDMSubscribeCost(pt, uint64(len(rsrs)))

	// Add the cost of initial notification.
	cost = cost.Add(pt.SubscriptionNotificationCost.Mul64(uint64(len(rsrs))))

	// Withdraw from the budget.
	if !info.staticBudget.Withdraw(cost) {
		return errors.AddContext(modules.ErrInsufficientPaymentForRPC, "managedHandleSubscribeRequest")
	}

	// Send notifications.
	ids := make([]subscriptionID, 0, len(rsrs))
	for _, rsr := range rsrs {
		ids = append(ids, deriveSubscriptionID(rsr.PubKey, rsr.Tweak))
		if rv, found := h.staticRegistry.Get(rsr.PubKey, rsr.Tweak); found {
			// Write rv to buffer.
			err = sendNotification(buf, rv)
		}
	}
	// Write buffer to stream.
	_, err = buf.WriteTo(stream)
	if err != nil {
		return errors.AddContext(err, "failed to write initial values to stream")
	}

	// Add the subscriptions.
	h.staticRegistrySubscriptions.AddSubscriptions(info, ids...)
	return nil
}

// managedHandleUnsubscribeRequest handles a request to unsubscribe.
func (h *Host) managedHandleUnsubscribeRequest(info *subscriptionInfo, pt *modules.RPCPriceTable) error {
	stream := info.staticStream

	// Read the requests.
	var rsrs []modules.RPCRegistrySubscriptionRequest
	err := modules.RPCRead(stream, &rsrs)
	if err != nil {
		return errors.AddContext(err, "failed to read subscription requests")
	}
	ids := make([]subscriptionID, 0, len(rsrs))
	for _, rsr := range rsrs {
		ids = append(ids, deriveSubscriptionID(rsr.PubKey, rsr.Tweak))
	}

	// Remove the subscription.
	h.staticRegistrySubscriptions.RemoveSubscriptions(info, ids...)
	return nil
}

// managedHandleExtendSubscriptionRequest handles a request to extend the subscription.
func (h *Host) managedHandleExtendSubscriptionRequest(stream siamux.Stream, subs map[subscriptionID]struct{}, oldDeadline time.Time, pt *modules.RPCPriceTable, info *subscriptionInfo) (time.Time, error) {
	// Get new deadline.
	newDeadline := oldDeadline.Add(modules.SubscriptionPeriod)

	// Check payment first.
	cost := modules.MDMSubscribeCost(pt, uint64(len(subs)))
	if !info.staticBudget.Withdraw(cost) {
		return time.Time{}, errors.AddContext(modules.ErrInsufficientPaymentForRPC, "managedHandleExtendSubscriptionRequest")
	}

	// Set deadline.
	err := stream.SetReadDeadline(newDeadline)
	if err != nil {
		return time.Time{}, errors.AddContext(err, "failed to extend stream deadline")
	}
	return newDeadline, nil
}

func (h *Host) managedHandlePrepayBandwidth(stream siamux.Stream, info *subscriptionInfo, limit *modules.BudgetLimit) (*modules.RPCPriceTable, error) {
	// Lock the subscription info to make sure no notifications are sent in the
	// background until this request is handled.
	info.mu.Lock()
	defer info.mu.Unlock()

	// Read the price table
	pt, err := h.staticReadPriceTableID(stream)
	if err != nil {
		return nil, errors.AddContext(err, "failed to read price table")
	}

	// Update the notification cost.
	info.notificationCost = pt.SubscriptionNotificationCost

	// Process payment.
	pd, err := h.ProcessPayment(stream)
	if err != nil {
		return nil, errors.AddContext(err, "managedHandlePrepaybandwidth: failed to process payment")
	}

	// Add to budget.
	info.staticBudget.Deposit(pd.Amount())

	// Update the limit. This happens last cause we know at this point that we
	// are not sending any data over the stream and the client knows that it
	// shouldn't send a new request until a confirmation of payment was
	// received. This way the budgets stay in sync between renter and host.
	limit.UpdateCosts(pt.UploadBandwidthCost, pt.DownloadBandwidthCost)

	// Notify the caller that the payment is done.
	err = modules.RPCWrite(info.staticStream, modules.RPCRegistrySubscriptionNotificationType{
		Type: modules.SubscriptionResponsePaymentDone,
	})
	if err != nil {
		return nil, errors.AddContext(err, "failed to signal payment completion")
	}
	return pt, nil
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

	id := deriveSubscriptionID(pubKey, rv.Tweak)
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

			// Withdraw the base notification cost.
			ok := info.staticBudget.Withdraw(info.notificationCost)
			if !ok {
				return
			}

			// Notify the caller.
			buf := new(bytes.Buffer)
			err = sendNotification(buf, rv)
			if err != nil {
				h.log.Debug("failed to write notification to buffer", err)
				return
			}
			_, err = buf.WriteTo(info.staticStream)
			if err != nil {
				h.log.Debug("failed to send notification", err)
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

	// Process bandwidth payment.
	pd, err := h.ProcessPayment(stream)
	if err != nil {
		return errors.AddContext(err, "failed to process payment")
	}

	// Add limit to the stream. The readCost is the UploadBandwidthCost since
	// reading from the stream means uploading from the host's perspective. That
	// makes the writeCost the DownloadBandwidthCost.
	budget := modules.NewBudget(pd.Amount())
	bandwidthLimit := modules.NewBudgetLimit(budget, pt.UploadBandwidthCost, pt.DownloadBandwidthCost)
	err = stream.SetLimit(bandwidthLimit)
	if err != nil {
		return errors.AddContext(err, "failed to set budget limit on stream")
	}

	// Set the stream deadline.
	deadline := time.Now().Add(modules.SubscriptionPeriod)
	err = stream.SetReadDeadline(deadline)
	if err != nil {
		return errors.AddContext(err, "failed to set intitial subscription deadline")
	}

	// Keep count of the unique subscriptions to be able to charge accordingly.
	subscriptions := make(map[subscriptionID]struct{})
	info := newSubscriptionInfo(stream, budget, pt.SubscriptionNotificationCost)

	// Clean up the subscriptions at the end.
	defer func() {
		entryIDs := make([]subscriptionID, 0, len(subscriptions))
		for entryID := range subscriptions {
			entryIDs = append(entryIDs, entryID)
		}
		h.staticRegistrySubscriptions.RemoveSubscriptions(info, entryIDs...)

		// Refund the unused bandwidth.
		info.mu.Lock()
		defer info.mu.Unlock()
		if !budget.Remaining().IsZero() {
			err = errors.Compose(err, h.staticAccountManager.callRefund(pd.AccountID(), budget.Remaining()))
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

		// Handle requests.
		switch requestType {
		case modules.SubscriptionRequestSubscribe:
			err = h.managedHandleSubscribeRequest(info, pt)
		case modules.SubscriptionRequestUnsubscribe:
			err = h.managedHandleUnsubscribeRequest(info, pt)
		case modules.SubscriptionRequestExtend:
			deadline, err = h.managedHandleExtendSubscriptionRequest(stream, subscriptions, deadline, pt, info)
		case modules.SubscriptionRequestPrepay:
			pt, err = h.managedHandlePrepayBandwidth(stream, info, bandwidthLimit)
		default:
			return errors.New("unknown request type")
		}
		// Check the errors.
		if err != nil {
			return errors.AddContext(err, "failed to handle request")
		}
	}
}

// sendNotification marshals a entry notification and writes it to the provided
// writer.
func sendNotification(stream io.Writer, rv modules.SignedRegistryValue) error {
	buf := new(bytes.Buffer)
	err := modules.RPCWrite(buf, modules.RPCRegistrySubscriptionNotificationType{
		Type: modules.SubscriptionResponseRegistryValue,
	})
	if err != nil {
		return errors.AddContext(err, "failed to write notification header to buffer")
	}
	err = modules.RPCWrite(buf, modules.RPCRegistrySubscriptionNotificationEntryUpdate{
		Entry: rv,
	})
	if err != nil {
		return errors.AddContext(err, "failed to write entry to buffer")
	}
	_, err = buf.WriteTo(stream)
	if err != nil {
		return errors.AddContext(err, "failed to write notification to stream")
	}
	return nil
}
