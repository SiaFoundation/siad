package hostdb

import (
	"fmt"
	"math"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/hostdb/hosttree"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// collateralExponentiation is the power to which we raise the weight
	// during collateral adjustment when the collateral is large. This sublinear
	// number ensures that there is not an overpreference on collateral when
	// collateral is large relative to the size of the allowance.
	collateralExponentiationLarge = 0.5

	// collateralExponentiationSmall is the power to which we raise the weight
	// during collateral adjustment when the collateral is small. A large number
	// ensures a heavy focus on collateral when distinguishing between hosts
	// that have a very small amount of collateral provided compared to the size
	// of the allowance.
	//
	// For safety, this number needs to be larger than priceExponentiationSmall.
	collateralExponentiationSmall = 4

	// collateralFloor is a part of the equation for determining the collateral
	// cutoff between large and small collateral. The equation figures out how
	// much collateral is expected given the allowance, and then divided by
	// 'collateralFloor' so that the cutoff for how much collateral counts as
	// 'not much' is reasonably below what we are actually expecting from the
	// host.

	// collateralFloor determines how much lower than the expected collateral
	// the host can provide before switching to a different scoring strategy. A
	// collateral floor of 0.5 means that once the host is offering a collateral
	// that is more than 50% of what the renter would expect given the amount of
	// storage being used, the host switching to a scoring strategy which less
	// intensly favors adding more collateral. As long as the host has provided
	// sufficient skin-in-the-game, enormous amounts of extra collateral are
	// less important.
	//
	// The collateralFloor is set relative to the price floor because generally
	// we look for the collateral to be about 2x the price.
	collateralFloor = priceFloor * 2

	// interactionExponentiation determines how heavily we penalize hosts for
	// having poor interactions - disconnecting, RPCs with errors, etc. The
	// exponentiation is very high because the renter will already intentionally
	// avoid hosts that do not have many successful interactions, meaning that
	// the bad points do not rack up very quickly.
	interactionExponentiation = 10

	// priceExponentiationLarge is the number of times that the weight is
	// divided by the price when the price is large relative to the allowance.
	// The exponentiation is a lot higher because we care greatly about high
	// priced hosts.
	priceExponentiationLarge = 5

	// priceExponentiationSmall is the number of times that the weight is
	// divided by the price when the price is small relative to the allowance.
	// The exponentiation is lower because we do not care about saving
	// substantial amounts of money when the price is low.
	priceExponentiationSmall = 0.75

	// priceFloor determines how much cheaper than the expected allowance the
	// host can be before switching to a different scoring strategy for the
	// score. A price floor of 0.2 means that once the host is less than 20% of
	// the expected price for that amount of resources (using the allowance as a
	// guide), instead of using priceExponentiationLarge to reward decreasing
	// prices, we use priceExponentiationSmall to reward decreasing prices. This
	// reduced steepness reflects the reality that getting 99.9% off is not all
	// that different from getting 80% off - both feel like an amazing deal.
	//
	// This is necessary to prevent exploits where a host gets an unreasonable
	// score by putting it's price way too low.
	priceFloor = 0.1
)

var (
	// requiredStorage indicates the amount of storage that the host must be
	// offering in order to be considered a valuable/worthwhile host.
	requiredStorage = build.Select(build.Var{
		Standard: uint64(20e9),
		Dev:      uint64(1e6),
		Testing:  uint64(1e3),
	}).(uint64)
)

