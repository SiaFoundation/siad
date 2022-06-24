package host

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
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

// TestExecuteProgramWriteDeadline verifies the ExecuteProgramRPC sets a write
// deadline
func TestExecuteProgramWriteDeadline(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a blank host tester
	deps := &dependencies.HostMDMProgramDelayedWrite{}
	rhp, err := newCustomRenterHostPair(t.Name(), deps)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rhp.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// prefund the EA
	his := rhp.staticHT.host.managedInternalSettings()
	_, err = rhp.managedFundEphemeralAccount(his.MaxEphemeralAccountBalance, true)
	if err != nil {
		t.Fatal(err)
	}

	// create stream
	stream := rhp.managedNewStream()
	defer func() {
		if err := stream.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// create a random sector
	sectorRoot, _, err := addRandomSector(rhp)
	if err != nil {
		t.Fatal(err)
	}

	// create the 'ReadSector' program.
	pt := rhp.managedPriceTable()
	pb := modules.NewProgramBuilder(pt, types.BlockHeight(fastrand.Uint64n(1000))) // random duration since ReadSector doesn't depend on duration.
	pb.AddReadSectorInstruction(modules.SectorSize, 0, sectorRoot, true)
	program, data := pb.Program()

	// prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.staticFCID,
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// execute program.
	budget := types.NewCurrency64(math.MaxUint64)
	_, _, err = rhp.managedExecuteProgram(epr, data, budget, false, true)
	if !errors.Contains(err, io.ErrClosedPipe) {
		t.Fatal("Expected managedExecuteProgram to fail with an ErrClosedPipe, instead err was", err)
	}
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
	defer func() {
		err := rhp.Close()
		if err != nil {
			t.Error(err)
		}
	}()
	ht := rhp.staticHT

	// create a random sector
	sectorRoot, sectorData, err := addRandomSector(rhp)
	if err != nil {
		t.Fatal(err)
	}

	// get a snapshot of the SO before running the program.
	sos, err := ht.host.managedGetStorageObligationSnapshot(rhp.staticFCID)
	if err != nil {
		t.Fatal(err)
	}
	// verify the root is not the zero root to begin with
	if sos.staticMerkleRoot == crypto.MerkleRoot([]byte{}) {
		t.Fatalf("expected merkle root to be non zero: %v", sos.staticMerkleRoot)
	}

	// create the 'ReadSector' program.
	pt := rhp.managedPriceTable()
	pb := modules.NewProgramBuilder(pt, types.BlockHeight(fastrand.Uint64n(1000))) // random duration since ReadSector doesn't depend on duration.
	pb.AddReadSectorInstruction(modules.SectorSize, 0, sectorRoot, true)
	program, data := pb.Program()
	programCost, storageCost, collateral := pb.Cost(true)

	// prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.staticFCID, // TODO: leave this empty since it's not required for a readonly program.
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// fund an account.
	his := rhp.staticHT.host.managedInternalSettings()
	maxBalance := his.MaxEphemeralAccountBalance
	fundingAmt := maxBalance.Add(pt.FundAccountCost)
	_, err = rhp.managedFundEphemeralAccount(fundingAmt, true)
	if err != nil {
		t.Fatal(err)
	}

	// Compute expected bandwidth cost. These hardcoded values were chosen after
	// running this test with a high budget and measuring the used bandwidth for
	// this particular program on the "renter" side. This way we can test that
	// the bandwidth measured by the renter is large enough to be accepted by
	// the host.
	expectedDownload := uint64(4380) // download
	expectedUpload := uint64(1460)   // upload
	downloadCost := pt.DownloadBandwidthCost.Mul64(expectedDownload)
	uploadCost := pt.UploadBandwidthCost.Mul64(expectedUpload)
	bandwidthCost := downloadCost.Add(uploadCost)
	cost := programCost.Add(bandwidthCost)

	// execute program.
	resps, limit, err := rhp.managedExecuteProgram(epr, data, cost, true, true)
	if err != nil {
		t.Log("cost", cost.HumanString())
		t.Log("expected ea balance", rhp.staticHT.host.managedInternalSettings().MaxEphemeralAccountBalance.HumanString())
		t.Fatal(err)
	}

	// Log the bandwidth used by this RPC.
	t.Logf("Used bandwidth (read full sector program): %v down, %v up", limit.Downloaded(), limit.Uploaded())

	// there should only be a single response.
	if len(resps) != 1 {
		t.Fatalf("expected 1 response but got %v", len(resps))
	}
	resp := resps[0]

	// check response.
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	// programs that don't require a snapshot return a 0 contract size.
	if resp.NewSize != 0 {
		t.Fatalf("expected contract size to stay the same: %v != %v", 0, resp.NewSize)
	}
	// programs that don't require a snapshot return a zero hash.
	zh := crypto.Hash{}
	if resp.NewMerkleRoot != zh {
		t.Fatalf("expected merkle root to stay the same: %v != %v", zh, resp.NewMerkleRoot)
	}
	if len(resp.Proof) != 0 {
		t.Fatalf("expected proof length to be %v but was %v", 0, len(resp.Proof))
	}

	if !resp.AdditionalCollateral.Equals(collateral) {
		t.Fatalf("collateral doesnt't match expected collateral: %v != %v", resp.AdditionalCollateral.HumanString(), collateral.HumanString())
	}
	if !resp.FailureRefund.Equals(storageCost) {
		t.Fatalf("storage cost doesn't match expected storage cost: %v != %v", resp.FailureRefund.HumanString(), storageCost.HumanString())
	}
	if uint64(len(resp.Output)) != modules.SectorSize {
		t.Fatalf("expected returned data to have length %v but was %v", modules.SectorSize, len(resp.Output))
	}
	if !bytes.Equal(sectorData, resp.Output) {
		t.Fatal("Unexpected data")
	}

	// verify the cost
	if !resp.TotalCost.Equals(programCost) {
		t.Fatalf("wrong TotalCost %v != %v", resp.TotalCost.HumanString(), programCost.HumanString())
	}

	// verify the EA balance
	am := rhp.staticHT.host.staticAccountManager
	expectedBalance := maxBalance.Sub(cost)
	err = verifyBalance(am, rhp.staticAccountID, expectedBalance)
	if err != nil {
		t.Fatal(err)
	}

	// rerun the program but now make sure the given budget does not cover the
	// cost, we expect this to return ErrInsufficientBandwidthBudget
	program, data = pb.Program()
	cost = cost.Sub64(1)
	_, limit, err = rhp.managedExecuteProgram(epr, data, cost, true, true)
	if err == nil || !strings.Contains(err.Error(), modules.ErrInsufficientBandwidthBudget.Error()) {
		t.Fatalf("expected ExecuteProgram to fail due to insufficient bandwidth budget: %v", err)
	}

	// verify the host charged us by checking the EA balance and Check that the
	// remaining balance is correct again. We expect the host to charge us for
	// the program since the bandwidth limit was reached when sending the
	// response, after executing the program.
	downloadCost = pt.DownloadBandwidthCost.Mul64(limit.Downloaded())
	uploadCost = pt.UploadBandwidthCost.Mul64(limit.Uploaded())

	expectedBalance = expectedBalance.Sub(downloadCost).Sub(uploadCost).Sub(programCost)
	err = verifyBalance(am, rhp.staticAccountID, expectedBalance)
	if err != nil {
		t.Fatal(err)
	}

	// Check bandwidth tallies.
	down := atomic.LoadUint64(&rhp.staticHT.host.atomicStreamDownload)
	up := atomic.LoadUint64(&rhp.staticHT.host.atomicStreamUpload)
	if down == 0 || up == 0 {
		t.Fatal("bandwidth tallies weren't updated", down, up)
	}
	up2, down2, _, _ := rhp.staticHT.host.BandwidthCounters()
	if up2 != up {
		t.Fatal("bandwidth counters don't match internal tallies", up, up2)
	}
	if down2 != down {
		t.Fatal("bandwidth counters don't match internal tallies", down, down2)
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
	defer func() {
		err := rhp.Close()
		if err != nil {
			t.Error(err)
		}
	}()
	ht := rhp.staticHT

	// create a random sector
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)
	// modify the host's storage obligation to add the sector
	so, err := ht.host.managedGetStorageObligation(rhp.staticFCID)
	if err != nil {
		t.Fatal(err)
	}
	so.SectorRoots = append(so.SectorRoots, sectorRoot)
	ht.host.managedLockStorageObligation(rhp.staticFCID)
	err = ht.host.managedModifyStorageObligation(so, []crypto.Hash{}, map[crypto.Hash][]byte{sectorRoot: sectorData})
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedUnlockStorageObligation(rhp.staticFCID)

	// select a random number of segments to read at random offset
	numSegments := fastrand.Uint64n(5) + 1
	totalSegments := modules.SectorSize / crypto.SegmentSize
	offset := fastrand.Uint64n(totalSegments-numSegments+1) * crypto.SegmentSize
	length := numSegments * crypto.SegmentSize

	// create the 'ReadSector' program.
	pt := rhp.managedPriceTable()
	pb := modules.NewProgramBuilder(pt, types.BlockHeight(fastrand.Uint64n(1000))) // random duration since ReadSector doesn't depend on duration.
	pb.AddReadSectorInstruction(length, offset, sectorRoot, true)
	program, data := pb.Program()
	programCost, refund, collateral := pb.Cost(true)

	// prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    types.FileContractID{},
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// fund an account.
	fundingAmt := rhp.staticHT.host.managedInternalSettings().MaxEphemeralAccountBalance.Add(pt.FundAccountCost)
	_, err = rhp.managedFundEphemeralAccount(fundingAmt, true)
	if err != nil {
		t.Fatal(err)
	}

	// Compute expected bandwidth cost. These hardcoded values were chosen after
	// running this test with a high budget and measuring the used bandwidth for
	// this particular program on the "renter" side. This way we can test that
	// the bandwidth measured by the renter is large enough to be accepted by
	// the host.
	expectedDownload := uint64(1460)
	expectedUpload := uint64(1460)
	downloadCost := pt.DownloadBandwidthCost.Mul64(expectedDownload)
	uploadCost := pt.UploadBandwidthCost.Mul64(expectedUpload)
	bandwidthCost := downloadCost.Add(uploadCost)
	cost := programCost.Add(bandwidthCost)

	// execute program.
	resps, bandwidth, err := rhp.managedExecuteProgram(epr, data, cost, true, true)
	if err != nil {
		t.Log("cost", cost.HumanString())
		t.Log("expected ea balance", rhp.staticHT.host.managedInternalSettings().MaxEphemeralAccountBalance.HumanString())
		t.Fatal(err)
	}
	// there should only be a single response.
	if len(resps) != 1 {
		t.Fatalf("expected 1 response but got %v", len(resps))
	}
	resp := resps[0]

	// check response.
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	if resp.NewSize != 0 {
		t.Fatalf("expected contract size to stay the same: %v != %v", 0, resp.NewSize)
	}
	zeroRoot := crypto.Hash{}
	if resp.NewMerkleRoot != zeroRoot {
		t.Fatalf("expected merkle root to stay the same: %v != %v", zeroRoot, resp.NewMerkleRoot)
	}
	if !resp.AdditionalCollateral.Equals(collateral) {
		t.Fatalf("collateral doesnt't match expected collateral: %v != %v", resp.AdditionalCollateral.HumanString(), collateral.HumanString())
	}
	if !resp.FailureRefund.Equals(refund) {
		t.Fatalf("storage cost doesn't match expected storage cost: %v != %v", resp.FailureRefund.HumanString(), refund.HumanString())
	}
	if uint64(len(resp.Output)) != length {
		t.Fatalf("expected returned data to have length %v but was %v", length, len(resp.Output))
	}

	if !bytes.Equal(sectorData[offset:offset+length], resp.Output) {
		t.Fatal("Unexpected data")
	}

	// verify the proof
	proofStart := int(offset) / crypto.SegmentSize
	proofEnd := int(offset+length) / crypto.SegmentSize
	proof := crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
	if !reflect.DeepEqual(proof, resp.Proof) {
		t.Fatal("proof doesn't match expected proof")
	}

	// verify the cost
	if !resp.TotalCost.Equals(programCost) {
		t.Fatalf("wrong TotalCost %v != %v", resp.TotalCost.HumanString(), programCost.HumanString())
	}

	t.Logf("Used bandwidth (read partial sector program): %v down, %v up", bandwidth.Downloaded(), bandwidth.Uploaded())
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
	defer func() {
		err := rhp.Close()
		if err != nil {
			t.Error(err)
		}
	}()
	ht := rhp.staticHT

	// get a snapshot of the SO before running the program.
	sos, err := rhp.staticHT.host.managedGetStorageObligationSnapshot(rhp.staticFCID)
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
	pt := rhp.managedPriceTable()
	pb := modules.NewProgramBuilder(pt, types.BlockHeight(fastrand.Uint64n(1000))) // random duration since HasSector doesn't depend on duration.
	pb.AddHasSectorInstruction(sectorRoot)
	program, data := pb.Program()
	programCost, refund, collateral := pb.Cost(true)

	// Prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.staticFCID,
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// Fund an account with the max balance.
	maxBalance := rhp.staticHT.host.managedInternalSettings().MaxEphemeralAccountBalance
	fundingAmt := maxBalance.Add(pt.FundAccountCost)
	_, err = rhp.managedFundEphemeralAccount(fundingAmt, true)
	if err != nil {
		t.Fatal(err)
	}

	// Compute expected bandwidth cost. These hardcoded values were chosen after
	// running this test with a high budget and measuring the used bandwidth for
	// this particular program on the "renter" side. This way we can test that
	// the bandwidth measured by the renter is large enough to be accepted by
	// the host.
	expectedDownload := uint64(1460) // download
	expectedUpload := uint64(1460)   // upload
	downloadCost := pt.DownloadBandwidthCost.Mul64(expectedDownload)
	uploadCost := pt.UploadBandwidthCost.Mul64(expectedUpload)
	bandwidthCost := downloadCost.Add(uploadCost)

	// Execute program.
	cost := programCost.Add(bandwidthCost)
	resps, limit, err := rhp.managedExecuteProgram(epr, data, cost, true, true)
	if err != nil {
		t.Fatal(err)
	}
	// Log the bandwidth used by this RPC.
	t.Logf("Used bandwidth (valid has sector program): %v down, %v up", limit.Downloaded(), limit.Uploaded())
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
	if resp.Output[0] != 1 {
		t.Fatalf("wrong Output %v != %v", resp.Output[0], []byte{1})
	}
	if !resp.TotalCost.Equals(programCost) {
		t.Fatalf("wrong TotalCost %v != %v", resp.TotalCost.HumanString(), programCost.HumanString())
	}
	if !resp.FailureRefund.Equals(refund) {
		t.Fatalf("wrong StorageCost %v != %v", resp.FailureRefund.HumanString(), refund.HumanString())
	}
	// Make sure the right amount of money remains on the EA.
	am := rhp.staticHT.host.staticAccountManager
	expectedBalance := maxBalance.Sub(cost)
	err = verifyBalance(am, rhp.staticAccountID, expectedBalance)
	if err != nil {
		t.Fatal(err)
	}

	// Execute program again. This time pay for 1 less byte of bandwidth. This
	// should fail.
	program, data = pb.Program()
	cost = programCost.Add(bandwidthCost.Sub64(1))
	_, limit, err = rhp.managedExecuteProgram(epr, data, cost, true, true)
	if err == nil || !strings.Contains(err.Error(), modules.ErrInsufficientBandwidthBudget.Error()) {
		t.Fatalf("expected ExecuteProgram to fail due to insufficient bandwidth budget: %v", err)
	}
	// Log the bandwidth used by this RPC.
	t.Logf("Used bandwidth (invalid has sector program): %v down, %v up", limit.Downloaded(), limit.Uploaded())
	// Check that the remaining balance is correct again. We expect the host to
	// charge us for the program since the bandwidth limit was reached when
	// sending the response, after executing the program.
	downloadCost = pt.DownloadBandwidthCost.Mul64(limit.Downloaded())
	uploadCost = pt.UploadBandwidthCost.Mul64(limit.Uploaded())

	expectedBalance = expectedBalance.Sub(downloadCost).Sub(uploadCost).Sub(programCost)
	err = verifyBalance(am, rhp.staticAccountID, expectedBalance)
	if err != nil {
		t.Fatal(err)
	}
}

// addRandomSector is a helper function that creates a random sector and adds it
// to the storage obligation.
func addRandomSector(rhp *renterHostPair) (crypto.Hash, []byte, error) {
	// grab some variables
	fcid := rhp.staticFCID
	renterPK := rhp.staticRenterPK
	host := rhp.staticHT.host
	ht := rhp.staticHT

	// create a random sector
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)

	// fetch the SO
	so, err := host.managedGetStorageObligation(fcid)
	if err != nil {
		return crypto.Hash{}, nil, err
	}

	// add a new revision
	so.SectorRoots = append(so.SectorRoots, sectorRoot)
	so, err = ht.addNewRevision(so, renterPK, uint64(len(sectorData)), sectorRoot)
	if err != nil {
		return crypto.Hash{}, nil, err
	}

	// modify the SO
	host.managedLockStorageObligation(fcid)
	err = host.managedModifyStorageObligation(so, []crypto.Hash{}, map[crypto.Hash][]byte{sectorRoot: sectorData})
	if err != nil {
		host.managedUnlockStorageObligation(fcid)
		return crypto.Hash{}, nil, err
	}
	host.managedUnlockStorageObligation(fcid)

	return sectorRoot, sectorData, nil
}

