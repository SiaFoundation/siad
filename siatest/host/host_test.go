package host

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/host"
	"gitlab.com/NebulousLabs/Sia/modules/host/contractmanager"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestHostGetPubKey confirms that the pubkey is returned through the API
func TestHostGetPubKey(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Host
	testDir := hostTestDir(t.Name())

	// Create a new server
	hostParams := node.Host(testDir)
	testNode, err := siatest.NewCleanNode(hostParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := testNode.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Call HostGet, confirm public key is not a blank key
	hg, err := testNode.HostGet()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(hg.PublicKey.Key, []byte{}) {
		t.Fatal("Host has empty pubkey key", hg.PublicKey.Key)
	}

	// Get host pubkey from the server and compare to the pubkey return through
	// the HostGet endpoint
	pk, err := testNode.HostPublicKey()
	if err != nil {
		t.Fatal(err)
	}
	if !pk.Equals(hg.PublicKey) {
		t.Log("HostGet PubKey:", hg.PublicKey)
		t.Log("Server PubKey:", pk)
		t.Fatal("Public Keys don't match")
	}
}

// TestHostAlertDiskTrouble verifies the host properly registers the disk
// trouble alert, and returns it through the alerts endpoint
func TestHostAlertDiskTrouble(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	groupParams := siatest.GroupParams{
		Miners: 1,
	}

	alert := modules.Alert{
		Cause:    "",
		Module:   "contractmanager",
		Msg:      contractmanager.AlertMSGHostDiskTrouble,
		Severity: modules.SeverityCritical,
	}

	testDir := hostTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Add a node which won't be able to form a contract due to disk trouble
	depDiskTrouble := dependencies.NewDependencyHostDiskTrouble()
	hostParams := node.Host(filepath.Join(testDir, "/host"))
	hostParams.StorageManagerDeps = depDiskTrouble
	nodes, err := tg.AddNodes(hostParams)
	if err != nil {
		t.Fatal(err)
	}
	h := nodes[0]

	// Add a storage folder and resize it - should trigger disk trouble
	sf := hostTestDir("/some/folder")
	err = h.HostStorageFoldersAddPost(sf, 1<<24)
	if err != nil {
		t.Fatal(err)
	}

	depDiskTrouble.Fail()
	_ = h.HostStorageFoldersResizePost(sf, 1<<23)

	// Test that host registered the alert.
	err = h.IsAlertRegistered(alert)
	if err != nil {
		t.Fatal(err)
	}

	// Test that host reload unregisters the alert
	err = tg.RestartNode(h)
	if err != nil {
		t.Fatal(err)
	}
	err = h.IsAlertUnregistered(alert)
	if err != nil {
		t.Fatal(err)
	}
}

// TestHostAlertInsufficientCollateral verifies the host properly registers the
// insufficient collateral alert, and returns it through the alerts endpoint
func TestHostAlertInsufficientCollateral(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a test group
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Renters: 1,
		Miners:  1,
	}
	testDir := hostTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Confirm contracts got created
	r := tg.Renters()[0]
	rc, err := r.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.ActiveContracts) == 0 {
		t.Fatal("No contracts created")
	}

	// Nullify the host's collateral budget
	h := tg.Hosts()[0]
	hS, _ := h.HostGet()
	err = h.HostModifySettingPost(client.HostParamCollateralBudget, 0)
	if err != nil {
		t.Fatal(err)
	}

	// Mine blocks to force contract renewal
	if err = siatest.RenewContractsByRenewWindow(r, tg); err != nil {
		t.Fatal(err)
	}

	// Test that host registered alert.
	alert := modules.Alert{
		Cause:    "",
		Module:   "host",
		Msg:      host.AlertMSGHostInsufficientCollateral,
		Severity: modules.SeverityWarning,
	}

	if err = h.IsAlertRegistered(alert); err != nil {
		t.Fatal(err)
	}

	// Reinstate the host's collateral budget
	err = h.HostModifySettingPost(client.HostParamCollateralBudget, hS.InternalSettings.CollateralBudget)
	if err != nil {
		t.Fatal(err)
	}

	// Test that host unregistered alert.
	if err = h.IsAlertUnregistered(alert); err != nil {
		t.Fatal(err)
	}
}

