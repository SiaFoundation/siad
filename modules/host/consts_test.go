package host

import (
	"testing"

	"go.sia.tech/siad/modules"
)

// TestSaneDefaults verifies that the defaults satisfy the ratios
func TestSaneDefaults(t *testing.T) {
	maxBaseRPCPrice := modules.DefaultDownloadBandwidthPrice.Mul64(modules.MaxBaseRPCPriceVsBandwidth)
	if modules.DefaultBaseRPCPrice.Cmp(maxBaseRPCPrice) > 0 {
		t.Log("modules.DefaultBaseRPCPrice", modules.DefaultBaseRPCPrice.HumanString())
		t.Log("maxBaseRPCPrice", maxBaseRPCPrice.HumanString())
		t.Log("modules.DefaultDownloadBandwidthPrice", modules.DefaultDownloadBandwidthPrice.HumanString())
		t.Fatal("Default for BaseRPCPrice is bad")
	}
	maxBaseSectorAccessPrice := modules.DefaultDownloadBandwidthPrice.Mul64(modules.MaxSectorAccessPriceVsBandwidth)
	if modules.DefaultSectorAccessPrice.Cmp(maxBaseSectorAccessPrice) > 0 {
		t.Log("defaultSectorAccessPrice", modules.DefaultSectorAccessPrice.HumanString())
		t.Log("maxBaseSectorAccessPrice", maxBaseSectorAccessPrice.HumanString())
		t.Log("modules.DefaultDownloadBandwidthPrice", modules.DefaultDownloadBandwidthPrice.HumanString())
		t.Fatal("Default for SectorAccessPrice is bad")
	}
}
