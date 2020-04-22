package host

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/host/mdm"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux/mux"
)

// updateRunningCosts is a testing helper function for updating the running
// costs of a program after adding an instruction.
func updateRunningCosts(pt *modules.RPCPriceTable, runningCost, runningRefund, runningCollateral types.Currency, runningMemory uint64, cost, refund, collateral types.Currency, memory, time uint64) (types.Currency, types.Currency, types.Currency, uint64) {
	runningMemory = runningMemory + memory
	memoryCost := modules.MDMMemoryCost(pt, runningMemory, time)
	runningCost = runningCost.Add(memoryCost).Add(cost)
	runningRefund = runningRefund.Add(refund)
	runningCollateral = runningCollateral.Add(collateral)

	return runningCost, runningRefund, runningCollateral, runningMemory
}

// newHasSectorInstruction is a convenience method for creating a single
// 'HasSector' instruction.
func newHasSectorInstruction(dataOffset uint64, pt *modules.RPCPriceTable) (modules.Instruction, types.Currency, types.Currency, types.Currency, uint64, uint64) {
	i := mdm.NewHasSectorInstruction(dataOffset)
	cost, refund := modules.MDMHasSectorCost(pt)
	collateral := modules.MDMHasSectorCollateral()
	return i, cost, refund, collateral, modules.MDMHasSectorMemory(), modules.MDMTimeHasSector
}

// newHasSectorProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// HasSectorInstruction.
func newHasSectorProgram(merkleRoot crypto.Hash, pt *modules.RPCPriceTable) ([]modules.Instruction, []byte, types.Currency, types.Currency, types.Currency, uint64) {
	data := make([]byte, crypto.HashSize)
	copy(data[:crypto.HashSize], merkleRoot[:])
	initCost := modules.MDMInitCost(pt, uint64(len(data)), 1)
	i, cost, refund, collateral, memory, time := newHasSectorInstruction(0, pt)
	cost, refund, collateral, memory = updateRunningCosts(pt, initCost, types.ZeroCurrency, types.ZeroCurrency, modules.MDMInitMemory(), cost, refund, collateral, memory, time)
	instructions := []modules.Instruction{i}
	return instructions, data, cost, refund, collateral, memory
}

// executeProgram executes an MDM program on the host using an EA payment and
// returns the responses received by the host. A failure to execute an
// instruction won't result in an error. Instead the returned responses need to
// be inspected for that depending on the testcase.
func (rhp *renterHostPair) executeProgram(epr modules.RPCExecuteProgramRequest, programData []byte, budget types.Currency) (resps []modules.RPCExecuteProgramResponse, limit mux.BandwidthLimit, _ error) {
	// create stream
	stream := rhp.newStream()
	defer stream.Close()

	// Get the limit to track bandwidth.
	limit = stream.Limit()

	// Write the specifier.
	err := modules.RPCWrite(stream, modules.RPCExecuteProgram)
	if err != nil {
		return nil, limit, err
	}

	// Write the pricetable uid.
	err = modules.RPCWrite(stream, rhp.latestPT.UID)
	if err != nil {
		return nil, limit, err
	}

	// Send the payment request.
	err = modules.RPCWrite(stream, modules.PaymentRequest{Type: modules.PayByEphemeralAccount})
	if err != nil {
		return nil, limit, err
	}

	// Send the payment details.
	pbear := newPayByEphemeralAccountRequest(rhp.accountID, rhp.ht.host.BlockHeight()+6, budget, rhp.accountKey)
	err = modules.RPCWrite(stream, pbear)
	if err != nil {
		return nil, limit, err
	}

	// Receive payment confirmation.
	var pc modules.PayByEphemeralAccountResponse
	err = modules.RPCRead(stream, &pc)
	if err != nil {
		return nil, limit, err
	}

	// Send the execute program request.
	err = modules.RPCWrite(stream, epr)
	if err != nil {
		return nil, limit, err
	}

	// Send the programData.
	_, err = stream.Write(programData)
	if err != nil {
		return nil, limit, err
	}

	// Read the responses.
	var resp modules.RPCExecuteProgramResponse
	for range epr.Program {
		// Read the response.
		err = modules.RPCRead(stream, &resp)
		if err != nil {
			return nil, limit, err
		}
		// Append response to resps.
		resps = append(resps, resp)
		// If the response contains an error we are done.
		if resp.Error != nil {
			return resps, limit, nil
		}
	}

	// The next read should return io.ErrClosedPipe since the host closes the
	// connection after the RPC is done.
	err = modules.RPCRead(stream, &resp)
	if !errors.Contains(err, io.ErrClosedPipe) {
		return nil, limit, fmt.Errorf("expected %v but got %v", io.EOF, err)
	}
	return
}

