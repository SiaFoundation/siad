package renterhost

import (
	"os"

	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/siatest"
)

// renterHostTestDir creates a temporary testing directory for a renterhost
// test. This should only every be called once per test. Otherwise it will
// delete the directory again.
func renterHostTestDir(testName string) string {
	path := siatest.TestDir("renterhost", testName)
	if err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest); err != nil {
		panic(err)
	}
	return path
}
