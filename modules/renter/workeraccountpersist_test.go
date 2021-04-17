package renter

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/ratelimit"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
)

// newRandomAccountPersistence is a helper function that returns an
// accountPersistence object, initialised with random values
func newRandomAccountPersistence() accountPersistence {
	// randomBalance is a small helper function that returns a random
	// types.Currency taking into account the given max value
	randomBalance := func(max uint64) types.Currency {
		return types.NewCurrency64(fastrand.Uint64n(max))
	}

	aid, sk := modules.NewAccountID()
	return accountPersistence{
		AccountID: aid,
		HostKey:   types.SiaPublicKey{},
		SecretKey: sk,

		Balance:              randomBalance(1e4),
		BalanceDriftPositive: randomBalance(1e2),
		BalanceDriftNegative: randomBalance(1e2),

		SpendingDownloads:         randomBalance(1e2),
		SpendingRegistryReads:     randomBalance(1e2),
		SpendingRegistryWrites:    randomBalance(1e2),
		SpendingRepairDownloads:   randomBalance(1e2),
		SpendingRepairUploads:     randomBalance(1e2),
		SpendingSnapshotDownloads: randomBalance(1e2),
		SpendingSnapshotUploads:   randomBalance(1e2),
		SpendingSubscriptions:     randomBalance(1e2),
		SpendingUploads:           randomBalance(1e2),
	}
}

// TestAccountSave verifies accounts are properly saved and loaded onto the
// renter when it goes through a graceful shutdown and reboot.
func TestAccountSave(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a renter
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rt.Close()
		if err != nil {
			t.Log(err)
		}
	}()
	r := rt.renter

	// verify accounts file was loaded and set
	if r.staticAccountManager.staticFile == nil {
		t.Fatal("Accounts persistence file not set on the Renter after startup")
	}

	// create a number of test accounts and reload the renter
	accounts, err := openRandomTestAccountsOnRenter(r)
	if err != nil {
		t.Fatal(err)
	}
	r, err = rt.reloadRenter(r)
	if err != nil {
		t.Fatal(err)
	}

	// verify the accounts got reloaded properly
	am := r.staticAccountManager
	am.mu.Lock()
	accountsLen := len(am.accounts)
	am.mu.Unlock()
	if accountsLen != len(accounts) {
		t.Errorf("Unexpected amount of accounts, %v != %v", len(am.accounts), len(accounts))
	}
	for _, account := range accounts {
		reloaded, err := am.managedOpenAccount(account.staticHostKey)
		if err != nil {
			t.Error(err)
		}
		if !account.staticID.SPK().Equals(reloaded.staticID.SPK()) {
			t.Error("Unexpected account ID")
		}
	}
}

// TestAccountUncleanShutdown verifies that accounts are dropped if the accounts
// persist file was not marked as 'clean' on shutdown.
func TestAccountUncleanShutdown(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a renter tester
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rt.Close()
		if err != nil {
			t.Log(err)
		}
	}()
	r := rt.renter

	// create a number accounts
	accounts, err := openRandomTestAccountsOnRenter(r)
	if err != nil {
		t.Fatal(err)
	}

	// close the renter and reload it with a dependency that interrupts the
	// accounts save on shutdown
	deps := &dependencies.DependencyInterruptAccountSaveOnShutdown{}
	r, err = rt.reloadRenterWithDependency(r, deps)
	if err != nil {
		t.Fatal(err)
	}

	// verify the accounts were saved on disk
	for _, account := range accounts {
		reloaded, err := r.staticAccountManager.managedOpenAccount(account.staticHostKey)
		if err != nil {
			t.Fatal(err)
		}
		if !reloaded.staticID.SPK().Equals(account.staticID.SPK()) {
			t.Fatal("Unexpected reloaded account ID")
		}

		if !reloaded.balance.Equals(account.managedMinExpectedBalance()) {
			t.Log(reloaded.balance)
			t.Log(account.managedMinExpectedBalance())
			t.Fatal("Unexpected account balance after reload")
		}
	}

	// reload it to trigger the unclean shutdown
	r, err = rt.reloadRenter(r)
	if err != nil {
		t.Fatal(err)
	}

	// verify the accounts were reloaded but the balances were cleared due to
	// the unclean shutdown
	for _, account := range accounts {
		reloaded, err := r.staticAccountManager.managedOpenAccount(account.staticHostKey)
		if err != nil {
			t.Fatal(err)
		}
		if !account.staticID.SPK().Equals(reloaded.staticID.SPK()) {
			t.Fatal("Unexpected reloaded account ID")
		}
		if !reloaded.balance.IsZero() {
			t.Fatal("Unexpected reloaded account balance")
		}
	}
}

