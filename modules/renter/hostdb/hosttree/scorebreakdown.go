package hosttree

import (
	"math/big"

	"gitlab.com/NebulousLabs/Sia/types"
)

// scoreBreakdown is an interface that allows us to mock the hostAdjustments
// during testing.
type scoreBreakdown interface {
	Score() types.Currency
}

// hostAdjustments contains all the adjustments relevant to a host's score and
// implements the scoreBreakdown interface.
type hostAdjustments struct {
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

// Score combines the individual adjustments of the breakdown into a single
// score.
func (h hostAdjustments) Score() types.Currency {
	// Combine the adjustments.
	fullPenalty := h.CollateralAdjustment * h.InteractionAdjustment * h.AgeAdjustment *
		h.PriceAdjustment * h.StorageRemainingAdjustment * h.UptimeAdjustment * h.VersionAdjustment

	// Return a types.Currency.
	weight := baseWeight.MulFloat(fullPenalty)
	if weight.IsZero() {
		// A weight of zero is problematic for for the host tree.
		return types.NewCurrency64(1)
	}
	return weight
}
