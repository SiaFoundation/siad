package host

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/consensus"
	"gitlab.com/NebulousLabs/Sia/modules/gateway"
	"gitlab.com/NebulousLabs/Sia/modules/transactionpool"
	"gitlab.com/NebulousLabs/Sia/modules/wallet"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// v112StorageManagerOne names the first legacy storage manager that can be
	// used to test upgrades.
	v112Host = "v112Host.tar.gz"
)

// loadExistingHostWithNewDeps will create all of the dependencies for a host,
// then load the host on top of the given directory.
func loadExistingHostWithNewDeps(modulesDir, siaMuxDir, hostDir string) (modules.Host, error) {
	// Create the siamux
	mux, err := modules.NewSiaMux(siaMuxDir, modulesDir, "localhost:0")
	if err != nil {
		return nil, err
	}

	// Create the host dependencies.
	g, err := gateway.New("localhost:0", false, filepath.Join(modulesDir, modules.GatewayDir))
	if err != nil {
		return nil, err
	}
	cs, errChan := consensus.New(g, false, filepath.Join(modulesDir, modules.ConsensusDir))
	if err := <-errChan; err != nil {
		return nil, err
	}
	tp, err := transactionpool.New(cs, g, filepath.Join(modulesDir, modules.TransactionPoolDir))
	if err != nil {
		return nil, err
	}
	w, err := wallet.New(cs, tp, filepath.Join(modulesDir, modules.WalletDir))
	if err != nil {
		return nil, err
	}

	// Create the host.
	h, err := NewCustomHost(modules.ProdDependencies, cs, g, tp, w, mux, "localhost:0", hostDir)
	if err != nil {
		return nil, err
	}

	pubKey := mux.PublicKey()
	if !bytes.Equal(h.publicKey.Key, pubKey[:]) {
		return nil, errors.New("host and siamux pubkeys don't match")
	}
	privKey := mux.PrivateKey()
	if !bytes.Equal(h.secretKey[:], privKey[:]) {
		return nil, errors.New("host and siamux privkeys don't match")
	}
	return h, nil
}

// loadHostPersistenceFile will copy the host's persistence file from the old
// location to the new location.
func loadHostPersistenceFile(oldPath, newPath, newDir string) error {
	err := os.MkdirAll(newDir, 0700)
	if err != nil {
		return err
	}

	err = os.Symlink(oldPath, newPath)
	if err != nil {
		return err
	}
	return nil
}

// TestV112StorageManagerUpgrade creates a host with a legacy storage manager,
// and then attempts to upgrade the storage manager.
func TestV112StorageManagerUpgrade(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Copy the testdir legacy storage manager to the temp directory.
	source := filepath.Join("testdata", v112Host)
	legacyHost := build.TempDir(modules.HostDir, t.Name(), modules.HostDir)
	err := build.ExtractTarGz(source, legacyHost)
	if err != nil {
		t.Fatal(err)
	}

	// Patch the storagemanager.json to point to the new storage folder
	// location.
	smPersist := new(v112StorageManagerPersist)
	err = persist.LoadJSON(v112StorageManagerMetadata, smPersist, filepath.Join(legacyHost, v112StorageManagerDir, v112StorageManagerPersistFilename))
	if err != nil {
		t.Fatal(err)
	}
	smPersist.StorageFolders[0].Path = filepath.Join(legacyHost, "storageFolderOne")
	smPersist.StorageFolders[1].Path = filepath.Join(legacyHost, "storageFolderTwo")
	err = persist.SaveJSON(v112StorageManagerMetadata, smPersist, filepath.Join(legacyHost, v112StorageManagerDir, v112StorageManagerPersistFilename))
	if err != nil {
		t.Fatal(err)
	}
	oldCapacity := smPersist.StorageFolders[0].Size + smPersist.StorageFolders[1].Size
	oldCapacityRemaining := smPersist.StorageFolders[0].SizeRemaining + smPersist.StorageFolders[1].SizeRemaining
	oldUsed := oldCapacity - oldCapacityRemaining

	// Create the symlink to point to the storage folder.
	err = os.Symlink(filepath.Join(legacyHost, "storageFolderOne"), filepath.Join(legacyHost, v112StorageManagerDir, "66"))
	if err != nil {
		t.Fatal(err)
	}
	err = os.Symlink(filepath.Join(legacyHost, "storageFolderTwo"), filepath.Join(legacyHost, v112StorageManagerDir, "04"))
	if err != nil {
		t.Fatal(err)
	}

	testDir := filepath.Join(t.Name(), "newDeps")
	modulesDir := build.TempDir(modules.HostDir, testDir)

	// Copy over the host persistence file to the appropriate location, this
	// ensures the siamux picks this up and triggers its compatibility flow for
	// v112 as well.
	src := filepath.Join(legacyHost, settingsFile)
	dst := filepath.Join(modulesDir, modules.HostDir, settingsFile)
	dstDir := filepath.Join(modulesDir, modules.HostDir)
	loadHostPersistenceFile(src, dst, dstDir)

	// Patching complete. Proceed to create the host and verify that the
	// upgrade went smoothly.
	siaMuxDir := filepath.Join(modulesDir, modules.SiaMuxDir)
	host, err := loadExistingHostWithNewDeps(modulesDir, siaMuxDir, legacyHost)
	if err != nil {
		t.Fatal(err)
	}

	storageFolders := host.StorageFolders()
	if len(storageFolders) != 2 {
		t.Fatal("Storage manager upgrade was unsuccessful.")
	}

	// The amount of data reported should match the previous amount of data
	// that was stored.
	capacity := storageFolders[0].Capacity + storageFolders[1].Capacity
	capacityRemaining := storageFolders[0].CapacityRemaining + storageFolders[1].CapacityRemaining
	capacityUsed := capacity - capacityRemaining
	if capacity != oldCapacity {
		t.Error("new storage folders don't have the same size as the old storage folders")
	}
	if capacityRemaining != oldCapacityRemaining {
		t.Error("capacity remaining statistics do not match up", capacityRemaining/modules.SectorSize, oldCapacityRemaining/modules.SectorSize)
	}
	if oldUsed != capacityUsed {
		t.Error("storage folders have different usage values", capacityUsed/modules.SectorSize, oldUsed/modules.SectorSize)
	}
}