// TestAccountCorrupted verifies accounts that are corrupted are not reloaded
func TestAccountCorrupted(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a renter
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rt.Close()
		if err != nil {
			t.Log(err)
		}
	}()
	r := rt.renter

	// create a number accounts
	accounts, err := openRandomTestAccountsOnRenter(r)
	if err != nil {
		t.Fatal(err)
	}

	// select a random account of which we'll corrupt data on disk
	var corrupted *account
	for _, account := range accounts {
		corrupted = account
		break
	}

	// manually close the renter and corrupt the data at that offset
	err = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	file, err := r.deps.OpenFile(filepath.Join(r.persistDir, accountsFilename), os.O_RDWR, defaultFilePerm)
	if err != nil {
		t.Fatal(err)
	}

	rN := fastrand.Intn(5) + 1
	rOffset := corrupted.staticOffset + int64(fastrand.Intn(accountSize-rN))
	n, err := file.WriteAt(fastrand.Bytes(rN), rOffset)
	if n != rN {
		t.Fatalf("Unexpected amount of bytes written, %v != %v", n, rN)
	}
	if err != nil {
		t.Fatal("Could not write corrupted account data")
	}

	// reopen the renter
	persistDir := filepath.Join(rt.dir, modules.RenterDir)
	rl := ratelimit.NewRateLimit(0, 0, 0)
	r, errChan := New(rt.gateway, rt.cs, rt.wallet, rt.tpool, rt.mux, rl, persistDir)
	if err := <-errChan; err != nil {
		t.Fatal(err)
	}
	err = rt.addRenter(r)

	// verify only the non corrupted accounts got reloaded properly
	am := r.staticAccountManager
	am.mu.Lock()
	// verify the amount of accounts reloaded is one less
	expected := len(accounts) - 1
	if len(am.accounts) != expected {
		t.Errorf("Unexpected amount of accounts, %v != %v", len(am.accounts), expected)
	}
	for _, account := range am.accounts {
		if account.staticID.SPK().Equals(corrupted.staticID.SPK()) {
			t.Error("Corrupted account was not properly skipped")
		}
	}
	am.mu.Unlock()
}

// TestAccountCompatV150 is a unit test that verifies the compatibility code
// added to ensure the accounts file is properly upgraded from v1.5.0 to v1.5.6
func TestAccountCompatV150(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a renter tester
	testdir := build.TempDir("renter", t.Name())
	rt, err := newRenterTester(testdir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rt.Close()
		if err != nil {
			t.Log(err)
		}
	}()

	t.Run("Basic", func(t *testing.T) {
		testAccountCompatV150Basic(t, rt)
	})
	t.Run("RecoveryFromCleanTmpFile", func(t *testing.T) {
		testAccountCompatV150_TmpFileExistsWithClean(t, rt, true)
	})
	t.Run("RecoveryFromDirtyTmpFile", func(t *testing.T) {
		testAccountCompatV150_TmpFileExistsWithClean(t, rt, false)
	})
}

