package host

import (
	"encoding/json"
	"fmt"
	"net"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// managedRPCUpdatePriceTable handles the RPC request from the renter to fetch
// the host's latest RPC price table.
func (h *Host) managedRPCUpdatePriceTable(stream net.Conn) (update modules.RPCPriceTable, err error) {
	h.mu.RLock()
	pt := h.priceTable
	h.mu.RUnlock()

	// take a snapshot of the host's price table
	encoded, err := json.Marshal(pt)
	if err != nil {
		errors.AddContext(err, "Failed to JSON encode the price table")
		return
	}
	_ = json.Unmarshal(encoded, &update)

	// encode it as JSON and send it to the renter. Note that we send the price
	// table before we process payment. This allows the renter to close the
	// stream if it decides the host's gouging the prices.
	uptResponse := modules.RPCUpdatePriceTableResponse{PriceTableJSON: encoded}
	if err = encoding.WriteObject(stream, uptResponse); err != nil {
		errors.AddContext(err, "Failed to write response")
		return
	}

	// TODO: process payment for this RPC call (introduced in other MR)
	amountPaid := types.ZeroCurrency

	// verify the renter payment was sufficient, since the renter already has
	// the updated prices, we expect he will have paid the latest price
	expected := update.Costs[modules.RPCUpdatePriceTable]
	if amountPaid.Cmp(expected) < 0 {
		errors.AddContext(modules.ErrInsufficientPaymentForRPC, fmt.Sprintf("The renter did not supply sufficient payment to cover the cost of the  UpdatePriceTableRPC. Expected: %v Actual: %v", expected.HumanString(), amountPaid.HumanString()))
		return
	}
	return
}

// managedCalculateUpdatePriceTableRPCPrice calculates the price for the
// UpdatePriceTableRPC. The price can be dependant on numerous factors.
// Note: for now this is a fixed cost equaling the base RPC price.
func (h *Host) managedCalculateUpdatePriceTableRPCPrice() types.Currency {
	hIS := h.InternalSettings()
	return hIS.MinBaseRPCPrice
}
