package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/types"
)

var (
	// ErrParsePeriodAmount is returned when the input is unable to be parsed
	// into a period unit due to a malformed amount.
	ErrParsePeriodAmount = errors.New("malformed amount")
	// ErrParsePeriodUnits is returned when the input is unable to be parsed
	// into a period unit due to missing units.
	ErrParsePeriodUnits = errors.New("amount is missing period units")

	// ErrParseRateLimitAmount is returned when the input is unable to be parsed into
	// a rate limit unit due to a malformed amount.
	ErrParseRateLimitAmount = errors.New("malformed amount")
	// ErrParseRateLimitNoAmount is returned when the input is unable to be
	// parsed into a rate limit unit due to no amount being given.
	ErrParseRateLimitNoAmount = errors.New("amount is missing")
	// ErrParseRateLimitUnits is returned when the input is unable to be parsed
	// into a rate limit unit due to missing units.
	ErrParseRateLimitUnits = errors.New("amount is missing rate limit units")

	// ErrParseSizeAmount is returned when the input is unable to be parsed into
	// a file size unit due to a malformed amount.
	ErrParseSizeAmount = errors.New("malformed amount")
	// ErrParseSizeUnits is returned when the input is unable to be parsed into
	// a file size unit due to missing units.
	ErrParseSizeUnits = errors.New("amount is missing filesize units")

	// ErrParseTimeoutAmount is returned when the input is unable to be parsed
	// into a timeout unit due to a malformed amount.
	ErrParseTimeoutAmount = errors.New("malformed amount")
	// ErrParseTimeoutUnits is returned when the input is unable to be parsed
	// into a timeout unit due to missing units.
	ErrParseTimeoutUnits = errors.New("amount is missing timeout units")
)

