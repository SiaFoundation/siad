package contractor

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/contractor"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/sync"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
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
	testDir := contractorTestDir(t.Name())
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
	err = build.Retry(100, 100*time.Millisecond, func() error {
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
		// Make sure the alert is sane.
		alert := dag.Alerts[0]
		if alert.Severity != modules.SeverityWarning {
			t.Fatal("alert has wrong severity")
		}
		if alert.Msg != contractor.AlertMSGWalletLockedDuringMaintenance {
			t.Fatal("alert has wrong msg", alert.Msg)
		}
		if alert.Cause != modules.ErrLockedWallet.Error() {
			t.Fatal("alert has wrong cause", alert.Cause)
		}
		if alert.Module != "contractor" {
			t.Fatal("alert module expected to be contractor but was ", alert.Module)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
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

// TestRemoveRecoverableContracts makes sure that recoverable contracts which
// have been reverted by a reorg are removed from the map.
func TestRemoveRecoverableContracts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup, creating without renter so the renter's
	// contract transactions can easily be obtained.
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Miners:  1,
		Renters: 1,
	}
	testDir := contractorTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Get the renter node and its seed.
	r := tg.Renters()[0]
	wsg, err := r.WalletSeedsGet()
	if err != nil {
		t.Fatal(err)
	}
	seed := wsg.PrimarySeed

	// The renter should have one contract with each host.
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.ActiveContracts) != len(tg.Hosts()) {
		t.Fatal("Insufficient active contracts")
	}

	// Stop the renter.
	if err := tg.RemoveNode(r); err != nil {
		t.Fatal(err)
	}
	// Bring up new hosts for the new renter to form contracts with, otherwise no
	// contracts will form because it will not form contracts with hosts it see to
	// have recoverable contracts with
	_, err = tg.AddNodeN(node.HostTemplate, 2)
	if err != nil {
		t.Fatal("Failed to create a new host", err)
	}

	// Start a new renter with the same seed but disable contract recovery.
	newRenterDir := filepath.Join(testDir, "renter")
	renterParams := node.Renter(newRenterDir)
	renterParams.Allowance = modules.DefaultAllowance
	renterParams.Allowance.Hosts = 2
	renterParams.PrimarySeed = seed
	renterParams.ContractorDeps = &dependencies.DependencyDisableContractRecovery{}
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	newRenter := nodes[0]

	// The new renter should have the right number of recoverable contracts.
	miner := tg.Miners()[0]
	numRetries := 0
	err = build.Retry(60, time.Second, func() error {
		if numRetries%10 == 0 {
			if err := miner.MineBlock(); err != nil {
				return err
			}
		}
		numRetries++
		rc, err = newRenter.RenterRecoverableContractsGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(rc.RecoverableContracts) != len(tg.Hosts()) {
			return fmt.Errorf("Don't have enough recoverable contracts, expected %v but was %v",
				len(tg.Hosts()), len(rc.RecoverableContracts))
		}
		return nil
	})

	// Get the current blockheight of the group.
	cg, err := newRenter.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	bh := cg.Height

	// Start a new miner which has a longer chain than the group.
	newMiner, err := siatest.NewNode(node.Miner(filepath.Join(testDir, "miner")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := newMiner.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Mine a longer chain.
	for i := types.BlockHeight(0); i < bh+10; i++ {
		if err := newMiner.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}
	// Connect the miner to the renter.
	gg, err := newRenter.GatewayGet()
	if err != nil {
		t.Fatal(err)
	}
	if err := newMiner.GatewayConnectPost(gg.NetAddress); err != nil {
		t.Fatal(err)
	}
	// The recoverable contracts should be gone now after the reorg.
	err = build.Retry(60, time.Second, func() error {
		rc, err = newRenter.RenterRecoverableContractsGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(rc.RecoverableContracts) != 0 {
			return fmt.Errorf("Expected no recoverable contracts, but was %v",
				len(rc.RecoverableContracts))
		}
		return nil
	})
}

// TestRenterContracts tests the formation of the contracts, the contracts
// endpoint, and canceling a contract
func TestRenterContracts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for testing
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Renters: 1,
		Miners:  1,
	}
	testDir := contractorTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Get Renter
	r := tg.Renters()[0]
	rg, err := r.RenterGet()
	if err != nil {
		t.Fatal(err)
	}

	// Record the start period at the beginning of test
	currentPeriodStart := rg.CurrentPeriod
	period := rg.Settings.Allowance.Period
	renewWindow := rg.Settings.Allowance.RenewWindow

	// Check if the current period was set in the past
	cg, err := r.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	if currentPeriodStart > cg.Height-renewWindow {
		t.Fatalf(`Current period not set in the past as expected.
		CP: %v
		BH: %v
		RW: %v
		`, currentPeriodStart, cg.Height, renewWindow)
	}

	// Confirm Contracts were created as expected.  There should only be active
	// contracts and no passive,refreshed, disabled, or expired contracts
	err = build.Retry(200, 100*time.Millisecond, func() error {
		return siatest.CheckExpectedNumberOfContracts(r, len(tg.Hosts()), 0, 0, 0, 0, 0)
	})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm contract end heights were set properly
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rc.ActiveContracts {
		if c.EndHeight != currentPeriodStart+period+renewWindow {
			t.Log("Endheight:", c.EndHeight)
			t.Log("Allowance Period:", period)
			t.Log("Renew Window:", renewWindow)
			t.Log("Current Period:", currentPeriodStart)
			t.Fatal("Contract endheight not set to Current period + Allowance Period + Renew Window")
		}

		// Confirm that the watchdog is monitoring all active contracts.
		_, err := r.RenterContractStatus(c.ID)
		if err != nil {
			t.Fatal("ContractStatus API call failed", err)
		}
	}

	// Record original Contracts and create Maps for comparison
	originalContracts := rc.ActiveContracts
	originalContractIDMap := make(map[types.FileContractID]struct{})
	for _, c := range originalContracts {
		originalContractIDMap[c.ID] = struct{}{}
	}

	// Mine blocks to force contract renewal
	if err = siatest.RenewContractsByRenewWindow(r, tg); err != nil {
		t.Fatal(err)
	}

	// Confirm Contracts were renewed as expected, all original contracts should
	// have been renewed if GoodForRenew = true.  There should be the same
	// number of active and expired contracts, and no other type of contract.
	// The renewed contracts should be with the same hosts as the original
	// active contracts.
	err = build.Retry(200, 100*time.Millisecond, func() error {
		// Confirm we have the expected number of each type of contract
		err := siatest.CheckExpectedNumberOfContracts(r, len(tg.Hosts()), 0, 0, 0, len(originalContracts), 0)
		if err != nil {
			return err
		}
		// Confirm the IDs and hosts make sense
		rc, err := r.RenterAllContractsGet()
		if err != nil {
			return err
		}
		if err = siatest.CheckRenewedContractIDs(rc.ExpiredContracts, rc.ActiveContracts); err != nil {
			return err
		}
		// Confirm the spending makes sense
		if err = siatest.CheckRenewedContractsSpending(rc.ActiveContracts); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm contract end heights were set properly End height should be the
	// end of the next period as the contracts are renewed due to reaching the
	// renew window
	rc, err = r.RenterAllContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rc.ActiveContracts {
		if c.EndHeight != currentPeriodStart+(2*period)+renewWindow && c.GoodForRenew {
			t.Log("Endheight:", c.EndHeight)
			t.Log("Allowance Period:", period)
			t.Log("Renew Window:", renewWindow)
			t.Log("Current Period:", currentPeriodStart)
			t.Fatal("Contract endheight not set to Current period + 2 * Allowance Period + Renew Window")
		}

		// Confirm that the watchdog is monitoring all active contracts.
		_, err := r.RenterContractStatus(c.ID)
		if err != nil {
			t.Fatal("ContractStatus API call failed", err)
		}
	}

	// Renewing contracts by spending is very time consuming, the rest of the
	// test is only run during vlong so the rest of the test package doesn't
	// time out
	if !build.VLONG {
		return
	}

	// Record current active contracts
	rc, err = r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	activeContracts := rc.ActiveContracts

	// Capturing end height to compare against renewed contracts
	endHeight := rc.ActiveContracts[0].EndHeight

	// Renew contracts by running out of funds
	startingUploadSpend, err := siatest.DrainContractsByUploading(r, tg, contractor.MinContractFundRenewalThreshold)
	if err != nil {
		r.PrintDebugInfo(t, true, true, true)
		t.Fatal(err)
	}

	// Confirm contracts were renewed as expected.  Active contracts prior to
	// renewal should now be in the refreshed contracts
	err = build.Retry(200, 100*time.Millisecond, func() error {
		err = siatest.CheckExpectedNumberOfContracts(r, len(tg.Hosts()), 0, len(tg.Hosts()), 0, len(tg.Hosts()), 0)
		if err != nil {
			return err
		}

		// Confirm active and refreshed contracts
		rc, err := r.RenterAllContractsGet()
		if err != nil {
			return err
		}
		refreshedContractIDMap := make(map[types.FileContractID]struct{})
		for _, c := range rc.RefreshedContracts {
			// refreshed contracts should be !GoodForUpload and !GoodForRenew
			if c.GoodForUpload || c.GoodForRenew {
				return errors.New("an renewed contract is being reported as either good for upload or good for renew")
			}
			refreshedContractIDMap[c.ID] = struct{}{}
		}
		for _, c := range activeContracts {
			if _, ok := refreshedContractIDMap[c.ID]; !ok && c.UploadSpending.Cmp(startingUploadSpend) <= 0 {
				return errors.New("ID from activeContacts not found in RefreshedContracts")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm contract end heights were set properly
	// End height should not have changed since the renewal
	// was due to running out of funds
	rc, err = r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rc.ActiveContracts {
		if c.EndHeight != endHeight && c.GoodForRenew && c.UploadSpending.Cmp(startingUploadSpend) <= 0 {
			t.Log("Allowance Period:", period)
			t.Log("Current Period:", currentPeriodStart)
			t.Fatalf("Contract endheight Changed, EH was %v, expected %v\n", c.EndHeight, endHeight)
		}
	}

	// Mine blocks to force contract renewal to start with fresh set of contracts
	if err = siatest.RenewContractsByRenewWindow(r, tg); err != nil {
		t.Fatal(err)
	}

	// Confirm Contracts were renewed as expected
	err = build.Retry(200, 100*time.Millisecond, func() error {
		err = siatest.CheckExpectedNumberOfContracts(r, len(tg.Hosts()), 0, 0, 0, len(tg.Hosts())*2, len(tg.Hosts()))
		if err != nil {
			return err
		}
		// checkContracts will confirm correct number of inactive and active contracts
		rc, err := r.RenterAllContractsGet()
		if err != nil {
			return err
		}
		if err = siatest.CheckRenewedContractIDs(rc.ExpiredContracts, rc.ActiveContracts); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Test canceling contract
	// Grab contract to cancel
	rc, err = r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	contract := rc.ActiveContracts[0]
	// Cancel Contract
	if err := r.RenterContractCancelPost(contract.ID); err != nil {
		t.Fatal(err)
	}

	// Add a new host so new contract can be formed
	hostParams := node.Host(testDir + "/host")
	_, err = tg.AddNodes(hostParams)
	if err != nil {
		t.Fatal(err)
	}

	// Mine a block to trigger contract maintenance
	if err = tg.Miners()[0].MineBlock(); err != nil {
		t.Fatal(err)
	}

	// Confirm contract is cancelled
	err = build.Retry(200, 100*time.Millisecond, func() error {
		// Check that Contract is now in disabled contracts and no longer in Active contracts
		rc, err = r.RenterDisabledContractsGet()
		if err != nil {
			return err
		}
		// Confirm Renter has the expected number of contracts, meaning canceled contract should have been replaced.
		if len(rc.ActiveContracts) < len(tg.Hosts())-1 {
			return fmt.Errorf("Canceled contract was not replaced, only %v active contracts, expected at least %v", len(rc.ActiveContracts), len(tg.Hosts())-1)
		}
		for _, c := range rc.ActiveContracts {
			if c.ID == contract.ID {
				return errors.New("Contract not cancelled, contract found in Active Contracts")
			}
		}
		i := 1
		for _, c := range rc.DisabledContracts {
			if c.ID == contract.ID {
				break
			}
			if i == len(rc.DisabledContracts) {
				return errors.New("Contract not found in Disabled Contracts")
			}
			i++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRenterContractAutomaticRecoveryScan tests that a renter which has already
// scanned the whole blockchain and has lost its contracts, will recover them
// automatically during the next contract maintenance.
func TestRenterContractAutomaticRecoveryScan(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:  2,
		Miners: 1,
	}
	testDir := contractorTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Add a renter node that can't run the automatic contract recovery scan.
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.ContractorDeps = &dependencies.DependencyDisableRecoveryStatusReset{}
	_, err = tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	r := tg.Renters()[0]

	// Upload a file to the renter.
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	fileSize := int(10 * modules.SectorSize)
	_, rf, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}

	// Remember the contracts the renter formed with the hosts.
	oldContracts := make(map[types.FileContractID]api.RenterContract)
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rc.ActiveContracts {
		oldContracts[c.ID] = c
	}

	// Cancel the allowance to avoid new contracts replacing the recoverable
	// ones.
	if err := r.RenterAllowanceCancelPost(); err != nil {
		t.Fatal(err)
	}

	// Stop the renter.
	if err := tg.StopNode(r); err != nil {
		t.Fatal(err)
	}

	// Delete the contracts.
	if err := os.RemoveAll(filepath.Join(r.Dir, modules.RenterDir, "contracts")); err != nil {
		t.Fatal(err)
	}

	// Start the renter again. This time it's unlocked and the automatic recovery
	// scan isn't disabled.
	if err := tg.StartNodeCleanDeps(r); err != nil {
		t.Fatal(err)
	}

	// The renter shouldn't have any contracts.
	rcg, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(rcg.ActiveContracts)+len(rcg.InactiveContracts)+len(rcg.ExpiredContracts) > 0 {
		t.Fatal("There shouldn't be any contracts after deleting them")
	}

	// The new renter should have the same active contracts as the old one.
	miner := tg.Miners()[0]
	numRetries := 0
	err = build.Retry(60, time.Second, func() error {
		if numRetries%10 == 0 {
			if err := miner.MineBlock(); err != nil {
				return err
			}
		}
		numRetries++
		rc, err = r.RenterContractsGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(rc.ActiveContracts) != len(oldContracts) {
			return fmt.Errorf("Didn't recover the right number of contracts, expected %v but was %v",
				len(oldContracts), len(rc.ActiveContracts))
		}
		for _, c := range rc.ActiveContracts {
			contract, exists := oldContracts[c.ID]
			if !exists {
				return errors.New(fmt.Sprint("Recovered unknown contract", c.ID))
			}
			if !contract.HostPublicKey.Equals(c.HostPublicKey) {
				return errors.New("public keys don't match")
			}
			if contract.EndHeight != c.EndHeight {
				return errors.New("endheights don't match")
			}
			if contract.GoodForRenew != c.GoodForRenew {
				return errors.New("GoodForRenew doesn't match")
			}
			if contract.GoodForUpload != c.GoodForUpload {
				return errors.New("GoodForRenew doesn't match")
			}

			// Check that the watchdog has been notified of the contracts being
			// recovered.
			_, err := r.RenterContractStatus(c.ID)
			if err != nil {
				t.Fatal("ContractStatus API call failed", err)
			}
		}
		return nil
	})
	if err != nil {
		rc, _ = r.RenterContractsGet()
		t.Log("Contracts in total:", len(rc.Contracts))
		t.Fatal(err)
	}
	// Download the whole file again to see if all roots were recovered.
	_, _, err = r.DownloadByStream(rf)
	if err != nil {
		t.Fatal(err)
	}
}

// TestRenterContractInitRecoveryScan tests that a renter which has already
// scanned the whole blockchain and has lost its contracts, can recover them by
// triggering a rescan through the API.
func TestRenterContractInitRecoveryScan(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:  2,
		Miners: 1,
	}
	testDir := contractorTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Add a Renter node
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.ContractorDeps = &dependencies.DependencyDisableRecoveryStatusReset{}
	_, err = tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	r := tg.Renters()[0]

	// Upload a file to the renter.
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	fileSize := int(10 * modules.SectorSize)
	_, rf, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}

	// Remember the contracts the renter formed with the hosts.
	oldContracts := make(map[types.FileContractID]api.RenterContract)
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rc.ActiveContracts {
		oldContracts[c.ID] = c
	}

	// Cancel the allowance to avoid new contracts replacing the recoverable
	// ones.
	if err := r.RenterAllowanceCancelPost(); err != nil {
		t.Fatal(err)
	}

	// Stop the renter.
	if err := tg.StopNode(r); err != nil {
		t.Fatal(err)
	}

	// Delete the contracts.
	if err := os.RemoveAll(filepath.Join(r.Dir, modules.RenterDir, "contracts")); err != nil {
		t.Fatal(err)
	}

	// Start the renter again.
	if err := tg.StartNode(r); err != nil {
		t.Fatal(err)
	}

	// The renter shouldn't have any contracts.
	rcg, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(rcg.ActiveContracts)+len(rcg.InactiveContracts)+len(rcg.ExpiredContracts) > 0 {
		t.Fatal("There shouldn't be any contracts after deleting them")
	}

	// Trigger a rescan of the blockchain.
	if err := r.RenterInitContractRecoveryScanPost(); err != nil {
		t.Fatal(err)
	}

	// The new renter should have the same active contracts as the old one.
	miner := tg.Miners()[0]
	numRetries := 0
	err = build.Retry(60, time.Second, func() error {
		if numRetries%10 == 0 {
			if err := miner.MineBlock(); err != nil {
				return err
			}
		}
		numRetries++
		rc, err = r.RenterContractsGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(rc.ActiveContracts) != len(oldContracts) {
			return fmt.Errorf("Didn't recover the right number of contracts, expected %v but was %v",
				len(oldContracts), len(rc.ActiveContracts))
		}
		for _, c := range rc.ActiveContracts {
			contract, exists := oldContracts[c.ID]
			if !exists {
				return errors.New(fmt.Sprint("Recovered unknown contract", c.ID))
			}
			if !contract.HostPublicKey.Equals(c.HostPublicKey) {
				return errors.New("public keys don't match")
			}
			if contract.EndHeight != c.EndHeight {
				return errors.New("endheights don't match")
			}
			if contract.GoodForRenew != c.GoodForRenew {
				return errors.New("GoodForRenew doesn't match")
			}
			if contract.GoodForUpload != c.GoodForUpload {
				return errors.New("GoodForRenew doesn't match")
			}

			// Check that the watchdog has been notified of the contracts being
			// recovered.
			_, err := r.RenterContractStatus(c.ID)
			if err != nil {
				t.Fatal("ContractStatus API call failed", err)
			}
		}
		return nil
	})
	if err != nil {
		rc, _ = r.RenterContractsGet()
		t.Log("Contracts in total:", len(rc.Contracts))
		t.Fatal(err)
	}
	// Download the whole file again to see if all roots were recovered.
	_, _, err = r.DownloadByStream(rf)
	if err != nil {
		t.Fatal(err)
	}
	// Check that the RecoveryScanStatus was set.
	rrs, err := r.RenterContractRecoveryProgressGet()
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// Check the recovery progress endpoint.
		if !rrs.ScanInProgress || rrs.ScannedHeight == 0 {
			return fmt.Errorf("ScanInProgress and/or ScannedHeight weren't set correctly: %v", rrs)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRenterContractRecovery tests that recovering a node from a seed that has
// contracts associated with it will recover those contracts.
func TestRenterContractRecovery(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup, creating without renter so the renter's
	// contract transactions can easily be obtained.
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Miners:  1,
		Renters: 1,
	}
	testDir := contractorTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Get the renter node and its seed.
	r := tg.Renters()[0]
	wsg, err := r.WalletSeedsGet()
	if err != nil {
		t.Fatal(err)
	}
	seed := wsg.PrimarySeed

	// Upload a file to the renter.
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	fileSize := int(10 * modules.SectorSize)
	lf, rf, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}

	// Remember the contracts the renter formed with the hosts.
	oldContracts := make(map[types.FileContractID]api.RenterContract)
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rc.ActiveContracts {
		oldContracts[c.ID] = c
	}

	// Stop the renter.
	if err := tg.RemoveNode(r); err != nil {
		t.Fatal(err)
	}

	// Copy the siafile to the new location.
	oldPath := filepath.Join(r.Dir, modules.RenterDir, modules.FileSystemRoot, modules.HomeFolderRoot, modules.UserRoot, lf.FileName()+modules.SiaFileExtension)
	siaFile, err := ioutil.ReadFile(oldPath)
	if err != nil {
		t.Fatal(err)
	}
	newRenterDir := filepath.Join(testDir, "renter")
	newPath := filepath.Join(newRenterDir, modules.RenterDir, modules.FileSystemRoot, modules.HomeFolderRoot, modules.UserRoot, lf.FileName()+modules.SiaFileExtension)
	if err := os.MkdirAll(filepath.Dir(newPath), persist.DefaultDiskPermissionsTest); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(newPath, siaFile, persist.DefaultDiskPermissionsTest); err != nil {
		t.Fatal(err)
	}

	// Start a new renter with the same seed.
	renterParams := node.Renter(newRenterDir)
	renterParams.PrimarySeed = seed
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	newRenter := nodes[0]

	// Make sure that the new renter actually uses the same primary seed.
	wsg, err = newRenter.WalletSeedsGet()
	if err != nil {
		t.Fatal(err)
	}
	newRenterSeed := wsg.PrimarySeed
	if seed != newRenterSeed {
		t.Log("old seed", seed)
		t.Log("new seed", newRenterSeed)
		t.Fatal("Seeds of new and old renters don't match")
	}

	// The new renter should have the same active contracts as the old one.
	miner := tg.Miners()[0]
	numRetries := 0
	err = build.Retry(60, time.Second, func() error {
		if numRetries%10 == 0 {
			if err := miner.MineBlock(); err != nil {
				return err
			}
		}
		numRetries++
		rc, err = newRenter.RenterContractsGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(rc.ActiveContracts) != len(oldContracts) {
			return fmt.Errorf("Didn't recover the right number of contracts, expected %v but was %v",
				len(oldContracts), len(rc.ActiveContracts))
		}
		for _, c := range rc.ActiveContracts {
			contract, exists := oldContracts[c.ID]
			if !exists {
				return errors.New(fmt.Sprint("Recovered unknown contract", c.ID))
			}
			if !contract.HostPublicKey.Equals(c.HostPublicKey) {
				return errors.New("public keys don't match")
			}
			if contract.StartHeight != c.StartHeight {
				return errors.New("startheights don't match")
			}
			if contract.EndHeight != c.EndHeight {
				return errors.New("endheights don't match")
			}
			if c.Fees.Cmp(types.ZeroCurrency) <= 0 {
				return errors.New("Fees wasn't set")
			}
			if contract.GoodForRenew != c.GoodForRenew {
				return errors.New("GoodForRenew doesn't match")
			}
			if contract.GoodForUpload != c.GoodForUpload {
				return errors.New("GoodForRenew doesn't match")
			}
		}
		return nil
	})
	if err != nil {
		rc, _ = newRenter.RenterContractsGet()
		t.Log("Contracts in total:", len(rc.Contracts))
		t.Fatal(err)
	}
	// Download the whole file again to see if all roots were recovered.
	_, _, err = newRenter.DownloadByStream(rf)
	if err != nil {
		t.Fatal(err)
	}
}

// TestRenterDownloadWithDrainedContract tests if draining a contract below
// MinContractFundUploadThreshold correctly sets a contract to !GoodForUpload
// while still being able to download the file.
func TestRenterDownloadWithDrainedContract(t *testing.T) {
	if testing.Short() || !build.VLONG {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for testing
	groupParams := siatest.GroupParams{
		Hosts:  2,
		Miners: 1,
	}
	testDir := contractorTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Add a renter with a dependency that prevents contract renewals due to
	// low funds.
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.RenterDeps = &dependencies.DependencyDisableRenewal{}
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter := nodes[0]
	miner := tg.Miners()[0]
	// Drain the contracts until they are supposed to no longer be good for
	// uploading.
	_, err = siatest.DrainContractsByUploading(renter, tg, contractor.MinContractFundUploadThreshold)
	if err != nil {
		t.Fatal(err)
	}
	numRetries := 0
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// The 2 contracts should no longer be good for upload.
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if numRetries%10 == 0 {
			if err := miner.MineBlock(); err != nil {
				return err
			}
		}
		numRetries++
		if len(rc.Contracts) != len(tg.Hosts()) {
			return fmt.Errorf("There should be %v contracts but was %v", len(tg.Hosts()), len(rc.Contracts))
		}
		for _, c := range rc.Contracts {
			if c.GoodForUpload || !c.GoodForRenew {
				return fmt.Errorf("Contract shouldn't be good for uploads but it should be good for renew: %v %v",
					c.GoodForUpload, c.GoodForRenew)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Choose a random file and download it.
	files, err := renter.Files(false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = renter.RenterStreamGet(files[fastrand.Intn(len(files))].SiaPath, true)
	if err != nil {
		t.Fatal(err)
	}
}

// TestLowAllowance alert checks if an allowance too low to form/renew contracts
// will trigger the corresponding alert.
func TestLowAllowanceAlert(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for testing
	groupParams := siatest.GroupParams{
		Hosts:  2,
		Miners: 1,
	}
	testDir := contractorTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Add a renter which won't be able to renew a contract due to low funds.
	renterParams := node.Renter(filepath.Join(testDir, "renter_renew"))
	renterParams.Allowance = siatest.DefaultAllowance
	renterParams.Allowance.Period = 10
	renterParams.Allowance.RenewWindow = 5
	renterParams.ContractorDeps = &dependencies.DependencyLowFundsRenewalFail{}
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter := nodes[0]
	// Wait for the alert to be registered.
	numRetries := 0
	err = build.Retry(100, 600*time.Millisecond, func() error {
		if numRetries%10 == 0 {
			if err := tg.Miners()[0].MineBlock(); err != nil {
				t.Fatal(err)
			}
		}
		numRetries++
		dag, err := renter.DaemonAlertsGet()
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, alert := range dag.Alerts {
			if alert.Msg == contractor.AlertMSGAllowanceLowFunds {
				found = true
			}
		}
		if !found {
			return errors.New("alert wasn't registered")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Add a renter which won't be able to form a contract due to low funds.
	renterParams = node.Renter(filepath.Join(testDir, "renter_form"))
	renterParams.SkipSetAllowance = true
	renterParams.ContractorDeps = &dependencies.DependencyLowFundsFormationFail{}
	nodes, err = tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter = nodes[0]
	// Manually set the allowance.
	if err := renter.RenterPostAllowance(siatest.DefaultAllowance); err != nil {
		t.Fatal(err)
	}
	// Wait for the alert to be registered.
	numRetries = 0
	err = build.Retry(100, 600*time.Millisecond, func() error {
		if numRetries%10 == 0 {
			if err := tg.Miners()[0].MineBlock(); err != nil {
				t.Fatal(err)
			}
		}
		numRetries++
		dag, err := renter.DaemonAlertsGet()
		if err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, alert := range dag.Alerts {
			if alert.Msg == contractor.AlertMSGAllowanceLowFunds {
				found = true
			}
		}
		if !found {
			return errors.New("alert wasn't registered")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRenterBadContracts tests that the renter will discard bad contracts.
func TestRenterBadContracts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for testing
	groupParams := siatest.GroupParams{
		Hosts:   1,
		Renters: 1,
		Miners:  1,
	}
	testDir := contractorTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}

	// Create a second host, but perform a dependency injection that will cause
	// the host to reject the contract when the renter tries to grab a session
	// lock.
	secondHostParams := node.HostTemplate
	secondHostParams.HostDeps = &dependencies.HostRejectAllSessionLocks{}
	_, err = tg.AddNodes(secondHostParams)
	if err != nil {
		t.Fatal("Failed to add node to group:", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Upload a file, we will need to download this file later.
	r := tg.Renters()[0]
	// Check the number of active contracts in the renter. Should be 2.
	rcg, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(rcg.ActiveContracts) != 2 {
		t.Fatal("expecting 2 active contracts formed with the 2 hosts", len(rcg.ActiveContracts))
	}

	// Upload a file, which will cause the renter to open a session with all of
	// the hosts, including the host that is explicitly rejecting session locks
	// on a contract.
	_, _, err = r.UploadNewFile(100, 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the renter dropped an active contract. This will have
	// happened because the injected host rejected the session lock due to the
	// contract not being recognized, which will have resulted in the contract
	// being marked bad.
	err = build.Retry(100, 250*time.Millisecond, func() error {
		rcg, err = r.RenterAllContractsGet()
		if err != nil {
			return err
		}
		if len(rcg.ActiveContracts) != 1 {
			return errors.New("expected 1 active contracts")
		}
		if len(rcg.DisabledContracts) != 1 {
			return errors.New("expected 1 disabled contract")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestWatchdogRebroadCast checks that the watchdog re-broadcasts contract
// formation transactions if they are missing.
func TestWatchdogRebroadcast(t *testing.T) {
	testWatchdogRebroadcastOrSweep(t, false)
}

// TestWatchdogSweep checks that the watchdog produces a valid sweep transaction
// when a formation transaction has not appeared on chain in time.
func TestWatchdogSweep(t *testing.T) {
	testWatchdogRebroadcastOrSweep(t, true)
}

// testWatchdogRebroadcastOrSweep tests both watchdog actions by checking its actions in a
// reorg.: re-broadcasting the formation transaction set and also producing
// valid sweep transactions.  Setting testSweep to true sets the reorg height
// high enough to cause the watchdog to attempt to sweep its inputs.
func testWatchdogRebroadcastOrSweep(t *testing.T, testSweep bool) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:   1,
		Miners:  2,
		Renters: 0,
	}
	testDir := contractorTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		err := tg.Close()
		if err != nil && !errors.Contains(err, sync.ErrStopped) {
			t.Fatal(err)
		}
	}()
	miners := tg.Miners()
	goodMiner := miners[0]
	reorgMiner := miners[1]

	// Setup a renter with a toggle-able watchdog.
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	toggleDep := &dependencies.DependencyToggleWatchdogBroadcast{}
	renterParams.ContractorDeps = toggleDep
	renterParams.SkipSetAllowance = true
	renterNodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter := renterNodes[0]

	// Stop the reorg miner before contracts are formed on chain.
	err = reorgMiner.StopNode()
	if err != nil {
		t.Fatal(err)
	}

	allowance := modules.DefaultAllowance
	allowance.Hosts = 1
	if err := renter.RenterPostAllowance(allowance); err != nil {
		t.Fatal(err)
	}
	// Mine a few blocks so the formation transaction can be confirmed.
	for i := 0; i < 5; i++ {
		if err := goodMiner.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}

	// Check that the renter has formed a contract and that it's confirmed.
	err = build.Retry(100, 500*time.Millisecond, func() error {
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != 1 {
			return errors.New("no contracts yet")
		}
		status, err := renter.RenterContractStatus(rc.ActiveContracts[0].ID)
		if err != nil {
			return errors.AddContext(err, "ContractStatus API call failed")
		}
		if !status.ContractFound {
			return errors.AddContext(err, "Active contract not being monitored by watchdog")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Get the filecontract id.
	rc, err := renter.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.ActiveContracts) != 1 {
		t.Fatal("expected 1 contract", len(rc.ActiveContracts))
	}
	fcID := rc.ActiveContracts[0].ID

	// Toggle off watchdog broadcast.
	toggleDep.DisableWatchdogBroadcast(true)

	// Disable the good miner and the host to prevent them from
	// mining or re-broadcasting transactions.
	err = goodMiner.StopNode()
	if err != nil {
		t.Fatal(err)
	}
	err = tg.Hosts()[0].StopNode()
	if err != nil {
		t.Fatal(err)
	}

	// Get the reorg height. If we're testing sweep functionality the height must
	// be beyond the formationwatchheight. Otherwise we just need a longer chain.
	var reorgHeight types.BlockHeight
	if testSweep {
		status, err := renter.RenterContractStatus(fcID)
		if err != nil {
			t.Fatal(err)
		}
		reorgHeight = status.FormationSweepHeight
	} else {
		cg, err := renter.ConsensusGet()
		if err != nil {
			t.Fatal(err)
		}
		reorgHeight = cg.Height + 2
	}
	// Disable the renter so that it doesn't relay blocks to the reorg miner.
	err = renter.StopNode()
	if err != nil {
		t.Fatal(err)
	}

	// Start and connect the miner to the renter, and mine the reorg.
	err = reorgMiner.StartNode()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= int(reorgHeight); i++ {
		if err := reorgMiner.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}
	err = renter.StartNode()
	if err != nil {
		t.Fatal(err)
	}
	// Connect the miner to the renter.
	gg, err := renter.GatewayGet()
	if err != nil {
		t.Fatal(err)
	}
	if err := reorgMiner.GatewayConnectPost(gg.NetAddress); err != nil {
		t.Fatal(err)
	}

	// Sync the renter to the reorg miner.
	err = build.Retry(50, 250*time.Millisecond, func() error {
		renterCG, err := renter.ConsensusGet()
		if err != nil {
			return err
		}
		minerCG, err := reorgMiner.ConsensusGet()
		if err != nil {
			return err
		}

		if minerCG.Height != renterCG.Height {
			return errors.New("unsynced: different heights")
		}
		if minerCG.CurrentBlock != renterCG.CurrentBlock {
			return errors.New("unsynced: different blockids")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Check that the contract is not marked as found, because the watchdog can't
	// rebroadcast.
	numRetries := 0
	err = build.Retry(50, 500*time.Millisecond, func() error {
		if numRetries%10 == 0 {
			if err := reorgMiner.MineBlock(); err != nil {
				t.Fatal(err)
			}
		}
		numRetries++

		status, err := renter.RenterContractStatus(fcID)
		if err != nil {
			return err
		}
		if status.ContractFound {
			return errors.New("contract marked as found)")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Let the watchdog send transactions now.
	toggleDep.DisableWatchdogBroadcast(false)
	numRetries = 0
	err = build.Retry(50, 500*time.Millisecond, func() error {
		if numRetries%5 == 0 {
			if err := reorgMiner.MineBlock(); err != nil {
				t.Fatal(err)
			}
		}
		numRetries++

		status, err := renter.RenterContractStatus(fcID)
		if err != nil {
			return err
		}

		// A valid sweep will appear as a double-spend.  This is guaranteed to be
		// the renter watchdog because the host is offline and because the renter's
		// wallet won't be making any transactions by itself.
		if testSweep {
			if status.DoubleSpendHeight == 0 {
				return errors.New("not double spent")
			}
			return nil
		}
		if !status.ContractFound {
			return errors.New("contract not marked as found)")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestContractorChurnLimiter tests that the churnLimiter limits churn in a
// given period.
func TestContractorChurnLimiter(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a group for testing
	groupParams := siatest.GroupParams{
		Hosts:   5,
		Renters: 0,
		Miners:  1,
	}
	testDir := contractorTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}
	miner := tg.Miners()[0]

	maxPeriodChurn := uint64(modules.SectorSize)
	newRenterDir := filepath.Join(testDir, "renter")
	renterParams := node.Renter(newRenterDir)
	minScoreDep := &dependencies.DependencyHighMinHostScore{}
	renterParams.ContractorDeps = minScoreDep
	renterParams.Allowance = siatest.DefaultAllowance
	renterParams.Allowance.MaxPeriodChurn = maxPeriodChurn
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	r := nodes[0]

	// Upload a file to the renter.
	dataPieces := uint64(1)
	parityPieces := uint64(len(tg.Hosts())) - dataPieces
	fileSize := 1000 // doesn't matter as long as it's at most sector size.
	_, _, err = r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}

	// Get the size of each contract.
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	size := rc.ActiveContracts[0].Size

	var i int
	err = build.Retry(50, 250*time.Millisecond, func() error {
		if i%5 == 0 {
			if err := miner.MineBlock(); err != nil {
				t.Fatal(err)
			}
		}
		i++

		churnStatus, apiErr := r.RenterContractorChurnStatus()
		if apiErr != nil {
			return errors.AddContext(apiErr, "ContractorChurnStatus err")
		}
		if churnStatus.AggregateCurrentPeriodChurn != 0 {
			return fmt.Errorf("Expected no churn for this period but got %v", churnStatus.AggregateCurrentPeriodChurn)
		}
		if churnStatus.MaxPeriodChurn != maxPeriodChurn {
			return fmt.Errorf("Expected max churn change to show: %v %v", churnStatus.MaxPeriodChurn, maxPeriodChurn)
		}
		rc, err = r.RenterDisabledContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != len(tg.Hosts()) {
			return fmt.Errorf("expected %v active contracts but got %v", len(tg.Hosts()), len(rc.ActiveContracts))
		}
		if len(rc.DisabledContracts) != 0 {
			return fmt.Errorf("expected %v disabled contracts but got %v", 0, len(rc.DisabledContracts))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	hosts := tg.Hosts()
	numHostsShutdown := 1

	// Before shutting down a host, get its pubkey to verify that the correct
	// contract is getting churned.
	hostInfo, err := hosts[0].HostGet()
	if err != nil {
		t.Fatal(err)
	}
	hostPubKey := hostInfo.PublicKey

	// Shutdown a host to cause churn.
	err = hosts[0].StopNode()
	if err != nil {
		t.Fatal(err)
	}

	// Check that churn is observable through API.
	expectedChurn := uint64(numHostsShutdown) * size
	i = 0
	err = build.Retry(50, 500*time.Millisecond, func() error {
		if i%5 == 0 {
			if err := miner.MineBlock(); err != nil {
				t.Fatal(err)
			}
		}
		i++

		churnStatus, apiErr := r.RenterContractorChurnStatus()
		if apiErr != nil {
			return errors.AddContext(apiErr, "ContractorChurnStatus err")
		}
		if churnStatus.AggregateCurrentPeriodChurn != expectedChurn {
			return errors.New("Expected more churn for this period")
		}
		rc, err = r.RenterDisabledContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != len(tg.Hosts())-1 {
			return fmt.Errorf("expected %v active contracts but got %v", len(tg.Hosts()), len(rc.ActiveContracts))
		}
		if len(rc.DisabledContracts) != 1 {
			return fmt.Errorf("expected %v disabled contracts but got %v", len(tg.Hosts())-1, len(rc.DisabledContracts))
		}
		churnedHostKey := rc.DisabledContracts[0].HostPublicKey
		if !churnedHostKey.Equals(hostPubKey) {
			return errors.New("wrong host churned")
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine to the beginning of the next period, to reset churn for the period and
	// to build up remainingChurnBudget.
	for i := 0; i <= int(siatest.DefaultAllowance.Period); i++ {
		if err := miner.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}

	// Turn on the renter dependency to simulate more churn. This forces the
	// minimum allowed score to be very high and causes all hosts to be queue to
	// the churnLimiter.
	minScoreDep.ForceHighMinHostScore(true)

	// Check that 1 of the hosts was churned, but that the churn limiter prevented
	// the other bad scoring hosts from getting churned, because the period limit
	// was reached.
	err = build.Retry(50, 500*time.Millisecond, func() error {
		// Mine blocks to increase remainingChurnBudget.
		if err := miner.MineBlock(); err != nil {
			t.Fatal(err)
		}
		i++

		churnStatus, apiErr := r.RenterContractorChurnStatus()
		if apiErr != nil {
			return errors.AddContext(apiErr, "ContractorChurnStatus err")
		}
		if churnStatus.AggregateCurrentPeriodChurn != maxPeriodChurn {
			return fmt.Errorf("Expected max churn for this period: %v %v", churnStatus.AggregateCurrentPeriodChurn, maxPeriodChurn)
		}
		rc, err = r.RenterDisabledContractsGet()
		if err != nil {
			return err
		}
		if len(rc.ActiveContracts) != 0 {
			return fmt.Errorf("expected %v active contracts but got %v", 0, len(rc.ActiveContracts))
		}
		if len(rc.DisabledContracts) != 1 {
			return fmt.Errorf("expected %v disabled contracts but got %v", 1, len(rc.DisabledContracts))
		}

		// Check that a *different* host (i.e. not the offline host) was churned
		// this time.
		churnedHostKey := rc.DisabledContracts[0].HostPublicKey
		if churnedHostKey.Equals(hostPubKey) {
			return errors.New("wrong host churned")
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestContractorHostRemoval checks that the contractor properly migrates away
// from low quality hosts when there are higher quality hosts available.
func TestContractorHostRemoval(t *testing.T) {
	if testing.Short() || !build.VLONG {
		t.SkipNow()
	}

	// Start 2 hosts.
	numInitialHosts := 2
	groupParams := siatest.GroupParams{
		Hosts:   numInitialHosts,
		Miners:  1,
		Renters: 0,
	}
	testDir := contractorTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	hosts := tg.Hosts()
	miner := tg.Miners()[0]

	// Raise prices significantly for each host.
	hostPrice := types.SiacoinPrecision.Mul64(5000) // 5 KS
	for _, host := range hosts {
		err = host.HostModifySettingPost(client.HostParamMinContractPrice, hostPrice)
		if err != nil {
			t.Fatal(err)
		}
	}

	newRenterDir := filepath.Join(testDir, "renter")
	renterParams := node.Renter(newRenterDir)
	renterParams.Allowance = siatest.DefaultAllowance
	renterParams.Allowance.Funds = hostPrice.Mul64(100)
	renterParams.Allowance.Hosts = uint64(numInitialHosts)
	// Set a high period churn so churn limiter does not do much in this test.
	renterParams.Allowance.MaxPeriodChurn = renterParams.Allowance.MaxPeriodChurn * 10000000
	nodes, err := tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
	renter := nodes[0]

	// Upload a file.
	fileSize := 100
	dataPieces := uint64(1)
	parityPieces := uint64(1)
	_, remoteFile, err := renter.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal("Failed to upload a file for testing: ", err)
	}

	// Wait for the redundancy to reach 2.
	expectedRedundancy := 2.0
	err = build.Retry(50, 250*time.Millisecond, func() error {
		fileInfo, err := renter.File(remoteFile)
		if err != nil {
			return err
		}
		if fileInfo.Redundancy < expectedRedundancy {
			return errors.New(fmt.Sprintf("Expected redundancy of %f", expectedRedundancy))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Downloading the file should be succesful.
	if _, _, err := renter.DownloadByStream(remoteFile); err != nil {
		t.Fatal("File download failed", err)
	}

	// Get the host pubkeys.
	initialHostPubKeys := make(map[string]struct{})
	rc, err := renter.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.ActiveContracts) != numInitialHosts {
		t.Fatal("Active contract count should equal number of hosts")
	}
	for _, contract := range rc.ActiveContracts {
		initialHostPubKeys[contract.HostPublicKey.String()] = struct{}{}
	}
	if len(initialHostPubKeys) != numInitialHosts {
		t.Fatal("expected to find all initial host pub keys")
	}

	// Add 3 new hosts that will be competing with the expensive hosts.
	numNewHosts := 3
	_, err = tg.AddNodeN(node.HostTemplate, numNewHosts)
	if err != nil {
		t.Fatal(err)
	}

	// Create a set of contract IDs to save.
	contractIDs := make(map[types.FileContractID]struct{})

	// Check that the renter has made 2 contracts with the new hosts.
	i := 0
	err = build.Retry(100, 250*time.Millisecond, func() error {
		if i%3 == 0 {
			err = miner.MineBlock()
			if err != nil {
				t.Fatal(err)
			}
		}
		i++

		newHostPubKeys := make(map[string]struct{})
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}
		for _, contract := range rc.ActiveContracts {
			contractIDs[contract.ID] = struct{}{} // save the contracts for later.
			if _, ok := initialHostPubKeys[contract.HostPublicKey.String()]; ok {
				continue
			}
			_, ok := newHostPubKeys[contract.HostPublicKey.String()]
			if !ok {
				newHostPubKeys[contract.HostPublicKey.String()] = struct{}{}
			}
		}
		if len(newHostPubKeys) != 2 {
			return errors.New("expected 2 contracts with new hosts")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for the redundancy to reach 2.
	err = build.Retry(50, 250*time.Millisecond, func() error {
		fileInfo, err := renter.File(remoteFile)
		if err != nil {
			return err
		}
		if fileInfo.Redundancy < expectedRedundancy {
			return errors.New(fmt.Sprintf("Expected redundancy of %f", expectedRedundancy))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Block until data has been uploaded to new contracts.
	err = build.Retry(120, 250*time.Millisecond, func() error {
		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}

		for _, contract := range rc.ActiveContracts {
			if contract.Size != modules.SectorSize {
				return fmt.Errorf("Each contrat should have 1 sector: %v - %v", contract.Size, contract.ID)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine into the next period to trigger a renew.
	rg, err := renter.RenterGet()
	if err != nil {
		t.Fatal(err)
	}
	cg, err := miner.ConsensusGet()
	for i := cg.Height; i <= rg.NextPeriod+1; i++ {
		err = miner.MineBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Check that 2 contracts were renewed, but not with the initial hosts.
	i = 0
	err = build.Retry(120, 250*time.Millisecond, func() error {
		if i%3 == 0 {
			err = miner.MineBlock()
			if err != nil {
				t.Fatal(err)
			}
		}
		i++

		rc, err := renter.RenterContractsGet()
		if err != nil {
			return err
		}

		// Count the number of contracts that were not seen in the previous
		// batch of contracts, and check that the new contracts are not with the
		// expensive hosts.
		if len(rc.ActiveContracts) != 2 {
			return errors.New("Expected 2 contracts")
		}

		for _, contract := range rc.ActiveContracts {
			_, exists := contractIDs[contract.ID]
			if exists {
				return errors.New("expected only new contracts to be active")
			}
			if _, ok := initialHostPubKeys[contract.HostPublicKey.String()]; ok {
				return errors.New("contracts with the wrong hosts are being renewed")
			}
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Download the whole file again as a sanity check.
	_, _, err = renter.DownloadByStream(remoteFile)
	if err != nil {
		t.Fatal(err)
	}
}
