package host

import (
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestAccountsReload verifies that an account is properly saved to disk and
// gets reinstated properly when the host is reloaded
func TestAccountsReload(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankHostTester("TestAccountsReload")
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

	ht, err := blankHostTester("TestFingerprintsReload")
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
	err = am.callWithdraw(msg1, sig1)
	if err != nil {
		t.Fatal(err)
	}
	msg2, sig2 := prepareWithdrawal(id, amount, am.h.blockHeight+10, sk)
	err = am.callWithdraw(msg2, sig2)
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
	fp1 := crypto.HashObject(msg1)
	exists := am.fingerprints.has(fp1)
	if !exists {
		t.Log(fp1)
		t.Error("Fingerprint 1 hash not found after reload")
	}
	fp2 := crypto.HashObject(msg2)
	exists = am.fingerprints.has(fp2)
	if !exists {
		t.Log(fp2)
		t.Error("Fingerprint 2 hash not found after reload")
	}
}

// balanceOf will return the balance for given account
func getAccountBalance(am *accountManager, id string) types.Currency {
	am.mu.Lock()
	defer am.mu.Unlock()

	acc, exists := am.accounts[id]
	if !exists {
		return types.ZeroCurrency
	}

	return acc.balance
}
