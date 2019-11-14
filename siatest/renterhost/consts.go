package renterhost

import (
	"os"

	"gitlab.com/NebulousLabs/Sia/siatest"
)

// renterHostTestDir creates a temporary testing directory for a renterhost
// test. This should only every be called once per test. Otherwise it will
// delete the directory again.
func renterHostTestDir(testName string) string {
	path := siatest.TestDir("renterhost", testName)
	if err := os.MkdirAll(path, 0750); err != nil {
		panic(err)
	}
	return path
}
