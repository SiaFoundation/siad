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
func (h *Host) managedRPCUpdatePriceTable(stream net.Conn) error {
	h.mu.RLock()
	pt := h.priceTable
	h.mu.RUnlock()

	// json encode the RPC price table
	ptBytes, err := json.Marshal(pt)
	if err != nil {
		return errors.AddContext(err, "Failed to JSON encode the RPC price table")
	}

	// send it to the renter, note we send it before we process payment, this
	// allows the renter to close the stream if it decides the host is gouging
	// the price
	uptResponse := modules.RPCUpdatePriceTableResponse{PriceTableJSON: ptBytes}
	if err = encoding.WriteObject(stream, uptResponse); err != nil {
		return errors.AddContext(err, "Failed to write response")
	}

	// TODO: process payment for this RPC call (introduced in other MR)
	amountPaid := pt.Costs[modules.RPCUpdatePriceTable]

	// verify the renter payment was sufficient, since the renter already has
	// the updated prices, we expect it will have paid the latest price
	expected := pt.Costs[modules.RPCUpdatePriceTable]
	if amountPaid.Cmp(expected) < 0 {
		return errors.AddContext(modules.ErrInsufficientPaymentForRPC, fmt.Sprintf("The renter did not supply sufficient payment to cover the cost of the  UpdatePriceTableRPC. Expected: %v Actual: %v", expected.HumanString(), amountPaid.HumanString()))
	}

	return nil
}

// managedCalculateUpdatePriceTableRPCPrice calculates the price for the
// UpdatePriceTableRPC. The price can be dependant on numerous factors.
// Note: for now this is a fixed cost equaling the base RPC price.
func (h *Host) managedCalculateUpdatePriceTableRPCPrice() types.Currency {
	hIS := h.InternalSettings()
	return hIS.MinBaseRPCPrice
}
