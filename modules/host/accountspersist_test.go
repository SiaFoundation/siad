package host

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestAccountReload verifies that an account is properly saved to disk and gets
// reinstated properly when the host is reloaded
func TestAccountReload(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankHostTester("TestAccountsPersist")
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
		actual := am.balanceOf(id.String())
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
		reloaded := am.balanceOf(id)
		if !reloaded.Equals(expected) {
			t.Log("Expected:", expected.String())
			t.Log("Reloaded:", reloaded.String())
			t.Fatal("Balance after host reload did not equal the expected balance")
		}
	}
}

// TestMarshalUnmarshalAccount
func TestMarshalUnmarshalAccount(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Generate SiaPublicKey
	_, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	// Marshal a dummy account
	acc := account{
		id:      spk,
		balance: types.SiacoinPrecision,
		updated: time.Now().Unix(),
	}
	buf := new(bytes.Buffer)
	err := acc.MarshalSia(buf)
	if err != nil {
		t.Fatal(err)
	}
	accBytes := make([]byte, accountSize)
	copy(accBytes, buf.Bytes())

	// Unmarshal the account bytes back into a struct
	buf = new(bytes.Buffer)
	buf.Write(accBytes)
	var uMarAcc account
	err = uMarAcc.UnmarshalSia(buf)
	if err != nil {
		t.Fatal(err)
	}

	// Verify unmarshal of the account id
	if acc.id.String() != uMarAcc.id.String() {
		t.Log("Before: ", acc.id.String())
		t.Log("After: ", uMarAcc.id.String())
		t.Error("Incorrect id after unmarshal")
	}

	// Verify unmarshal of the balance
	if acc.balance.Cmp(uMarAcc.balance) != 0 {
		t.Log("Before: ", acc.balance.String())
		t.Log("After: ", uMarAcc.balance.String())
		t.Error("Incorrect balance after unmarshal")
	}

	// Verify unmarshal of the updated timestamp
	if acc.updated != uMarAcc.updated {
		t.Log("Before: ", acc.updated)
		t.Log("After: ", uMarAcc.updated)
		t.Error("Incorrect updated timestamp after unmarshal")
	}
}
