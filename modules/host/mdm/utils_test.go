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
func testCompareProgramValues(pt *modules.RPCPriceTable, p modules.Program, programDataLen uint64, data io.Reader, values programValues) error {
	expectedValues, err := testProgramValues(p, programDataLen, data, pt)
	if err != nil {
		return err
	}
	if !values.Equals(expectedValues) {
		return fmt.Errorf("expected program values %v, got %v", expectedValues.HumanString(), values.HumanString())
	}
	return nil
}

// testCompareOutputs returns an error if an actual actual output does not match
// its expected output. It also verifies that the number of outputs matches.
// Returns the last output.
func testCompareOutputs(actualOutputs <-chan Output, expectedOutputs []Output) (Output, error) {
	numOutputs := 0
	var lastOutput Output

	for output := range actualOutputs {
		expectedOutput := expectedOutputs[numOutputs]

		if output.Error != expectedOutput.Error {
			return Output{}, fmt.Errorf("expected error %v, got %v", expectedOutput.Error, output.Error)
		}
		if output.NewSize != expectedOutput.NewSize {
			return Output{}, fmt.Errorf("expected contract size %v, got %v", expectedOutput.NewSize, output.NewSize)
		}
		if output.NewMerkleRoot != expectedOutput.NewMerkleRoot {
			return Output{}, fmt.Errorf("expected merkle root %v, got %v", expectedOutput.NewMerkleRoot, output.NewMerkleRoot)
		}

		// Check proof.
		if len(output.Proof) != len(expectedOutput.Proof) {
			return Output{}, fmt.Errorf("expected proof to have length %v, got %v", len(expectedOutput.Proof), len(output.Proof))
		}
		for i, proof := range output.Proof {
			if proof != expectedOutput.Proof[i] {
				return Output{}, fmt.Errorf("expected proof %v, got %v", proof, output.Proof[i])
			}
		}

		// Check data.
		if len(output.Output) != len(expectedOutput.Output) {
			return Output{}, fmt.Errorf("expected output data to have length %v, got %v", len(expectedOutput.Output), len(output.Output))
		}
		if !bytes.Equal(output.Output, expectedOutput.Output) {
			return Output{}, fmt.Errorf("expected output data %v, got %v", expectedOutput.Output, output.Output)
		}

		// Check values.
		if !output.RunningValues.Equals(expectedOutput.RunningValues) {
			return Output{}, fmt.Errorf("expected output values %v, got %v", output.RunningValues.HumanString(), expectedOutput.RunningValues.HumanString())
		}

		numOutputs++
		lastOutput = output
	}

	if numOutputs != len(expectedOutputs) {
		return Output{}, fmt.Errorf("expected number of outputs %v, got %v", numOutputs, len(expectedOutputs))
	}

	return lastOutput, nil
}

// testProgramValues estimates the execution cost, refund, collateral, memory,
// and time given a program in the form of a list of instructions. This function
// creates a dummy program that decodes the instructions and their parameters,
// testing that they were properly encoded.
func testProgramValues(p modules.Program, programDataLen uint64, data io.Reader, pt *modules.RPCPriceTable) (programValues, error) {
	// Make a dummy program to allow us to get the instruction values.
	program := &program{
		staticProgramState: &programState{
			priceTable: pt,
		},
		staticData: openProgramData(data, programDataLen),
	}
	runningValues := initialProgramValues(pt, programDataLen, uint64(len(p)))

	for _, i := range p {
		// Decode instruction.
		instruction, err := decodeInstruction(program, i)
		if err != nil {
			return programValues{}, err
		}
		// Get the values for the instruction.
		values, err := getInstructionValues(instruction)
		if err != nil {
			return programValues{}, err
		}
		// Update running values.
		runningValues.AddValues(pt, values)
	}

	// Get the final values for the program.
	finalValues := runningValues.FinalizeProgramValues(pt, p.ReadOnly(), true)

	return finalValues, nil
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