// testAccountCompatV150Basic verifies the accounts compat code successfully
// upgrades the accounts file from v150 to v156.
func testAccountCompatV150Basic(t *testing.T, rt *renterTester) {
	// create the renter dir
	testdir := build.TempDir("renter", t.Name())
	renterDir := filepath.Join(testdir, modules.RenterDir)
	err := os.MkdirAll(renterDir, persist.DefaultDiskPermissionsTest)
	if err != nil {
		t.Fatal(err)
	}

	// copy the compat file to the accounts file
	src := "../../compatibility/accounts_v1.5.0.dat"
	dst := filepath.Join(renterDir, accountsFilename)
	err = build.CopyFile(src, dst)
	if err != nil {
		t.Fatal(err)
	}

	// create a renter
	r, err := newRenterWithDependency(rt.gateway, rt.cs, rt.wallet, rt.tpool, rt.mux, filepath.Join(testdir, modules.RenterDir), &modules.ProductionDependencies{})
	if err != nil {
		t.Fatal(err)
	}

	// add it to the renter tester
	err = rt.addRenter(r)
	if err != nil {
		t.Fatal(err)
	}

	// verify the compat file was properly read and upgraded to
	am := r.staticAccountManager
	am.mu.Lock()
	numAccounts := len(am.accounts)
	am.mu.Unlock()
	if numAccounts != 377 {
		t.Fatal("unexpected amount of accounts", numAccounts)
	}
}

