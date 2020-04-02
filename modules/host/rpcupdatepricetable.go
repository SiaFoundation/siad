package host

import (
	"encoding/json"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
)

// staticRPCUpdatePriceTable returns a copy of the host's current rpc price
// table. These prices are valid for the duration of the
// rpcPriceGuaranteePeriod, which is defined by the price table's Expiry
func (h *Host) staticRPCUpdatePriceTable(stream siamux.Stream) error {
	// copy the host's price table
	pt := h.staticPriceTables.managedCurrent()

	// generate a random.UID
	var newUID modules.UniqueID
	fastrand.Read(newUID[:])
	pt.UID = newUID

	// update the epxiry to ensure prices are guaranteed for the duration of the
	// rpcPriceGuaranteePeriod
	pt.Expiry = time.Now().Add(rpcPriceGuaranteePeriod).Unix()

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

	// Note that we have sent the price table before processing payment for this
	// RPC. This allows the renter to check for price gouging and close out the
	// stream if it does not agree with pricing. The price table has not yet
	// been added to the map, which means that the renter has to pay for it in
	// order for it to became active and accepted by the host.

	payment, err := h.ProcessPayment(stream)
	if err != nil {
		return errors.AddContext(err, "Failed to process payment")
	}
	if payment.Amount().Cmp(pt.UpdatePriceTableCost) < 0 {
		return modules.ErrInsufficientPaymentForRPC
	}

	// after payment has been received, track the price table in the host's list
	// of price tables
	h.staticPriceTables.managedTrack(&pt)

	return nil
}
