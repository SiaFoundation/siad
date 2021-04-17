package hostdb

import (
	"os"

	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/siatest"
)

// hostdbTestDir creates a temporary testing directory for a hostdb test. This
// should only every be called once per test. Otherwise it will delete the
// directory again.
func hostdbTestDir(testName string) string {
	path := siatest.TestDir("renter/hostdb", testName)
	if err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest); err != nil {
		panic(err)
	}
	return path
}
