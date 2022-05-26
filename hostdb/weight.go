package hostdb

import (
	"math"
	"math/big"
	"sort"
	"strings"
	"time"

	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/types"
)

// AgeWeight calculates a weight based on the "age" of a host, i.e. the amount of
// time since its pubkey was first announced.
func AgeWeight(firstAnnouncement time.Time) float64 {
	const day = 24 * time.Hour
	weights := []struct {
		age    time.Duration
		factor float64
	}{
		{128 * day, 1.5},
		{64 * day, 2},
		{32 * day, 2},
		{16 * day, 2},
		{8 * day, 3},
		{4 * day, 3},
		{2 * day, 3},
		{1 * day, 3},
	}

	age := time.Since(firstAnnouncement)
	weight := 1.0
	for _, w := range weights {
		if age >= w.age {
			break
		}
		weight /= w.factor
	}
	return weight
}

// CollateralWeight calculates a weight based on the amount of collateral
// offered by a host, in relation to the expected usage of that host.
func CollateralWeight(settings rhp.HostSettings, fundsPerHost types.Currency, storage, duration uint64) float64 {
	// NOTE: This math is copied directly from the old siad hostdb. It would
	// probably benefit from a thorough review.

	contractCollateral := settings.Collateral.Mul64(storage).Mul64(duration)
	if maxCollateral := settings.MaxCollateral.Div64(2); contractCollateral.Cmp(maxCollateral) > 0 {
		contractCollateral = maxCollateral
	}
	collateral, _ := new(big.Rat).SetInt(contractCollateral.Big()).Float64()
	cutoff, _ := new(big.Rat).SetInt(fundsPerHost.Div64(5).Big()).Float64()
	collateral = math.Max(1, collateral)               // 1 <= collateral
	cutoff = math.Min(math.Max(1, cutoff), collateral) // 1 <= cutoff <= collateral
	return math.Pow(cutoff, 4) * math.Pow(collateral/cutoff, 0.5)
}

// InteractionWeight calculates a weight based on the number of successful and
// failed interactions with the host.
func InteractionWeight(his []Interaction) float64 {
	// NOTE: the old siad hostdb uses a baseline success rate of 30 to 1, and an
	// exponent of 10, both of which we replicate here

	success, fail := 30.0, 1.0
	for _, hi := range his {
		if hi.Success {
			success++
		} else {
			fail++
		}
	}
	return math.Pow(success/(success+fail), 10)
}

// UptimeWeight calculates a weight based on the host's uptime. The interactions
// must be sorted oldest-to-newest.
func UptimeWeight(his []Interaction) float64 {
	if !sort.SliceIsSorted(his, func(i, j int) bool {
		return his[i].Timestamp.Before(his[j].Timestamp)
	}) {
		panic("UptimeWeight: interactions not sorted")
	}

	// special cases
	switch len(his) {
	case 0:
		return 0.25
	case 1:
		if his[0].Success {
			return 0.75
		}
		return 0.25
	case 2:
		if his[0].Success && his[1].Success {
			return 0.85
		} else if his[0].Success || his[1].Success {
			return 0.50
		}
		return 0.05
	}

	// compute the total uptime and downtime by assuming that a host's
	// online/offline state persists in the interval between each interaction
	var downtime, uptime time.Duration
	for i := 1; i < len(his); i++ {
		prev, cur := his[i-1], his[i]
		interval := cur.Timestamp.Sub(prev.Timestamp)
		if prev.Success {
			uptime += interval
		} else {
			downtime += interval
		}
	}
	// account for the interval between the most recent interaction and the
	// current time
	finalInterval := time.Since(his[len(his)-1].Timestamp)
	if his[len(his)-1].Success {
		uptime += finalInterval
	} else {
		downtime += finalInterval
	}
	ratio := float64(uptime) / float64(uptime+downtime)

	// unconditionally forgive up to 2% downtime
	if ratio >= 0.98 {
		ratio = 1
	}

	// forgive downtime inversely proportional to the number of interactions;
	// e.g. if we have only interacted 4 times, and half of the interactions
	// failed, assume a ratio of 88% rather than 50%
	ratio = math.Max(ratio, 1-(0.03*float64(len(his))))

	// Calculate the penalty for poor uptime. Penalties increase extremely
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
	return math.Pow(ratio, 200*math.Min(1-ratio, 0.30))
}

// VersionWeight calculates a weight based on the host's RHP version.
func VersionWeight(version string) float64 {
	if !strings.HasPrefix(version, "2") {
		return 0
	}

	switch version {
	case "2.0.0":
		return 1.0
	}

	// If the version is unrecognized, it's probably because a new version has
	// been released that we don't know about. To avoid disincentivizing hosts
	// from upgrading, we return a perfect score in this case. Note that this
	// means a malicious host can get a perfect score by claiming an
	// illegitimate version like 2.99.99 -- but if they're going to lie, they
	// might as well claim to be on the latest (legitimate) version, and there's
	// nothing we can do about that.
	return 1.0
}