// verifyBalance is a helper function that will verify if the ephemeral account
// with given id has a certain balance. It does this in a (short) build.Retry
// loop to avoid race conditions.
func verifyBalance(am *accountManager, id modules.AccountID, expected types.Currency) error {
	return build.Retry(100, 100*time.Millisecond, func() error {
		actual := am.callAccountBalance(id)
		if !actual.Equals(expected) {
			return fmt.Errorf("expected %v remaining balance but got %v", expected, actual)
		}
		return nil
	})
}

// TestExecuteReadOffsetProgram tests the managedRPCExecuteProgram with a valid
// 'ReadOffset' program that only reads from a sector.
func TestExecuteReadOffsetProgram(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a blank host tester
	rhp, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rhp.Close()
		if err != nil {
			t.Error(err)
		}
	}()
	ht := rhp.staticHT

	// get a snapshot of the SO before running the program.
	sos, err := ht.host.managedGetStorageObligationSnapshot(rhp.staticFCID)
	if err != nil {
		t.Fatal(err)
	}

	// create a random sector
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)
	// modify the host's storage obligation to add the sector
	so, err := ht.host.managedGetStorageObligation(rhp.staticFCID)
	if err != nil {
		t.Fatal(err)
	}
	so.SectorRoots = []crypto.Hash{sectorRoot}
	ht.host.managedLockStorageObligation(rhp.staticFCID)
	err = ht.host.managedModifyStorageObligation(so, []crypto.Hash{}, map[crypto.Hash][]byte{sectorRoot: sectorData})
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedUnlockStorageObligation(rhp.staticFCID)

	// select a random number of segments to read at random offset
	numSegments := fastrand.Uint64n(5) + 1
	totalSegments := modules.SectorSize / crypto.SegmentSize
	offset := fastrand.Uint64n(totalSegments-numSegments+1) * crypto.SegmentSize
	length := numSegments * crypto.SegmentSize

	// create the 'ReadOffset' program.
	pt := rhp.managedPriceTable()
	pb := modules.NewProgramBuilder(pt, 0)
	pb.AddReadOffsetInstruction(length, offset, true)
	program, data := pb.Program()
	programCost, refund, collateral := pb.Cost(true)

	// prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.staticFCID,
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// fund an account.
	fundingAmt := rhp.staticHT.host.managedInternalSettings().MaxEphemeralAccountBalance.Add(pt.FundAccountCost)
	_, err = rhp.managedFundEphemeralAccount(fundingAmt, true)
	if err != nil {
		t.Fatal(err)
	}

	// Compute expected bandwidth cost. These hardcoded values were chosen after
	// running this test with a high budget and measuring the used bandwidth for
	// this particular program on the "renter" side. This way we can test that
	// the bandwidth measured by the renter is large enough to be accepted by
	// the host.
	expectedDownload := uint64(1460)
	expectedUpload := uint64(1460)
	downloadCost := pt.DownloadBandwidthCost.Mul64(expectedDownload)
	uploadCost := pt.UploadBandwidthCost.Mul64(expectedUpload)
	bandwidthCost := downloadCost.Add(uploadCost)
	cost := programCost.Add(bandwidthCost)

	// execute program.
	resps, bandwidth, err := rhp.managedExecuteProgram(epr, data, cost, true, true)
	if err != nil {
		t.Log("cost", cost.HumanString())
		t.Log("expected ea balance", rhp.staticHT.host.managedInternalSettings().MaxEphemeralAccountBalance.HumanString())
		t.Fatal(err)
	}
	// there should only be a single response.
	if len(resps) != 1 {
		t.Fatalf("expected 1 response but got %v", len(resps))
	}
	resp := resps[0]

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
	if !resp.AdditionalCollateral.Equals(collateral) {
		t.Fatalf("collateral doesnt't match expected collateral: %v != %v", resp.AdditionalCollateral.HumanString(), collateral.HumanString())
	}
	if !resp.FailureRefund.Equals(refund) {
		t.Fatalf("refund doesn't match expected refund: %v != %v", resp.FailureRefund.HumanString(), refund.HumanString())
	}
	if uint64(len(resp.Output)) != length {
		t.Fatalf("expected returned data to have length %v but was %v", length, len(resp.Output))
	}

	if !bytes.Equal(sectorData[offset:offset+length], resp.Output) {
		t.Fatal("Unexpected data")
	}

	// verify the proof
	proofStart := int(offset) / crypto.SegmentSize
	proofEnd := int(offset+length) / crypto.SegmentSize
	proof := crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
	if !reflect.DeepEqual(proof, resp.Proof) {
		t.Fatal("proof doesn't match expected proof")
	}

	// verify the cost
	if !resp.TotalCost.Equals(programCost) {
		t.Fatalf("wrong TotalCost %v != %v", resp.TotalCost.HumanString(), programCost.HumanString())
	}

	t.Logf("Used bandwidth (read offset program): %v down, %v up", bandwidth.Downloaded(), bandwidth.Uploaded())
}

