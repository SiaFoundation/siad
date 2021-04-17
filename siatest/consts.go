package siatest

import (
	"encoding/json"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
)

const (
	// NumberOfParallelGroups is the number of testgroups that can be created in
	// parallel to prevent `too many open files` errors
	//
	// The value of 1 is based on running the siatest package with 8 threads, so
	// 8 tests can be run in parallel and the testgroup creation is throttled to
	// 1 at a time
	NumberOfParallelGroups = 1
)

// ChunkSize is a helper method to calculate the size of a chunk depending on
// the minimum number of pieces required to restore the chunk.
func ChunkSize(minPieces uint64, ct crypto.CipherType) uint64 {
	return (modules.SectorSize - ct.Overhead()) * minPieces
}

// PrintJSON is a helper function that wraps the jsonMarshalIndent function
func PrintJSON(a interface{}) string {
	json, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(json)
}
