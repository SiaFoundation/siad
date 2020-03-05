package host

import (
	"encoding/json"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
)

// managedRPCUpdatePriceTable returns a copy of the host's current rpc price
// table. These prices are valid for the duration of the
// rpcPriceGuaranteePeriod, which is defined by the price table's Expiry
func (h *Host) managedRPCUpdatePriceTable(stream siamux.Stream) error {
	pt := &modules.RPCPriceTable{}

	// deep copy the current price table and track it using its UUID
	if err := func() error {
		h.mu.Lock()
		defer h.mu.Unlock()

		ptBytes, err := json.Marshal(h.priceTable)
		if err != nil {
			return err
		}
		err = json.Unmarshal(ptBytes, &pt)
		if err != nil {
			return err
		}
		pt.Expiry = time.Now().Add(rpcPriceGuaranteePeriod).Unix()
		h.priceTableMap[pt.UUID] = pt
		return nil
	}(); err != nil {
		return errors.AddContext(err, "Failed to copy the host price table")
	}

	// json encode the price table
	ptBytes, err := json.Marshal(pt)
	if err != nil {
		return errors.AddContext(err, "Failed to JSON encode the price table")
	}

	// send it to the renter
	uptResp := modules.RPCUpdatePriceTableResponse{PriceTableJSON: ptBytes}
	if err = modules.RPCWrite(stream, uptResp); err != nil {
		return errors.AddContext(err, "Failed to write response")
	}

	// Note: we have sent the price table before processing payment for this
	// RPC. This allows the renter to check for price gouging and close out the
	// stream if it does not agree with pricing. After this the host processes
	// payment, and the renter will pay for the RPC according to the price it
	// just received. This essentially means the host is optimistically sending
	// over the price table, which is ok.

	// TODO: enable when the PaymentProcessor gets introduced
	// process payment
	// pp := h.NewPaymentProcessor()
	// amountPaid, err := pp.ProcessPaymentForRPC(stream)
	// if err != nil {
	// 	return errors.AddContext(err, "Failed to process payment")
	// }
	// verify payment
	// expected := pt.UpdatePriceTableCost
	// if amountPaid.Cmp(expected) < 0 {
	// 	return errors.AddContext(modules.ErrInsufficientPaymentForRPC, fmt.Sprintf("The renter did not supply sufficient payment to cover the cost of the  UpdatePriceTableRPC. Expected: %v Actual: %v", expected.HumanString(), amountPaid.HumanString()))
	// }

	return nil
}
