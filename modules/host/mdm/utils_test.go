package mdm

import (
	"bytes"
	"fmt"
	"io"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/fastrand"
)

// testCompareProgramValues compares the values of a program calculated using
// the test builder with the expected values returned from an actual program.
func testCompareProgramValues(pt *modules.RPCPriceTable, p modules.Program, programDataLen uint64, data io.Reader, values values) error {
	expectedValues, err := testProgramValues(p, programDataLen, data, pt)
	if err != nil {
		return err
	}
	if !values.Equals(expectedValues) {
		return fmt.Errorf("expected program values %v, got %v", expectedValues.HumanString(), values.HumanString())
	}
	return nil
}

// testCompareOutputs returns an error if an actual output does not match
// expected output.
func testCompareOutputs(output Output, expectedOutput Output) error {
	if output.Error != expectedOutput.Error {
		return fmt.Errorf("expected error %v, got %v", expectedOutput.Error, output.Error)
	}
	if output.NewSize != expectedOutput.NewSize {
		return fmt.Errorf("expected contract size %v, got %v", expectedOutput.NewSize, output.NewSize)
	}
	if output.NewMerkleRoot != expectedOutput.NewMerkleRoot {
		return fmt.Errorf("expected merkle root %v, got %v", expectedOutput.NewMerkleRoot, output.NewMerkleRoot)
	}

	// Check proof.
	if len(output.Proof) != len(expectedOutput.Proof) {
		return fmt.Errorf("expected proof to have length %v, got %v", len(expectedOutput.Proof), len(output.Proof))
	}
	for i, proof := range output.Proof {
		if proof != expectedOutput.Proof[i] {
			return fmt.Errorf("expected proof %v, got %v", proof, output.Proof[i])
		}
	}

	// Check data.
	if len(output.Output) != len(expectedOutput.Output) {
		return fmt.Errorf("expected output data to have length %v, got %v", len(expectedOutput.Output), len(output.Output))
	}
	if !bytes.Equal(output.Output, expectedOutput.Output) {
		return fmt.Errorf("expected output data %v, got %v", expectedOutput.Output, output.Output)
	}

	// Check values.
	actualValues := values{
		ExecutionCost: output.ExecutionCost,
		Refund:        output.PotentialRefund,
		Collateral:    output.AdditionalCollateral,
	}
	expectedValues := values{
		ExecutionCost: expectedOutput.ExecutionCost,
		Refund:        expectedOutput.PotentialRefund,
		Collateral:    expectedOutput.AdditionalCollateral,
	}
	if !actualValues.Equals(expectedValues) {
		return fmt.Errorf("expected %v, got %v", expectedValues.HumanString(), actualValues.HumanString())
	}

	return nil
}

// testProgramValues estimates the execution cost, refund, collateral, memory,
// and time given a program in the form of a list of instructions. This function
// creates a dummy program that decodes the instructions and their parameters,
// testing that they were properly encoded.
func testProgramValues(p modules.Program, programDataLen uint64, data io.Reader, pt *modules.RPCPriceTable) (values, error) {
	// Make a dummy program to allow us to get the instruction values.
	program := &program{
		staticProgramState: &programState{
			priceTable: pt,
		},
		staticData: openProgramData(data, programDataLen),
	}
	// Get initial program values.
	runningValues := values{
		ExecutionCost: modules.MDMInitCost(pt, programDataLen, uint64(len(p))),
		Memory:        modules.MDMInitMemory(),
	}

	for _, i := range p {
		// Decode instruction.
		i, err := decodeInstruction(program, i)
		if err != nil {
			return values{}, err
		}
		// Get the values for the instruction.
		v := values{}
		v.ExecutionCost, v.Refund, err = i.Cost()
		if err != nil {
			return values{}, err
		}
		v.Collateral = i.Collateral()
		v.Memory = i.Memory()
		time, err := i.Time()
		if err != nil {
			return values{}, err
		}
		// Update running values.
		runningValues.AddValues(pt, v, time)
	}

	// Get the final values for the program.
	runningValues = runningValues.Cost(pt, p.ReadOnly())

	return runningValues, nil
}

// randomSector is a testing helper function that initializes a random sector.
func randomSector() crypto.Hash {
	var sector crypto.Hash
	fastrand.Read(sector[:])
	return sector
}

// randomSectorData is a testing helper function that initializes random sector
// data.
func randomSectorData() []byte {
	return fastrand.Bytes(int(modules.SectorSize))
}

// randomSectorRoots is a testing helper function that initializes a number of
// random sector roots.
func randomSectorRoots(numRoots int) []crypto.Hash {
	roots := make([]crypto.Hash, numRoots)
	for i := 0; i < numRoots; i++ {
		fastrand.Read(roots[i][:]) // random initial merkle root
	}
	return roots
}

// randomSectorMap is a testing helper function that initializes a map with
// random sector data.
func randomSectorMap(roots []crypto.Hash) map[crypto.Hash][]byte {
	rootMap := make(map[crypto.Hash][]byte)
	for _, root := range roots {
		rootMap[root] = randomSectorData()
	}
	return rootMap
}
