package mdm

import (
	"bytes"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// testCompareCosts returns an error if an actual cost does not match its
// expected cost.
func testCompareCosts(actualCost, actualRefund, actualCollateral types.Currency, actualMemory, actualTime uint64, expectedCost, expectedRefund, expectedCollateral types.Currency, expectedMemory, expectedTime uint64) error {
	if actualCost.Cmp(expectedCost) != 0 {
		return fmt.Errorf("expected cost %v, got %v", expectedCost, actualCost)
	}
	if actualRefund.Cmp(expectedRefund) != 0 {
		return fmt.Errorf("expected refund %v, got %v", expectedRefund, actualRefund)
	}
	if actualCollateral.Cmp(expectedCollateral) != 0 {
		return fmt.Errorf("expected collateral %v, got %v", expectedCollateral, actualCollateral)
	}
	if actualMemory != expectedMemory {
		return fmt.Errorf("expected memory %v, got %v", expectedMemory, actualMemory)
	}
	if actualTime != expectedTime {
		return fmt.Errorf("expected time %v, got %v", expectedTime, actualTime)
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

		// Check costs.
		if !output.ExecutionCost.Equals(expectedOutput.ExecutionCost) {
			return Output{}, fmt.Errorf("expected execution cost %v, got %v", output.ExecutionCost.HumanString(), expectedOutput.ExecutionCost.HumanString())
		}
		if !output.AdditionalCollateral.Equals(expectedOutput.AdditionalCollateral) {
			return Output{}, fmt.Errorf("expected collateral %v, got %v", output.AdditionalCollateral.HumanString(), expectedOutput.AdditionalCollateral.HumanString())
		}
		if !output.PotentialRefund.Equals(expectedOutput.PotentialRefund) {
			return Output{}, fmt.Errorf("expected refund %v, got %v", output.PotentialRefund.HumanString(), expectedOutput.PotentialRefund.HumanString())
		}

		numOutputs++
		lastOutput = output
	}

	if numOutputs != len(expectedOutputs) {
		return Output{}, fmt.Errorf("expected number of outputs %v, got %v", numOutputs, len(expectedOutputs))
	}

	return lastOutput, nil
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
	roots := make([]crypto.Hash, 10)
	for i := 0; i < 10; i++ { // initial contract size is 10 sectors.
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
