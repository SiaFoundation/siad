package renter

// TODO: the rpcclient code can be merged into the worker code, it is mostly
// static except for the blockheight which is passed by the worker anyway

import (
	"encoding/json"
	"io"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
)

// rpcClient is a helper struct that is capable of performing all of the RPCs on
// the host. Note that functionality of this struct will get extended as we add
// on more and more RPCs that support the new renter-host protocol.
type rpcClient struct {
	staticHostAddress   string
	staticHostKey       types.SiaPublicKey
	staticRefundAccount modules.AccountID

	// The current block height is cached on the client and gets updated by the
	// renter when the consensus changes. This to avoid fetching the block
	// height from the renter on every RPC call.
	blockHeight types.BlockHeight

	mu sync.Mutex
}

// newRPCClient returns a new RPC client.
func newRPCClient(hostEntry modules.HostDBEntry, refundAccount modules.AccountID, blockHeight types.BlockHeight) *rpcClient {
	return &rpcClient{
		staticHostAddress:   string(hostEntry.NetAddress),
		staticHostKey:       hostEntry.PublicKey,
		staticRefundAccount: refundAccount,
		blockHeight:         blockHeight,
	}
}

// programResponse is a helper struct that wraps the RPCExecuteProgramResponse
// alongside the data output
type programResponse struct {
	modules.RPCExecuteProgramResponse
	Output []byte
}

// ExecuteProgram performs the ExecuteProgramRPC on the host
func (c *rpcClient) ExecuteProgram(pp modules.PaymentProvider, stream siamux.Stream, pt *modules.RPCPriceTable, p modules.Program, data []byte, fcid types.FileContractID, cost types.Currency) (responses []programResponse, err error) {
	// close the stream
	defer func() {
		cErr := stream.Close()
		if cErr != nil {
			err = errors.Compose(err, cErr)
			responses = nil
		}
	}()

	// write the specifier
	err = modules.RPCWrite(stream, modules.RPCExecuteProgram)
	if err != nil {
		return
	}

	// send price table uid
	err = modules.RPCWrite(stream, pt.UID)
	if err != nil {
		return
	}

	// prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    fcid,
		Program:           p,
		ProgramDataLength: uint64(len(data)),
	}

	// provide payment
	err = pp.ProvidePayment(stream, c.staticHostKey, modules.RPCUpdatePriceTable, cost, c.staticRefundAccount, c.managedBlockHeight())
	if err != nil {
		return
	}

	// send the execute program request.
	err = modules.RPCWrite(stream, epr)
	if err != nil {
		return
	}

	// send the programData.
	_, err = stream.Write(data)
	if err != nil {
		return
	}

	// read the responses.
	responses = make([]programResponse, len(epr.Program))
	for i := range responses {
		err = modules.RPCRead(stream, &responses[i])
		if err != nil {
			return
		}

		// Read the output data.
		outputLen := responses[i].OutputLength
		responses[i].Output = make([]byte, outputLen, outputLen)
		_, err = io.ReadFull(stream, responses[i].Output)
		if err != nil {
		}

		// If the response contains an error we are done.
		if responses[i].Error != nil {
			return
		}
	}
	return
}

// FundAccount will call the fundAccountRPC on the host and if successful will
// deposit the given amount into the specified ephemeral account.
func (c *rpcClient) FundAccount(pp modules.PaymentProvider, stream siamux.Stream, pt modules.RPCPriceTable, id modules.AccountID, amount types.Currency) (resp modules.FundAccountResponse, err error) {
	// close the stream
	defer func() {
		cErr := stream.Close()
		if cErr != nil {
			err = errors.Compose(err, cErr)
			resp = modules.FundAccountResponse{}
		}
	}()

	// write the specifier
	err = modules.RPCWrite(stream, modules.RPCFundAccount)
	if err != nil {
		return
	}

	// send price table uid
	err = modules.RPCWrite(stream, pt.UID)
	if err != nil {
		return
	}

	// send fund account request
	err = modules.RPCWrite(stream, modules.FundAccountRequest{Account: id})
	if err != nil {
		return
	}

	// provide payment
	err = pp.ProvidePayment(stream, c.staticHostKey, modules.RPCFundAccount, amount.Add(pt.FundAccountCost), modules.ZeroAccountID, c.managedBlockHeight())
	if err != nil {
		return
	}

	// receive FundAccountResponse
	err = modules.RPCRead(stream, &resp)
	return
}

// UpdatePriceTable performs the UpdatePriceTableRPC on the host.
func (c *rpcClient) UpdatePriceTable(pp modules.PaymentProvider, stream siamux.Stream) (pt modules.RPCPriceTable, err error) {
	// close the stream
	defer func() {
		cErr := stream.Close()
		if cErr != nil {
			err = errors.Compose(err, cErr)
			pt = modules.RPCPriceTable{}
		}
	}()

	// write the specifier
	err = modules.RPCWrite(stream, modules.RPCUpdatePriceTable)
	if err != nil {
		return
	}

	// receive the price table
	var uptr modules.RPCUpdatePriceTableResponse
	err = modules.RPCRead(stream, &uptr)
	if err != nil {
		return
	}

	// decode the JSON
	err = json.Unmarshal(uptr.PriceTableJSON, &pt)
	if err != nil {
		return
	}

	// TODO: perform gouging check
	// TODO: (follow-up) this should negatively affect the host's score

	// provide payment
	err = pp.ProvidePayment(stream, c.staticHostKey, modules.RPCUpdatePriceTable, pt.UpdatePriceTableCost, c.staticRefundAccount, c.managedBlockHeight())
	return
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
