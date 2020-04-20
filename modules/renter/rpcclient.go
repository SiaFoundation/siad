package renter

// TODO: the rpcclient code can be merged into the worker code, it is mostly
// static except for the blockheight which is passed by the worker anyway

import (
	"encoding/json"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/siamux"
)

// rpcClient is a helper struct that is capable of performing all of the RPCs on
// the host. Note that functionality of this struct will get extended as we add
// on more and more RPCs that support the new renter-host protocol.
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

// ExecuteProgram performs the ExecuteProgramRPC on the host
func (c *rpcClient) ExecuteProgram(pp modules.PaymentProvider, stream siamux.Stream, pt *modules.RPCPriceTable, p modules.Program, data []byte, fcid types.FileContractID, cost types.Currency) ([]modules.RPCExecuteProgramResponse, error) {
	// write the specifier
	err := modules.RPCWrite(stream, modules.RPCExecuteProgram)
	if err != nil {
		return nil, err
	}

	// send price table uid
	err = modules.RPCWrite(stream, pt.UID)
	if err != nil {
		return nil, err
	}

	// prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    fcid,
		Program:           p,
		ProgramDataLength: uint64(len(data)),
	}

	// provide payment
	// TODO: NTH cost := program.EstimateCost(data, dataLen)
	err = pp.ProvidePayment(stream, modules.RPCUpdatePriceTable, cost, c.staticRefundAccount, c.managedBlockHeight())
	if err != nil {
		return nil, err
	}

	// send the execute program request.
	err = modules.RPCWrite(stream, epr)
	if err != nil {
		return nil, err
	}

	// send the programData.
	_, err = stream.Write(data)
	if err != nil {
		return nil, err
	}

	// read the responses.
	responses := make([]modules.RPCExecuteProgramResponse, len(epr.Program))
	for i := range responses {
		err = modules.RPCRead(stream, &responses[i])
		if err != nil {
			return nil, err
		}
		if responses[i].Error != nil {
			return nil, responses[i].Error
		}
	}

	return responses, nil
}

// UpdatePriceTable performs the UpdatePriceTableRPC on the host.
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

	// TODO: perform gouging check
	// TODO: (follow-up) this should negatively affect the host's score

	// provide payment
	err = pp.ProvidePayment(stream, modules.RPCUpdatePriceTable, pt.UpdatePriceTableCost, c.staticRefundAccount, c.managedBlockHeight())
	if err != nil {
		return nil, err
	}

	return &pt, nil
}

// FundAccount will call the fundAccountRPC on the host and if successful will
// deposit the given amount into the specified ephemeral account.
func (c *rpcClient) FundAccount(pp modules.PaymentProvider, stream siamux.Stream, pt *modules.RPCPriceTable, id modules.AccountID, amount types.Currency) (*modules.FundAccountResponse, error) {
	// write the specifier
	err := modules.RPCWrite(stream, modules.RPCFundAccount)
	if err != nil {
		return nil, err
	}

	// send price table uid
	err = modules.RPCWrite(stream, pt.UID)
	if err != nil {
		return nil, err
	}

	// send fund account request
	err = modules.RPCWrite(stream, modules.FundAccountRequest{Account: id})
	if err != nil {
		return nil, err
	}

	// provide payment
	err = pp.ProvidePayment(stream, modules.RPCFundAccount, amount.Add(pt.FundAccountCost), modules.ZeroAccountID, c.managedBlockHeight())
	if err != nil {
		return nil, err
	}

	// receive FundAccountResponse
	var resp modules.FundAccountResponse
	err = modules.RPCRead(stream, &resp)
	if err != nil {
		return nil, err
	}

	return &resp, nil
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
