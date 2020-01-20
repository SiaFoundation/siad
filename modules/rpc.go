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

	// costEntry is a helper struct used when marshaling the RPC price table
	costEntry struct {
		ID   types.Specifier
		Cost types.Currency
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

// NewRPCPriceTable returns an empty RPC price table
func NewRPCPriceTable() RPCPriceTable {
	return RPCPriceTable{
		Costs: make(map[types.Specifier]types.Currency),
	}
}
