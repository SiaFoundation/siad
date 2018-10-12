package hosttree

import (
	"math/big"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// ScoreBreakdown is an interface that allows us to mock the hostAdjustments
// during testing.
type ScoreBreakdown interface {
	HostScoreBreakdown(totalScore types.Currency, ignoreAge, ignoreUptime bool) modules.HostScoreBreakdown
	Score() types.Currency
}

// HostAdjustments contains all the adjustments relevant to a host's score and
// implements the scoreBreakdown interface.
type HostAdjustments struct {
	AgeAdjustment              float64
	BurnAdjustment             float64
	CollateralAdjustment       float64
	InteractionAdjustment      float64
	PriceAdjustment            float64
	StorageRemainingAdjustment float64
	UptimeAdjustment           float64
	VersionAdjustment          float64
}

var (
	// Because most weights would otherwise be fractional, we set the base
	// weight to be very large.
	baseWeight = types.NewCurrency(new(big.Int).Exp(big.NewInt(10), big.NewInt(80), nil))
)

// conversionRate computes the likelyhood of a host with 'score' to be drawn
// from the hosttree assuming that all hosts have 'totalScore'.
func conversionRate(score, totalScore types.Currency) float64 {
	if totalScore.IsZero() {
		totalScore = types.NewCurrency64(1)
	}
	conversionRate, _ := big.NewRat(0, 1).SetFrac(score.Mul64(50).Big(), totalScore.Big()).Float64()
	if conversionRate > 100 {
		conversionRate = 100
	}
	return conversionRate
}

// HostScoreBreakdown converts a HostAdjustments object into a
// modules.HostScoreBreakdown.
func (h HostAdjustments) HostScoreBreakdown(totalScore types.Currency, ignoreAge, ignoreUptime bool) modules.HostScoreBreakdown {
	// Set the ignored fields to 1.
	if ignoreAge {
		h.AgeAdjustment = 1.0
	}
	if ignoreUptime {
		h.UptimeAdjustment = 1.0
	}
	// Create the breakdown.
	score := h.Score()
	return modules.HostScoreBreakdown{
		Score:          score,
		ConversionRate: conversionRate(score, totalScore),

		AgeAdjustment:              h.AgeAdjustment,
		BurnAdjustment:             h.BurnAdjustment,
		CollateralAdjustment:       h.CollateralAdjustment,
		InteractionAdjustment:      h.InteractionAdjustment,
		PriceAdjustment:            h.PriceAdjustment,
		StorageRemainingAdjustment: h.StorageRemainingAdjustment,
		UptimeAdjustment:           h.UptimeAdjustment,
		VersionAdjustment:          h.VersionAdjustment,
	}
}

// Score combines the individual adjustments of the breakdown into a single
// score.
func (h HostAdjustments) Score() types.Currency {
	// Combine the adjustments.
	fullPenalty := h.BurnAdjustment * h.CollateralAdjustment * h.InteractionAdjustment * h.AgeAdjustment *
		h.PriceAdjustment * h.StorageRemainingAdjustment * h.UptimeAdjustment * h.VersionAdjustment

	// Return a types.Currency.
	weight := baseWeight.MulFloat(fullPenalty)
	if weight.IsZero() {
		// A weight of zero is problematic for for the host tree.
		return types.NewCurrency64(1)
	}
	return weight
}
