package hostdb

import (
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	DefaultTestAllowance = modules.Allowance{
		Funds:                     types.SiacoinPrecision.Mul64(500),
		Hosts:                     uint64(50),
		Period:                    types.BlockHeight(12096),
		RenewWindow:               types.BlockHeight(4032),
		ExpectedStorage:           modules.DefaultAllowance.ExpectedStorage,
		ExpectedUploadFrequency:   modules.DefaultAllowance.ExpectedUploadFrequency,
		ExpectedDownloadFrequency: modules.DefaultAllowance.ExpectedDownloadFrequency,
		ExpectedRedundancy:        modules.DefaultAllowance.ExpectedRedundancy,
	}
)

func calculateWeightFromUInt64Price(price, collateral uint64) (weight types.Currency) {
	hdb := bareHostDB()
	hdb.SetAllowance(DefaultTestAllowance)
	hdb.blockHeight = 0
	var entry modules.HostDBEntry
	entry.Version = build.Version
	entry.RemainingStorage = 250e3
	entry.MaxCollateral = types.NewCurrency64(1e3).Mul(types.SiacoinPrecision)
	entry.ContractPrice = types.NewCurrency64(5).Mul(types.SiacoinPrecision)
	entry.StoragePrice = types.NewCurrency64(price).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.Collateral = types.NewCurrency64(collateral).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	return hdb.weightFunc(entry).Score()
}

// TestHostWeightDistinctPrices ensures that the host weight is different if the
// prices are different, and that a higher price has a lower score.
func TestHostWeightDistinctPrices(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	weight1 := calculateWeightFromUInt64Price(300, 100)
	weight2 := calculateWeightFromUInt64Price(301, 100)
	if weight1.Cmp(weight2) <= 0 {
		t.Log(weight1)
		t.Log(weight2)
		t.Error("Weight of expensive host is not the correct value.")
	}
}

// TestHostWeightDistinctCollateral ensures that the host weight is different if
// the collaterals are different, and that a higher collateral has a higher
// score.
func TestHostWeightDistinctCollateral(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	weight1 := calculateWeightFromUInt64Price(300, 100)
	weight2 := calculateWeightFromUInt64Price(300, 99)
	if weight1.Cmp(weight2) <= 0 {
		t.Log(weight1)
		t.Log(weight2)
		t.Error("Weight of expensive host is not the correct value.")
	}
}

// When the collateral is below the cutoff, the collateral should be more
// important than the price.
func TestHostWeightCollateralBelowCutoff(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	weight1 := calculateWeightFromUInt64Price(300, 10)
	weight2 := calculateWeightFromUInt64Price(150, 5)
	if weight1.Cmp(weight2) <= 0 {
		t.Log(weight1)
		t.Log(weight2)
		t.Error("Weight of expensive host is not the correct value.")
	}
}

// When the collateral is below the cutoff, the price should be more important
// than the collateral.
func TestHostWeightCollateralAboveCutoff(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	weight1 := calculateWeightFromUInt64Price(300, 1000)
	weight2 := calculateWeightFromUInt64Price(150, 500)
	if weight1.Cmp(weight2) >= 0 {
		t.Log(weight1)
		t.Log(weight2)
		t.Error("Weight of expensive host is not the correct value.")
	}
}

// TestHostWeightIdenticalPrices checks that the weight function is
// deterministic for two hosts that have identical settings - each should get
// the same score.
func TestHostWeightIdenticalPrices(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	weight1 := calculateWeightFromUInt64Price(42, 100)
	weight2 := calculateWeightFromUInt64Price(42, 100)
	if weight1.Cmp(weight2) != 0 {
		t.Error("Weight of identically priced hosts should be equal.")
	}
}

// TestHostWeightWithOnePricedZero checks that nothing unexpected happens when
// there is a zero price, and also checks that the zero priced host scores
// higher  than the host that charges money.
func TestHostWeightWithOnePricedZero(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	weight1 := calculateWeightFromUInt64Price(5, 100)
	weight2 := calculateWeightFromUInt64Price(0, 100)
	if weight1.Cmp(weight2) >= 0 {
		t.Error("Zero-priced host should have higher weight than nonzero-priced host.")
	}
}

// TestHostWeightBothPricesZero checks that there is nondeterminism in the
// weight function even with zero value prices.
func TestHostWeightWithBothPricesZero(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	weight1 := calculateWeightFromUInt64Price(0, 100)
	weight2 := calculateWeightFromUInt64Price(0, 100)
	if weight1.Cmp(weight2) != 0 {
		t.Error("Weight of two zero-priced hosts should be equal.")
	}
}

// TestHostWeightWithNoCollateral checks that nothing bad (like a panic) happens
// when the collateral is set to zero.
func TestHostWeightWithNoCollateral(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	weight1 := calculateWeightFromUInt64Price(300, 1)
	weight2 := calculateWeightFromUInt64Price(300, 0)
	if weight1.Cmp(weight2) <= 0 {
		t.Log(weight1)
		t.Log(weight2)
		t.Error("Weight of lower priced host should be higher")
	}
}

// TestHostWeightStorageRemainingDifferences checks that the host with more
// collateral has more weight.
func TestHostWeightCollateralDifferences(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	var entry modules.HostDBEntry
	entry.RemainingStorage = 250e3
	entry.StoragePrice = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Collateral = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry2 := entry
	entry2.Collateral = types.NewCurrency64(500).Mul(types.SiacoinPrecision)

	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()
	if w1.Cmp(w2) < 0 {
		t.Error("Larger collateral should have more weight")
	}
}