// TestHostBandwidth confirms that the host module is monitoring bandwidth
func TestHostBandwidth(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	gp := siatest.GroupParams{
		Hosts:   2,
		Renters: 0,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(hostTestDir(t.Name()), gp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	hostNode := tg.Hosts()[0]

	hbw, err := hostNode.HostBandwidthGet()
	if err != nil {
		t.Fatal(err)
	}

	if hbw.Upload != 0 || hbw.Download != 0 {
		t.Fatal("Expected host to have no upload or download bandwidth")
	}

	if _, err := tg.AddNodes(node.RenterTemplate); err != nil {
		t.Fatal(err)
	}

	hbw, err = hostNode.HostBandwidthGet()
	if err != nil {
		t.Fatal(err)
	}

	if hbw.Upload == 0 || hbw.Download == 0 {
		t.Fatal("Expected host to use bandwidth from rpc with new renter node")
	}

	lastUpload := hbw.Upload
	lastDownload := hbw.Download
	renterNode := tg.Renters()[0]

	_, rf, err := renterNode.UploadNewFileBlocking(100, 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}

	hbw, err = hostNode.HostBandwidthGet()
	if err != nil {
		t.Fatal(err)
	}

	if hbw.Upload <= lastUpload || hbw.Download <= lastDownload {
		t.Fatal("Expected host to use more bandwidth from uploaded file")
	}

	lastUpload = hbw.Upload
	lastDownload = hbw.Download

	if _, _, err := renterNode.DownloadToDisk(rf, false); err != nil {
		t.Fatal(err)
	}

	hbw, err = hostNode.HostBandwidthGet()
	if err != nil {
		t.Fatal(err)
	}

	if hbw.Upload <= lastUpload || hbw.Download <= lastDownload {
		t.Fatal("Expected host to use more bandwidth from downloaded file")
	}
}

// TestHostContracts confirms that the host contracts endpoint returns the expected values
func TestHostContracts(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	gp := siatest.GroupParams{
		Hosts:   2,
		Renters: 0,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(hostTestDir(t.Name()), gp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	hostNode := tg.Hosts()[0]
	hc, err := hostNode.HostContractInfoGet()
	if err != nil {
		t.Fatal(err)
	}

	if len(hc.Contracts) != 0 {
		t.Fatal("expected host to have no contracts")
	}

	if _, err := tg.AddNodes(node.RenterTemplate); err != nil {
		t.Fatal(err)
	}

	renterNode := tg.Renters()[0]
	hc, err = hostNode.HostContractInfoGet()
	if err != nil {
		t.Fatal(err)
	}

	if len(hc.Contracts) == 0 {
		t.Fatal("expected host to have new contract")
	}

	if hc.Contracts[0].DataSize != 0 {
		t.Fatal("contract should have 0 datasize")
	}

	if hc.Contracts[0].RevisionNumber != 1 {
		t.Fatal("contract should have 1 revision")
	}

	prevValidPayout := hc.Contracts[0].ValidProofOutputs[1].Value
	prevMissPayout := hc.Contracts[0].MissedProofOutputs[1].Value
	_, _, err = renterNode.UploadNewFileBlocking(4096, 1, 1, true)
	if err != nil {
		t.Fatal(err)
	}

	hc, err = hostNode.HostContractInfoGet()
	if err != nil {
		t.Fatal(err)
	}

	if hc.Contracts[0].DataSize != 4096 {
		t.Fatal("contract should have 1 sector uploaded")
	}

	// to avoid an NDF we do not compare the RevisionNumber to an exact number
	// because that is not deterministic due to the new RHP3 protocol, which
	// uses the contract to fund EAs do balance checks and so forth
	if hc.Contracts[0].RevisionNumber == 1 {
		t.Fatal("contract should have received more revisions from the upload", hc.Contracts[0].RevisionNumber)
	}

	// We don't need a funded account for uploading so the account might not be
	// funded yet. That's why we retry to avoid an NDF.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		hc, err = hostNode.HostContractInfoGet()
		if err != nil {
			return err
		}
		if hc.Contracts[0].PotentialAccountFunding.IsZero() {
			return errors.New("contract should have account funding")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if hc.Contracts[0].PotentialUploadRevenue.IsZero() {
		t.Fatal("contract should have upload revenue")
	}

	if hc.Contracts[0].PotentialStorageRevenue.IsZero() {
		t.Fatal("contract should have storage revenue")
	}

	if hc.Contracts[0].ValidProofOutputs[1].Value.Cmp(prevValidPayout) != 1 {
		t.Fatal("valid payout should be greater than old valid payout")
	}

	if cmp := hc.Contracts[0].MissedProofOutputs[1].Value.Cmp(prevMissPayout); cmp != 1 {
		t.Fatal("missed payout should be more than old missed payout", cmp)
	}
}

// TestHostExternalSettingsEphemeralAccountFields confirms the host's external
// settings contain both ephemeral account fields and they are initialized to
// their defaults. It will also check if both fields can be updated and whether
// or not those updates take effect properly.
func TestHostExternalSettingsEphemeralAccountFields(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Host
	testDir := hostTestDir(t.Name())
	hostParams := node.Host(testDir)
	host, err := siatest.NewCleanNode(hostParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := host.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	hg, err := host.HostGet()
	if err != nil {
		t.Fatal(err)
	}

	// verify settings exist and are set to their defaults
	if hg.ExternalSettings.EphemeralAccountExpiry != modules.DefaultEphemeralAccountExpiry {
		t.Fatalf("Expected EphemeralAccountExpiry to be set, and to equal the default, instead it was %v", hg.ExternalSettings.EphemeralAccountExpiry)
	}
	if !hg.ExternalSettings.MaxEphemeralAccountBalance.Equals(modules.DefaultMaxEphemeralAccountBalance) {
		t.Fatalf("Expected MaxEphemeralAccountBalance to be set, and to equal the default, instead it was %v", hg.ExternalSettings.MaxEphemeralAccountBalance)
	}

	// modify them
	updatedExpiry := int64(modules.DefaultEphemeralAccountExpiry.Seconds()) + 1
	err = host.HostModifySettingPost(client.HostParamEphemeralAccountExpiry, updatedExpiry)
	if err != nil {
		t.Fatal(err)
	}
	updatedMaxBalance := modules.DefaultMaxEphemeralAccountBalance.Mul64(2)
	err = host.HostModifySettingPost(client.HostParamMaxEphemeralAccountBalance, updatedMaxBalance)
	if err != nil {
		t.Fatal(err)
	}

	// verify settings were updated
	hg, err = host.HostGet()
	if err != nil {
		t.Fatal(err)
	}
	expectedExpiry := time.Duration(updatedExpiry) * time.Second
	if hg.ExternalSettings.EphemeralAccountExpiry != expectedExpiry {
		t.Fatalf("Expected EphemeralAccountExpiry to be set, and to equal the updated value, instead it was %v", hg.ExternalSettings.EphemeralAccountExpiry)
	}
	if !hg.ExternalSettings.MaxEphemeralAccountBalance.Equals(updatedMaxBalance) {
		t.Fatalf("Expected MaxEphemeralAccountBalance to be set, and to equal the updated value, instead it was %v", hg.ExternalSettings.MaxEphemeralAccountBalance)
	}
}

// TestHostValidPrices confirms that the user can't set invalid prices through
// the API
func TestHostValidPrices(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Host
	testDir := hostTestDir(t.Name())
	hostParams := node.Host(testDir)
	host, err := siatest.NewCleanNode(hostParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := host.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Get the Host
	hg, err := host.HostGet()
	if err != nil {
		t.Fatal(err)
	}

	// Verify that setting an invalid RPC price will return an error
	rpcPrice := hg.InternalSettings.MaxBaseRPCPrice().Mul64(modules.MaxBaseRPCPriceVsBandwidth)
	err = host.HostModifySettingPost(client.HostParamMinBaseRPCPrice, rpcPrice)
	if err == nil || !strings.Contains(err.Error(), api.ErrInvalidRPCDownloadRatio.Error()) {
		t.Fatalf("Expected Error %v but got %v", api.ErrInvalidRPCDownloadRatio, err)
	}

	// Verify that setting an invalid Sector price will return an error
	sectorPrice := hg.InternalSettings.MaxSectorAccessPrice().Mul64(modules.MaxSectorAccessPriceVsBandwidth)
	err = host.HostModifySettingPost(client.HostParamMinSectorAccessPrice, sectorPrice)
	if err == nil || !strings.Contains(err.Error(), api.ErrInvalidSectorAccessDownloadRatio.Error()) {
		t.Fatalf("Expected Error %v but got %v", api.ErrInvalidSectorAccessDownloadRatio, err)
	}

	// Verify that setting an invalid download price will return an error. Error
	// should be the RPC error since that is the first check
	downloadPrice := hg.InternalSettings.MinDownloadBandwidthPrice.Div64(modules.MaxBaseRPCPriceVsBandwidth)
	err = host.HostModifySettingPost(client.HostParamMinDownloadBandwidthPrice, downloadPrice)
	if err == nil || !strings.Contains(err.Error(), api.ErrInvalidRPCDownloadRatio.Error()) {
		t.Fatalf("Expected Error %v but got %v", api.ErrInvalidRPCDownloadRatio, err)
	}
}

// TestStorageProofEmptyContract tests that both empty contracts as well as
// not-empty contracts will result in storage proofs.
func TestStorageProofEmptyContract(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup.
	groupParams := siatest.GroupParams{
		Hosts:  2,
		Miners: 1,
	}
	groupDir := hostTestDir(t.Name())

	tg, err := siatest.NewGroupFromTemplate(groupDir, groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Prevent contract renewals to make sure the revision number stays at 1.
	rt := node.RenterTemplate
	rt.ContractorDeps = &dependencies.DependencyDisableRenewal{}
	_, err = tg.AddNodeN(rt, 2)
	if err != nil {
		t.Fatal(err)
	}

	// Fetch the renters.
	renters := tg.Renters()
	renterUpload, renterDownload := renters[0], renters[1]

	// Upload a file to skynet from one renter.
	skylink, _, _, err := renterUpload.UploadNewSkyfileBlocking("test", 100, false)
	if err != nil {
		t.Fatal(err)
	}

	// Download a file from the second renter. This should cause the second
	// renter to spend money on its contracts without increasing their size.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		_, _, err = renterDownload.SkynetSkylinkGet(skylink)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get the storage obligations from the hosts.
	hosts := tg.Hosts()
	host1, host2 := hosts[0], hosts[1]
	cig1, err := host1.HostContractInfoGet()
	if err != nil {
		t.Fatal(err)
	}
	cig2, err := host2.HostContractInfoGet()
	if err != nil {
		t.Fatal(err)
	}

	// There should be 2 contracts per host.
	contracts := append(cig1.Contracts, cig2.Contracts...)
	if len(contracts) != len(hosts)*len(renters) {
		t.Fatalf("expected %v contracts but got %v", len(hosts)*len(renters), len(contracts))
	}

	// Mine until the proof deadline that is furthest in the future.
	var proofDeadline types.BlockHeight
	for _, so := range contracts {
		if so.ProofDeadLine > proofDeadline {
			proofDeadline = so.ProofDeadLine
		}
	}
	bh, err := renterDownload.BlockHeight()
	if err != nil {
		t.Fatal(err)
	}
	for ; bh <= proofDeadline; bh++ {
		err = tg.Miners()[0].MineBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Check that the right number of storage obligations were provided.
	retries := 0
	err = build.Retry(1000, 100*time.Millisecond, func() error {
		if retries%10 == 0 {
			err = tg.Miners()[0].MineBlock()
			if err != nil {
				t.Error(err)
				return nil
			}
		}
		retries++

		cig1, err = host1.HostContractInfoGet()
		if err != nil {
			return err
		}
		cig2, err = host2.HostContractInfoGet()
		if err != nil {
			return err
		}
		proofs := 0
		emptyContracts := 0
		for _, contract := range append(cig1.Contracts, cig2.Contracts...) {
			if contract.ProofConfirmed {
				proofs++
				if contract.DataSize == 0 {
					emptyContracts++
				}
			}
		}

		expectedProofs := len(contracts)
		expectedEmptyContracts := 2
		if proofs < expectedProofs {
			return fmt.Errorf("expected at least %v submitted proofs but got %v", expectedProofs, proofs)
		}
		if emptyContracts < expectedEmptyContracts {
			return fmt.Errorf("expected at least %v submitted empty proofs but got %v", expectedEmptyContracts, emptyContracts)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
