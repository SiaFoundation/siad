package renter

import (
	"encoding/json"
	"io"
	"strings"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
)

// programResponse is a helper struct that wraps the RPCExecuteProgramResponse
// alongside the data output
type programResponse struct {
	modules.RPCExecuteProgramResponse
	Output []byte
}

// managedExecuteProgram performs the ExecuteProgramRPC on the host
func (w *worker) managedExecuteProgram(p modules.Program, data []byte, fcid types.FileContractID, cost types.Currency) (responses []programResponse, err error) {
	// check host version
	if build.VersionCmp(w.staticHostVersion, modules.MinimumSupportedNewRenterHostProtocolVersion) < 0 {
		build.Critical("Executing new RHP RPC on host with version", w.staticHostVersion)
	}

	// track the withdrawal
	// TODO: this is very naive and does not consider refunds at all
	w.staticAccount.managedTrackWithdrawal(cost)
	defer func() {
		w.staticAccount.managedCommitWithdrawal(cost, err == nil)
	}()

	// create a new stream
	var stream siamux.Stream
	stream, err = w.staticNewStream()
	if err != nil {
		err = errors.AddContext(err, "Unable to create a new stream")
		return
	}
	defer func() {
		if err := stream.Close(); err != nil {
			w.renter.log.Println("ERROR: failed to close stream", err)
		}
	}()

	// grab some variables from the worker
	w.mu.Lock()
	bh := w.cachedBlockHeight
	w.mu.Unlock()

	// write the specifier
	err = modules.RPCWrite(stream, modules.RPCExecuteProgram)
	if err != nil {
		return
	}

	// send price table uid
	pt := w.staticHostPrices.managedPriceTable()
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
	err = w.staticAccount.ProvidePayment(stream, w.staticHostPubKey, modules.RPCUpdatePriceTable, cost, w.staticAccount.staticID, bh)
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

	// TODO we call this manually here in order to save a RT and write the
	// program instructions and data before reading. Remove when !4446 is
	// merged.

	// receive PayByEphemeralAccountResponse
	var payByResponse modules.PayByEphemeralAccountResponse
	err = modules.RPCRead(stream, &payByResponse)
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
			return
		}

		// If the response contains an error we are done.
		if responses[i].Error != nil {
			break
		}
	}
	return
}

// managedFundAccount will call the fundAccountRPC on the host and if successful
// will deposit the given amount into the worker's ephemeral account.
func (w *worker) managedFundAccount(amount types.Currency) (resp modules.FundAccountResponse, err error) {
	// check host version
	if build.VersionCmp(w.staticHostVersion, modules.MinimumSupportedNewRenterHostProtocolVersion) < 0 {
		build.Critical("Executing new RHP RPC on host with version", w.staticHostVersion)
	}

	// track the deposit
	w.staticAccount.managedTrackDeposit(amount)
	defer func() {
		w.staticAccount.managedCommitDeposit(amount, err == nil)
	}()

	// create a new stream
	var stream siamux.Stream
	stream, err = w.staticNewStream()
	if err != nil {
		err = errors.AddContext(err, "Unable to create a new stream")
		return
	}
	defer func() {
		if err := stream.Close(); err != nil {
			w.renter.log.Println("ERROR: failed to close stream", err)
		}
	}()

	// grab some variables from the worker
	w.mu.Lock()
	bh := w.cachedBlockHeight
	w.mu.Unlock()

	// write the specifier
	err = modules.RPCWrite(stream, modules.RPCFundAccount)
	if err != nil {
		return
	}

	// send price table uid
	pt := w.staticHostPrices.managedPriceTable()
	err = modules.RPCWrite(stream, pt.UID)
	if err != nil {
		return
	}

	// send fund account request
	err = modules.RPCWrite(stream, modules.FundAccountRequest{Account: w.staticAccount.staticID})
	if err != nil {
		return
	}

	// provide payment
	err = w.renter.hostContractor.ProvidePayment(stream, w.staticHostPubKey, modules.RPCFundAccount, amount.Add(pt.FundAccountCost), modules.ZeroAccountID, bh)
	if err != nil {
		return
	}

	// receive FundAccountResponse
	err = modules.RPCRead(stream, &resp)
	if err != nil {
		return
	}
	return
}

