package host

import (
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestHostContractCountPersistence checks that the host persists its contract
// counts correctly
func TestHostContractCountPersistence(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// add a storage obligation, which should increment contract count
	so, err := ht.newTesterStorageObligation()
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedLockStorageObligation(so.id())
	err = ht.host.managedAddStorageObligation(so, false)
	if err != nil {
		t.Fatal(err)
	}
	ht.host.managedUnlockStorageObligation(so.id())

	// should have 1 contract now
	if ht.host.financialMetrics.ContractCount != 1 {
		t.Fatal("expected one contract, got", ht.host.financialMetrics.ContractCount)
	}

	// reload the host
	err = ht.host.Close()
	if err != nil {
		t.Fatal(err)
	}
	ht.host, err = New(ht.cs, ht.gateway, ht.tpool, ht.wallet, ht.mux, "localhost:0", filepath.Join(ht.persistDir, modules.HostDir))
	if err != nil {
		t.Fatal(err)
	}

	// contract count should still be 1
	if ht.host.financialMetrics.ContractCount != 1 {
		t.Fatal("expected one contract, got", ht.host.financialMetrics.ContractCount)
	}
}

// TestHostAddressPersistence checks that the host persists any updates to the
// address upon restart.
func TestHostAddressPersistence(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Set the address of the host.
	settings := ht.host.InternalSettings()
	settings.NetAddress = "foo.com:234"
	err = ht.host.SetInternalSettings(settings)
	if err != nil {
		t.Fatal(err)
	}

	// Reboot the host.
	err = ht.host.Close()
	if err != nil {
		t.Fatal(err)
	}
	ht.host, err = New(ht.cs, ht.gateway, ht.tpool, ht.wallet, ht.mux, "localhost:0", filepath.Join(ht.persistDir, modules.HostDir))
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the address persisted.
	if ht.host.settings.NetAddress != "foo.com:234" {
		t.Error("User-set address does not seem to be persisting.")
	}
}

// TestHostPriceRatios checks that the host fixes and price ratios that were
// incorrect and persisted.
func TestHostPriceRatios(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	ht, err := newHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	// Set the unreasonable defaults for the RPC and Sector Access Prices.
	rpcPrice := defaultBaseRPCPrice.Mul64(1e9)
	sectorPrice := defaultSectorAccessPrice.Mul64(1e9)
	settings := ht.host.InternalSettings()
	settings.MinBaseRPCPrice = rpcPrice
	settings.MinSectorAccessPrice = sectorPrice
	err = ht.host.SetInternalSettings(settings)
	if err != nil {
		t.Fatal(err)
	}

	// Reboot the host.
	err = ht.host.Close()
	if err != nil {
		t.Fatal(err)
	}
	ht.host, err = New(ht.cs, ht.gateway, ht.tpool, ht.wallet, ht.mux, "localhost:0", filepath.Join(ht.persistDir, modules.HostDir))
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the RPC and Sector Access Prices were updated as expected
	settings = ht.host.InternalSettings()
	rpcPrice = settings.MinDownloadBandwidthPrice.Mul(modules.MaxMinBaseRPCPricesToDownloadPricesRatioDiv)
	sectorPrice = settings.MinDownloadBandwidthPrice.Mul(modules.MaxMinSectorAccessPriceToDownloadPricesRatioDiv)
	if settings.MinBaseRPCPrice.Cmp(rpcPrice) != 0 {
		t.Log("Actual:", settings.MinBaseRPCPrice.HumanString())
		t.Log("Expected:", rpcPrice.HumanString())
		t.Fatal("rpc price not as expected")
	}
	if settings.MinSectorAccessPrice.Cmp(sectorPrice) != 0 {
		t.Log("Actual:", settings.MinSectorAccessPrice.HumanString())
		t.Log("Expected:", sectorPrice.HumanString())
		t.Fatal("sector price not as expected")
	}

	// Not try setting the mindownload price to an unreasonable value that would
	// force the RPC and Sector prices to be updated
	downloadPrice := settings.MinDownloadBandwidthPrice.Div64(1e6)
	settings.MinDownloadBandwidthPrice = downloadPrice
	err = ht.host.SetInternalSettings(settings)
	if err != nil {
		t.Fatal(err)
	}

	// Reboot the host.
	err = ht.host.Close()
	if err != nil {
		t.Fatal(err)
	}
	ht.host, err = New(ht.cs, ht.gateway, ht.tpool, ht.wallet, ht.mux, "localhost:0", filepath.Join(ht.persistDir, modules.HostDir))
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the RPC and Sector Access Prices were updated as expected
	settings = ht.host.InternalSettings()
	rpcPrice = downloadPrice.Mul(modules.MaxMinBaseRPCPricesToDownloadPricesRatioDiv)
	sectorPrice = downloadPrice.Mul(modules.MaxMinSectorAccessPriceToDownloadPricesRatioDiv)
	if settings.MinBaseRPCPrice.Cmp(rpcPrice) != 0 {
		t.Log("Actual:", settings.MinBaseRPCPrice.HumanString())
		t.Log("Expected:", rpcPrice.HumanString())
		t.Fatal("rpc price not as expected")
	}
	if settings.MinSectorAccessPrice.Cmp(sectorPrice) != 0 {
		t.Log("Actual:", settings.MinSectorAccessPrice.HumanString())
		t.Log("Expected:", sectorPrice.HumanString())
		t.Fatal("sector price not as expected")
	}
}
