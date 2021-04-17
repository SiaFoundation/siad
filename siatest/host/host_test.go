package host

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/host"
	"go.sia.tech/siad/modules/host/contractmanager"
	"go.sia.tech/siad/node"
	"go.sia.tech/siad/node/api"
	"go.sia.tech/siad/node/api/client"
	"go.sia.tech/siad/siatest"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
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
	time.Sleep(time.Second)

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
		Hosts:   1,
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

	if hc.Contracts[0].RevisionNumber == 0 {
		t.Fatal("contract should have more than 0 revisions but had", hc.Contracts[0].RevisionNumber)
	}

	prevValidPayout := hc.Contracts[0].ValidProofOutputs[1].Value
	prevMissPayout := hc.Contracts[0].MissedProofOutputs[1].Value

	// Upload a file. It will only reach 50% progress.
	_, rf, err := renterNode.UploadNewFile(4096, 1, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	err = renterNode.WaitForUploadProgress(rf, 0.5)
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

	vpo := hc.Contracts[0].ValidProofOutputs[1].Value
	if vpo.Cmp(prevValidPayout) != 1 {
		t.Fatalf("valid payout should be greater than old valid payout %v %v", vpo, prevValidPayout)
	}

	mpo := hc.Contracts[0].MissedProofOutputs[1].Value
	if mpo.Cmp(prevMissPayout) != 1 {
		t.Fatalf("missed payout should be more than old missed payout %v %v", mpo, prevMissPayout)
	}
}

// TestHostContract confirms that the host contract endpoint returns the expected values
func TestHostContract(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	gp := siatest.GroupParams{
		Hosts:   2,
		Renters: 1,
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
	renterNode := tg.Renters()[0]

	_, err = hostNode.HostContractGet(types.FileContractID{})
	if err == nil {
		t.Fatal("expected unknown obligation id to return error")
	}

	formed, err := hostNode.HostContractInfoGet()
	if err != nil {
		t.Fatal(err)
	}

	if len(formed.Contracts) == 0 {
		t.Fatal("expected renter to form contract")
	}

	contractID := formed.Contracts[0].ObligationId
	hcg, err := hostNode.HostContractGet(contractID)
	if err != nil {
		t.Fatal(err)
	}

	if hcg.Contract.ObligationId != contractID {
		t.Fatalf("returned contract id %s should match requested id %s", hcg.Contract.ObligationId, contractID)
	}

	if hcg.Contract.DataSize != 0 {
		t.Fatal("contract should have 0 datasize")
	}

	prevValidPayout := hcg.Contract.ValidProofOutputs[1].Value
	prevMissPayout := hcg.Contract.MissedProofOutputs[1].Value
	_, _, err = renterNode.UploadNewFileBlocking(int(modules.SectorSize), 1, 1, true)
	if err != nil {
		t.Fatal(err)
	}

	hcg, err = hostNode.HostContractGet(contractID)
	if err != nil {
		t.Fatal(err)
	}

	if hcg.Contract.DataSize != modules.SectorSize {
		t.Fatal("contract should have 1 sector uploaded")
	}

	// to avoid an NDF we do not compare the RevisionNumber to an exact number
	// because that is not deterministic due to the new RHP3 protocol, which
	// uses the contract to fund EAs do balance checks and so forth
	if hcg.Contract.RevisionNumber == 1 {
		t.Fatal("contract should have received more revisions from the upload", hcg.Contract.RevisionNumber)
	}

	// We don't need a funded account for uploading so the account might not be
	// funded yet. That's why we retry to avoid an NDF.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		hcg, err = hostNode.HostContractGet(contractID)
		if err != nil {
			return err
		}
		if hcg.Contract.PotentialAccountFunding.IsZero() {
			return errors.New("contract should have account funding")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if hcg.Contract.PotentialUploadRevenue.IsZero() {
		t.Fatal("contract should have upload revenue")
	}

	if hcg.Contract.PotentialStorageRevenue.IsZero() {
		t.Fatal("contract should have storage revenue")
	}

	vpo := hcg.Contract.ValidProofOutputs[1].Value
	if vpo.Cmp(prevValidPayout) == 0 {
		t.Fatalf("valid payout should be different than old valid payout %v %v", vpo, prevValidPayout)
	}

	mpo := hcg.Contract.MissedProofOutputs[1].Value
	if mpo.Cmp(prevMissPayout) == 0 {
		t.Fatalf("missed payout should be different than old missed payout %v %v", mpo, prevMissPayout)
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

// TestHostGetPriceTable confirms that the price table is returned through the
// API
func TestHostGetPriceTable(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Host
	testDir := hostTestDir(t.Name())

	// Create a new group with only a host. We create a group to make sure the
	// host is initialized with the default registry.
	groupParams := siatest.GroupParams{
		Miners: 1,
		Hosts:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Call HostGet, confirm price table is not a blank table.
	h := tg.Hosts()[0]
	hg, err := h.HostGet()
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(hg.PriceTable, modules.RPCPriceTable{}) {
		t.Fatal("HostGet contains empty price table")
	}

	// Check the fields in the price table against its counterparts in the internal
	// settings.
	es := hg.ExternalSettings
	pt := hg.PriceTable
	if !es.UploadBandwidthPrice.Equals(pt.UploadBandwidthCost) {
		t.Fatal("upload bandwidth doesn't match")
	}
	if !es.DownloadBandwidthPrice.Equals(pt.DownloadBandwidthCost) {
		t.Fatal("download bandwidth doesn't match")
	}
	if !es.StoragePrice.Equals(pt.WriteStoreCost) {
		t.Fatal("storage price doesn't match")
	}

	// Registry defaults to 0 entries.
	if pt.RegistryEntriesTotal != pt.RegistryEntriesLeft {
		t.Fatal("all registry entries should be free")
	}
	// Check for default entries. Hardcoded to make sure we notice changes.
	if pt.RegistryEntriesTotal != 1024 {
		t.Fatal("wrong number of total registry entries")
	}
	// Check that validity is set. Hardcoded for the same reasons as before.
	if pt.Validity != time.Minute {
		t.Fatal("invalid validity")
	}

	if !pt.SubscriptionMemoryCost.Equals(types.NewCurrency64(1)) {
		t.Fatal("wrong subscription memory cost")
	}
	if !pt.SubscriptionNotificationCost.Equals(types.NewCurrency64(1)) {
		t.Fatal("wrong subscription notification cost")
	}
}
