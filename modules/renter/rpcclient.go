package renter

// TODO: The RPC client is used by the worker to interact with the host. It
// holds the RPC price table and can be seen as a renter RPC session. For now
// this is extracted in a separate object, quite possible though this state will
// move to the worker, and the RPCs will be exposed as static functions,
// callable by the worker.

import (
	"encoding/json"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/siamux"
)

// rpcClient wraps all necessities to communicate with a host
type rpcClient struct {
	staticHostAddress   string
	staticRefundAccount modules.AccountID

	// The current block height is cached on the client and gets updated by the
	// renter when the consensus changes. This to avoid fetching the block
	// height from the renter on every RPC call.
	blockHeight types.BlockHeight

	mu sync.Mutex
	r  *Renter
}

// newRPCClient returns a new RPC client.
func (r *Renter) newRPCClient(he modules.HostDBEntry, ra modules.AccountID, bh types.BlockHeight) *rpcClient {
	return &rpcClient{
		staticHostAddress:   string(he.NetAddress),
		staticRefundAccount: ra,
		blockHeight:         bh,
		r:                   r,
	}
}

// UpdatePriceTable performs the updatePriceTableRPC on the host.
func (c *rpcClient) UpdatePriceTable(pp modules.PaymentProvider, stream siamux.Stream) (*modules.RPCPriceTable, error) {
	// write the specifier
	err := modules.RPCWrite(stream, modules.RPCUpdatePriceTable)
	if err != nil {
		return nil, err
	}

	// receive the price table
	var uptr modules.RPCUpdatePriceTableResponse
	if err := modules.RPCRead(stream, &uptr); err != nil {
		return nil, err
	}
	var pt modules.RPCPriceTable
	if err := json.Unmarshal(uptr.PriceTableJSON, &pt); err != nil {
		return nil, err
	}

	// perform gouging check
	allowance := c.r.hostContractor.Allowance()
	if err := checkPriceTableGouging(allowance, pt); err != nil {
		// TODO: (follow-up) this should negatively affect the host's score
		return nil, err
	}

	// provide payment for the RPC
	err = pp.ProvidePayment(stream, modules.RPCUpdatePriceTable, pt.UpdatePriceTableCost, c.staticRefundAccount, c.managedBlockHeight())
	if err != nil {
		return nil, err
	}

	return &pt, nil
}

// FundAccount will call the fundAccountRPC on the host and if successful will
// deposit the given amount into the specified ephemeral account.
func (c *rpcClient) FundAccount(pp modules.PaymentProvider, stream siamux.Stream, pt modules.RPCPriceTable, id modules.AccountID, amount types.Currency) error {
	// send all necessary request objects, this consists out of the rpc
	// identifier, the price table identifier and the actual rpc request
	err := modules.RPCWriteAll(stream, modules.RPCFundAccount, pt.UID, modules.FundAccountRequest{Account: id})
	if err != nil {
		return err
	}

	// provide payment
	payment := amount.Add(pt.FundAccountCost)
	err = pp.ProvidePayment(stream, modules.RPCFundAccount, payment, c.staticRefundAccount, c.managedBlockHeight())
	if err != nil {
		return err
	}

	// read response
	var fundAccResponse modules.FundAccountResponse
	return modules.RPCRead(stream, &fundAccResponse)
}

// UpdateBlockHeight is called by the renter when it processes a consensus
// change. The RPC client keeps the current block height as state to avoid
// fetching it from the renter on every RPC call.
func (c *rpcClient) UpdateBlockHeight(bh types.BlockHeight) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blockHeight = bh
}

// managedBlockHeight returns the cached blockheight
func (c *rpcClient) managedBlockHeight() types.BlockHeight {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.blockHeight
}

// checkPriceTableGouging checks that the host is not gouging the renter during
// a price table update.
func checkPriceTableGouging(allowance modules.Allowance, priceTable modules.RPCPriceTable) error {
	// TODO
	return nil
}
