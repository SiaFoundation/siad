package renter

import (
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TODO: The RPC client is used by the worker to interact with the host. It
// holds the RPC price table and can be seen as a renter RPC session. For now
// this is extracted in a separate object, quite possible though this state will
// move to the worker, and the RPCs will be exposed as static functions,
// callable by the worker.

// RPCClient interface lists all possible RPC that can be called on the host
type RPCClient interface {
	// UpdatePriceTable updates the price table.
	UpdatePriceTable() error
	// FundEphemeralAccount funds the given ephemeral account by given amount.
	FundEphemeralAccount(id modules.AccountID, amount types.Currency) error
}

// MockRPCClient mocks the RPC Client
type MockRPCClient struct{}

// FundEphemeralAccount funds the given ephemeral account by given amount.
func (m *MockRPCClient) FundEphemeralAccount(id modules.AccountID, amount types.Currency) error {
	return nil
}

// UpdatePriceTable updates the price table.
func (m *MockRPCClient) UpdatePriceTable() error { return nil }