// collateralAdjustments improves the host's weight according to the amount of
// collateral that they have provided.
func (hdb *HostDB) collateralAdjustments(entry modules.HostDBEntry, allowance modules.Allowance) float64 {
	// Ensure that all values will avoid divide by zero errors.
	if allowance.Hosts == 0 {
		allowance.Hosts = 1
	}
	if allowance.Period == 0 {
		allowance.Period = 1
	}
	if allowance.ExpectedStorage == 0 {
		allowance.ExpectedStorage = 1
	}
	if allowance.ExpectedUpload == 0 {
		allowance.ExpectedUpload = 1
	}
	if allowance.ExpectedDownload == 0 {
		allowance.ExpectedDownload = 1
	}
	if allowance.ExpectedRedundancy == 0 {
		allowance.ExpectedRedundancy = 1
	}

	// Convert each element of the allowance into a number of resources that we
	// expect to use in this contract.
	contractExpectedFunds := allowance.Funds.Div64(allowance.Hosts)
	contractExpectedStorage := uint64(float64(allowance.ExpectedStorage) * allowance.ExpectedRedundancy / float64(allowance.Hosts))
	contractExpectedStorageTime := types.NewCurrency64(contractExpectedStorage).Mul64(uint64(allowance.Period))

	// Ensure that the allowance and expected storage will not brush up against
	// the max collateral. If the allowance comes within half of the max
	// collateral, cap the collateral that we use during adjustments based on
	// the max collateral instead of the per-byte collateral.
	//
	// The purpose of this code is to make sure that the host actually has a
	// high enough MaxCollateral to cover all of the data that we intend to
	// store with the host at the collateral price that the host is advertising.
	// We add a 2x buffer to account for the fact that the renter may end up
	// storing extra data on this host.
	hostCollateral := entry.Collateral.Mul(contractExpectedStorageTime)
	possibleCollateral := entry.MaxCollateral.Div64(2)
	if possibleCollateral.Cmp(hostCollateral) < 0 {
		hostCollateral = possibleCollateral
	}

	// Determine the cutoff for the difference between small collateral and
	// large collateral. The cutoff is used to create a step function in the
	// collateral scoring where decreasing collateral results in much higher
	// penalties below a certain threshold.
	//
	// This threshold is attempting to be the threshold where the amount of
	// money becomes insignificant. A collateral that is 10x higher than the
	// price is not interesting, compelling, nor a sign of reliability if the
	// price and collateral are both effectively zero.
	//
	// TODO: This method has no way to account for bandwidth heavy vs. storage
	// heavy hosts, nor did we give the user any way to configure a situation
	// where hosts aren't needed to be nearly as reliable.
	cutoff := contractExpectedFunds.MulFloat(collateralFloor)

	// Get the ratio between the cutoff and the actual collateral so we can
	// award the bonus for having a large collateral.
	collateral64, _ := hostCollateral.Float64()
	cutoff64, _ := cutoff.Float64()
	// If the hostCollateral is less than the cutoff, set the cutoff equal to
	// the collateral so that the ratio has a minimum of 1, and also so that
	// the smallWeight is computed based on the actual collateral instead of
	// just the cutoff.
	if collateral64 < cutoff64 {
		cutoff64 = collateral64
	}
	// One last check for safety before grabbing the ratio. This ensures that
	// the ratio is never less than one, which is critical to getting a coherent
	// large weight - large weight should never be below one.
	if collateral64 < 1 {
		collateral64 = 1
	}
	if cutoff64 < 1 {
		cutoff64 = 1
	}
	ratio := collateral64 / cutoff64

	// Use the cutoff to determine the score based on the small exponentiation
	// factor (which has a high exponentiation), and then use the ratio between
	// the two to determine the bonus gained from having a high collateral.
	smallWeight := math.Pow(cutoff64, collateralExponentiationSmall)
	largeWeight := math.Pow(ratio, collateralExponentiationLarge)
	return smallWeight * largeWeight
}

// interactionAdjustments determine the penalty to be applied to a host for the
// historic and current interactions with that host. This function focuses on
// historic interactions and ignores recent interactions.
func (hdb *HostDB) interactionAdjustments(entry modules.HostDBEntry) float64 {
	// Give the host a baseline of 30 successful interactions and 1 failed
	// interaction. This gives the host a baseline if we've had few
	// interactions with them. The 1 failed interaction will become
	// irrelevant after sufficient interactions with the host.
	hsi := entry.HistoricSuccessfulInteractions + 30
	hfi := entry.HistoricFailedInteractions + 1

	// Determine the intraction ratio based off of the historic interactions.
	ratio := float64(hsi) / float64(hsi+hfi)
	return math.Pow(ratio, interactionExponentiation)
}

