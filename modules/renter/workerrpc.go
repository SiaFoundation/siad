package renter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"gitlab.com/NebulousLabs/ratelimit"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/siamux/mux"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"

	"gitlab.com/NebulousLabs/errors"
)

// defaultNewStreamTimeout is a default timeout for creating a new stream.
var defaultNewStreamTimeout = build.Select(build.Var{
	Standard: 5 * time.Minute,
	Testnet:  5 * time.Minute,
	Testing:  10 * time.Second,
	Dev:      time.Minute,
}).(time.Duration)

// defaultRPCDeadline is a default timeout for executing an RPC.
var defaultRPCDeadline = build.Select(build.Var{
	Standard: 5 * time.Minute,
	Testnet:  5 * time.Minute,
	Testing:  10 * time.Second,
	Dev:      time.Minute,
}).(time.Duration)

var (
	// renewGougingFeeMultiplier is the acceptable multiple by which the fee
	// estimation of the host may differ from the renter's.
	renewGougingFeeMultiplier = types.NewCurrency64(5)
)

// programResponse is a helper struct that wraps the RPCExecuteProgramResponse
// alongside the data output
type programResponse struct {
	modules.RPCExecuteProgramResponse
	Output []byte
}

// managedExecuteProgram performs the ExecuteProgramRPC on the host
func (w *worker) managedExecuteProgram(p modules.Program, data []byte, fcid types.FileContractID, category spendingCategory, cost types.Currency) (responses []programResponse, limit mux.BandwidthLimit, err error) {
	// Defer a function that schedules a price table update in case we received
	// an error that indicates the host deems our price table invalid.
	defer func() {
		if modules.IsPriceTableInvalidErr(err) {
			w.staticTryForcePriceTableUpdate()
		}
	}()

	// track the withdrawal
	var refund types.Currency
	w.staticAccount.managedTrackWithdrawal(cost)
	defer func() {
		withdrawn := cost.Sub(refund)
		w.staticAccount.managedCommitWithdrawal(category, withdrawn, refund, err == nil)
	}()

	// create a new stream
	stream, err := w.staticNewStream()
	if err != nil {
		err = errors.AddContext(err, "Unable to create a new stream")
		return
	}
	defer func() {
		if err := stream.Close(); err != nil {
			w.renter.log.Println("ERROR: failed to close stream", err)
		}
	}()

	// set the limit return var.
	limit = stream.Limit()

	// prepare a buffer so we can optimize our writes
	buffer := bytes.NewBuffer(nil)

	// write the specifier
	err = modules.RPCWrite(buffer, modules.RPCExecuteProgram)
	if err != nil {
		return
	}

	// send price table uid
	pt := w.staticPriceTable().staticPriceTable
	err = modules.RPCWrite(buffer, pt.UID)
	if err != nil {
		return
	}

	// provide payment, note that we use the host's block height if we are
	// making ephemeral account payments
	bh := pt.HostBlockHeight
	err = w.staticAccount.ProvidePayment(buffer, cost, bh)

	// prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    fcid,
		Program:           p,
		ProgramDataLength: uint64(len(data)),
	}

	// send the execute program request.
	err = modules.RPCWrite(buffer, epr)
	if err != nil {
		return
	}

	// send the programData.
	_, err = buffer.Write(data)
	if err != nil {
		return
	}

	// write contents of the buffer to the stream
	_, err = stream.Write(buffer.Bytes())
	if err != nil {
		return
	}

	// read the cancellation token.
	var ct modules.MDMCancellationToken
	err = modules.RPCRead(stream, &ct)
	if err != nil {
		return
	}

	// read the responses.
	responses = make([]programResponse, 0, len(epr.Program))
	for i := 0; i < len(epr.Program); i++ {
		var response programResponse
		err = modules.RPCRead(stream, &response)
		if err != nil {
			return
		}

		// Read the output data.
		outputLen := response.OutputLength
		response.Output = make([]byte, outputLen)
		_, err = io.ReadFull(stream, response.Output)
		if err != nil {
			return
		}

		refund = refund.Add(response.FailureRefund)

		// We received a valid response. Append it.
		responses = append(responses, response)

		// If the response contains an error we are done.
		if response.Error != nil {
			break
		}
	}
	return
}

