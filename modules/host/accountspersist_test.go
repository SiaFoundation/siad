package host

import (
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestAccountsPersist verifies accounts are properly saved and reloaded
func TestAccountsPersist(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	ht, err := blankHostTester("TestAccountsPersist")
	if err != nil {
		t.Fatal(err)
	}

	am := ht.host.staticAccountManager
	_, err = am.callDeposit(accountID, types.NewCurrency64(100))
	if err != nil {
		t.Fatal(err)
	}
	saved := am.balanceOf(accountID)

	// reload the host
	err = ht.host.Close()
	if err != nil {
		t.Fatal(err)
	}
	ht.host, err = New(ht.cs, ht.gateway, ht.tpool, ht.wallet, "localhost:0", filepath.Join(ht.persistDir, modules.HostDir))
	if err != nil {
		t.Fatal(err)
	}

	loaded := am.balanceOf(accountID)
	if !loaded.Equals(saved) {
		t.Fatal("Loaded balance did not equal saved balance")
	}
}