// priceAdjustments will adjust the weight of the entry according to the prices
// that it has set.
//
// REMINDER: The allowance contains an absolute number of bytes for expected
// storage on a per-renter basis that doesn't account for redundancy.. This
// value needs to be adjusted to a per-contract basis that accounts for
// redundancy. The upload and download values also do not account for
// redundancy, and they are on a per-block basis, meaning you need to multiply
// be the allowance period when working with these values.
func (hdb *HostDB) priceAdjustments(entry modules.HostDBEntry, allowance modules.Allowance, txnFees types.Currency) float64 {
	// Divide by zero mitigation.
	if allowance.Hosts == 0 {
		allowance.Hosts = 1
	}
	if allowance.Period == 0 {
		allowance.Period = 1
	}
	if allowance.ExpectedStorage == 0 {
		allowance.ExpectedStorage = 1
	}
	if allowance.ExpectedUpload == 0 {
		allowance.ExpectedUpload = 1
	}
	if allowance.ExpectedDownload == 0 {
		allowance.ExpectedDownload = 1
	}
	if allowance.ExpectedRedundancy == 0 {
		allowance.ExpectedRedundancy = 1
	}

	// Convert each element of the allowance into a number of resources that we
	// expect to use in this contract.
	contractExpectedDownload := types.NewCurrency64(allowance.ExpectedDownload).Mul64(uint64(allowance.Period)).Div64(allowance.Hosts)
	contractExpectedFunds := allowance.Funds.Div64(allowance.Hosts)
	contractExpectedStorage := uint64(float64(allowance.ExpectedStorage) * allowance.ExpectedRedundancy / float64(allowance.Hosts))
	contractExpectedStorageTime := types.NewCurrency64(contractExpectedStorage).Mul64(uint64(allowance.Period))
	contractExpectedUpload := types.NewCurrency64(allowance.ExpectedUpload).Mul64(uint64(allowance.Period)).MulFloat(allowance.ExpectedRedundancy).Div64(allowance.Hosts)

	// Calculate the hostCollateral the renter would expect the host to put
	// into a contract.
	//
	_, _, hostCollateral, err := modules.RenterPayoutsPreTax(entry, contractExpectedFunds, txnFees, types.ZeroCurrency, types.ZeroCurrency, allowance.Period, contractExpectedStorage)
	if err != nil {
		info := fmt.Sprintf("Error while estimating collateral for host: Host %v, ContractPrice %v, TxnFees %v, Funds %v",
			entry.PublicKey.String(), entry.ContractPrice.HumanString(), txnFees.HumanString(), allowance.Funds.HumanString())
		hdb.log.Debugln(errors.AddContext(err, info))
		return 0
	}

	// Determine the pricing for each type of resource in the contract. We have
	// already converted the resources into absolute terms for this contract.
	//
	// The contract price and transaction fees get doubled because we expect
	// that there will be on average one early renewal per contract, due to
	// spending all of the contract's money.
	contractPrice := entry.ContractPrice.Add(txnFees).Mul64(2)
	downloadPrice := entry.DownloadBandwidthPrice.Mul(contractExpectedDownload)
	storagePrice := entry.StoragePrice.Mul(contractExpectedStorageTime)
	uploadPrice := entry.UploadBandwidthPrice.Mul(contractExpectedUpload)
	siafundFee := contractPrice.Add(hostCollateral).Add(downloadPrice).Add(storagePrice).Add(uploadPrice).MulTax()
	totalPrice := contractPrice.Add(downloadPrice).Add(storagePrice).Add(uploadPrice).Add(siafundFee)

	// Determine a cutoff for whether the total price is considered a high price
	// or a low price. This cutoff attempts to determine where the price becomes
	// insignificant.
	cutoff := contractExpectedFunds.MulFloat(priceFloor)

	// Convert the price and cutoff to floats.
	price64, _ := totalPrice.Float64()
	cutoff64, _ := cutoff.Float64()
	// If the total price is less than the cutoff, set the cutoff equal to the
	// price. This ensures that the ratio (totalPrice / cutoff) can never be
	// less than 1.
	if price64 < cutoff64 {
		cutoff64 = price64
	}

	// Check for less-than-one.
	if price64 < 1 {
		price64 = 1
	}
	if cutoff64 < 1 {
		cutoff64 = 1
	}
	// Perform this check one more time after all of the conversions, just in
	// case there was some sort of rounding error.
	if price64 < cutoff64 {
		cutoff64 = price64
	}
	ratio := price64 / cutoff64

	smallWeight := math.Pow(cutoff64, priceExponentiationSmall)
	largeWeight := math.Pow(ratio, priceExponentiationLarge)

	return 1 / (smallWeight * largeWeight)
}

