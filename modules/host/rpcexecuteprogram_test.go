package host

import (
	"bytes"
	"encoding/binary"
	"io"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/host/mdm"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
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

// newReadSectorInstruction is a convenience method for creating a single
// 'ReadSector' instruction.
func newReadSectorInstruction(length uint64, merkleProof bool, dataOffset uint64, pt *modules.RPCPriceTable) (modules.Instruction, types.Currency, types.Currency, types.Currency, uint64, uint64) {
	i := mdm.NewReadSectorInstruction(dataOffset, dataOffset+8, dataOffset+16, merkleProof)
	cost, refund := modules.MDMReadCost(pt, length)
	collateral := modules.MDMReadCollateral()
	return i, cost, refund, collateral, modules.MDMReadMemory(), modules.MDMTimeReadSector
}

// newReadSectorProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// ReadSectorInstruction.
func newReadSectorProgram(length, offset uint64, merkleRoot crypto.Hash, pt *modules.RPCPriceTable) ([]modules.Instruction, []byte, types.Currency, types.Currency, types.Currency, uint64) {
	data := make([]byte, 8+8+crypto.HashSize)
	binary.LittleEndian.PutUint64(data[:8], length)
	binary.LittleEndian.PutUint64(data[8:16], offset)
	copy(data[16:], merkleRoot[:])
	initCost := modules.MDMInitCost(pt, uint64(len(data)), 1)
	i, cost, refund, collateral, memory, time := newReadSectorInstruction(length, true, 0, pt)
	cost, refund, collateral, memory = updateRunningCosts(pt, initCost, types.ZeroCurrency, types.ZeroCurrency, modules.MDMInitMemory(), cost, refund, collateral, memory, time)
	instructions := []modules.Instruction{i}
	cost = cost.Add(modules.MDMMemoryCost(pt, memory, modules.MDMTimeCommit))
	return instructions, data, cost, refund, collateral, memory
}

// executeProgram executes an MDM program on the host using an EA payment and
// returns the responses received by the host. A failure to execute an
// instruction won't result in an error. Instead the returned responses need to
// be inspected for that depending on the testcase.
func (rhp *renterHostPair) executeProgram(epr modules.RPCExecuteProgramRequest, programData []byte, budget types.Currency) (resps []modules.RPCExecuteProgramResponse, outputs [][]byte, _ error) {
	// create stream
	stream := rhp.newStream()
	defer stream.Close()

	// Write the specifier.
	err := modules.RPCWrite(stream, modules.RPCExecuteProgram)
	if err != nil {
		return nil, nil, err
	}

	// Write the pricetable uid.
	err = modules.RPCWrite(stream, rhp.latestPT.UID)
	if err != nil {
		return nil, nil, err
	}

	// Send the payment request.
	err = modules.RPCWrite(stream, modules.PaymentRequest{Type: modules.PayByEphemeralAccount})
	if err != nil {
		return nil, nil, err
	}

	// Send the payment details.
	pbear := newPayByEphemeralAccountRequest(rhp.accountID, rhp.ht.host.BlockHeight()+6, budget, rhp.accountKey)
	err = modules.RPCWrite(stream, pbear)
	if err != nil {
		return nil, nil, err
	}

	// Receive payment confirmation.
	var pc modules.PayByEphemeralAccountResponse
	err = modules.RPCRead(stream, &pc)
	if err != nil {
		return nil, nil, err
	}

	// Send the execute program request.
	err = modules.RPCWrite(stream, epr)
	if err != nil {
		return nil, nil, err
	}

	// Send the programData.
	_, err = stream.Write(programData)
	if err != nil {
		return nil, nil, err
	}

	// Read the responses.
	var resp modules.RPCExecuteProgramResponse
	for range epr.Program {
		// Read the response.
		err = modules.RPCRead(stream, &resp)
		if err != nil {
			return nil, nil, err
		}
		// Append response to resps.
		resps = append(resps, resp)
		// If the response contains an error we are done.
		if resp.Error != nil {
			return
		}
		// Read the output data.
		output := make([]byte, resp.OutputLength, resp.OutputLength)
		_, err = stream.Read(output)
		if err != nil {
			return nil, nil, err
		}
		// Append response to resps.
		outputs = append(outputs, output)
	}

	// The next read should return io.EOF since the host closes the connection
	// after the RPC is done.
	err = modules.RPCRead(stream, &resp)
	if !errors.Contains(err, io.ErrClosedPipe) {
		return nil, nil, err
	}
	return
}

// TestExecuteReadSectorProgram tests the managedRPCExecuteProgram with a valid
// 'ReadSector' program.
func TestExecuteReadSectorProgram(t *testing.T) {
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
	sos, err := ht.host.managedGetStorageObligationSnapshot(rhp.fcid)
	if err != nil {
		t.Fatal(err)
	}

	// create a random sector
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)

	// modify the host's storage obligation to add the sector
	so, err := ht.host.managedGetStorageObligation(rhp.fcid)
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedLockStorageObligation(rhp.fcid)
	err = ht.host.managedModifyStorageObligation(so, []crypto.Hash{}, map[crypto.Hash][]byte{sectorRoot: sectorData})
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedUnlockStorageObligation(rhp.fcid)

	// create the 'ReadSector' program.
	program, data, cost, refund, collateral, memory := newReadSectorProgram(modules.SectorSize, 0, sectorRoot, rhp.latestPT)

	// prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.fcid, // TODO: leave this empty since it's not required for a readonly program.
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// fund an account.
	fundingAmt := rhp.ht.host.managedInternalSettings().MaxEphemeralAccountBalance.Add(rhp.latestPT.FundAccountCost)
	_, err = rhp.fundEphemeralAccount(fundingAmt)
	if err != nil {
		t.Fatal(err)
	}

	// execute program.
	resps, outputs, err := rhp.executeProgram(epr, data, cost)
	if err != nil {
		t.Log("cost", cost.HumanString())
		t.Log("expected ea balance", rhp.ht.host.managedInternalSettings().MaxEphemeralAccountBalance.HumanString())
		t.Fatal(err)
	}
	// there should only be a single response.
	if len(resps) != 1 {
		t.Fatalf("expected 1 response but got %v", len(resps))
	}
	resp := resps[0]

	// there should only be a single output.
	if len(outputs) != 1 {
		t.Fatalf("expected 1 output but got %v", len(outputs))
	}
	output := outputs[0]

	// check response.
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if resp.NewSize != sos.staticContractSize {
		t.Fatalf("expected contract size to stay the same: %v != %v", sos.staticContractSize, resp.NewSize)
	}
	if resp.NewMerkleRoot != sos.staticMerkleRoot {
		t.Fatalf("expected merkle root to stay the same: %v != %v", sos.staticMerkleRoot, resp.NewMerkleRoot)
	}
	if len(resp.Proof) != 0 {
		t.Fatalf("expected proof length to be %v but was %v", 0, len(resp.Proof))
	}
	if !resp.TotalCost.Equals(cost.Sub(modules.MDMMemoryCost(rhp.latestPT, memory, modules.MDMTimeCommit))) {
		t.Fatalf("execution cost doesn't match expected execution cost: %v != %v", resp.TotalCost.HumanString(), cost.HumanString())
	}
	if !resp.AdditionalCollateral.Equals(collateral) {
		t.Fatalf("collateral doesnt't match expected collateral: %v != %v", resp.AdditionalCollateral.HumanString(), collateral.HumanString())
	}
	if !resp.PotentialRefund.Equals(refund) {
		t.Fatalf("refund doesn't match expected refund: %v != %v", resp.PotentialRefund.HumanString(), refund.HumanString())
	}
	if uint64(len(output)) != modules.SectorSize {
		t.Fatalf("expected returned data to have length %v but was %v", modules.SectorSize, len(output))
	}
	if !bytes.Equal(sectorData, output) {
		t.Fatal("Unexpected data")
	}
}

