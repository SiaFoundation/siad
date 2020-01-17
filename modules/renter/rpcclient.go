package renter

import (
	"gitlab.com/NebulousLabs/Sia/types"
)

// RPCClient interface lists all possible RPC that can be called on the host
type RPCClient interface {
	UpdatePriceTable() error
	FundEphemeralAccount(id string, amount types.Currency) error
}
