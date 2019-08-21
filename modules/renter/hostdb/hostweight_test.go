package hostdb

import (
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	// Set the default test allowance
	DefaultTestAllowance = modules.Allowance{
		Funds:       types.SiacoinPrecision.Mul64(500),
		Hosts:       uint64(50),
		Period:      3 * types.BlocksPerMonth,
		RenewWindow: types.BlocksPerMonth,

		ExpectedStorage:    1e12,                                         // 1 TB
		ExpectedUpload:     uint64(200e9) / uint64(types.BlocksPerMonth), // 200 GB per month
		ExpectedDownload:   uint64(100e9) / uint64(types.BlocksPerMonth), // 100 GB per month
		ExpectedRedundancy: 3.0,                                          // default is 10/30 erasure coding
	}

	// The default entry to use when performing scoring.
	DefaultHostDBEntry = modules.HostDBEntry{
		HostExternalSettings: modules.HostExternalSettings{
			AcceptingContracts: true,
			MaxDuration:        26e3,
			RemainingStorage:   250e9,
			WindowSize:         144,

			Collateral:    types.NewCurrency64(250).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte),
			MaxCollateral: types.NewCurrency64(750).Mul(types.SiacoinPrecision),

			ContractPrice: types.NewCurrency64(5).Mul(types.SiacoinPrecision),
			StoragePrice:  types.NewCurrency64(100).Mul(types.SiacoinPrecision).Div(modules.BlockBytesPerMonthTerabyte),

			Version: build.Version,
		},
	}
)

