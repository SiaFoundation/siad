package renter

import (
	"fmt"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/Sia/siatest"
)

// TestContractorIncompleteMaintenanceAlert tests that having the wallet locked
// during maintenance results in an alert.
func TestContractorIncompleteMaintenanceAlert(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:   1,
		Miners:  1,
		Renters: 1,
	}
	testDir := renterTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// The renter shouldn't have any alerts.
	r := tg.Renters()[0]
	dag, err := r.DaemonAlertsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(dag.Alerts) != 0 {
		t.Fatal("number of alerts is not 0")
	}
	// Save the seed for later.
	wsg, err := r.WalletSeedsGet()
	if err != nil {
		t.Fatal(err)
	}
	// Lock the renter.
	if err := r.WalletLockPost(); err != nil {
		t.Fatal("Failed to lock wallet", err)
	}
	// The renter should have 1 alert once we have mined enough blocks to trigger a
	// renewal.
	err = build.Retry(1000, 100*time.Millisecond, func() error {
		// Mine a block to trigger contract maintenance.
		if err := tg.Miners()[0].MineBlock(); err != nil {
			return err
		}
		dag, err = r.DaemonAlertsGet()
		if err != nil {
			return err
		}
		if len(dag.Alerts) != 1 {
			return fmt.Errorf("Expected 1 alert but got %v", len(dag.Alerts))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Make sure the alert is sane.
	alert := dag.Alerts[0]
	if alert.Severity != modules.SeverityWarning {
		t.Fatal("alert has wrong severity")
	}
	if alert.Msg != "contractor is attempting to renew/form contracts, however the wallet is locked" {
		t.Fatal("alert has wrong msg", alert.Msg)
	}
	if alert.Cause != modules.ErrLockedWallet.Error() {
		t.Fatal("alert has wrong cause", alert.Cause)
	}
	if alert.Module != "contractor" {
		t.Fatal("alert module expected to be contractor but was ", alert.Module)
	}
	// Unlock the renter.
	if err := r.WalletUnlockPost(wsg.PrimarySeed); err != nil {
		t.Fatal("Failed to lock wallet", err)
	}
	// Mine a block to trigger contract maintenance.
	if err := tg.Miners()[0].MineBlock(); err != nil {
		t.Fatal("Failed to mine block", err)
	}
	// The renter should have 0 alerts now.
	err = build.Retry(1000, 100*time.Millisecond, func() error {
		dag, err = r.DaemonAlertsGet()
		if err != nil {
			return err
		}
		if len(dag.Alerts) != 0 {
			return fmt.Errorf("Expected 0 alert but got %v", len(dag.Alerts))
		}
		return nil
	})
}
