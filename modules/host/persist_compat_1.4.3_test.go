package host

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/errors"
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

	// simulate an existing siamux in the persist dir.
	_, err1 := os.Create(filepath.Join(persistDir, "siamux.json"))
	_, err2 := os.Create(filepath.Join(persistDir, "siamux.json_temp"))
	_, err3 := os.Create(filepath.Join(persistDir, "siamux.log"))
	if err := errors.Compose(err1, err2, err3); err != nil {
		t.Fatal(err)
	}

	// load a new host, the siamux should be created in the sia root.
	siaMuxDir := filepath.Join(persistDir, modules.SiaMuxDir)
	host, err := loadExistingHostWithNewDeps(persistDir, siaMuxDir, hostPersistDir)
	if err != nil {
		t.Fatal(err)
	}

	// the old siamux files should be gone.
	_, err1 = os.Stat(filepath.Join(persistDir, "siamux.json"))
	_, err2 = os.Stat(filepath.Join(persistDir, "siamux.json_temp"))
	_, err3 = os.Stat(filepath.Join(persistDir, "siamux.log"))
	if !os.IsNotExist(err1) || !os.IsNotExist(err2) || !os.IsNotExist(err3) {
		t.Fatal("files still exist", err1, err2, err3)
	}

	// the new siamux files should be in the right spot.
	_, err1 = os.Stat(filepath.Join(siaMuxDir, "siamux.json"))
	_, err2 = os.Stat(filepath.Join(siaMuxDir, "siamux.json_temp"))
	_, err3 = os.Stat(filepath.Join(siaMuxDir, "siamux.log"))
	if err := errors.Compose(err1, err2, err3); err != nil {
		t.Fatal("files should exist", err1, err2, err3)
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

	// sanity check the metadata version
	err = persist.LoadJSON(modules.Hostv143PersistMetadata, struct{}{}, filepath.Join(hostPersistDir, modules.HostSettingsFile))
	if err != nil {
		t.Fatal(err)
	}
}