// testAccountCompatV150_TmpFileExistsWithClean verifies the disaster recovery
// flow in the accounts compat code where an earlier attempt failed and left
// behind a tmp accounts file where the "clean" flag is equal to the value given
// as parameter to this function. Depending on whether the tmp file is true or
// not the upgrade code should either use or discard the tmp file.
func testAccountCompatV150_TmpFileExistsWithClean(t *testing.T, rt *renterTester, clean bool) {
	// create a renter tester without renter
	testdir := build.TempDir("renter", t.Name())
	rt, err := newRenterTesterNoRenter(testdir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := rt.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// create the renter dir
	renterDir := filepath.Join(testdir, modules.RenterDir)
	err = os.MkdirAll(renterDir, persist.DefaultDiskPermissionsTest)
	if err != nil {
		t.Fatal(err)
	}

	// copy the compat file to the accounts file
	src := "../../compatibility/accounts_v1.5.0.dat"
	dst := filepath.Join(renterDir, accountsFilename)
	err = build.CopyFile(src, dst)
	if err != nil {
		t.Fatal(err)
	}

	// copy the tmp file to the tmp accounts file
	src = "../../compatibility/accounts_v1.5.6.tmp.dat"
	dst = filepath.Join(renterDir, accountsTmpFilename)
	err = build.CopyFile(src, dst)
	if err != nil {
		t.Fatal(err)
	}

	// corrupt either the original or tmp file depending on clean param
	var corruptErr error
	if clean {
		corruptErr = corruptAccountsFile(filepath.Join(renterDir, accountsFilename))
	} else {
		corruptErr = corruptAccountsFile(filepath.Join(renterDir, accountsTmpFilename))
	}
	if corruptErr != nil {
		t.Fatal(err)
	}

	// open the tmp file
	tmpFile, err := os.OpenFile(dst, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		t.Fatal(err)
	}

	// write the header
	_, err = tmpFile.WriteAt(encoding.Marshal(accountsMetadata{
		Header:  metadataHeader,
		Version: metadataVersion,
		Clean:   clean,
	}), 0)

	// sync and close the tmp file
	err = errors.Compose(tmpFile.Sync(), tmpFile.Close())
	if err != nil {
		t.Fatal(err)
	}

	// create a renter
	r, err := newRenterWithDependency(rt.gateway, rt.cs, rt.wallet, rt.tpool, rt.mux, filepath.Join(testdir, modules.RenterDir), &modules.ProductionDependencies{})
	if err != nil {
		t.Fatal(err)
	}

	// add it to the renter tester
	err = rt.addRenter(r)
	if err != nil {
		t.Fatal(err)
	}

	// verify the compat file was properly read and upgraded to
	am := r.staticAccountManager
	am.mu.Lock()
	numAccounts := len(am.accounts)
	am.mu.Unlock()
	if numAccounts != 377 {
		t.Fatal("unexpected amount of accounts")
	}
}

// TestAccountPersistenceToAndFromBytes verifies the functionality of the
// `bytes` and `loadBytes` method on the accountPersistence object
func TestAccountPersistenceToAndFromBytes(t *testing.T) {
	t.Parallel()

	// create a random persistence object and get its bytes
	ap := newRandomAccountPersistence()
	accountBytes := ap.bytes()
	if len(accountBytes) != accountSize {
		t.Fatal("Unexpected account bytes")
	}

	// load the bytes onto a new persistence object and compare for equality
	var uMar accountPersistence
	err := uMar.loadBytes(accountBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !ap.AccountID.SPK().Equals(uMar.AccountID.SPK()) {
		t.Fatal("Unexpected AccountID")
	}
	if !ap.HostKey.Equals(uMar.HostKey) {
		t.Fatal("Unexpected hostkey")
	}
	if !bytes.Equal(ap.SecretKey[:], uMar.SecretKey[:]) {
		t.Fatal("Unexpected secretkey")
	}
	if !ap.Balance.Equals(uMar.Balance) ||
		!ap.BalanceDriftPositive.Equals(uMar.BalanceDriftPositive) ||
		!ap.BalanceDriftNegative.Equals(uMar.BalanceDriftNegative) {
		t.Fatal("Unexpected balance details")
	}

	if !ap.SpendingDownloads.Equals(uMar.SpendingDownloads) ||
		!ap.SpendingRegistryReads.Equals(uMar.SpendingRegistryReads) ||
		!ap.SpendingRegistryWrites.Equals(uMar.SpendingRegistryWrites) ||
		!ap.SpendingRepairDownloads.Equals(uMar.SpendingRepairDownloads) ||
		!ap.SpendingRepairUploads.Equals(uMar.SpendingRepairUploads) ||
		!ap.SpendingSnapshotDownloads.Equals(uMar.SpendingSnapshotDownloads) ||
		!ap.SpendingSnapshotUploads.Equals(uMar.SpendingSnapshotUploads) ||
		!ap.SpendingSubscriptions.Equals(uMar.SpendingSubscriptions) ||
		!ap.SpendingUploads.Equals(uMar.SpendingUploads) {
		t.Fatal("Unexpected spending details")
	}

	// corrupt the checksum of the account bytes
	corruptedBytes := accountBytes
	corruptedBytes[fastrand.Intn(crypto.HashSize)] += 1
	err = uMar.loadBytes(corruptedBytes)
	if !errors.Contains(err, errInvalidChecksum) {
		t.Fatalf("Expected error '%v', instead '%v'", errInvalidChecksum, err)
	}

	// corrupt the account data bytes
	corruptedBytes2 := accountBytes
	corruptedBytes2[fastrand.Intn(accountSize-crypto.HashSize)+crypto.HashSize] += 1
	err = uMar.loadBytes(corruptedBytes2)
	if !errors.Contains(err, errInvalidChecksum) {
		t.Fatalf("Expected error '%v', instead '%v'", errInvalidChecksum, err)
	}
}

// corruptAccountsFile will write random data to an accounts file at given path
//
// NOTE: this function assumes the accounts file holds at least 2 accounts
func corruptAccountsFile(path string) (err error) {
	// open the file
	var file *os.File
	file, err = os.OpenFile(path, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return err
	}

	// defer a sync and close
	defer func() {
		err = errors.Compose(err, file.Sync(), file.Close())
	}()

	// write random bytes
	randOff := fastrand.Intn(accountSize) + accountsOffset
	_, err = file.WriteAt(fastrand.Bytes(accountSize), int64(randOff))
	return err
}
