package renter

import (
	"gitlab.com/NebulousLabs/Sia/types"
)

// RPCClient interface lists all possible RPC that can be called on the host
type RPCClient interface {
	UpdatePriceTable() error
	FundEphemeralAccount(id string, amount types.Currency) error
}

// MockRPCClient is a temporary mock object of the RPC Client.
// TODO: replace
type MockRPCClient struct{}

// UpdatePriceTable updates the price table
func (m *MockRPCClient) UpdatePriceTable() error { return nil }

// FundEphemeralAccount funds the given ephemeral account by given amount
func (m *MockRPCClient) FundEphemeralAccount(id string, amount types.Currency) error { return nil }