// TestVerifyExecuteProgramRevision is a unit test covering
// verifyExecuteProgramRevision.
func TestVerifyExecuteProgramRevision(t *testing.T) {
	t.Parallel()

	// create a current revision and a payment revision
	height := types.BlockHeight(0)
	curr := types.FileContractRevision{
		NewValidProofOutputs: []types.SiacoinOutput{
			{Value: types.NewCurrency64(100)}, // renter
			{Value: types.NewCurrency64(50)},  // host
		},
		NewMissedProofOutputs: []types.SiacoinOutput{
			{Value: types.NewCurrency64(100)}, // renter
			{Value: types.NewCurrency64(50)},  // host
			{Value: types.ZeroCurrency},       // void
		},
		NewWindowStart:    types.BlockHeight(revisionSubmissionBuffer) + 1,
		NewFileSize:       fastrand.Uint64n(1000) * modules.SectorSize,
		NewRevisionNumber: fastrand.Uint64n(1000),
	}

	// deepCopy is a helper function that makes a deep copy of a revision
	deepCopy := func(rev types.FileContractRevision) (revCopy types.FileContractRevision) {
		rBytes := encoding.Marshal(rev)
		err := encoding.Unmarshal(rBytes, &revCopy)
		if err != nil {
			panic(err)
		}
		return
	}

	// create a valid revision as a baseline for the test.
	newFileSize := curr.NewFileSize + modules.SectorSize
	newRevisionNumber := curr.NewRevisionNumber + 1
	transferred := types.NewCurrency64(20)
	newRoot := crypto.Hash{}
	fastrand.Read(newRoot[:])
	validRevision, err := curr.ExecuteProgramRevision(newRevisionNumber, transferred, newRoot, newFileSize)
	if err != nil {
		t.Fatal(err)
	}

	// verify a properly created payment revision is accepted
	err = verifyExecuteProgramRevision(curr, validRevision, height, transferred, newFileSize, newRoot)
	if err != nil {
		t.Fatal("Unexpected error when verifying revision, ", err)
	}

	// expect ErrBadContractOutputCounts
	badOutputs := []types.SiacoinOutput{validRevision.NewMissedProofOutputs[0]}
	badRevision := deepCopy(validRevision)
	badRevision.NewMissedProofOutputs = badOutputs
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrBadContractOutputCounts) {
		t.Fatalf("Expected ErrBadContractOutputCounts but received '%v'", err)
	}

	// same but for missed outputs
	badOutputs = []types.SiacoinOutput{validRevision.NewMissedProofOutputs[0]}
	badRevision = deepCopy(validRevision)
	badRevision.NewMissedProofOutputs = badOutputs
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrBadContractOutputCounts) {
		t.Fatalf("Expected ErrBadContractOutputCounts but received '%v'", err)
	}

	// expect ErrLateRevision
	badCurr := deepCopy(curr)
	badCurr.NewWindowStart = curr.NewWindowStart - 1
	err = verifyExecuteProgramRevision(badCurr, validRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrLateRevision) {
		t.Fatalf("Expected ErrLateRevision but received '%v'", err)
	}

	// expect host payout address changed
	hash := crypto.HashBytes([]byte("random"))
	badRevision = deepCopy(validRevision)
	badRevision.NewValidProofOutputs[1].UnlockHash = types.UnlockHash(hash)
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if err == nil || !strings.Contains(err.Error(), "valid host output address changed") {
		t.Fatalf("Expected host payout error but received '%v'", err)
	}

	// expect host payout address changed
	badRevision = deepCopy(validRevision)
	badRevision.NewMissedProofOutputs[1].UnlockHash = types.UnlockHash(hash)
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if err == nil || !strings.Contains(err.Error(), "missed host output address changed") {
		t.Fatalf("Expected host payout error but received '%v'", err)
	}

	// expect lost collateral address changed
	badRevision = deepCopy(validRevision)
	badRevision.NewMissedProofOutputs[2].UnlockHash = types.UnlockHash(hash)
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrVoidAddressChanged) {
		t.Fatalf("Expected lost collaterall error but received '%v'", err)
	}

	// renter valid payout changed.
	badRevision = deepCopy(validRevision)
	badRevision.NewValidProofOutputs[0].Value = badRevision.NewValidProofOutputs[0].Value.Add64(1)
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrValidRenterPayoutChanged) {
		t.Fatalf("Expected ErrValidRenterPayoutChanged error but received '%v'", err)
	}

	// renter missed payout changed.
	badRevision = deepCopy(validRevision)
	badRevision.NewMissedProofOutputs[0].Value = badRevision.NewMissedProofOutputs[0].Value.Add64(1)
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrMissedRenterPayoutChanged) {
		t.Fatalf("Expected ErrMissedRenterPayoutChanged error but received '%v'", err)
	}

	// host valid payout changed.
	badRevision = deepCopy(validRevision)
	badRevision.NewValidProofOutputs[1].Value = badRevision.NewValidProofOutputs[1].Value.Add64(1)
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrValidHostPayoutChanged) {
		t.Fatalf("Expected ErrValidHostPayoutChanged error but received '%v'", err)
	}

	// expect ErrLowHostMissedOutput
	badCurr = deepCopy(curr)
	currOut := curr.MissedHostOutput()
	currOut.Value = currOut.Value.Add64(1)
	badCurr.NewMissedProofOutputs[1] = currOut
	err = verifyExecuteProgramRevision(badCurr, validRevision, height, transferred, newFileSize, newRoot)
	if err == nil || !strings.Contains(err.Error(), string(ErrLowHostMissedOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrLowHostMissedOutput), err)
	}

	// expect an error saying too much money was transferred
	badRevision = deepCopy(validRevision)
	badRevision.NewMissedProofOutputs[1].Value = badRevision.NewMissedProofOutputs[1].Value.Sub64(1)
	badRevision.NewMissedProofOutputs[2].Value = badRevision.NewMissedProofOutputs[2].Value.Add64(1)
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrLowHostMissedOutput) {
		t.Fatalf("Expected '%v' but received '%v'", ErrLowHostMissedOutput.Error(), err)
	}

	// expect ErrHighRenterMissedOutput
	badCurr = deepCopy(curr)
	badCurr.SetMissedRenterPayout(badCurr.MissedRenterPayout().Sub64(1))
	badRevision = deepCopy(badCurr)
	badRevision.NewRevisionNumber++
	err = verifyExecuteProgramRevision(badCurr, badRevision, height, transferred, newFileSize, newRoot)
	if err == nil || !strings.Contains(err.Error(), string(ErrHighRenterMissedOutput)) {
		t.Fatalf("Expected '%v' but received '%v'", string(ErrHighRenterMissedOutput), err)
	}

	// expect ErrBadRevisionNumber
	badOutputs = []types.SiacoinOutput{validRevision.NewMissedProofOutputs[0]}
	badRevision = deepCopy(validRevision)
	badRevision.NewMissedProofOutputs = badOutputs
	badRevision.NewRevisionNumber--
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrBadRevisionNumber) {
		t.Fatalf("Expected ErrBadRevisionNumber but received '%v'", err)
	}

	// expect ErrBadParentID
	badRevision = deepCopy(validRevision)
	badRevision.ParentID = types.FileContractID(hash)
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrBadParentID) {
		t.Fatalf("Expected ErrBadParentID but received '%v'", err)
	}

	// expect ErrBadUnlockConditions
	badRevision = deepCopy(validRevision)
	badRevision.UnlockConditions.Timelock = validRevision.UnlockConditions.Timelock + 1
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrBadUnlockConditions) {
		t.Fatalf("Expected ErrBadUnlockConditions but received '%v'", err)
	}

	// expect ErrBadFileSize
	badRevision = deepCopy(validRevision)
	badRevision.NewFileSize = validRevision.NewFileSize + 1
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrBadFileSize) {
		t.Fatalf("Expected ErrBadFileSize but received '%v'", err)
	}

	// expect ErrBadFileMerkleRoot
	badRevision = deepCopy(validRevision)
	badRevision.NewFileMerkleRoot = hash
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrBadFileMerkleRoot) {
		t.Fatalf("Expected ErrBadFileMerkleRoot but received '%v'", err)
	}

	// expect ErrBadWindowStart
	badRevision = deepCopy(validRevision)
	badRevision.NewWindowStart = curr.NewWindowStart + 1
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrBadWindowStart) {
		t.Fatalf("Expected ErrBadWindowStart but received '%v'", err)
	}

	// expect ErrBadWindowEnd
	badRevision = deepCopy(validRevision)
	badRevision.NewWindowEnd = curr.NewWindowEnd - 1
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrBadWindowEnd) {
		t.Fatalf("Expected ErrBadWindowEnd but received '%v'", err)
	}

	// expect ErrBadUnlockHash
	badRevision = deepCopy(validRevision)
	badRevision.NewUnlockHash = types.UnlockHash(hash)
	err = verifyExecuteProgramRevision(curr, badRevision, height, transferred, newFileSize, newRoot)
	if !errors.Contains(err, ErrBadUnlockHash) {
		t.Fatalf("Expected ErrBadUnlockHash but received '%v'", err)
	}
}

