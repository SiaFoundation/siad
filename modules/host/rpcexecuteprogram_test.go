package host

import (
	"fmt"
	"io"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/host/mdm"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// newHasSectorProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// HasSectorInstruction.
func newHasSectorProgram(merkleRoot crypto.Hash, pt *modules.RPCPriceTable) (modules.Program, []byte, types.Currency, types.Currency, uint64) {
	i := mdm.NewHasSectorInstruction(0)
	instructions := []modules.Instruction{i}
	data := make([]byte, crypto.HashSize)
	copy(data[:crypto.HashSize], merkleRoot[:])

	// Compute cost and used memory.
	cost, refund := modules.MDMHasSectorCost(pt)
	usedMemory := modules.MDMHasSectorMemory()
	memoryCost := modules.MDMMemoryCost(pt, usedMemory, mdm.TimeHasSector+mdm.TimeCommit)
	initCost := modules.MDMInitCost(pt, uint64(len(data)))
	cost = cost.Add(memoryCost).Add(initCost)
	return instructions, data, cost, refund, usedMemory
}

// executeProgram executes an MDM program on the host using an EA payment and
// returns the responses received by the host. A failure to execute an
// instruction won't result in an error. Instead the returned responses need to
// be inspected for that depending on the testcase.
func (rhp *renterHostPair) executeProgram(epr modules.RPCExecuteProgramRequest, programData []byte, accountID modules.AccountID, budget types.Currency) (resps []modules.RPCExecuteProgramResponse, _ error) {
	// create stream
	stream := rhp.newStream()
	defer stream.Close()

	// Write the specifier.
	err := modules.RPCWrite(stream, modules.RPCExecuteProgram)
	if err != nil {
		return nil, err
	}

	// Send the payment request.
	err = modules.RPCWrite(stream, modules.PaymentRequest{Type: modules.PayByEphemeralAccount})
	if err != nil {
		return nil, err
	}

	// Send the payment details.
	pbear := newPayByEphemeralAccountRequest(accountID, rhp.ht.host.BlockHeight()+6, budget, rhp.renter)
	err = modules.RPCWrite(stream, pbear)
	if err != nil {
		return nil, err
	}

	// Receive payment confirmation.
	var pc modules.PayByContractResponse
	err = modules.RPCRead(stream, &pc)
	if err != nil {
		return nil, err
	}

	// Send the execute program request.
	err = modules.RPCWrite(stream, epr)
	if err != nil {
		return nil, err
	}

	// Send the programData.
	_, err = stream.Write(programData)
	if err != nil {
		return nil, err
	}

	// Read the responses.
	var resp modules.RPCExecuteProgramResponse
	for range epr.Program {
		// Read the response.
		err = modules.RPCRead(stream, &resp)
		if err != nil {
			return nil, err
		}
		// Append response to resps.
		resps = append(resps, resp)
		// If the response contains an error we are done.
		if resp.Error != nil {
			return
		}
	}

	// The next read should return io.EOF since the host closes the connection
	// after the RPC is done.
	err = modules.RPCRead(stream, &resp)
	if !errors.Contains(err, io.EOF) {
		return nil, fmt.Errorf("expected %v but got %v", io.EOF, err)
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

	// Add a sector to the host but not the storage obligation or contract. This
	// instruction should also work for foreign sectors.
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)
	err = ht.host.AddSector(sectorRoot, sectorData)
	if err != nil {
		t.Fatal(err)
	}

	// Fetch the price table.
	pt, err := rhp.negotiatePriceTable()
	if err != nil {
		t.Fatal(err)
	}

	// Create the 'HasSector' program.
	program, data, cost, refund, _ := newHasSectorProgram(sectorRoot, pt)

	// Prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.fcid, // TODO: leave this empty since it's not required for a readonly program.
		PriceTableID:      pt.UID,
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// Fund an account.
	fundingAmt := types.SiacoinPrecision.Mul64(100)
	_, accountID := prepareAccount()
	_, err = rhp.fundEphemeralAccount(accountID, fundingAmt)
	if err != nil {
		t.Fatal(err)
	}

	// Execute program.
	resps, err := rhp.executeProgram(epr, data, accountID, types.ZeroCurrency)
	if err != nil {
		t.Fatal(err)
	}
	// There should only be a single response.
	if len(resps) != 1 {
		t.Fatalf("expected 1 response but got %v", len(resps))
	}
	resp := resps[0]

	// Check response.
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if resp.NewMerkleRoot != sectorRoot {
		t.Fatalf("wrong NewMerkleRoot")
	}
	if resp.NewSize != 0 {
		t.Fatal("wrong NewSize")
	}
	if len(resp.Proof) != 0 {
		t.Fatal("wrong Proof")
	}
	if len(resp.Output) != 1 && resp.Output[0] != 1 {
		t.Fatal("wrong Output")
	}
	if !resp.TotalCost.Equals(cost) {
		t.Fatal("wrong TotalCost")
	}
	if !resp.PotentialRefund.Equals(refund) {
		t.Fatal("wrong PotentialRefund")
	}
}