// staticNewStream returns a new stream to the worker's host
func (w *worker) staticNewStream() (siamux.Stream, error) {
	// If disrupt is called we sleep for the specified 'defaultNewStreamTimeout'
	// simulating how an unreachable host would behave in production.
	timeout := defaultNewStreamTimeout
	if w.renter.deps.Disrupt("InterruptNewStreamTimeout") {
		time.Sleep(timeout)
		return nil, errors.New("InterruptNewStreamTimeout")
	}

	// Create a stream with a reasonable dial up timeout.
	stream, err := w.renter.staticMux.NewStreamTimeout(modules.HostSiaMuxSubscriberName, w.staticCache().staticHostMuxAddress, timeout, modules.SiaPKToMuxPK(w.staticHostPubKey))
	if err != nil {
		return nil, err
	}
	// Set deadline on the stream.
	err = stream.SetDeadline(time.Now().Add(defaultRPCDeadline))
	if err != nil {
		return nil, err
	}

	// Wrap the stream in the renter's ratelimit
	//
	// NOTE: this only ratelimits the data going over the stream and not the raw
	// bytes going over the wire, so the ratelimit might be off by a few bytes.
	rlStream := ratelimit.NewRLStream(stream, w.renter.rl, w.renter.tg.StopChan())

	// Wrap the stream in global ratelimit.
	return ratelimit.NewRLStream(rlStream, modules.GlobalRateLimits, w.renter.tg.StopChan()), nil
}

// managedRenew renews the contract with the worker's host.
func (w *worker) managedRenew(fcid types.FileContractID, params modules.ContractParams, txnBuilder modules.TransactionBuilder) (_ modules.RenterContract, _ []types.Transaction, err error) {
	// Defer a function that schedules a price table update in case we received
	// an error that indicates the host deems our price table invalid.
	defer func() {
		if modules.IsPriceTableInvalidErr(err) {
			w.staticTryForcePriceTableUpdate()
		}
	}()

	// create a new stream
	stream, err := w.staticNewStream()
	if err != nil {
		return modules.RenterContract{}, nil, errors.AddContext(err, "managedRenew: unable to create a new stream")
	}
	defer func() {
		if err := stream.Close(); err != nil {
			w.renter.log.Println("managedRenew: failed to close stream", err)
		}
	}()

	// write the specifier.
	err = modules.RPCWrite(stream, modules.RPCRenewContract)
	if err != nil {
		return modules.RenterContract{}, nil, errors.AddContext(err, "managedRenew: failed to write RPC specifier")
	}

	// send price table uid
	pt := w.staticPriceTable().staticPriceTable
	err = modules.RPCWrite(stream, pt.UID)
	if err != nil {
		return modules.RenterContract{}, nil, errors.AddContext(err, "managedRenew: failed to write price table uid")
	}

	// if the price table we sent contained a zero uid, we receive a temporary
	// one.
	if pt.UID == (modules.UniqueID{}) {
		var ptr modules.RPCUpdatePriceTableResponse
		err = modules.RPCRead(stream, &ptr)
		if err != nil {
			return modules.RenterContract{}, nil, errors.AddContext(err, "managedRenew: failed to fetch temporary price table")
		}
		err = json.Unmarshal(ptr.PriceTableJSON, &pt)
		if err != nil {
			return modules.RenterContract{}, nil, errors.AddContext(err, "managedRenew: failed to unmarshal temporary price table")
		}
	}

	// price table gouging check. The cost for renewing the price table is
	// currently hardcoded in the host. So we simply check for that value.
	if pt.RenewContractCost.Cmp(modules.DefaultBaseRPCPrice) > 0 {
		return modules.RenterContract{}, nil, fmt.Errorf("managedRenew: price table renew contract cost gouging %v > %v", pt.RenewContractCost, modules.DefaultBaseRPCPrice)
	}
	// For the txn fee estimate take we use a constant multiple of our own
	// expectation.
	min, max := w.renter.tpool.FeeEstimation()
	if pt.TxnFeeMinRecommended.Cmp(min.Mul(renewGougingFeeMultiplier)) > 0 {
		return modules.RenterContract{}, nil, fmt.Errorf("managedRenew: price table txn fee min gouging %v > %v", pt.TxnFeeMinRecommended, min.Mul(renewGougingFeeMultiplier))
	}
	if pt.TxnFeeMaxRecommended.Cmp(max.Mul(renewGougingFeeMultiplier)) > 0 {
		return modules.RenterContract{}, nil, fmt.Errorf("managedRenew: price table txn fee max gouging %v > %v", pt.TxnFeeMaxRecommended, max.Mul(renewGougingFeeMultiplier))
	}
	// Check blockheight.
	if !hostBlockHeightWithinTolerance(w.staticCache().staticSynced, w.staticCache().staticBlockHeight, pt.HostBlockHeight) {
		return modules.RenterContract{}, nil, errors.AddContext(errHostBlockHeightNotWithinTolerance, fmt.Sprintf("managedRenew failed pt height gouging: renter height: %v synced: %v, host height: %v", w.staticCache().staticBlockHeight, w.staticCache().staticSynced, pt.HostBlockHeight))
	}

	// have the contractset handle the renewal.
	r := w.renter
	newContract, txnSet, err := w.renter.hostContractor.RenewContract(stream, fcid, params, txnBuilder, r.tpool, r.hostDB, &pt)
	if err != nil {
		return modules.RenterContract{}, nil, errors.AddContext(err, "managedRenew: call to RenewContract failed")
	}
	return newContract, txnSet, nil
}
