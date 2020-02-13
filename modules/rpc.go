package modules

import (
	"gitlab.com/NebulousLabs/Sia/types"
)

// RPCPriceTable contains the cost of executing a RPC on a host. Each host can
// set its own prices for the individual MDM instructions and RPC costs.
type RPCPriceTable struct {
	// Expiry is a unix timestamp that specifies the time until which the
	// MDMCostTable is valid.
	Expiry int64 `json:"expiry"`

	// UpdatePriceTableCost refers to the cost of fetching a new price table
	// from the host.
	UpdatePriceTableCost types.Currency `json:"updatepricetablecost"`

	// MDM related costs
	//
	// InitBaseCost is the amount of cost that is incurred when an MDM program
	// starts to run. This doesn't include the memory used by the program data.
	// The total cost to initialize a program is calculated as
	// InitCost = InitiBaseCost + MemoryTimeCost * Time
	InitBaseCost types.Currency `json:"initbasecost"`

	// MemoryTimeCost is the amount of cost per byte per time that is incurred
	// by the memory consumption of the program.
	MemoryTimeCost types.Currency `json:"memorytimecost"`

	// Cost values specific to the Read instruction.
	ReadBaseCost   types.Currency `json:"readbasecost"`
	ReadLengthCost types.Currency `json:"readlengthcost"`

	// Cost values specific to the Write instruction.
	WriteBaseCost   types.Currency `json:"writebasecost"`
	WriteLengthCost types.Currency `json:"writelengthcost"`
}

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
