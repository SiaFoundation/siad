package host

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
)

const (
	// v120Host is the name of the file that contains the legacy host
	// persistence directory testdata.
	v120Host = "v120Host.tar.gz"
)

// TestV120HostUpgrade creates a host with a legacy persistence file,
// and then attempts to upgrade.
func TestV120HostUpgrade(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// ensure the host directory is empty
	persistDir := build.TempDir(modules.HostDir, t.Name())
	hostPersistDir := build.TempDir(modules.HostDir, t.Name(), modules.HostDir)
	err := os.RemoveAll(hostPersistDir)
	if err != nil {
		t.Fatal(err)
	}

	// copy the testdir legacy persistence data to the temp directory
	source := filepath.Join("testdata", v120Host)
	err = build.ExtractTarGz(source, persistDir)
	if err != nil {
		t.Fatal(err)
	}

	// load a new host
	host, err := loadExistingHostWithNewDeps(persistDir, hostPersistDir)
	if err != nil {
		t.Fatal(err)
	}

	// verify the upgrade properly decorated the ephemeral account related
	// settings onto the persistence object
	his := host.InternalSettings()
	if his.EphemeralAccountExpiry != defaultEphemeralAccountExpiry {
		t.Fatal("EphemeralAccountExpiry not properly decorated on the persistence object after upgrade")
	}

	if !his.MaxEphemeralAccountBalance.Equals(defaultMaxEphemeralAccountBalance) {
		t.Fatal("MaxEphemeralAccountBalance not properly decorated on the persistence object after upgrade")
	}

	if !his.MaxEphemeralAccountRisk.Equals(defaultMaxEphemeralAccountRisk) {
		t.Fatal("MaxEphemeralAccountRisk not properly decorated on the persistence object after upgrade")
	}

}
