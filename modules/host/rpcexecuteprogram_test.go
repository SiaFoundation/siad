package host

import (
	"io"
	"sync"
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
func newHasSectorProgram(merkleRoot crypto.Hash, pt modules.RPCPriceTable) (modules.Program, []byte, types.Currency, types.Currency, uint64) {
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

// TestExecuteProgram tests the managedRPCExecuteProgram with a valid
// 'HasSector' program.
func TestExecuteHasSectorProgram(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a blank host tester
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	// Add a sector.
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)
	err = ht.host.AddSector(sectorRoot, sectorData)
	if err != nil {
		t.Fatal(err)
	}

	// Add a storage obligation for testing.
	so, err := ht.newTesterStorageObligation()
	if err != nil {
		t.Fatal(err)
	}
	soid := so.id()
	ht.host.managedLockStorageObligation(soid)
	err = ht.host.managedAddStorageObligation(so, false)
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedUnlockStorageObligation(so.id())

	// Call the handler directly.
	cc, sc := createTestingConns()
	var wg sync.WaitGroup
	defer wg.Wait()
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := ht.host.managedRPCExecuteProgram(sc)
		if err != nil {
			t.Error("Failed to execute the 'HasSector' program", err)
		}
	}()

	// Fetch the price table.
	ht.host.mu.RLock()
	pt := ht.host.priceTable
	ht.host.mu.RUnlock()

	// Create the 'HasSector' program.
	program, data, cost, refund, _ := newHasSectorProgram(sectorRoot, pt)

	// Prepare the request.
	epr := modules.RPCExecuteProgramRequest{
		FileContractID:    soid,
		PriceTableID:      pt.UUID,
		Program:           program,
		ProgramDataLength: uint64(len(data)),
	}

	// Send the request.
	err = modules.RPCWrite(cc, epr)
	if err != nil {
		t.Fatal(err)
	}

	// Send the programData.
	_, err = cc.Write(data)
	if err != nil {
		t.Fatal(err)
	}

	// Read the response. There should only be one since there was only one
	// instruction.
	var resp modules.RPCExecuteProgramResponse
	err = modules.RPCRead(cc, &resp)
	if err != nil {
		t.Fatal(err)
	}

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

	// The next read should return io.EOF since the host closes the connection
	// after the RPC is done.
	err = modules.RPCRead(cc, &resp)
	if !errors.Contains(err, io.EOF) {
		t.Fatal(err)
	}
}
