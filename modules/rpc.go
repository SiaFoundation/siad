package modules

import (
	"encoding/json"

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

// MarshalJSON defines a JSON encoding for the RPC price table.
// Tested in rpcupdatepricetable_test.go
func (pt RPCPriceTable) MarshalJSON() ([]byte, error) {
	// convert the map to an array of cost entries
	converted := struct {
		Expiry uint64
		Costs  []costEntry
	}{
		Expiry: uint64(pt.Expiry),
		Costs:  make([]costEntry, len(pt.Costs)),
	}

	// range over the costs and add them
	index := 0
	for r, c := range pt.Costs {
		converted.Costs[index] = costEntry{ID: r, Cost: c}
		index++
	}

	// marshal the converted price table object
	return json.Marshal(converted)
}

// UnmarshalJSON attempts to decode an RPC price table.
// Tested in rpcupdatepricetable_test.go
func (pt *RPCPriceTable) UnmarshalJSON(b []byte) error {
	// initialize the costs map on the price table if necessary
	if pt.Costs == nil {
		pt.Costs = make(map[types.Specifier]types.Currency)
	}

	// unmarshal the given byte slice into a converted price table object
	converted := struct {
		Expiry uint64
		Costs  []costEntry
	}{}
	if err := json.Unmarshal(b, &converted); err != nil {
		return err
	}

	// copy over the data to the price table
	pt.Expiry = types.BlockHeight(converted.Expiry)
	for _, e := range converted.Costs {
		pt.Costs[e.ID] = e.Cost
	}

	return nil
}