// TestExecuteReadPartialSectorProgram tests the managedRPCExecuteProgram with a
// valid 'ReadSector' program that only reads half a sector.
func TestExecuteReadPartialSectorProgram(t *testing.T) {
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
	sos, err := ht.host.managedGetStorageObligationSnapshot(rhp.fcid)
	if err != nil {
		t.Fatal(err)
	}

	// create a random sector
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)

	// modify the host's storage obligation to add the sector
	so, err := ht.host.managedGetStorageObligation(rhp.fcid)
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedLockStorageObligation(rhp.fcid)
	err = ht.host.managedModifyStorageObligation(so, []crypto.Hash{}, map[crypto.Hash][]byte{sectorRoot: sectorData})
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedUnlockStorageObligation(rhp.fcid)

	// create the 'ReadSector' program.
	program, data, cost, refund, collateral, memory := newReadSectorProgram(modules.SectorSize/2, modules.SectorSize/2, sectorRoot, rhp.latestPT)

	// prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.fcid, // TODO: leave this empty since it's not required for a readonly program.
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// fund an account.
	fundingAmt := rhp.ht.host.managedInternalSettings().MaxEphemeralAccountBalance.Add(rhp.latestPT.FundAccountCost)
	_, err = rhp.fundEphemeralAccount(fundingAmt)
	if err != nil {
		t.Fatal(err)
	}

	// execute program.
	resps, outputs, err := rhp.executeProgram(epr, data, cost)
	if err != nil {
		t.Log("cost", cost.HumanString())
		t.Log("expected ea balance", rhp.ht.host.managedInternalSettings().MaxEphemeralAccountBalance.HumanString())
		t.Fatal(err)
	}
	// there should only be a single response.
	if len(resps) != 1 {
		t.Fatalf("expected 1 response but got %v", len(resps))
	}
	resp := resps[0]

	// there should only be a single output.
	if len(outputs) != 1 {
		t.Fatalf("expected 1 output but got %v", len(outputs))
	}
	output := outputs[0]

	// check response.
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if resp.NewSize != sos.staticContractSize {
		t.Fatalf("expected contract size to stay the same: %v != %v", sos.staticContractSize, resp.NewSize)
	}
	if resp.NewMerkleRoot != sos.staticMerkleRoot {
		t.Fatalf("expected merkle root to stay the same: %v != %v", sos.staticMerkleRoot, resp.NewMerkleRoot)
	}
	if len(resp.Proof) != 1 {
		t.Fatalf("expected proof length to be %v but was %v", 1, len(resp.Proof))
	}
	if !resp.TotalCost.Equals(cost.Sub(modules.MDMMemoryCost(rhp.latestPT, memory, modules.MDMTimeCommit))) {
		t.Fatalf("execution cost doesn't match expected execution cost: %v != %v", resp.TotalCost.HumanString(), cost.HumanString())
	}
	if !resp.AdditionalCollateral.Equals(collateral) {
		t.Fatalf("collateral doesnt't match expected collateral: %v != %v", resp.AdditionalCollateral.HumanString(), collateral.HumanString())
	}
	if !resp.PotentialRefund.Equals(refund) {
		t.Fatalf("refund doesn't match expected refund: %v != %v", resp.PotentialRefund.HumanString(), refund.HumanString())
	}
	if uint64(len(output)) != modules.SectorSize/2 {
		t.Fatalf("expected returned data to have length %v but was %v", modules.SectorSize/2, len(output))
	}
	if !bytes.Equal(sectorData[modules.SectorSize/2:], output) {
		t.Fatal("Unexpected data")
	}
	// verify the proof
	proofStart := int(modules.SectorSize/2) / crypto.SegmentSize
	proofEnd := int(modules.SectorSize) / crypto.SegmentSize
	proof := crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
	if !reflect.DeepEqual(proof, resp.Proof) {
		t.Fatal("proof doesn't match expected proof")
	}
}

