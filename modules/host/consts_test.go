package host

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestSaneDefaults verifies that the defaults satisfy the ratios
func TestSaneDefaults(t *testing.T) {
	if defaultBaseRPCPrice.Div(modules.MaxMinBaseRPCPricesToDownloadPricesRatioDiv).Cmp(defaultDownloadBandwidthPrice) > 0 {
		t.Log("defaultBaseRPCPrice", defaultBaseRPCPrice.HumanString())
		t.Log("defaultBaseRPCPrice / ratio", defaultBaseRPCPrice.Div(modules.MaxMinBaseRPCPricesToDownloadPricesRatioDiv).HumanString())
		t.Log("defaultDownloadBandwidthPrice", defaultDownloadBandwidthPrice.HumanString())
		t.Fatal("Default for BaseRPCPrice is bad")
	}
	if defaultSectorAccessPrice.Div(modules.MaxMinSectorAccessPriceToDownloadPricesRatioDiv).Cmp(defaultDownloadBandwidthPrice) > 0 {
		t.Log("defaultSectorAccessPrice", defaultSectorAccessPrice.HumanString())
		t.Log("defaultSectorAccessPrice / ratio", defaultSectorAccessPrice.Div(modules.MaxMinSectorAccessPriceToDownloadPricesRatioDiv).HumanString())
		t.Log("defaultDownloadBandwidthPrice", defaultDownloadBandwidthPrice.HumanString())
		t.Fatal("Default for SectorAccessPrice is bad")
	}
}
