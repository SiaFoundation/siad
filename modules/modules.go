// Package modules contains definitions for all of the major modules of Sia, as
// well as some helper functions for performing actions that are common to
// multiple modules.
package modules

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/types"
)

var (
	// SafeMutexDelay is the recommended timeout for the deadlock detecting
	// mutex. This value is DEPRECATED, as safe mutexes are no longer
	// recommended. Instead, the locking conventions should be followed and a
	// traditional mutex or a demote mutex should be used.
	SafeMutexDelay time.Duration
)

func init() {
	if build.Release == "dev" {
		SafeMutexDelay = 60 * time.Second
	} else if build.Release == "standard" || build.Release == "testnet" {
		SafeMutexDelay = 90 * time.Second
	} else if build.Release == "testing" {
		SafeMutexDelay = 30 * time.Second
	}
}

// AddCommas produces a string form of the given number in base 10 with commas
// after every three orders of magnitude.
//
// e.g. AddCommas(834142) -> 834,142
//
// This code was pulled from the 'humanize' package at
// github.com/dustin/go-humanize - thanks Dustin!
func AddCommas(v uint64) string {
	parts := []string{"", "", "", "", "", "", ""}
	j := len(parts) - 1

	for v > 999 {
		parts[j] = strconv.FormatUint(v%1000, 10)
		switch len(parts[j]) {
		case 2:
			parts[j] = "0" + parts[j]
		case 1:
			parts[j] = "00" + parts[j]
		}
		v = v / 1000
		j--
	}
	parts[j] = strconv.Itoa(int(v))
	return strings.Join(parts[j:], ",")
}

// BandwidthUnits takes bps (bits per second) as an argument and converts them
// into a more human-readable string with a unit.
func BandwidthUnits(bps uint64) string {
	units := []string{"Bps", "Kbps", "Mbps", "Gbps", "Tbps", "Pbps", "Ebps", "Zbps", "Ybps"}
	mag := uint64(1)
	unit := ""
	for _, unit = range units {
		if bps < 1e3*mag {
			break
		} else if unit != units[len(units)-1] {
			// don't want to perform this multiply on the last iter; that
			// would give us 1.235 Ybps instead of 1235 Ybps
			mag *= 1e3
		}
	}
	return fmt.Sprintf("%.2f %s", float64(bps)/float64(mag), unit)
}

// CurrencyUnits converts a types.Currency to a string with human-readable
// units. The unit used will be the largest unit that results in a value
// greater than 1. The value is rounded to 4 significant digits.
func CurrencyUnits(c types.Currency) string {
	pico := types.SiacoinPrecision.Div64(1e12)
	if c.Cmp(pico) < 0 {
		return c.String() + " H"
	}

	// iterate until we find a unit greater than c
	mag := pico
	unit := ""
	for _, unit = range []string{"pS", "nS", "uS", "mS", "SC", "KS", "MS", "GS", "TS"} {
		if c.Cmp(mag.Mul64(1e3)) < 0 {
			break
		} else if unit != "TS" {
			// don't want to perform this multiply on the last iter; that
			// would give us 1.235 TS instead of 1235 TS
			mag = mag.Mul64(1e3)
		}
	}

	num := new(big.Rat).SetInt(c.Big())
	denom := new(big.Rat).SetInt(mag.Big())
	res, _ := new(big.Rat).Mul(num, denom.Inv(denom)).Float64()

	return fmt.Sprintf("%.4g %s", res, unit)
}

// FilesizeUnits returns a string that displays a filesize in human-readable units.
func FilesizeUnits(size uint64) string {
	if size == 0 {
		return "0  B"
	}
	sizes := []string{" B", "KB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"}
	i := int(math.Log10(float64(size)) / 3)
	return fmt.Sprintf("%.*f %s", i, float64(size)/math.Pow10(3*i), sizes[i])
}

// PeekErr checks if a chan error has an error waiting to be returned. If it has
// it will return that error. Otherwise it returns 'nil'.
func PeekErr(errChan <-chan error) (err error) {
	select {
	case err = <-errChan:
	default:
	}
	return
}