// storageRemainingAdjustments adjusts the weight of the entry according to how
// much storage it has remaining.
func storageRemainingAdjustments(entry modules.HostDBEntry) float64 {
	base := float64(1)
	if entry.RemainingStorage < 100*requiredStorage {
		base = base / 2 // 2x total penalty
	}
	if entry.RemainingStorage < 80*requiredStorage {
		base = base / 2 // 4x total penalty
	}
	if entry.RemainingStorage < 40*requiredStorage {
		base = base / 2 // 8x total penalty
	}
	if entry.RemainingStorage < 20*requiredStorage {
		base = base / 2 // 16x total penalty
	}
	if entry.RemainingStorage < 15*requiredStorage {
		base = base / 2 // 32x total penalty
	}
	if entry.RemainingStorage < 10*requiredStorage {
		base = base / 2 // 64x total penalty
	}
	if entry.RemainingStorage < 5*requiredStorage {
		base = base / 2 // 128x total penalty
	}
	if entry.RemainingStorage < 3*requiredStorage {
		base = base / 2 // 256x total penalty
	}
	if entry.RemainingStorage < 2*requiredStorage {
		base = base / 2 // 512x total penalty
	}
	if entry.RemainingStorage < requiredStorage {
		base = base / 2 // 1024x total penalty
	}
	return base
}

// versionAdjustments will adjust the weight of the entry according to the siad
// version reported by the host.
func versionAdjustments(entry modules.HostDBEntry) float64 {
	base := float64(1)
	if build.VersionCmp(entry.Version, "1.5.0") < 0 {
		base = base * 0.99999 // Safety value to make sure we update the version penalties every time we update the host.
	}
	// we shouldn't use pre hardfork hosts
	if build.VersionCmp(entry.Version, "1.3.7") < 0 {
		base = math.SmallestNonzeroFloat64
	}
	return base
}

// lifetimeAdjustments will adjust the weight of the host according to the total
// amount of time that has passed since the host's original announcement.
func (hdb *HostDB) lifetimeAdjustments(entry modules.HostDBEntry) float64 {
	base := float64(1)
	if hdb.blockHeight >= entry.FirstSeen {
		age := hdb.blockHeight - entry.FirstSeen
		if age < 12000 {
			base = base * 2 / 3 // 1.5x total
		}
		if age < 6000 {
			base = base / 2 // 3x total
		}
		if age < 4000 {
			base = base / 2 // 6x total
		}
		if age < 2000 {
			base = base / 2 // 12x total
		}
		if age < 1000 {
			base = base / 3 // 36x total
		}
		if age < 576 {
			base = base / 3 // 108x total
		}
		if age < 288 {
			base = base / 3 // 324x total
		}
		if age < 144 {
			base = base / 3 // 972x total
		}
	}
	return base
}