// calculateWeightFromUInt64Price will fill out a host entry with a bunch of
// defaults, and then grab the weight of that host using a set price.
func calculateWeightFromUInt64Price(price, collateral uint64) (weight types.Currency) {
	hdb := bareHostDB()
	hdb.SetAllowance(DefaultTestAllowance)
	hdb.blockHeight = 0

	entry := DefaultHostDBEntry
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
	weight1 := calculateWeightFromUInt64Price(5, 10)
	weight2 := calculateWeightFromUInt64Price(0, 10)
	if weight1.Cmp(weight2) >= 0 {
		t.Log(weight1)
		t.Log(weight2)
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

// TestHostWeightMaxDuration checks that the host with an unacceptable duration
// has a lower score.
func TestHostWeightMaxDuration(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	hdb.SetAllowance(DefaultTestAllowance)

	entry := DefaultHostDBEntry
	entry2 := DefaultHostDBEntry
	entry2.MaxDuration = 100 // Shorter than the allowance period.

	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()
	if w1.Cmp(w2) <= 0 {
		t.Error("Acceptable duration should have more weight", w1, w2)
	}
}

// TestHostWeightStorageRemainingDifferences checks that the host with more
// collateral has more weight.
func TestHostWeightCollateralDifferences(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()

	entry := DefaultHostDBEntry
	entry2 := DefaultHostDBEntry
	entry2.Collateral = types.NewCurrency64(500).Mul(types.SiacoinPrecision)

	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()
	if w1.Cmp(w2) <= 0 {
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

	// Create two entries with different host keys.
	entry := DefaultHostDBEntry
	entry.PublicKey.Key = fastrand.Bytes(16)
	entry2 := DefaultHostDBEntry
	entry2.PublicKey.Key = fastrand.Bytes(16)

	// The first entry has more storage remaining than the second.
	entry.RemainingStorage = modules.DefaultAllowance.ExpectedStorage // 1e12
	entry2.RemainingStorage = 1e3

	// The entry with more storage should have the higher score.
	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()
	if w1.Cmp(w2) <= 0 {
		t.Log(w1)
		t.Log(w2)
		t.Error("Larger storage remaining should have more weight")
	}

	// Change both entries to have the same remaining storage but add contractInfo
	// to the HostDB to make it think that we already uploaded some data to one of
	// the entries. This entry should have the higher score.
	entry.RemainingStorage = 1e3
	entry2.RemainingStorage = 1e3
	hdb.knownContracts[entry.PublicKey.String()] = contractInfo{
		HostPublicKey: entry.PublicKey,
		StoredData:    hdb.allowance.ExpectedStorage,
	}
	w1 = hdb.weightFunc(entry).Score()
	w2 = hdb.weightFunc(entry2).Score()
	if w1.Cmp(w2) <= 0 {
		t.Log(w1)
		t.Log(w2)
		t.Error("Entry with uploaded data should have higher score")
	}
}

// TestHostWeightVersionDifferences checks that a host with an out of date
// version has a lower score than a host with a more recent version.
func TestHostWeightVersionDifferences(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()

	entry := DefaultHostDBEntry
	entry2 := DefaultHostDBEntry
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

	entry := DefaultHostDBEntry
	entry2 := DefaultHostDBEntry
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

	entry := DefaultHostDBEntry
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

	if w1.Cmp(w2) <= 0 {
		t.Log(w1)
		t.Log(w2)
		t.Error("A host with recorded downtime should have a lower score")
	}
}

// TestHostWeightUptimeDifferences2 checks that hosts with poorer uptimes have
// lower weights.
func TestHostWeightUptimeDifferences2(t *testing.T) {
	t.Skip("Hostdb is not currently doing exponentiation on uptime")
	if testing.Short() {
		t.SkipNow()
	}
	hdb := bareHostDB()
	hdb.blockHeight = 10000

	entry := DefaultHostDBEntry
	entry.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -200), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -180), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -160), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -140), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -120), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: false},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: true},
	}

	entry2 := entry
	entry2.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -200), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -180), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -160), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -140), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -120), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: false},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: true},
	}
	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()

	if w1.Cmp(w2) <= 0 {
		t.Log(w1)
		t.Log(w2)
		t.Errorf("Downtime that's further in the past should be penalized less")
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

	entry := DefaultHostDBEntry
	entry.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -200), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -180), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -160), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -140), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -120), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: false},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: true},
	}

	entry2 := entry
	entry2.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -200), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -180), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -160), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -140), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -120), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: false},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: false},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: true},
	}
	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()

	if w1.Cmp(w2) <= 0 {
		t.Log(w1)
		t.Log(w2)
		t.Error("A host with longer downtime should have a lower score")
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

	entry := DefaultHostDBEntry
	entry.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -200), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -180), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -160), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -140), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -120), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: false},
	}

	entry2 := entry
	entry2.ScanHistory = modules.HostDBScans{
		{Timestamp: time.Now().Add(time.Hour * -200), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -180), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -160), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -140), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -120), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -100), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -80), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -60), Success: true},
		{Timestamp: time.Now().Add(time.Hour * -40), Success: false},
		{Timestamp: time.Now().Add(time.Hour * -20), Success: false},
	}
	w1 := hdb.weightFunc(entry).Score()
	w2 := hdb.weightFunc(entry2).Score()

	if w1.Cmp(w2) <= 0 {
		t.Log(w1)
		t.Log(w2)
		t.Error("longer tail downtime should have a lower score")
	}
}

// TestHostWeightConstants checks a few relationships between the constants in
// the hostdb.
func TestHostWeightConstants(t *testing.T) {
	// Becaues we no longer use a large base weight, we require that the
	// collateral floor be higher than the price floor, and also that the
	// collateralExponentiationSmall be larger than the
	// priceExponentiationSmall. This protects most hosts from going anywhere
	// near a 0 score.
	if collateralFloor < priceFloor {
		t.Error("Collateral floor should be greater than or equal to price floor")
	}
	if collateralExponentiationSmall < priceExponentiationSmall {
		t.Error("small collateral exponentiation should be larger than small price exponentiation")
	}

	// Try a few hosts and make sure we always end up with a score that is
	// greater than 1 million.
	weight := calculateWeightFromUInt64Price(300, 100)
	if weight.Cmp(types.NewCurrency64(1e9)) < 0 {
		t.Error("weight is not sufficiently high for hosts")
	}
	weight = calculateWeightFromUInt64Price(1000, 1)
	if weight.Cmp(types.NewCurrency64(1e9)) < 0 {
		t.Error("weight is not sufficiently high for hosts")
	}

	hdb := bareHostDB()
	hdb.SetAllowance(DefaultTestAllowance)
	hdb.blockHeight = 0

	entry := DefaultHostDBEntry
	weight = hdb.weightFunc(entry).Score()
	if weight.Cmp(types.NewCurrency64(1e9)) < 0 {
		t.Error("weight is not sufficiently high for hosts")
	}
}