// managedHasSector returns whether or not the host has a sector with given root
func (w *worker) managedHasSector(sectorRoot crypto.Hash) (bool, error) {
	// create the program
	pt := w.staticHostPrices.managedPriceTable()
	pb := modules.NewProgramBuilder(&pt)
	pb.AddHasSectorInstruction(sectorRoot)
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	bandwidthCost := pb.BandwidthCost()

	// TODO temporarily increase budget to ensure it is sufficient to cover the
	// cost, until we have defined the true bandwidth cost of the new protocol
	bandwidthCost = bandwidthCost.Mul64(10)
	cost = cost.Add(bandwidthCost)

	// exeucte it
	var responses []programResponse
	responses, err := w.managedExecuteProgram(program, programData, w.staticHostFCID, cost)
	if err != nil {
		return false, errors.AddContext(err, "Unable to execute program")
	}

	// return the response
	var hasSector bool
	for _, resp := range responses {
		if resp.Error != nil {
			return false, errors.AddContext(resp.Error, "Output error")
		}
		hasSector = resp.Output[0] == 1
		break
	}
	return hasSector, nil
}

// managedReadSector returns the sector data for given root
func (w *worker) managedReadSector(sectorRoot crypto.Hash, offset, length uint64) ([]byte, error) {
	// create the program
	pt := w.staticHostPrices.managedPriceTable()
	pb := modules.NewProgramBuilder(&pt)
	pb.AddReadSectorInstruction(length, offset, sectorRoot, true)
	program, programData := pb.Program()
	cost, _, _ := pb.Cost(true)

	// take into account bandwidth costs
	bandwidthCost := pb.BandwidthCost()

	// TODO temporarily increase budget to ensure it is sufficient to cover the
	// cost, until we have defined the true bandwidth cost of the new protocol
	bandwidthCost = bandwidthCost.Mul64(1000)
	cost = cost.Add(bandwidthCost)

	// exeucte it
	responses, err := w.managedExecuteProgram(program, programData, w.staticHostFCID, cost)
	if err != nil {
		return nil, err
	}

	// return the response
	var sectorData []byte
	for _, resp := range responses {
		if resp.Error != nil {
			return nil, resp.Error
		}
		sectorData = resp.Output
		break
	}
	return sectorData, nil
}

// managedUpdatePriceTable performs the UpdatePriceTableRPC on the host.
func (w *worker) managedUpdatePriceTable() error {
	// check host version
	if build.VersionCmp(w.staticHostVersion, modules.MinimumSupportedNewRenterHostProtocolVersion) < 0 {
		build.Critical("Executing new RHP RPC on host with version", w.staticHostVersion)
	}

	// create a new stream
	stream, err := w.staticNewStream()
	if err != nil {
		return errors.AddContext(err, "Unable to create a new stream")
	}
	defer func() {
		if err := stream.Close(); err != nil {
			w.renter.log.Println("ERROR: failed to close stream", err)
		}
	}()

	// grab some variables from the worker
	w.mu.Lock()
	bh := w.cachedBlockHeight
	w.mu.Unlock()

	// write the specifier
	err = modules.RPCWrite(stream, modules.RPCUpdatePriceTable)
	if err != nil {
		return err
	}

	// receive the price table
	var uptr modules.RPCUpdatePriceTableResponse
	err = modules.RPCRead(stream, &uptr)
	if err != nil {
		return err
	}

	// decode the JSON
	var pt modules.RPCPriceTable
	err = json.Unmarshal(uptr.PriceTableJSON, &pt)
	if err != nil {
		return err
	}

	// TODO: (follow-up) perform gouging check
	// TODO: (follow-up) this should negatively affect the host's score

	// provide payment
	err = w.renter.hostContractor.ProvidePayment(stream, w.staticHostPubKey, modules.RPCUpdatePriceTable, pt.UpdatePriceTableCost, w.staticAccount.staticID, bh)
	if err != nil {
		return err
	}

	// expect stream to be closed (the host only sees a PT as valid if it
	// successfully managed to process payment, not awaiting the close allows
	// for a race condition where we consider it valid but the host does not
	err = modules.RPCRead(stream, struct{}{})
	if err == nil || !strings.Contains(err.Error(), io.ErrClosedPipe.Error()) {
		w.renter.log.Println("ERROR: expected io.ErrClosedPipe, instead received err:", err)
	}

	// update the price table
	w.staticHostPrices.managedUpdate(pt)
	return nil
}

// staticNewStream returns a new stream to the worker's host
func (w *worker) staticNewStream() (siamux.Stream, error) {
	return w.renter.staticMux.NewStream(modules.HostSiaMuxSubscriberName, w.staticHostMuxAddress, modules.SiaPKToMuxPK(w.staticHostPubKey))
}
