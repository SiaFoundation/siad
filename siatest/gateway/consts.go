package gateway

import (
	"os"

	"gitlab.com/NebulousLabs/Sia/siatest"
)

// gatewayTestDir creates a temporary testing directory for a gateway. This
// should only every be called once per test. Otherwise it will delete the
// directory again.
func gatewayTestDir(testName string) string {
	path := siatest.TestDir("gateway", testName)
	if err := os.MkdirAll(path, siatest.DefaultDiskPermissions); err != nil {
		panic(err)
	}
	return path
}
