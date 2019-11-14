package miner

import (
	"os"

	"gitlab.com/NebulousLabs/Sia/siatest"
)

// minerTestDir creates a temporary testing directory for a miner test. This
// should only every be called once per test. Otherwise it will delete the
// directory again.
func minerTestDir(testName string) string {
	path := siatest.TestDir("miner", testName)
	if err := os.MkdirAll(path, 0750); err != nil {
		panic(err)
	}
	return path
}