// TestExecuteAppendProgram tests the managedRPCExecuteProgram with a valid
// 'Append' program.
func TestExecuteAppendProgram(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a testing pair.
	rhp, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rhp.Close()
		if err != nil {
			t.Error(err)
		}
	}()

	// helper to get current revision's number.
	revNum := func() uint64 {
		recent, err := rhp.managedRecentHostRevision()
		if err != nil {
			t.Fatal(err)
		}
		return recent.NewRevisionNumber
	}

	// Prepare data to upload.
	data := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(data)

	// Get the remaining contract duration.
	so, err := rhp.staticHT.host.managedGetStorageObligation(rhp.staticFCID)
	if err != nil {
		t.Fatal(err)
	}
	duration := so.proofDeadline() - rhp.staticHT.host.BlockHeight()

	// create the 'Append' program.
	pt := rhp.managedPriceTable()
	pb := modules.NewProgramBuilder(pt, duration)
	err = pb.AddAppendInstruction(data, true, duration)
	if err != nil {
		t.Fatal(err)
	}
	program, data := pb.Program()
	programCost, storageCost, collateral := pb.Cost(true)
	totalCost, _, _ := pb.Cost(false)

	// prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.staticFCID,
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// fund an account.
	his := rhp.staticHT.host.managedInternalSettings()
	maxBalance := his.MaxEphemeralAccountBalance
	fundingAmt := maxBalance.Add(pt.FundAccountCost)
	_, err = rhp.managedFundEphemeralAccount(fundingAmt, true)
	if err != nil {
		t.Fatal(err)
	}

	// Compute expected bandwidth cost. These hardcoded values were chosen after
	// running this test with a high budget and measuring the used bandwidth for
	// this particular program on the "renter" side. This way we can test that
	// the bandwidth measured by the renter is large enough to be accepted by
	// the host.
	expectedDownload := uint64(2920) // download
	expectedUpload := uint64(7300)   // upload
	downloadCost := pt.DownloadBandwidthCost.Mul64(expectedDownload)
	uploadCost := pt.UploadBandwidthCost.Mul64(expectedUpload)
	bandwidthCost := downloadCost.Add(uploadCost)
	cost := programCost.Add(bandwidthCost)

	// check contract revision number before executing the program.
	revNumBefore := revNum()

	// execute program.
	resps, limit, err := rhp.managedExecuteProgram(epr, data, cost, true, true)
	if err != nil {
		t.Fatal(err)
	}

	// the revision number should have increased by 1.
	revNumAfter := revNum()
	if revNumAfter != revNumBefore+1 {
		t.Errorf("revision number wasn't incremented by 1 %v %v", revNumBefore, revNumAfter)
	}

	// Log the bandwidth used by this RPC.
	t.Logf("Used bandwidth (append sector program): %v down, %v up", limit.Downloaded(), limit.Uploaded())

	// there should only be a single response.
	if len(resps) != 1 {
		t.Fatalf("expected 1 response but got %v", len(resps))
	}
	resp := resps[0]

	// check response.
	if resp.Error != nil {
		t.Fatal(resp.Error)
	}
	// programs that don't require a snapshot return a 0 contract size.
	if resp.NewSize != modules.SectorSize {
		t.Fatalf("expected contract size to stay the same: %v != %v", modules.SectorSize, resp.NewSize)
	}
	// programs that don't require a snapshot return a zero hash.
	if resp.NewMerkleRoot != sectorRoot {
		t.Fatalf("expected merkle root to stay the same: %v != %v", sectorRoot, resp.NewMerkleRoot)
	}
	if len(resp.Proof) != 0 {
		t.Fatalf("expected proof length to be %v but was %v", 0, len(resp.Proof))
	}

	if !resp.AdditionalCollateral.Equals(collateral) {
		t.Fatalf("collateral doesnt't match expected collateral: %v != %v", resp.AdditionalCollateral.HumanString(), collateral.HumanString())
	}
	if !resp.FailureRefund.Equals(storageCost) {
		t.Fatalf("storage cost doesn't match expected storage cost: %v != %v", resp.FailureRefund.HumanString(), storageCost.HumanString())
	}
	if uint64(len(resp.Output)) != 0 {
		t.Fatalf("expected returned data to have length %v but was %v", 0, len(resp.Output))
	}

	// verify the cost
	if !resp.TotalCost.Equals(totalCost) {
		t.Fatalf("wrong TotalCost %v != %v", resp.TotalCost.HumanString(), programCost.HumanString())
	}

	// verify the EA balance
	am := rhp.staticHT.host.staticAccountManager
	expectedBalance := maxBalance.Sub(cost)
	err = verifyBalance(am, rhp.staticAccountID, expectedBalance)
	if err != nil {
		t.Fatal(err)
	}

	// execute program but this time without finalizing it to check for the
	// refund.
	programCost, _, _ = pb.Cost(false)
	expectedDownload = uint64(5840) // download
	expectedUpload = uint64(4380)   // upload
	downloadCost = pt.DownloadBandwidthCost.Mul64(expectedDownload)
	uploadCost = pt.UploadBandwidthCost.Mul64(expectedUpload)
	bandwidthCost = downloadCost.Add(uploadCost)
	cost = programCost.Add(bandwidthCost)

	resps, limit, err = rhp.managedExecuteProgram(epr, data, cost, true, false)
	if err != nil {
		t.Fatal(err)
	}

	// Log the bandwidth used by this RPC.
	t.Logf("Used bandwidth (append sector program): %v down, %v up", limit.Downloaded(), limit.Uploaded())

	// verify the EA balance
	expectedBalance = expectedBalance.Sub(cost)
	err = verifyBalance(am, rhp.staticAccountID, expectedBalance)
	if err != nil {
		t.Fatal(err)
	}
}

