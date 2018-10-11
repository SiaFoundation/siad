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
		Funds:       types.SiacoinPrecision.Mul64(500),
		Hosts:       uint64(50),
		Period:      types.BlockHeight(12096),
		RenewWindow: types.BlockHeight(4032),
	}
)

func calculateWeightFromUInt64Price(price, collateral uint64) (weight types.Currency) {
	hdb := bareHostDB()
	hdb.UpdateAllowance(DefaultTestAllowance)
	hdb.blockHeight = 0
	var entry modules.HostDBEntry
	entry.Version = build.Version
	entry.RemainingStorage = 250e3
	entry.ContractPrice = types.NewCurrency64(5).Mul(types.SiacoinPrecision)
	entry.StoragePrice = types.NewCurrency64(price).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	entry.Collateral = types.NewCurrency64(collateral).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte)
	return hdb.weightFunc(entry)
}

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

	w1 := hdb.weightFunc(entry)
	w2 := hdb.weightFunc(entry2)
	if w1.Cmp(w2) < 0 {
		t.Error("Larger collateral should have more weight")
	}
}

func TestHostWeightStorageRemainingDifferences(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	var entry modules.HostDBEntry
	entry.RemainingStorage = 250e3
	entry.StoragePrice = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Collateral = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)

	entry2 := entry
	entry2.RemainingStorage = 50e3
	w1 := hdb.weightFunc(entry)
	w2 := hdb.weightFunc(entry2)

	if w1.Cmp(w2) < 0 {
		t.Error("Larger storage remaining should have more weight")
	}
}

func TestHostWeightVersionDifferences(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	var entry modules.HostDBEntry
	entry.RemainingStorage = 250e3
	entry.StoragePrice = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Collateral = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Version = "v1.0.4"

	entry2 := entry
	entry2.Version = "v1.0.3"
	w1 := hdb.weightFunc(entry)
	w2 := hdb.weightFunc(entry2)

	if w1.Cmp(w2) < 0 {
		t.Error("Higher version should have more weight")
	}
}

func TestHostWeightLifetimeDifferences(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	hdb.blockHeight = 10000
	var entry modules.HostDBEntry
	entry.RemainingStorage = 250e3
	entry.StoragePrice = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Collateral = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Version = "v1.0.4"

	entry2 := entry
	entry2.FirstSeen = 8100
	w1 := hdb.weightFunc(entry)
	w2 := hdb.weightFunc(entry2)

	if w1.Cmp(w2) < 0 {
		t.Error("Been around longer should have more weight")
	}
}

func TestHostWeightUptimeDifferences(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	hdb.blockHeight = 10000
	var entry modules.HostDBEntry
	entry.RemainingStorage = 250e3
	entry.StoragePrice = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Collateral = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Version = "v1.0.4"
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
	w1 := hdb.weightFunc(entry)
	w2 := hdb.weightFunc(entry2)

	if w1.Cmp(w2) < 0 {
		t.Error("Been around longer should have more weight")
	}
}

func TestHostWeightUptimeDifferences2(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	hdb.blockHeight = 10000
	var entry modules.HostDBEntry
	entry.RemainingStorage = 250e3
	entry.StoragePrice = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Collateral = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Version = "v1.0.4"
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
	w1 := hdb.weightFunc(entry)
	w2 := hdb.weightFunc(entry2)

	if w1.Cmp(w2) < 0 {
		t.Errorf("Been around longer should have more weight\n\t%v\n\t%v", w1, w2)
	}
}

func TestHostWeightUptimeDifferences3(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	hdb.blockHeight = 10000
	var entry modules.HostDBEntry
	entry.RemainingStorage = 250e3
	entry.StoragePrice = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Collateral = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Version = "v1.0.4"
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
	w1 := hdb.weightFunc(entry)
	w2 := hdb.weightFunc(entry2)

	if w1.Cmp(w2) < 0 {
		t.Error("Been around longer should have more weight")
	}
}

func TestHostWeightUptimeDifferences4(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	hdb.blockHeight = 10000
	var entry modules.HostDBEntry
	entry.RemainingStorage = 250e3
	entry.StoragePrice = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Collateral = types.NewCurrency64(1000).Mul(types.SiacoinPrecision)
	entry.Version = "v1.0.4"
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
	w1 := hdb.weightFunc(entry)
	w2 := hdb.weightFunc(entry2)

	if w1.Cmp(w2) < 0 {
		t.Error("Been around longer should have more weight")
	}
}