// bandwidthUnit takes bps (bits per second) as an argument and converts
// them into a more human-readable string with a unit.
func bandwidthUnit(bps uint64) string {
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

// parseFilesize converts strings of form '10GB' or '10 gb' to a size in bytes.
// Fractional sizes are truncated at the byte size.
func parseFilesize(strSize string) (string, error) {
	units := []struct {
		suffix     string
		multiplier int64
	}{
		{"kb", 1e3},
		{"mb", 1e6},
		{"gb", 1e9},
		{"tb", 1e12},
		{"kib", 1 << 10},
		{"mib", 1 << 20},
		{"gib", 1 << 30},
		{"tib", 1 << 40},
		{"b", 1}, // must be after others else it'll match on them all
	}

	strSize = strings.ToLower(strings.TrimSpace(strSize))
	for _, unit := range units {
		if strings.HasSuffix(strSize, unit.suffix) {
			// Trim spaces after removing the suffix to allow spaces between the
			// value and the unit.
			value := strings.TrimSpace(strings.TrimSuffix(strSize, unit.suffix))
			r, ok := new(big.Rat).SetString(value)
			if !ok {
				return "", ErrParseSizeAmount
			}
			r.Mul(r, new(big.Rat).SetInt(big.NewInt(unit.multiplier)))
			if !r.IsInt() {
				f, _ := r.Float64()
				return fmt.Sprintf("%d", int64(f)), nil
			}
			return r.RatString(), nil
		}
	}

	return "", ErrParseSizeUnits
}

// periodUnits turns a period in terms of blocks to a number of weeks.
func periodUnits(blocks types.BlockHeight) string {
	return fmt.Sprint(blocks / 1008) // 1008 blocks per week
}

// parsePeriod converts a duration specified in blocks, hours, or weeks to a
// number of blocks.
func parsePeriod(period string) (string, error) {
	units := []struct {
		suffix     string
		multiplier float64
	}{
		{"b", 1},        // blocks
		{"block", 1},    // blocks
		{"blocks", 1},   // blocks
		{"h", 6},        // hours
		{"hour", 6},     // hours
		{"hours", 6},    // hours
		{"d", 144},      // days
		{"day", 144},    // days
		{"days", 144},   // days
		{"w", 1008},     // weeks
		{"week", 1008},  // weeks
		{"weeks", 1008}, // weeks
	}

	period = strings.ToLower(strings.TrimSpace(period))
	for _, unit := range units {
		if strings.HasSuffix(period, unit.suffix) {
			var base float64
			_, err := fmt.Sscan(strings.TrimSuffix(period, unit.suffix), &base)
			if err != nil {
				return "", ErrParsePeriodAmount
			}
			blocks := int(base * unit.multiplier)
			return fmt.Sprint(blocks), nil
		}
	}

	return "", ErrParsePeriodUnits
}

// parseTimeout converts a duration specified in seconds, hours, days or weeks
// to a number of seconds
func parseTimeout(duration string) (string, error) {
	units := []struct {
		suffix     string
		multiplier float64
	}{
		{"s", 1},          // seconds
		{"second", 1},     // seconds
		{"seconds", 1},    // seconds
		{"h", 3600},       // hours
		{"hour", 3600},    // hours
		{"hours", 3600},   // hours
		{"d", 86400},      // days
		{"day", 86400},    // days
		{"days", 86400},   // days
		{"w", 604800},     // weeks
		{"week", 604800},  // weeks
		{"weeks", 604800}, // weeks
	}

	duration = strings.ToLower(strings.TrimSpace(duration))
	for _, unit := range units {
		if strings.HasSuffix(duration, unit.suffix) {
			value := strings.TrimSpace(strings.TrimSuffix(duration, unit.suffix))
			var base float64
			_, err := fmt.Sscan(value, &base)
			if err != nil {
				return "", ErrParseTimeoutAmount
			}
			seconds := int(base * unit.multiplier)
			return fmt.Sprint(seconds), nil
		}
	}

	return "", ErrParseTimeoutUnits
}

// currencyUnits converts a types.Currency to a string with human-readable
// units. The unit used will be the largest unit that results in a value
// greater than 1. The value is rounded to 4 significant digits.
func currencyUnits(c types.Currency) string {
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

// currencyUnitsWithExchangeRate will format a types.Currency in the same way as
// currencyUnits. If a non-nil exchange rate is provided, it will additionally
// provide the result of applying the rate to the amount.
func currencyUnitsWithExchangeRate(c types.Currency, rate *types.ExchangeRate) string {
	cString := currencyUnits(c)
	if rate == nil {
		return cString
	}

	return fmt.Sprintf("%s (%s)", cString, rate.ApplyAndFormat(c))
}

// parseRatelimit converts a ratelimit input string of to an int64 representing
// the bytes per second ratelimit.
func parseRatelimit(rateLimitStr string) (int64, error) {
	// Check for 0 values signifying that the no limit is being set
	if rateLimitStr == "0" {
		return 0, nil
	}
	// Create struct of rates. Have to start at the high end so that B/s is
	// checked last, otherwise it would return false positives
	rates := []struct {
		unit   string
		factor float64
	}{
		{"TB/s", 1e12},
		{"GB/s", 1e9},
		{"MB/s", 1e6},
		{"KB/s", 1e3},
		{"B/s", 1e0},
		{"Tbps", 1e12 / 8},
		{"Gbps", 1e9 / 8},
		{"Mbps", 1e6 / 8},
		{"Kbps", 1e3 / 8},
		{"Bps", 1e0 / 8},
	}
	rateLimitStr = strings.TrimSpace(rateLimitStr)
	for _, rate := range rates {
		if !strings.HasSuffix(rateLimitStr, rate.unit) {
			continue
		}

		// trim units and spaces
		rateLimitStr = strings.TrimSuffix(rateLimitStr, rate.unit)
		rateLimitStr = strings.TrimSpace(rateLimitStr)

		// Check for empty string meaning only the units were provided
		if rateLimitStr == "" {
			return 0, ErrParseRateLimitNoAmount
		}

		// convert string to float for exponation
		rateLimitFloat, err := strconv.ParseFloat(rateLimitStr, 64)
		if err != nil {
			return 0, errors.Compose(ErrParseRateLimitAmount, err)
		}
		// Check for Bps to make sure it is greater than 8 Bps meaning that it is at
		// least 1 B/s
		if rateLimitFloat < 8 && rate.unit == "Bps" {
			return 0, errors.AddContext(ErrParseRateLimitAmount, "Bps rate limit cannot be < 8 Bps")
		}

		// Determine factor and convert to int64 for bps
		rateLimit := int64(rateLimitFloat * rate.factor)

		return rateLimit, nil
	}

	return 0, ErrParseRateLimitUnits
}

// ratelimitUnits converts an int64 to a string with human-readable ratelimit
// units. The unit used will be the largest unit that results in a value greater
// than 1. The value is rounded to 4 significant digits.
func ratelimitUnits(ratelimit int64) string {
	// Check for bps
	if ratelimit < 1e3 {
		return fmt.Sprintf("%v %s", ratelimit, "B/s")
	}
	// iterate until we find a unit greater than c
	mag := 1e3
	unit := ""
	for _, unit = range []string{"KB/s", "MB/s", "GB/s", "TB/s"} {
		if float64(ratelimit) < mag*1e3 {
			break
		} else if unit != "TB/s" {
			// don't want to perform this multiply on the last iter; that
			// would give us 1.235 tbps instead of 1235 tbps
			mag = mag * 1e3
		}
	}

	return fmt.Sprintf("%.4g %s", float64(ratelimit)/mag, unit)
}

// yesNo returns "Yes" if b is true, and "No" if b is false.
func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// parseTxn decodes a transaction from s, which can be JSON, base64, or a path
// to a file containing either encoding.
func parseTxn(s string) (types.Transaction, error) {
	// first assume s is a file
	txnBytes, err := ioutil.ReadFile(s)
	if os.IsNotExist(err) {
		// assume s is a literal encoding
		txnBytes = []byte(s)
	} else if err != nil {
		return types.Transaction{}, errors.New("could not read transaction file: " + err.Error())
	}
	// txnBytes now contains either s or the contents of the file, so it is
	// either JSON or base64
	var txn types.Transaction
	if json.Valid(txnBytes) {
		if err := json.Unmarshal(txnBytes, &txn); err != nil {
			return types.Transaction{}, errors.New("could not decode JSON transaction: " + err.Error())
		}
	} else {
		bin, err := base64.StdEncoding.DecodeString(string(txnBytes))
		if err != nil {
			return types.Transaction{}, errors.New("argument is not valid JSON, base64, or filepath")
		}
		if err := encoding.Unmarshal(bin, &txn); err != nil {
			return types.Transaction{}, errors.New("could not decode binary transaction: " + err.Error())
		}
	}
	return txn, nil
}

// fmtDuration converts a time.Duration into a days,hours,minutes string
func fmtDuration(dur time.Duration) string {
	dur = dur.Round(time.Minute)
	d := dur / time.Hour / 24
	dur -= d * time.Hour * 24
	h := dur / time.Hour
	dur -= h * time.Hour
	m := dur / time.Minute
	return fmt.Sprintf("%02d days %02d hours %02d minutes", d, h, m)
}

// parsePercentages takes a range of floats and returns them rounded to
// percentages that add up to 100. They will be returned in the same order that
// they were provided
func parsePercentages(values []float64) []float64 {
	// Create a slice of percentInfo to track information of the values in the
	// slice and calculate the subTotal of the floor values
	type percentInfo struct {
		index       int
		floorVal    float64
		originalVal float64
	}
	var percentages []*percentInfo
	var subTotal float64
	for i, v := range values {
		fv := math.Floor(v)
		percentages = append(percentages, &percentInfo{
			index:       i,
			floorVal:    fv,
			originalVal: v,
		})
		subTotal += fv
	}

	// Sanity check and regression check that all values were added. Fine to
	// continue through in production as result will only be a minor UX
	// descrepency
	if len(percentages) != len(values) {
		build.Critical("Not all values added to percentage slice; potential duplicate value error")
	}

	// Determine the difference to 100 from the subTotal of the floor values
	diff := 100 - subTotal

	// Diff should always be smaller than the number of values. Sanity check for
	// developers, fine to continue through in production as result will only be
	// a minor UX descrepency
	if int(diff) > len(values) {
		build.Critical(fmt.Errorf("Unexpected diff value %v, number of values %v", diff, len(values)))
	}

	// Sort the slice based on the size of the decimal value
	sort.Slice(percentages, func(i, j int) bool {
		_, a := math.Modf(percentages[i].originalVal)
		_, b := math.Modf(percentages[j].originalVal)
		return a > b
	})

	// Divide the diff amongst the floor values from largest decimal value to
	// the smallest to decide which values get rounded up.
	for _, pi := range percentages {
		if diff <= 0 {
			break
		}
		pi.floorVal++
		diff--
	}

	// Reorder the slice and return
	for _, pi := range percentages {
		values[pi.index] = pi.floorVal
	}

	return values
}

// sizeString converts the uint64 size to a string with appropriate units and
// truncates to 4 significant digits.
func sizeString(size uint64) string {
	sizes := []struct {
		unit   string
		factor float64
	}{
		{"EB", 1e18},
		{"PB", 1e15},
		{"TB", 1e12},
		{"GB", 1e9},
		{"MB", 1e6},
		{"KB", 1e3},
		{"B", 1e0},
	}

	// Convert size to a float
	for i, s := range sizes {
		// Check to see if we are at the right order of magnitude.
		res := float64(size) / s.factor
		if res < 1 {
			continue
		}
		// Create the string
		str := fmt.Sprintf("%.4g %s", res, s.unit)
		// Check for rounding to three 0s
		if !strings.Contains(str, "000") {
			return str
		}
		// If we are at the max unit then there is no trimming to do
		if i == 0 {
			build.Critical("input uint64 overflows uint64, shouldn't be possible")
			return str
		}
		// Trim the trailing three 0s and round to the next unit size
		return fmt.Sprintf("1 %s", sizes[i-1].unit)
	}
	return "0 B"
}
