package contractmanager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
)

// contractManagerTester holds a contract manager along with some other fields
// useful for testing, and has methods implemented on it that can assist
// testing.
type contractManagerTester struct {
	cm *ContractManager

	persistDir string
}

// panicClose will attempt to call Close on the contract manager tester. If
// there is an error, the function will panic. A convenient function for making
// sure that the cleanup code is always running correctly, without needing to
// write a lot of boiler code.
func (cmt *contractManagerTester) panicClose() {
	err := cmt.Close()
	if err != nil {
		panic(err)
	}
}

// Close will perform clean shutdown on the contract manager tester.
func (cmt *contractManagerTester) Close() error {
	if cmt.cm == nil {
		return errors.New("nil contract manager")
	}
	return cmt.cm.Close()
}

// newContractManagerTester returns a ready-to-rock contract manager tester.
func newContractManagerTester(name string) (*contractManagerTester, error) {
	if testing.Short() {
		panic("use of newContractManagerTester during short testing")
	}

	testdir := build.TempDir(modules.ContractManagerDir, name)
	cm, err := New(filepath.Join(testdir, modules.ContractManagerDir))
	if err != nil {
		return nil, err
	}
	cmt := &contractManagerTester{
		cm:         cm,
		persistDir: testdir,
	}
	return cmt, nil
}

// newMockedContractManagerTester returns a contract manager tester that uses
// the input dependencies instead of the production ones.
func newMockedContractManagerTester(d modules.Dependencies, name string) (*contractManagerTester, error) {
	if testing.Short() {
		panic("use of newContractManagerTester during short testing")
	}

	testdir := build.TempDir(modules.ContractManagerDir, name)
	cm, err := newContractManager(d, filepath.Join(testdir, modules.ContractManagerDir))
	if err != nil {
		return nil, err
	}
	cmt := &contractManagerTester{
		cm:         cm,
		persistDir: testdir,
	}
	return cmt, nil
}

// TestNewContractManager does basic startup and shutdown of a contract
// manager, checking for egregious errors.
func TestNewContractManager(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a contract manager.
	parentDir := build.TempDir(modules.ContractManagerDir, "TestNewContractManager")
	cmDir := filepath.Join(parentDir, modules.ContractManagerDir)
	cm, err := New(cmDir)
	if err != nil {
		t.Fatal(err)
	}
	// Close the contract manager.
	err = cm.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Create a new contract manager using the same directory.
	cm, err = New(cmDir)
	if err != nil {
		t.Fatal(err)
	}
	// Close it again.
	err = cm.Close()
	if err != nil {
		t.Fatal(err)
	}
}

// dependencyErroredStartupis a mocked dependency that will cause the contract
// manager to be returned with an error upon startup.
type dependencyErroredStartup struct {
	modules.ProductionDependencies
}

// disrupt will disrupt the threadedSyncLoop, causing the loop to terminate as
// soon as it is created.
func (d *dependencyErroredStartup) Disrupt(s string) bool {
	// Cause an error to be returned during startup.
	return s == "erroredStartup"
}

// TestNewContractManagerErroredStartup uses disruption to simulate an error
// during startup, allowing the test to verify that the cleanup code ran
// correctly.
func TestNewContractManagerErroredStartup(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a new contract manager where the startup gets disrupted.
	d := new(dependencyErroredStartup)
	testdir := build.TempDir(modules.ContractManagerDir, "TestNewContractManagerErroredStartup")
	cmd := filepath.Join(testdir, modules.ContractManagerDir)
	_, err := newContractManager(d, cmd)
	if err == nil || !strings.Contains(err.Error(), "startup disrupted") {
		t.Fatal("expecting contract manager startup to be disrupted:", err)
	}

	// Verify that shutdown was triggered correctly - tmp files should be gone,
	// WAL file should also be gone.
	walFileName := filepath.Join(cmd, walFile)
	walFileTmpName := filepath.Join(cmd, walFileTmp)
	settingsFileTmpName := filepath.Join(cmd, settingsFileTmp)
	_, err = os.Stat(walFileName)
	if !os.IsNotExist(err) {
		t.Error("file should have been removed:", err)
	}
	_, err = os.Stat(walFileTmpName)
	if !os.IsNotExist(err) {
		t.Error("file should have been removed:", err)
	}
	_, err = os.Stat(settingsFileTmpName)
	if !os.IsNotExist(err) {
		t.Error("file should have been removed:", err)
	}
}

// TestSectorMuDataRace tests that cm.sectorMu is properly locked and unlocked
// during sector operations.
func TestSectorMuDataRace(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	cm, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()

	exit := make(chan struct{})
	defer func() {
		close(exit)
	}()

	go func() {
		for {
			select {
			case <-exit:
				return
			case <-time.After(time.Millisecond * 10):
				// note: was causing a data race with cm.setUsage and
				// cm.clearUsage because cm.sectorMu was not being locked
				// in certain cases.
				cm.StorageFolders()
			}
		}
	}()

	// add a storage folder
	if err := cm.AddStorageFolder(t.TempDir(), modules.SectorSize*1024); err != nil {
		t.Fatal(err)
	}

	// add sectors to the contract manager
	var added []crypto.Hash
	for i := 0; i < 200; i++ {
		data := make([]byte, modules.SectorSize)
		fastrand.Read(data[:256])
		root := crypto.MerkleRoot(data)
		if err := cm.AddSector(root, data); err != nil {
			t.Fatal(err)
		}
		added = append(added, root)
	}

	// split the added sectors between the three removal operations: remove,
	// delete, and batch remove.
	// remove sectors
	n := len(added) / 3
	for _, root := range added[:n] {
		if err := cm.RemoveSector(root); err != nil {
			t.Fatal(err)
		}
	}

	// delete sectors
	m := n * 2
	for _, root := range added[n:m] {
		if err := cm.DeleteSector(root); err != nil {
			t.Fatal(err)
		}
	}

	// batch remove sectors
	if err := cm.MarkSectorsForRemoval(added[m:]); err != nil {
		t.Fatal(err)
	}
	// wait for the sector removal batch to complete
	time.Sleep(time.Second * 10)
}
