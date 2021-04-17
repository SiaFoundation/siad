package host

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/log"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/siamux/mux"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
)

const (
	// v143Host is the name of the file that contains the legacy host
	// persistence directory testdata.
	v143Host = "v143Host.tar.gz"

	// v143HostDir is the name of the directory containing the v143Host
	// test data.
	v143HostDir = "v143Host"
)

// TestV143HostUpgrade creates a host with a legacy persistence file,
// and then attempts to upgrade.
func TestV143HostUpgrade(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a tmp dir
	persistDir := build.TempDir(t.Name())

	// extract the legacy host data
	hostDir := build.TempDir(persistDir, modules.HostDir)
	source := filepath.Join("testdata", v143Host)
	err := build.ExtractTarGz(source, hostDir)
	if err != nil {
		t.Fatal(err)
	}

	// copy its persistence file into the appropriate location
	src := filepath.Join(hostDir, v143HostDir, settingsFile)
	dst := filepath.Join(hostDir, settingsFile)
	err = build.CopyFile(src, dst)
	if err != nil {
		t.Fatal(err)
	}

	// load the persistence, to extract the public and secret keys for the
	// siamux but also to verify we're using the correct testdata
	hp := persistence{}
	err = persist.LoadJSON(modules.Hostv143PersistMetadata, &hp, dst)
	if err != nil {
		t.Fatal(err)
	}

	// create a siamux using the compat flow to ensure we save it using the
	// host's key pair, this simulates how it would be on production (without
	// having to add siamux testdata in the host module)
	siaMuxDir := filepath.Join(persistDir, modules.SiaMuxDir)
	err = os.MkdirAll(siaMuxDir, persist.DefaultDiskPermissionsTest)
	if err != nil {
		t.Fatal(err)
	}

	// convert the keypair into a mux.ED25519 key pair
	smPK := modules.SiaPKToMuxPK(hp.PublicKey)
	var smSK mux.ED25519SecretKey
	copy(smSK[:], hp.SecretKey[:])

	mux, err := siamux.CompatV1421NewWithKeyPair("localhost:0", "localhost:0", log.DiscardLogger, siaMuxDir, smSK, smPK)
	if err != nil {
		t.Fatal(err)
	}
	err = mux.Close()
	if err != nil {
		t.Fatal(err)
	}

	// load a new host
	closefn, host, err := loadExistingHostWithNewDeps(persistDir, siaMuxDir, hostDir)
	if err != nil {
		t.Fatal(err)
	}

	// verify the upgrade properly fixed the EphemeralAccountExpiry
	his := host.InternalSettings()
	if his.EphemeralAccountExpiry != modules.DefaultEphemeralAccountExpiry {
		t.Fatal("EphemeralAccountExpiry not properly fixed after upgrade")
	}

	// sanity check the metadata version
	err = persist.LoadJSON(modules.Hostv151PersistMetadata, struct{}{}, filepath.Join(hostDir, modules.HostSettingsFile))
	if err != nil {
		t.Fatal(err)
	}

	// close the host
	err = closefn()
	if err != nil {
		t.Fatal(err)
	}

	// undo the changes by overwriting the file with the legacy version, and
	// alter it in a way that simulates the user has manually set the
	// ephemeralaccountexpiry field to 0
	err = build.CopyFile(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	err = persist.LoadJSON(modules.Hostv143PersistMetadata, &hp, dst)
	if err != nil {
		t.Fatal(err)
	}
	hp.Settings.EphemeralAccountExpiry = 0
	err = persist.SaveJSON(modules.Hostv143PersistMetadata, hp, dst)
	if err != nil {
		t.Fatal(err)
	}

	// reload the host
	closefn, host, err = loadExistingHostWithNewDeps(persistDir, siaMuxDir, hostDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := closefn()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// verify the upgrade ignored the field and left it at 0
	his = host.InternalSettings()
	if his.EphemeralAccountExpiry.Nanoseconds() != 0 {
		t.Fatal("EphemeralAccountExpiry not properly ignored on upgrade")
	}

	// sanity check the metadata version
	err = persist.LoadJSON(modules.Hostv151PersistMetadata, struct{}{}, filepath.Join(hostDir, modules.HostSettingsFile))
	if err != nil {
		t.Fatal(err)
	}
}

// TestShouldResetEphemeralAccountExpiry is a unit test that covers the
// functionality of `shouldResetEphemeralAccountExpiry`
func TestShouldResetEphemeralAccountExpiry(t *testing.T) {
	t.Parallel()

	oneWeekInSeconds := 60 * 60 * 24 * 7

	// legacy default (seconds interpreted in nanoseconds)
	his := modules.HostInternalSettings{}
	his.EphemeralAccountExpiry = time.Duration(oneWeekInSeconds)
	if !shouldResetEphemeralAccountExpiry(his) {
		t.Fatal("Unexpected outcome of `shouldResetEphemeralAccountExpiry`")
	}

	// altered to zero, (seconds interpreted in nanoseconds)
	his.EphemeralAccountExpiry = time.Duration(0)
	if shouldResetEphemeralAccountExpiry(his) {
		t.Fatal("Unexpected outcome of `shouldResetEphemeralAccountExpiry`")
	}

	// altered to something other than zero (seconds interpreted in nanoseconds)
	his.EphemeralAccountExpiry = time.Duration(oneWeekInSeconds / 2)
	if !shouldResetEphemeralAccountExpiry(his) {
		t.Fatal("Unexpected outcome of `shouldResetEphemeralAccountExpiry`")
	}

	// default in seconds
	his.EphemeralAccountExpiry = time.Duration(oneWeekInSeconds) * time.Second
	if shouldResetEphemeralAccountExpiry(his) {
		t.Fatal("Unexpected outcome of `shouldResetEphemeralAccountExpiry`")
	}
}