// TestHostWeightStorageRemainingDifferences checks that hosts with less storage
// remaining have a lower weight.
func TestHostWeightStorageRemainingDifferences(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	var entry modules.HostDBEntry
	entry.Version = build.Version
	entry.RemainingStorage = 250e3
	entry.MaxCollateral = types.NewCurrency64(1e3).Mul(types.SiacoinPrecision)
	entry.ContractPrice = types.NewCurrency64(5).Mul(types.SiacoinPrecision)
	entry.StoragePrice = types.NewCurrency64(100).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.Collateral = types.NewCurrency64(300).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)

	entry2 := entry
	entry2.RemainingStorage = 50e3
	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()

	if w1.Cmp(w2) <= 0 {
		t.Log(w1)
		t.Log(w2)
		t.Error("Larger storage remaining should have more weight")
	}
}

// TestHostWeightVersionDifferences checks that a host with an out of date
// version has a lower score than a host with a more recent version.
func TestHostWeightVersionDifferences(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	var entry modules.HostDBEntry
	entry.Version = build.Version
	entry.RemainingStorage = 250e3
	entry.MaxCollateral = types.NewCurrency64(1e3).Mul(types.SiacoinPrecision)
	entry.ContractPrice = types.NewCurrency64(5).Mul(types.SiacoinPrecision)
	entry.StoragePrice = types.NewCurrency64(100).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.Collateral = types.NewCurrency64(300).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)

	entry2 := entry
	entry2.Version = "v1.3.2"
	w1 := hdb.weightFunc(entry)
	w2 := hdb.weightFunc(entry2)

	if w1.Score().Cmp(w2.Score()) <= 0 {
		t.Log(w1)
		t.Log(w2)
		t.Error("Higher version should have more weight")
	}
}

// TestHostWeightLifetimeDifferences checks that a host that has been on the
// chain for more time has a higher weight than a host that is newer.
func TestHostWeightLifetimeDifferences(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	hdb.blockHeight = 10000
	var entry modules.HostDBEntry
	entry.Version = build.Version
	entry.RemainingStorage = 250e3
	entry.MaxCollateral = types.NewCurrency64(1e3).Mul(types.SiacoinPrecision)
	entry.ContractPrice = types.NewCurrency64(5).Mul(types.SiacoinPrecision)
	entry.StoragePrice = types.NewCurrency64(100).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.Collateral = types.NewCurrency64(300).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)

	entry2 := entry
	entry2.FirstSeen = 8100
	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()

	if w1.Cmp(w2) <= 0 {
		t.Log(w1)
		t.Log(w2)
		t.Error("Been around longer should have more weight")
	}
}

// TestHostWeightUptimeDifferences checks that hosts with poorer uptimes have
// lower weights.
func TestHostWeightUptimeDifferences(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	hdb.blockHeight = 10000
	var entry modules.HostDBEntry
	entry.Version = build.Version
	entry.RemainingStorage = 250e3
	entry.MaxCollateral = types.NewCurrency64(1e3).Mul(types.SiacoinPrecision)
	entry.ContractPrice = types.NewCurrency64(5).Mul(types.SiacoinPrecision)
	entry.StoragePrice = types.NewCurrency64(100).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.Collateral = types.NewCurrency64(300).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: true},
	}

	entry2 := entry
	entry2.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: false},
	}
	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()

	if w1.Cmp(w2) < 0 {
		t.Log(w1)
		t.Log(w2)
		t.Error("Been around longer should have more weight")
	}
}

// TestHostWeightUptimeDifferences2 checks that hosts with poorer uptimes have
// lower weights.
func TestHostWeightUptimeDifferences2(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	hdb.blockHeight = 10000
	var entry modules.HostDBEntry
	entry.Version = build.Version
	entry.RemainingStorage = 250e3
	entry.MaxCollateral = types.NewCurrency64(1e3).Mul(types.SiacoinPrecision)
	entry.ContractPrice = types.NewCurrency64(5).Mul(types.SiacoinPrecision)
	entry.StoragePrice = types.NewCurrency64(100).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.Collateral = types.NewCurrency64(300).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: false},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: true},
	}

	entry2 := entry
	entry2.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: false},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: true},
	}
	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()

	if w1.Cmp(w2) < 0 {
		t.Errorf("Been around longer should have more weight\n\t%v\n\t%v", w1, w2)
	}
}

// TestHostWeightUptimeDifferences3 checks that hosts with poorer uptimes have
// lower weights.
func TestHostWeightUptimeDifferences3(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	hdb.blockHeight = 10000
	var entry modules.HostDBEntry
	entry.Version = build.Version
	entry.RemainingStorage = 250e3
	entry.MaxCollateral = types.NewCurrency64(1e3).Mul(types.SiacoinPrecision)
	entry.ContractPrice = types.NewCurrency64(5).Mul(types.SiacoinPrecision)
	entry.StoragePrice = types.NewCurrency64(100).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.Collateral = types.NewCurrency64(300).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: false},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: true},
	}

	entry2 := entry
	entry2.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: false},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: false},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: true},
	}
	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()

	if w1.Cmp(w2) < 0 {
		t.Error("Been around longer should have more weight")
	}
}

// TestHostWeightUptimeDifferences4 checks that hosts with poorer uptimes have
// lower weights.
func TestHostWeightUptimeDifferences4(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	hdb.blockHeight = 10000
	var entry modules.HostDBEntry
	entry.Version = build.Version
	entry.RemainingStorage = 250e3
	entry.MaxCollateral = types.NewCurrency64(1e3).Mul(types.SiacoinPrecision)
	entry.ContractPrice = types.NewCurrency64(5).Mul(types.SiacoinPrecision)
	entry.StoragePrice = types.NewCurrency64(100).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.Collateral = types.NewCurrency64(300).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: false},
	}

	entry2 := entry
	entry2.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: false},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: false},
	}
	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()

	if w1.Cmp(w2) < 0 {
		t.Error("Been around longer should have more weight")
	}
}