// TestExecuteHasSectorProgram tests the managedRPCExecuteProgram with a valid
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

	// Create the 'HasSector' program.
	program, data, cost, refund, collateral, _ := newHasSectorProgram(sectorRoot, rhp.latestPT)

	// Prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.fcid, // TODO: leave this empty since it's not required for a readonly program.
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// Fund an account.
	fundingAmt := rhp.ht.host.managedInternalSettings().MaxEphemeralAccountBalance.Add(rhp.latestPT.FundAccountCost)
	_, err = rhp.fundEphemeralAccount(fundingAmt)
	if err != nil {
		t.Fatal(err)
	}

	// Execute program.
	resps, outputs, err := rhp.executeProgram(epr, data, cost)
	if err != nil {
		t.Log("cost", cost.HumanString())
		t.Log("expected ea balance", rhp.ht.host.managedInternalSettings().MaxEphemeralAccountBalance.HumanString())
		t.Fatal(err)
	}
	// There should only be a single response.
	if len(resps) != 1 {
		t.Fatalf("expected 1 response but got %v", len(resps))
	}
	resp := resps[0]

	// There should only be a single output.
	if len(outputs) != 1 {
		t.Fatalf("expected 1 output but got %v", len(outputs))
	}
	output := outputs[0]

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
	if output[0] != 1 {
		t.Fatalf("wrong Output %v != %v", output[0], []byte{1})
	}
	if !resp.TotalCost.Equals(cost) {
		t.Fatalf("wrong TotalCost %v != %v", resp.TotalCost.HumanString(), cost.HumanString())
	}
	if !resp.PotentialRefund.Equals(refund) {
		t.Fatalf("wrong PotentialRefund %v != %v", resp.PotentialRefund.HumanString(), refund.HumanString())
	}
}