// TestExecuteHasSectorProgramBatching tests the managedRPCExecuteProgram with a
// valid 'HasSector' program containing multiple HasSector instructions.
func TestExecuteHasSectorProgramBatching(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a blank host tester
	rhp, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rhp.Close()
		if err != nil {
			t.Error(err)
		}
	}()
	ht := rhp.staticHT

	// Add a sector to the host but not the storage obligation or contract. This
	// instruction should also work for foreign sectors.
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)
	err = ht.host.AddSector(sectorRoot, sectorData)
	if err != nil {
		t.Fatal(err)
	}

	// Create the 'HasSector' program.
	pt := rhp.managedPriceTable()
	pb := modules.NewProgramBuilder(pt, types.BlockHeight(fastrand.Uint64n(1000))) // random duration since HasSector doesn't depend on duration.
	for i := 0; i < 10; i++ {
		pb.AddHasSectorInstruction(sectorRoot)
	}
	program, data := pb.Program()
	programCost, _, _ := pb.Cost(true)

	// Prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.staticFCID,
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// Fund an account with the max balance.
	maxBalance := rhp.staticHT.host.managedInternalSettings().MaxEphemeralAccountBalance
	fundingAmt := maxBalance.Add(pt.FundAccountCost)
	_, err = rhp.managedFundEphemeralAccount(fundingAmt, true)
	if err != nil {
		t.Fatal(err)
	}

	// Compute expected bandwidth cost. These hardcoded values were chosen after
	// running this test with a high budget and measuring the used bandwidth for
	// this particular program on the "renter" side. This way we can test that
	// the bandwidth measured by the renter is large enough to be accepted by
	// the host.
	expectedDownload := uint64(1460) // download
	expectedUpload := uint64(1460)   // upload
	downloadCost := pt.DownloadBandwidthCost.Mul64(expectedDownload)
	uploadCost := pt.UploadBandwidthCost.Mul64(expectedUpload)
	bandwidthCost := downloadCost.Add(uploadCost)

	// Execute program.
	cost := programCost.Add(bandwidthCost)
	resps, limit, err := rhp.managedExecuteProgram(epr, data, cost, true, true)
	if err != nil {
		t.Fatal(err)
	}
	// Log the bandwidth used by this RPC.
	t.Logf("Used bandwidth (valid has sector program): %v down, %v up", limit.Downloaded(), limit.Uploaded())
	// There should only be a single response.
	if len(resps) != 10 {
		t.Fatalf("expected 10 responses but got %v", len(resps))
	}

	// Check response.
	for _, resp := range resps {
		if resp.Error != nil {
			t.Fatal(resp.Error)
		}
		if resp.Output[0] != 1 {
			t.Fatalf("wrong Output %v != %v", resp.Output[0], []byte{1})
		}
	}

	// Make sure the right amount of money remains on the EA.
	am := rhp.staticHT.host.staticAccountManager
	expectedBalance := maxBalance.Sub(cost)
	err = verifyBalance(am, rhp.staticAccountID, expectedBalance)
	if err != nil {
		t.Fatal(err)
	}
}

