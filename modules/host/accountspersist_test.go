package host

import (
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
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

	// Create an account and deposit coins into it
	_, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	id := spk.String()
	err = am.callDeposit(id, types.NewCurrency64(10))
	if err != nil {
		t.Fatal(err)
	}

	a := types.NewCurrency64(1)
	fp1 := randomFingerprint(ht.host, 5)
	err = am.callSpend(id, a, fp1)
	if err != nil {
		t.Fatal(err)
	}
	fp2 := randomFingerprint(ht.host, 10)
	err = am.callSpend(id, a, fp2)
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
	exists := am.fingerprints.has(fp1)
	if !exists {
		t.Log(fp1.Hash)
		t.Error("Fingerprint 1 hash not found after reload")
	}
	exists = am.fingerprints.has(fp2)
	if !exists {
		t.Log(fp2.Hash)
		t.Error("Fingerprint 2 hash not found after reload")
	}
}

// TestMarshalUnmarshalFingerprint
func TestMarshalUnmarshalFingerprint(t *testing.T) {
	t.Parallel()

	ht, err := blankHostTester("TestMarshalUnmarshalFingerprint")
	if err != nil {
		t.Fatal(err)
	}

	expected := randomFingerprint(ht.host, 5)
	b := encoding.Marshal(*expected)

	actual := &fingerprint{}
	err = encoding.Unmarshal(b, actual)
	if err != nil {
		t.Fatal(err)
	}

	// Verify hash
	if actual.Hash != expected.Hash {
		t.Error("Incorrect hash after unmarshal")
		t.Log("Expected: ", expected.Hash)
		t.Log("Actual: ", actual.Hash)
	}

	// Verify blockheight
	if actual.Expiry != expected.Expiry {
		t.Error("Incorrect expiry after unmarshal")
		t.Log("Expected: ", expected.Expiry)
		t.Log("Actual: ", actual.Expiry)
	}
}

// TestMarshalUnmarshalAccount
func TestMarshalUnmarshalAccountData(t *testing.T) {
	t.Parallel()

	// Generate SiaPublicKey
	_, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	// Marshal a dummy account
	expected := accountData{
		Id:      spk,
		Balance: types.SiacoinPrecision,
		LastTxn: time.Now().Unix(),
	}
	accBytes := make([]byte, accountSize)
	copy(accBytes, encoding.Marshal(expected))

	// Unmarshal the account bytes back into a struct
	actual := &accountData{}
	if err := encoding.Unmarshal(accBytes, actual); err != nil {
		t.Fatal(err)
	}

	// Verify unmarshal of the account id
	if expected.Id.String() != actual.Id.String() {
		t.Error("Incorrect id after unmarshal")
		t.Log("Expected: ", expected.Id.String())
		t.Log("Actual: ", actual.Id.String())
	}

	// Verify unmarshal of the balance
	if expected.Balance.Cmp(actual.Balance) != 0 {
		t.Error("Incorrect balance after unmarshal")
		t.Log("Expected: ", expected.Balance.String())
		t.Log("Actual: ", actual.Balance.String())
	}

	// Verify unmarshal of the updated timestamp
	if expected.LastTxn != actual.LastTxn {
		t.Error("Incorrect lastTxn timestamp after unmarshal")
		t.Log("Expected: ", expected.LastTxn)
		t.Log("Actual: ", actual.LastTxn)
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
