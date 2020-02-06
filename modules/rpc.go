package modules

import (
	"gitlab.com/NebulousLabs/Sia/types"
)

type (
	// RPCPriceTable contains a list of RPC costs to remain vaild up until the
	// specified expiry timestamp.
	RPCPriceTable struct {
		Costs  map[types.Specifier]types.Currency
		Expiry int64
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
func NewRPCPriceTable(expiry int64) RPCPriceTable {
	return RPCPriceTable{
		Expiry: expiry,
		Costs:  make(map[types.Specifier]types.Currency),
	}
}