// TestExecuteUpdateRegistryProgram tests the managedRPCExecuteProgram with a
// valid 'UpdateRegistry' program.
func TestExecuteUpdateRegistryProgram(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a blank host tester
	rhp, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rhp.Close()
		if err != nil {
			t.Error(err)
		}
	}()

	// Grow the registry.
	h := rhp.staticHT.host
	is := h.InternalSettings()
	is.RegistrySize = 64 * modules.RegistryEntrySize
	err = h.SetInternalSettings(is)
	if err != nil {
		t.Fatal(err)
	}

	// get a snapshot of the SO before running the program.
	sos, err := rhp.staticHT.host.managedGetStorageObligationSnapshot(rhp.staticFCID)
	if err != nil {
		t.Fatal(err)
	}

	// Create a signed registry value.
	sk, pk := crypto.GenerateKeyPair()
	tweak := crypto.Hash{1, 2, 3}
	data := fastrand.Bytes(modules.RegistryDataSize)
	rev := uint64(1)
	rv := modules.NewRegistryValue(tweak, data, rev, modules.RegistryTypeWithoutPubkey).Sign(sk)
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	// Create the 'UpdateRegistry' program.
	pt := rhp.managedPriceTable()
	pb := modules.NewProgramBuilder(pt, types.BlockHeight(fastrand.Uint64n(1000))) // random duration since UpdateRegistry doesn't depend on duration.
	err = pb.AddUpdateRegistryInstruction(spk, rv)
	if err != nil {
		t.Fatal(err)
	}
	program, data := pb.Program()
	programCost, refund, collateral := pb.Cost(true)

	// Prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.staticFCID,
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// Fund an account with the max balance.
	maxBalance := rhp.staticHT.host.managedInternalSettings().MaxEphemeralAccountBalance
	fundingAmt := maxBalance.Add(pt.FundAccountCost)
	_, err = rhp.managedFundEphemeralAccount(fundingAmt, true)
	if err != nil {
		t.Fatal(err)
	}

	// Compute expected bandwidth cost. These hardcoded values were chosen after
	// running this test with a high budget and measuring the used bandwidth for
	// this particular program on the "renter" side. This way we can test that
	// the bandwidth measured by the renter is large enough to be accepted by
	// the host.
	expectedDownload := uint64(1460) // download
	expectedUpload := uint64(1460)   // upload
	downloadCost := pt.DownloadBandwidthCost.Mul64(expectedDownload)
	uploadCost := pt.UploadBandwidthCost.Mul64(expectedUpload)
	bandwidthCost := downloadCost.Add(uploadCost)

	// Execute program.
	cost := programCost.Add(bandwidthCost)
	resps, limit, err := rhp.managedExecuteProgram(epr, data, cost, true, true)
	if err != nil {
		t.Fatal(err)
	}
	// Log the bandwidth used by this RPC.
	t.Logf("Used bandwidth (valid UpdateRegistry program): %v down, %v up", limit.Downloaded(), limit.Uploaded())
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
	if len(resp.Output) != 0 {
		t.Fatalf("wrong Output length %v != %v", len(resp.Output), 0)
	}
	if !resp.TotalCost.Equals(programCost) {
		t.Fatalf("wrong TotalCost %v != %v", resp.TotalCost.HumanString(), programCost.HumanString())
	}
	if !resp.FailureRefund.Equals(refund) {
		t.Fatalf("wrong StorageCost %v != %v", resp.FailureRefund.HumanString(), refund.HumanString())
	}
	// Make sure the right amount of money remains on the EA.
	am := rhp.staticHT.host.staticAccountManager
	expectedBalance := maxBalance.Sub(cost)
	err = verifyBalance(am, rhp.staticAccountID, expectedBalance)
	if err != nil {
		t.Fatal(err)
	}

	// Run the same program again. Should fail due to the revision number being
	// the same.
	resps, limit, err = rhp.managedExecuteProgram(epr, data, cost, true, true)
	if err != nil {
		t.Fatal(err)
	}
	// Log the bandwidth used by this RPC.
	t.Logf("Used bandwidth (valid UpdateRegistry program): %v down, %v up", limit.Downloaded(), limit.Uploaded())
	// There should only be a single response.
	if len(resps) != 1 {
		t.Fatalf("expected 1 response but got %v", len(resps))
	}
	resp = resps[0]

	// Check response.
	if resp.Error == nil || !strings.Contains(resp.Error.Error(), modules.ErrSameRevNum.Error()) {
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
	}
	if len(resp.Proof) != 0 {
		t.Fatalf("wrong Proof %v != %v", resp.Proof, []crypto.Hash{})
	}
	if !resp.TotalCost.Equals(programCost) {
		t.Fatalf("wrong TotalCost %v != %v", resp.TotalCost.HumanString(), programCost.HumanString())
	}
	if !resp.FailureRefund.Equals(refund) {
		t.Fatalf("wrong StorageCost %v != %v", resp.FailureRefund.HumanString(), refund.HumanString())
	}
	// Parse response.
	var sig2 crypto.Signature
	copy(sig2[:], resp.Output[:crypto.SignatureSize])
	rev2 := binary.LittleEndian.Uint64(resp.Output[crypto.SignatureSize:])
	data2 := resp.Output[crypto.SignatureSize+8 : len(resp.Output)-1]
	entryType := modules.RegistryEntryType(resp.Output[len(resp.Output)-1])
	rv2 := modules.NewSignedRegistryValue(tweak, data2, rev2, sig2, entryType)
	if !reflect.DeepEqual(rv, rv2) {
		t.Log(rv)
		t.Log(rv2)
		t.Fatal("rvs don't match")
	}

	// Make sure the right amount of money remains on the EA.
	expectedBalance = expectedBalance.Sub(cost).Add(refund)
	err = verifyBalance(am, rhp.staticAccountID, expectedBalance)
	if err != nil {
		t.Fatal(err)
	}

	// Run it one more time. Time time with a lower revision number which should
	// also fail.
	pb = modules.NewProgramBuilder(pt, types.BlockHeight(fastrand.Uint64n(1000))) // random duration since UpdateRegistry doesn't depend on duration.
	rvLowRev := rv
	rvLowRev.Revision--
	rvLowRev = rvLowRev.Sign(sk)
	err = pb.AddUpdateRegistryInstruction(spk, rvLowRev)
	if err != nil {
		t.Fatal(err)
	}
	program, data = pb.Program()

	// Prepare the request.
	epr = modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.staticFCID,
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	resps, limit, err = rhp.managedExecuteProgram(epr, data, cost, true, true)
	if err != nil {
		t.Fatal(err)
	}
	// Log the bandwidth used by this RPC.
	t.Logf("Used bandwidth (valid UpdateRegistry program): %v down, %v up", limit.Downloaded(), limit.Uploaded())
	// There should only be a single response.
	if len(resps) != 1 {
		t.Fatalf("expected 1 response but got %v", len(resps))
	}
	resp = resps[0]

	// Check response.
	if resp.Error == nil || !strings.Contains(resp.Error.Error(), modules.ErrLowerRevNum.Error()) {
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
	}
	if len(resp.Proof) != 0 {
		t.Fatalf("wrong Proof %v != %v", resp.Proof, []crypto.Hash{})
	}
	if !resp.TotalCost.Equals(programCost) {
		t.Fatalf("wrong TotalCost %v != %v", resp.TotalCost.HumanString(), programCost.HumanString())
	}
	if !resp.FailureRefund.Equals(refund) {
		t.Fatalf("wrong StorageCost %v != %v", resp.FailureRefund.HumanString(), refund.HumanString())
	}
	// Parse response.
	copy(sig2[:], resp.Output[:crypto.SignatureSize])
	rev2 = binary.LittleEndian.Uint64(resp.Output[crypto.SignatureSize:])
	data2 = resp.Output[crypto.SignatureSize+8 : len(resp.Output)-1]
	entryType = modules.RegistryEntryType(resp.Output[len(resp.Output)-1])
	rv2 = modules.NewSignedRegistryValue(tweak, data2, rev2, sig2, entryType)
	if !reflect.DeepEqual(rv, rv2) {
		t.Log(rv)
		t.Log(rv2)
		t.Fatal("rvs don't match")
	}

	// Make sure the right amount of money remains on the EA.
	expectedBalance = expectedBalance.Sub(cost).Add(refund)
	err = verifyBalance(am, rhp.staticAccountID, expectedBalance)
	if err != nil {
		t.Fatal(err)
	}
}