// TestExecuteProgram tests the managedRPCExecuteProgram with a valid
// 'HasSector' program.
func TestExecuteHasSectorProgram(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a blank host tester
	rhp, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rhp.Close()
	ht := rhp.ht

	// get a snapshot of the SO before running the program.
	sos, err := rhp.ht.host.managedGetStorageObligationSnapshot(rhp.fcid)
	if err != nil {
		t.Fatal(err)
	}

	// Add a sector to the host but not the storage obligation or contract. This
	// instruction should also work for foreign sectors.
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)
	err = ht.host.AddSector(sectorRoot, sectorData)
	if err != nil {
		t.Fatal(err)
	}

	// Fetch the price table.
	err = rhp.updatePriceTable()
	if err != nil {
		t.Fatal(err)
	}

	// Create the 'HasSector' program.
	program, data, programCost, refund, collateral, _ := newHasSectorProgram(sectorRoot, rhp.latestPT)

	// Prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.fcid, // TODO: leave this empty since it's not required for a readonly program.
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// Fund an account with the max balance.
	maxBalance := rhp.ht.host.managedInternalSettings().MaxEphemeralAccountBalance
	fundingAmt := maxBalance.Add(rhp.latestPT.FundAccountCost)
	_, err = rhp.fundEphemeralAccount(fundingAmt)
	if err != nil {
		t.Fatal(err)
	}

	// Compute expected bandwidth cost. These hardcoded values were chosen after
	// running this test with a high budget and measuring the used bandwidth for
	// this particular program on the "renter" side. This way we can test that
	// the bandwidth measured by the renter is large enough to be accepted by
	// the host.
	expectedDownload := uint64(167424) // download
	expectedUpload := uint64(362752)   // upload
	downloadCost := rhp.latestPT.DownloadBandwidthCost.Mul64(expectedDownload)
	uploadCost := rhp.latestPT.UploadBandwidthCost.Mul64(expectedUpload)
	bandwidthCost := downloadCost.Add(uploadCost)

	// Execute program.
	cost := programCost.Add(bandwidthCost)
	resps, limit, err := rhp.executeProgram(epr, data, cost)
	if err != nil {
		t.Fatal(err)
	}
	// Log the bandwidth used by this RPC.
	t.Logf("Used bandwidth (valid program): %v down, %v up", limit.Downloaded(), limit.Uploaded())
	// There should only be a single response.
	if len(resps) != 1 {
		t.Fatalf("expected 1 response but got %v", len(resps))
	}
	resp := resps[0]

	// Check response.
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if !resp.AdditionalCollateral.Equals(collateral) {
		t.Fatalf("wrong AdditionalCollateral %v != %v", resp.AdditionalCollateral.HumanString(), collateral.HumanString())
	}
	if resp.NewMerkleRoot != sos.MerkleRoot() {
		t.Fatalf("wrong NewMerkleRoot %v != %v", resp.NewMerkleRoot, sos.MerkleRoot())
	}
	if resp.NewSize != 0 {
		t.Fatalf("wrong NewSize %v != %v", resp.NewSize, 0)
		t.Fatal("wrong NewSize")
	}
	if len(resp.Proof) != 0 {
		t.Fatalf("wrong Proof %v != %v", resp.Proof, []crypto.Hash{})
	}
	if len(resp.Output) != 1 && resp.Output[0] != 1 {
		t.Fatalf("wrong Output %v != %v", resp.Output, []byte{1})
	}
	if !resp.TotalCost.Equals(programCost) {
		t.Fatalf("wrong TotalCost %v != %v", resp.TotalCost.HumanString(), cost.HumanString())
	}
	if !resp.PotentialRefund.Equals(refund) {
		t.Fatalf("wrong PotentialRefund %v != %v", resp.PotentialRefund.HumanString(), refund.HumanString())
	}
	// Make sure the right amount of money remains on the EA.
	remainingBalance := rhp.ht.host.staticAccountManager.callAccountBalance(rhp.accountID)
	expectedRemainingBalance := maxBalance.Sub(cost)
	if !remainingBalance.Equals(expectedRemainingBalance) {
		t.Fatalf("expected %v remaining balance but got %v", expectedRemainingBalance.HumanString(), remainingBalance.HumanString())
	}
	// Execute program again. This time pay for 1 less byte of bandwidth. This should fail.
	cost = programCost.Add(bandwidthCost.Sub64(1))
	_, limit, err = rhp.executeProgram(epr, data, cost)
	if err == nil || !strings.Contains(err.Error(), modules.ErrInsufficientBandwidthBudget.Error()) {
		t.Fatalf("expected executeProgram to fail due to insufficient bandwidth budget: %v", err)
	}
	// Log the bandwidth used by this RPC.
	t.Logf("Used bandwidth (invalid program): %v down, %v up", limit.Downloaded(), limit.Uploaded())
	// Check that the remaining balance is correct again. We expect the host to
	// charge us for the program since the bandwidth limit was reached when
	// sending the response, after executing the program.
	downloadCost = rhp.latestPT.DownloadBandwidthCost.Mul64(limit.Downloaded())
	uploadCost = rhp.latestPT.UploadBandwidthCost.Mul64(limit.Uploaded())
	remainingBalance = rhp.ht.host.staticAccountManager.callAccountBalance(rhp.accountID)
	expectedRemainingBalance = expectedRemainingBalance.Sub(downloadCost).Sub(uploadCost).Sub(programCost)
	if !remainingBalance.Equals(expectedRemainingBalance) {
		t.Fatalf("expected %v remaining balance but got %v", expectedRemainingBalance, remainingBalance)
	}
}
