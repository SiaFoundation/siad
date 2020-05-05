package renter

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestNewAccount verifies newAccount returns a valid account object
func TestNewAccount(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	r := rt.renter

	// create a random hostKey
	_, pk := crypto.GenerateKeyPair()
	hostKey := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	// create an account with a different hostkey to ensure the account we are
	// going to validate has an offset different from 0
	tmpKey := hostKey
	fastrand.Read(tmpKey.Key[:4])
	_ = r.newAccount(tmpKey)

	// create a new account object
	account := r.newAccount(hostKey)

	// validate the account object
	if account.staticID.IsZeroAccount() {
		t.Fatal("Invalid account ID")
	}
	if account.staticOffset == 0 {
		t.Fatal("Invalid offset")
	}
	if !account.staticHostKey.Equals(hostKey) {
		t.Fatal("Invalid host key")
	}

	// validate the account id is built using a valid SiaPublicKey and the
	// account's secret key belongs to the public key used to construct the id
	hash := crypto.HashBytes(fastrand.Bytes(10))
	sig := crypto.SignHash(hash, account.staticSecretKey)
	err = crypto.VerifyHash(hash, account.staticID.SPK().ToPublicKey(), sig)
	if err != nil {
		t.Fatal("Invalid secret key")
	}
}

// TestNewWithdrawalMessage verifies the newWithdrawalMessage helper
// properly instantiates all required fields on the WithdrawalMessage
func TestNewWithdrawalMessage(t *testing.T) {
	// create a withdrawal message using random parameters
	aid, _ := modules.NewAccountID()
	amount := types.NewCurrency64(fastrand.Uint64n(100))
	blockHeight := types.BlockHeight(fastrand.Intn(100))
	msg := newWithdrawalMessage(aid, amount, blockHeight)

	// validate the withdrawal message
	if msg.Account != aid {
		t.Fatal("Unexpected account ID")
	}
	if !msg.Amount.Equals(amount) {
		t.Fatal("Unexpected amount")
	}
	if msg.Expiry != blockHeight+withdrawalValidityPeriod {
		t.Fatal("Unexpected expiry")
	}
	if len(msg.Nonce) != modules.WithdrawalNonceSize {
		t.Fatal("Unexpected nonce length")
	}
	var nonce [modules.WithdrawalNonceSize]byte
	if bytes.Equal(msg.Nonce[:], nonce[:]) {
		t.Fatal("Uninitialized nonce")
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
	if r.staticAccountsFile == nil {
		t.Fatal("Accounts persistence file not set on the Renter after startup")
	}

	// create a number of test accounts and reload the renter
	accounts := createRandomTestAccountsOnRenter(r)
	r, err = rt.reloadRenter(r)
	if err != nil {
		t.Fatal(err)
	}

	// verify the accounts got reloaded properly
	id := r.mu.Lock()
	reloaded := r.accounts
	r.mu.Unlock(id)
	if len(reloaded) != len(accounts) {
		t.Fatalf("Unexpected amount of accounts, %v != %v", len(reloaded), len(accounts))
	}
	for _, account := range accounts {
		reloaded := r.managedOpenAccount(account.staticHostKey)
		if !account.staticID.SPK().Equals(reloaded.staticID.SPK()) {
			t.Fatal("Unexpected account ID")
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
	accounts := createRandomTestAccountsOnRenter(r)
	for _, account := range accounts {
		account.staticMu.Lock()
		account.balance = types.NewCurrency64(fastrand.Uint64n(1e3))
		account.staticMu.Unlock()
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
		reloaded := r.managedOpenAccount(account.staticHostKey)
		if !reloaded.staticID.SPK().Equals(account.staticID.SPK()) {
			t.Fatal("Unexpected reloaded account ID")
		}
		if !reloaded.balance.Equals(account.balance) {
			t.Log(reloaded.balance)
			t.Log(account.balance)
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
		reloaded := r.managedOpenAccount(account.staticHostKey)
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
	defer rt.Close()
	r := rt.renter

	// create a number accounts
	accounts := createRandomTestAccountsOnRenter(r)

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
	n, err := file.WriteAt(fastrand.Bytes(accountSize), corrupted.staticOffset)
	if n != accountSize {
		t.Fatalf("Unexpected amount of bytes written, %v != %v", n, accountSize)
	}
	if err != nil {
		t.Fatal("Could not write corrupted account data")
	}

	// reopen the renter
	persistDir := filepath.Join(rt.dir, modules.RenterDir)
	r, errChan := New(rt.gateway, rt.cs, rt.wallet, rt.tpool, rt.mux, persistDir)
	err = rt.addRenter(r)
	if err := <-errChan; err != nil {
		t.Fatal(err)
	}

	// verify only the non corrupted accounts got reloaded properly
	id := r.mu.Lock()
	reloaded := r.accounts
	r.mu.Unlock(id)

	// verify the amount of accounts reloaded is one less
	expected := len(accounts) - 1
	if len(reloaded) != expected {
		t.Fatalf("Unexpected amount of accounts, %v != %v", len(reloaded), expected)
	}

	var maxOffset int64
	for _, account := range reloaded {
		if account.staticID.SPK().Equals(corrupted.staticID.SPK()) {
			t.Fatal("Corrupted account was not properly skipped")
		}
		if account.staticOffset > maxOffset {
			maxOffset = account.staticOffset
		}
	}

	if maxOffset >= int64(len(reloaded)+1)*accountSize {
		t.Fatal("Unexpected max offset, corrupted account offset was not properly filled by the next valid account")
	}
}

// TestAccountPersistenceToBytes verifies the functionality of the `toBytes`
// method on the accountPersistence object
func TestAccountPersistenceToBytes(t *testing.T) {
	t.Parallel()

	ap := createRandomTestAccountPersistence()
	apBytes := ap.toBytes()

	var uMar accountPersistence
	err := encoding.Unmarshal(apBytes, &uMar)
	if err != nil {
		t.Fatal(err)
	}

	uMarBytes := uMar.toBytes()
	if !bytes.Equal(apBytes, uMarBytes) {
		t.Fatal("Account persistence object not equal after unmarshaling the account persistence bytes")
	}
}

// TestAccountPersistenceVerifyChecksum verifies the functionality of the
// 'checksum' and 'verifyChecksum' methods on the account persistence object
func TestAccountPersistenceVerifyChecksum(t *testing.T) {
	t.Parallel()

	ap := createRandomTestAccountPersistence()
	checksum := ap.checksum()
	if !ap.verifyChecksum(checksum) {
		t.Fatal("Unexpected outcome of verifyChecksum")
	}
	fastrand.Read(checksum[:4]) // corrupt the checksum
	if ap.verifyChecksum(checksum) {
		t.Fatal("Unexpected outcome of verifyChecksum")
	}
}

// TestAccountMetadataMarhsaling verifies the behaviour of the Marshal interface
// on the accountsMetadata
func TestAccountMetadataMarhsaling(t *testing.T) {
	t.Parallel()

	// create random metadata
	var am accountsMetadata
	fastrand.Read(am.Header[:])
	fastrand.Read(am.Version[:])
	am.Clean = fastrand.Intn(2) == 0

	// marshal it into bytes and verify the length
	mdBytes := encoding.Marshal(am)
	if len(mdBytes) != metadataSize {
		t.Fatalf("Unexpected metadata size, %v != %v", len(mdBytes), metadataSize)
	}

	// unmarshal and verify the unmarshaled object
	var uMar accountsMetadata
	err := encoding.Unmarshal(mdBytes, &uMar)
	if err != nil {
		t.Fatal("Failed to unmarshal metadata bytes", err)
	}
	if uMar.Header != am.Header {
		t.Fatal("Unexpected header")
	}
	if uMar.Version != am.Version {
		t.Fatal("Unexpected version")
	}
	if uMar.Clean != am.Clean {
		t.Fatal("Unexpected clean flag")
	}
}

// createRandomTestAccountsOnRenter is a helper function that creates a random
// number of accounts by calling 'managedOpenAccount' on the given renter
func createRandomTestAccountsOnRenter(r *Renter) []*account {
	accounts := make([]*account, 0)
	for i := 0; i < fastrand.Intn(10)+1; i++ {
		hostKey := types.SiaPublicKey{
			Algorithm: types.SignatureEd25519,
			Key:       fastrand.Bytes(crypto.PublicKeySize),
		}
		account := r.managedOpenAccount(hostKey)
		accounts = append(accounts, account)
	}
	return accounts
}

// createRandomTestAccountPersistence is a helper function that returns an
// accountPersistence object, initialised with random values
func createRandomTestAccountPersistence() accountPersistence {
	var checksum crypto.Hash
	fastrand.Read(checksum[:])
	aid, sk := modules.NewAccountID()
	ap := accountPersistence{
		AccountID: aid,
		Balance:   types.NewCurrency64(fastrand.Uint64n(1e3)),
		HostKey:   types.SiaPublicKey{},
		SecretKey: sk,
		Checksum:  checksum,
	}
	return ap
}
