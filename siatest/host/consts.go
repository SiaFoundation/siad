package host

import (
	"os"

	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/siatest"
)

// hostTestDir creates a temporary testing directory for a host. This should
// only every be called once per test. Otherwise it will delete the directory
// again.
func hostTestDir(testName string) string {
	path := siatest.TestDir("host", testName)
	if err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest); err != nil {
		panic(err)
	}
	return path
}
