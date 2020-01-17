package contractor

import (
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// Constants related to the contractor's alerts.
var (
	// AlertMSGWalletLockedDuringMaintenance indicates that forming/renewing a
	// contract during contract maintenance isn't possible due to a locked wallet.
	AlertMSGWalletLockedDuringMaintenance = "contractor is attempting to renew/form contracts, however the wallet is locked"

	// AlertMSGAllowanceLowFunds indicates that forming/renewing a contract during
	// contract maintenance isn't possible due to the allowance being low on
	// funds.
	AlertMSGAllowanceLowFunds = "At least one contract formation/renewal failed due to the allowance being low on funds"
)

// Constants related to contract formation parameters.
var (
	// ContractFeeFundingMulFactor is the multiplying factor for contract fees
	// to determine the funding for a new contract
	ContractFeeFundingMulFactor = uint64(10)

	// MaxInitialContractFundingDivFactor is the dividing factor for determining
	// the maximum amount of funds to put into a new contract
	MaxInitialContractFundingDivFactor = uint64(3)

	// MaxInitialContractFundingMulFactor is the multiplying factor for
	// determining the maximum amount of funds to put into a new contract
	MaxInitialContractFundingMulFactor = uint64(2)

	// MinInitialContractFundingDivFactor is the dividing factor for determining
	// the minimum amount of funds to put into a new contract
	MinInitialContractFundingDivFactor = uint64(20)

	// consecutiveRenewalsBeforeReplacement is the number of times a contract
	// attempt to be renewed before it is marked as !goodForRenew.
	consecutiveRenewalsBeforeReplacement = build.Select(build.Var{
		Dev:      types.BlockHeight(12),
		Standard: types.BlockHeight(12), // ~2h
		Testing:  types.BlockHeight(12),
	}).(types.BlockHeight)

	// fileContractMinimumFunding is the lowest percentage of an allowace (on a
	// per-contract basis) that is allowed to go into funding a contract. If the
	// allowance is 100 SC per contract (5,000 SC total for 50 contracts, or
	// 2,000 SC total for 20 contracts, etc.), then the minimum amount of funds
	// that a contract would be allowed to have is fileContractMinimumFunding *
	// 100SC.
	fileContractMinimumFunding = float64(0.15)

	// MinContractFundRenewalThreshold defines the ratio of remaining funds to
	// total contract cost below which the contractor will prematurely renew a
	// contract.
	//
	// This number is deliberately a little higher than the
	// minContractFundUploadThreshold because we want to make sure that renewals
	// will kick in before uploading stops.
	MinContractFundRenewalThreshold = float64(0.06) // 6%

	// minContractFundUploadThreshold is the percentage of contract funds
	// remaining at which the contract gets marked !GoodForUpload. The number is
	// high so that there is plenty of money available for downloading, so that
	// urgent repairs can be performed and also so that user file access is not
	// interrupted until after uploading progress is interrupted. Structuring
	// things this way essentially allows the user to experience the failure
	// mode of 'can't store additional stuff' before the user experiences the
	// failure mode of 'can't retrieve stuff already uploaded'.
	MinContractFundUploadThreshold = float64(0.05) // 5%

	// randomHostsBufferForScore defines how many extra hosts are queried when trying
	// to figure out an appropriate minimum score for the hosts that we have.
	randomHostsBufferForScore = build.Select(build.Var{
		Dev:      2,
		Standard: 50,
		Testing:  1,
	}).(int)

	// oosRetryInterval is the time we wait for a host that ran out of storage to
	// add more storage before trying to upload to it again.
	oosRetryInterval = build.Select(build.Var{
		Dev:      types.BlockHeight(types.BlocksPerHour / 2), // 30 minutes
		Standard: types.BlockHeight(types.BlocksPerWeek),     // 7 days
		Testing:  types.BlockHeight(types.BlocksPerHour * 2),
	}).(types.BlockHeight)
)

// Constants related to the safety values for when the contractor is forming
// contracts.
var (
	maxCollateral    = types.SiacoinPrecision.Mul64(1e3) // 1k SC
	maxDownloadPrice = maxStoragePrice.Mul64(3 * uint64(types.BlocksPerMonth))
	maxStoragePrice  = build.Select(build.Var{
		Dev:      types.SiacoinPrecision.Mul64(300e3).Div(modules.BlockBytesPerMonthTerabyte), // 1 order of magnitude greater
		Standard: types.SiacoinPrecision.Mul64(30e3).Div(modules.BlockBytesPerMonthTerabyte),  // 30k SC / TB / Month
		Testing:  types.SiacoinPrecision.Mul64(3e6).Div(modules.BlockBytesPerMonthTerabyte),   // 2 orders of magnitude greater
	}).(types.Currency)
	maxUploadPrice = build.Select(build.Var{
		Dev:      maxStoragePrice.Mul64(30 * uint64(types.BlocksPerMonth)),  // 1 order of magnitude greater
		Standard: maxStoragePrice.Mul64(3 * uint64(types.BlocksPerMonth)),   // 3 months of storage
		Testing:  maxStoragePrice.Mul64(300 * uint64(types.BlocksPerMonth)), // 2 orders of magnitude greater
	}).(types.Currency)

	// scoreLeewayGoodForRenew defines the factor by which a host can miss the
	// goal score for a set of hosts and still be GoodForRenew. To determine the
	// goal score, a new set of hosts is queried from the hostdb and the lowest
	// scoring among them is selected.  That score is then divided by
	// scoreLeewayGoodForRenew to get the minimum score that a host is allowed
	// to have before being marked as !GoodForRenew.
	//
	// TODO: At this point in time, this value is somewhat arbitrary and could
	// be getting set in a lot more scientific way.
	scoreLeewayGoodForRenew = types.NewCurrency64(500)

	// scoreLeewayGoodForUpload defines the factor by which a host can miss the
	// goal score for a set of hosts and still be GoodForUpload. To determine the
	// goal score, a new set of hosts is queried from the hostdb and the lowest
	// scoring among them is selected.  That score is then divided by
	// scoreLeewayGoodForUpload to get the minimum score that a host is allowed
	// to have before being marked as !GoodForUpload.
	//
	// Hosts are marked !GoodForUpload before they are marked !GoodForRenew
	// because churn can harm the health and scalability of a user's filesystem.
	// Switching away from adding new files to a host can minimize the damage of
	// using a bad host without incurring data churn.
	//
	// TODO: At this point in time, this value is somewhat arbitrary and could
	// be getting set in a lot more scientific way.
	scoreLeewayGoodForUpload = types.NewCurrency64(40)
)
