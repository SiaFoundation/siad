package host

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestSaneDefaults verifies that the defaults satisfy the ratios
func TestSaneDefaults(t *testing.T) {
	maxBaseRPCPrice := defaultDownloadBandwidthPrice.Mul64(modules.MaxBaseRPCPriceVsBandwidth)
	if defaultBaseRPCPrice.Cmp(maxBaseRPCPrice) > 0 {
		t.Log("defaultBaseRPCPrice", defaultBaseRPCPrice.HumanString())
		t.Log("maxBaseRPCPrice", maxBaseRPCPrice.HumanString())
		t.Log("defaultDownloadBandwidthPrice", defaultDownloadBandwidthPrice.HumanString())
		t.Fatal("Default for BaseRPCPrice is bad")
	}
	maxBaseSectorAccessPrice := defaultDownloadBandwidthPrice.Mul64(modules.MaxSectorAccessPriceVsBandwidth)
	if defaultSectorAccessPrice.Cmp(maxBaseSectorAccessPrice) > 0 {
		t.Log("defaultSectorAccessPrice", defaultSectorAccessPrice.HumanString())
		t.Log("maxBaseSectorAccessPrice", maxBaseSectorAccessPrice.HumanString())
		t.Log("defaultDownloadBandwidthPrice", defaultDownloadBandwidthPrice.HumanString())
		t.Fatal("Default for SectorAccessPrice is bad")
	}
}
