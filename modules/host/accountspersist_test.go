package host

import (
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// TestAccountsReload verifies that an account is properly saved to disk and
// gets reinstated properly when the host is reloaded
func TestAccountsReload(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Generate couple of accounts and deposit some coins into them
	accounts := make(map[string]types.Currency)
	var i uint64
	for i = 1; i <= 10; i++ {
		_, pk := crypto.GenerateKeyPair()
		id := types.SiaPublicKey{
			Algorithm: types.SignatureEd25519,
			Key:       pk[:],
		}
		expected := types.NewCurrency64(i)
		err = am.callDeposit(id.String(), expected)
		if err != nil {
			t.Fatal(err)
		}
		actual := getAccountBalance(am, id.String())
		if !expected.Equals(actual) {
			t.Log("Expected:", expected.String())
			t.Log("Actual:", actual.String())
			t.Fatal("Deposit was unsuccessful")
		}
		accounts[id.String()] = expected
	}

	// Reload the host
	err = ht.host.Close()
	if err != nil {
		t.Fatal(err)
	}
	ht.host, err = New(ht.cs, ht.gateway, ht.tpool, ht.wallet, "localhost:0", filepath.Join(ht.persistDir, modules.HostDir))
	if err != nil {
		t.Fatal(err)
	}

	// Important, reload the accountmanager to avoid looking at old data
	am = ht.host.staticAccountManager

	// Verify the account balances were reloaded properly
	for id, expected := range accounts {
		reloaded := getAccountBalance(am, id)
		if !reloaded.Equals(expected) {
			t.Log("Expected:", expected.String())
			t.Log("Reloaded:", reloaded.String())
			t.Fatal("Balance after host reload did not equal the expected balance")
		}
	}
}

// TestFingerprintsReload verifies fingerprints are properly reloaded
func TestFingerprintsReload(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Prepare an account
	sk, spk := prepareAccount()
	id := spk.String()
	err = am.callDeposit(id, types.NewCurrency64(10))
	if err != nil {
		t.Fatal(err)
	}

	// Prepare a withdrawal message
	amount := types.NewCurrency64(1)
	msg1, sig1 := prepareWithdrawal(id, amount, am.h.blockHeight+10, sk)
	err = callWithdraw(am, msg1, sig1)
	if err != nil {
		t.Fatal(err)
	}

	msg2, sig2 := prepareWithdrawal(id, amount, am.h.blockHeight+10, sk)
	err = callWithdraw(am, msg2, sig2)
	if err != nil {
		t.Fatal(err)
	}

	// Reload the host
	err = ht.host.Close()
	if err != nil {
		t.Fatal(err)
	}
	ht.host, err = New(ht.cs, ht.gateway, ht.tpool, ht.wallet, "localhost:0", filepath.Join(ht.persistDir, modules.HostDir))
	if err != nil {
		t.Fatal(err)
	}
	am = ht.host.staticAccountManager

	// Verify fingerprints got reloaded
	fp1 := crypto.HashObject(*msg1)
	exists := am.fingerprints.has(fp1)
	if !exists {
		t.Log(fp1)
		t.Error("Fingerprint 1 hash not found after reload")
	}
	fp2 := crypto.HashObject(*msg2)
	exists = am.fingerprints.has(fp2)
	if !exists {
		t.Log(fp2)
		t.Error("Fingerprint 2 hash not found after reload")
	}
}

// TestFingerprintsRotate will verify if mining blocks properly rotates the
// fingerprints, both in-memory and on-disk.
func TestFingerprintsRotate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	am := ht.host.staticAccountManager

	// Unlock the wallet
	err = ht.wallet.Reset()
	if err != nil {
		t.Fatal(err)
	}
	err = ht.initWallet()
	if err != nil {
		t.Fatal(err)
	}

	// Prepare account
	sk, pk := prepareAccount()
	id := pk.String()
	err = am.callDeposit(id, types.NewCurrency64(2))
	if err != nil {
		t.Fatal(err)
	}

	// Prepare 2 withdrawal messages, one that will end up in the current
	// bucket, and one that'll end up in the next fingerprints bucket
	cbh := ht.host.BlockHeight()
	msg1, sig1 := prepareWithdrawal(id, types.NewCurrency64(1), cbh+1, sk)
	msg2, sig2 := prepareWithdrawal(id, types.NewCurrency64(1), cbh+bucketBlockRange, sk)
	if err = errors.Compose(
		callWithdraw(am, msg1, sig1),
		callWithdraw(am, msg2, sig2),
	); err != nil {
		t.Fatal(err)
	}

	// Vefify we have the fingerprints in memory by perform the same withdrawal
	// and asserting errWithdrawalSpent
	if err = callWithdraw(am, msg1, sig1); err != ErrWithdrawalSpent {
		t.Fatal("Unexpected error, expected ErrWithdrawalSpent but got:", err)
	}
	if err = callWithdraw(am, msg2, sig2); err != ErrWithdrawalSpent {
		t.Fatal("Unexpected error, expected ErrWithdrawalSpent but got:", err)
	}

	// Verify we have the fingerprints on disk by using the persister
	data, err := am.staticAccountsPersister.callLoadData()
	if err != nil {
		t.Fatal(err)
	}
	fp1 := crypto.HashObject(*msg1)
	_, ok := data.fingerprints[fp1]
	if !ok {
		t.Fatal("Fingerprint of withdrawal msg 1 not found on disk")
	}
	fp2 := crypto.HashObject(*msg2)
	_, ok = data.fingerprints[fp2]
	if !ok {
		t.Fatal("Fingerprint of withdrawal msg 2 not found on disk")
	}

	// Mine blocks until we've reached the block height threshold at which the
	// fingerprints are expected to rotate
	numBlocks := bucketBlockRange
	for numBlocks > 0 {
		_, err = ht.miner.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
		numBlocks--
	}

	// Verify the fingerprint for withdrawal 1 is gone from memory. Verify the
	// fingerprint for withdrawal 2 moved to the current bucket
	am.mu.Lock()
	_, found1 := am.fingerprints.current[fp1]
	_, found2 := am.fingerprints.current[fp2]
	has1 := am.fingerprints.has(fp1)
	has2 := am.fingerprints.has(fp2)
	am.mu.Unlock()

	if found1 || has1 {
		t.Fatal("Fingerprint should have been removed from memory")
	}
	if !found2 {
		if has2 {
			t.Fatal("Fingerprint not in the correct bucket")
		} else {
			t.Fatal("Fingerprint not found in memory")
		}
	}

	// Verify the fingerprints got reloaded on disk as well
	data, err = am.staticAccountsPersister.callLoadData()
	if err != nil {
		t.Fatal(err)
	}
	_, has1 = data.fingerprints[fp1]
	_, has2 = data.fingerprints[fp2]
	if !(has1 == false && has2 == true) {
		t.Fatal("Unexpected contents of fingerprint buckets on disk")
	}
}

// getAccountBalance will return the balance for given account
func getAccountBalance(am *accountManager, id string) types.Currency {
	am.mu.Lock()
	defer am.mu.Unlock()

	acc, exists := am.accounts[id]
	if !exists {
		return types.ZeroCurrency
	}

	return acc.balance
}
