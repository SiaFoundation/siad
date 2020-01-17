package modules

import (
	"gitlab.com/NebulousLabs/Sia/types"
)

type (
	// RPCPriceTable contains the cost of every RPC the host offers. These
	// prices are guaranteed to remain valid up until the specified expiry block
	// height.
	RPCPriceTable struct {
		Costs  map[types.Specifier]types.Currency
		Expiry types.BlockHeight
	}
)

var (
	// RPCUpdatePriceTable specifier
	RPCUpdatePriceTable = types.NewSpecifier("UpdatePriceTable")
)

type (
	// RPCUpdatePriceTableResponse contains a JSON encoded RPC price table
	RPCUpdatePriceTableResponse struct {
		PriceTableJSON []byte
	}
)