// uptimeAdjustments penalizes the host for having poor uptime, and for being
// offline.
//
// CAUTION: The function 'updateEntry' will manually fill out two scans for a
// new host to give the host some initial uptime or downtime. Modification of
// this function needs to be made paying attention to the structure of that
// function.
func (hdb *HostDB) uptimeAdjustments(entry modules.HostDBEntry) float64 {
	// Special case: if we have scanned the host twice or fewer, don't perform
	// uptime math.
	if len(entry.ScanHistory) == 0 {
		return 0.25
	}
	if len(entry.ScanHistory) == 1 {
		if entry.ScanHistory[0].Success {
			return 0.75
		}
		return 0.25
	}
	if len(entry.ScanHistory) == 2 {
		if entry.ScanHistory[0].Success && entry.ScanHistory[1].Success {
			return 0.85
		}
		if entry.ScanHistory[0].Success || entry.ScanHistory[1].Success {
			return 0.50
		}
		return 0.05
	}

	// Compute the total measured uptime and total measured downtime for this
	// host.
	downtime := entry.HistoricDowntime
	uptime := entry.HistoricUptime
	recentTime := entry.ScanHistory[0].Timestamp
	recentSuccess := entry.ScanHistory[0].Success
	for _, scan := range entry.ScanHistory[1:] {
		if recentTime.After(scan.Timestamp) {
			if build.DEBUG {
				hdb.log.Critical("Host entry scan history not sorted.")
			} else {
				hdb.log.Print("WARNING: Host entry scan history not sorted.")
			}
			// Ignore the unsorted scan entry.
			continue
		}
		if recentSuccess {
			uptime += scan.Timestamp.Sub(recentTime)
		} else {
			downtime += scan.Timestamp.Sub(recentTime)
		}
		recentTime = scan.Timestamp
		recentSuccess = scan.Success
	}
	// Sanity check against 0 total time.
	if uptime == 0 && downtime == 0 {
		return 0.001 // Shouldn't happen.
	}

	// Compute the uptime ratio, but shift by 0.02 to acknowledge fully that
	// 98% uptime and 100% uptime is valued the same.
	uptimeRatio := float64(uptime) / float64(uptime+downtime)
	if uptimeRatio > 0.98 {
		uptimeRatio = 0.98
	}
	uptimeRatio += 0.02

	// Cap the total amount of downtime allowed based on the total number of
	// scans that have happened.
	allowedDowntime := 0.03 * float64(len(entry.ScanHistory))
	if uptimeRatio < 1-allowedDowntime {
		uptimeRatio = 1 - allowedDowntime
	}

	// Calculate the penalty for low uptime. Penalties increase extremely
	// quickly as uptime falls away from 95%.
	//
	// 100% uptime = 1
	// 98%  uptime = 1
	// 95%  uptime = 0.83
	// 90%  uptime = 0.26
	// 85%  uptime = 0.03
	// 80%  uptime = 0.001
	// 75%  uptime = 0.00001
	// 70%  uptime = 0.0000001
	exp := 200 * math.Min(1-uptimeRatio, 0.30)
	return math.Pow(uptimeRatio, exp)
}

// managedCalculateHostWeightFn creates a hosttree.WeightFunc given an Allowance.
func (hdb *HostDB) managedCalculateHostWeightFn(allowance modules.Allowance) hosttree.WeightFunc {
	// Get the txnFees.
	hdb.mu.RLock()
	txnFees := hdb.txnFees
	hdb.mu.RUnlock()
	// Create the weight function.
	return func(entry modules.HostDBEntry) hosttree.ScoreBreakdown {
		return hosttree.HostAdjustments{
			BurnAdjustment:             1,
			CollateralAdjustment:       hdb.collateralAdjustments(entry, allowance),
			InteractionAdjustment:      hdb.interactionAdjustments(entry),
			AgeAdjustment:              hdb.lifetimeAdjustments(entry),
			PriceAdjustment:            hdb.priceAdjustments(entry, allowance, txnFees),
			StorageRemainingAdjustment: storageRemainingAdjustments(entry),
			UptimeAdjustment:           hdb.uptimeAdjustments(entry),
			VersionAdjustment:          versionAdjustments(entry),
		}
	}
}

// EstimateHostScore takes a HostExternalSettings and returns the estimated
// score of that host in the hostdb, assuming no penalties for age or uptime.
func (hdb *HostDB) EstimateHostScore(entry modules.HostDBEntry, allowance modules.Allowance) modules.HostScoreBreakdown {
	return hdb.managedScoreBreakdown(entry, true, true)
}

// ScoreBreakdown provdes a detailed set of scalars and bools indicating
// elements of the host's overall score.
func (hdb *HostDB) ScoreBreakdown(entry modules.HostDBEntry) modules.HostScoreBreakdown {
	return hdb.managedScoreBreakdown(entry, false, false)
}

// managedScoreBreakdown computes the score breakdown of a host. Certain
// adjustments can be ignored.
func (hdb *HostDB) managedScoreBreakdown(entry modules.HostDBEntry, ignoreAge, ignoreUptime bool) modules.HostScoreBreakdown {
	hosts := hdb.ActiveHosts()

	// Compute the totalScore.
	hdb.mu.Lock()
	defer hdb.mu.Unlock()
	totalScore := types.Currency{}
	for _, host := range hosts {
		totalScore = totalScore.Add(hdb.weightFunc(host).Score())
	}
	// Compute the breakdown.
	return hdb.weightFunc(entry).HostScoreBreakdown(totalScore, ignoreAge, ignoreUptime)
}