// TestExecuteReadRegistryProgram tests the managedRPCExecuteProgram with a
// valid 'ReadRegistry' program.
func TestExecuteReadRegistryProgram(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Regular cases.
	t.Run("NoPubkeyNoType", func(t *testing.T) {
		testExecuteReadRegistryProgram(t, modules.RegistryTypeWithoutPubkey, modules.ReadRegistryVersionNoType, false)
	})
	t.Run("NoPubkeyWithType", func(t *testing.T) {
		testExecuteReadRegistryProgram(t, modules.RegistryTypeWithoutPubkey, modules.ReadRegistryVersionWithType, false)
	})
	t.Run("WithPubkeyNoType", func(t *testing.T) {
		testExecuteReadRegistryProgram(t, modules.RegistryTypeWithPubkey, modules.ReadRegistryVersionNoType, false)
	})
	t.Run("WithPubkeyWithType", func(t *testing.T) {
		testExecuteReadRegistryProgram(t, modules.RegistryTypeWithPubkey, modules.ReadRegistryVersionWithType, false)
	})
	t.Run("NoPubkeyV156Update", func(t *testing.T) {
		testExecuteReadRegistryProgram(t, modules.RegistryTypeWithoutPubkey, modules.ReadRegistryVersionWithType, false)
	})

	// Special v156 cases.
	//
	t.Run("WithPubkeyV156", func(t *testing.T) {
		testExecuteReadRegistryProgram(t, modules.RegistryTypeWithPubkey, modules.ReadRegistryVersionNoType, true)
	})
	t.Run("WithoutPubkeyV156", func(t *testing.T) {
		testExecuteReadRegistryProgram(t, modules.RegistryTypeWithoutPubkey, modules.ReadRegistryVersionNoType, true)
	})
}

