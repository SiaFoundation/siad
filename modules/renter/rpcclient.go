package renter

import (
	"gitlab.com/NebulousLabs/Sia/types"
)

// TODO: The RPC client is used by the worker to interact with the host. It
// holds the RPC price table and can be seen as a renter RPC session. For now
// this is extracted in a separate object, quite possible though this state will
// move to the worker, and the RPCs will be exposed as static functions,
// callable by the worker.

// RPCClient interface lists all possible RPC that can be called on the host
type RPCClient interface {
	UpdatePriceTable() error
	FundEphemeralAccount(id string, amount types.Currency) error
}

// MockRPCClient mocks the RPC Client
type MockRPCClient struct{}

// UpdatePriceTable updates the price table
func (m *MockRPCClient) UpdatePriceTable() error { return nil }

// FundEphemeralAccount funds the given ephemeral account by given amount
func (m *MockRPCClient) FundEphemeralAccount(id string, amount types.Currency) error {
	return nil
}