// testExecuteReadRegistryProgram tests the managedRPCExecuteProgram with a
// valid 'ReadRegistry' program.
func testExecuteReadRegistryProgram(t *testing.T, regEntryType modules.RegistryEntryType, regReadVersion modules.ReadRegistryVersion, v156Read bool) {
	t.Parallel()

	// create a blank host tester
	rhp, err := newRenterHostPair(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rhp.Close()
		if err != nil {
			t.Error(err)
		}
	}()

	// Grow the registry.
	h := rhp.staticHT.host
	is := h.InternalSettings()
	is.RegistrySize = 64 * modules.RegistryEntrySize
	err = h.SetInternalSettings(is)
	if err != nil {
		t.Fatal(err)
	}

	// get a snapshot of the SO before running the program.
	sos, err := rhp.staticHT.host.managedGetStorageObligationSnapshot(rhp.staticFCID)
	if err != nil {
		t.Fatal(err)
	}

	// Create a signed registry value.
	sk, pk := crypto.GenerateKeyPair()
	tweak := crypto.Hash{1, 2, 3}
	data := fastrand.Bytes(modules.RegistryDataSize)
	rev := uint64(0)
	rv := modules.NewRegistryValue(tweak, data, rev, regEntryType).Sign(sk)
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	// Update the registry.
	_, err = h.RegistryUpdate(rv, spk, 123)
	if err != nil {
		t.Fatal(err)
	}

	// Create the 'UpdateRegistry' program.
	pt := rhp.managedPriceTable()
	pb := modules.NewProgramBuilder(pt, types.BlockHeight(fastrand.Uint64n(1000))) // random duration since ReadRegistry doesn't depend on duration.
	if v156Read {
		_, err = pb.V156AddReadRegistryInstruction(spk, rv.Tweak)
	} else {
		_, err = pb.AddReadRegistryInstruction(spk, rv.Tweak, regReadVersion)
	}
	if err != nil {
		t.Fatal(err)
	}
	program, data := pb.Program()
	programCost, refund, collateral := pb.Cost(true)

	// Prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    rhp.staticFCID,
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// Fund an account with the max balance.
	maxBalance := rhp.staticHT.host.managedInternalSettings().MaxEphemeralAccountBalance
	fundingAmt := maxBalance.Add(pt.FundAccountCost)
	_, err = rhp.managedFundEphemeralAccount(fundingAmt, true)
	if err != nil {
		t.Fatal(err)
	}

	// Compute expected bandwidth cost. These hardcoded values were chosen after
	// running this test with a high budget and measuring the used bandwidth for
	// this particular program on the "renter" side. This way we can test that
	// the bandwidth measured by the renter is large enough to be accepted by
	// the host.
	expectedDownload := uint64(1460) // download
	expectedUpload := uint64(1460)   // upload
	downloadCost := pt.DownloadBandwidthCost.Mul64(expectedDownload)
	uploadCost := pt.UploadBandwidthCost.Mul64(expectedUpload)
	bandwidthCost := downloadCost.Add(uploadCost)

	// Execute program.
	cost := programCost.Add(bandwidthCost)
	resps, limit, err := rhp.managedExecuteProgram(epr, data, cost, true, true)
	if err != nil {
		t.Fatal(err)
	}
	// Log the bandwidth used by this RPC.
	t.Logf("Used bandwidth (valid UpdateRegistry program): %v down, %v up", limit.Downloaded(), limit.Uploaded())
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
	expectedOutputLength := 185 // 185 = 64 (sig) + 8 (revision) + 113 (data)
	if regReadVersion == modules.ReadRegistryVersionWithType {
		expectedOutputLength++
	}
	if len(resp.Output) != expectedOutputLength {
		t.Fatalf("wrong Output length %v != %v", len(resp.Output), expectedOutputLength)
	}
	if !resp.TotalCost.Equals(programCost) {
		t.Fatalf("wrong TotalCost %v != %v", resp.TotalCost.HumanString(), programCost.HumanString())
	}
	if !resp.FailureRefund.Equals(refund) {
		t.Fatalf("wrong StorageCost %v != %v", resp.FailureRefund.HumanString(), refund.HumanString())
	}

	// Parse output.
	var sig2 crypto.Signature
	copy(sig2[:], resp.Output[:crypto.SignatureSize])
	rev2 := binary.LittleEndian.Uint64(resp.Output[crypto.SignatureSize:])
	data2 := resp.Output[crypto.SignatureSize+8:]
	if regReadVersion == modules.ReadRegistryVersionWithType {
		// The last byte might be the entry type.
		if data2[len(data2)-1] != byte(rv.Type) {
			t.Fatal("wrong type")
		}
		data2 = data2[:len(data2)-1]
	}
	rv2 := modules.NewSignedRegistryValue(tweak, data2, rev2, sig2, rv.Type)
	if err := rv2.Verify(pk); err != nil {
		t.Fatal("verification failed", err)
	}

	// Make sure the right amount of money remains on the EA.
	am := rhp.staticHT.host.staticAccountManager
	expectedBalance := maxBalance.Sub(cost)
	err = verifyBalance(am, rhp.staticAccountID, expectedBalance)
	if err != nil {
		t.Fatal(err)
	}
}
