package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"math/big"
	"os"
	"strconv"
	"strings"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// errUnableToParseSize is returned when the input is unable to be parsed
	// into a file size unit
	errUnableToParseSize = errors.New("unable to parse size")

	// errUnableToParseRateLimit is returned when the input is unable to be
	// parsed into a rate limit unit
	errUnableToParseRateLimit = errors.New("unable to parse ratelimit")
)

// filesize returns a string that displays a filesize in human-readable units.
func filesizeUnits(size uint64) string {
	if size == 0 {
		return "0  B"
	}
	sizes := []string{" B", "KB", "MB", "GB", "TB", "PB", "EB", "ZB", "YB"}
	i := int(math.Log10(float64(size)) / 3)
	return fmt.Sprintf("%.*f %s", i, float64(size)/math.Pow10(3*i), sizes[i])
}

// parseFilesize converts strings of form 10GB to a size in bytes. Fractional
// sizes are truncated at the byte size.
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

	strSize = strings.ToLower(strSize)
	for _, unit := range units {
		if strings.HasSuffix(strSize, unit.suffix) {
			r, ok := new(big.Rat).SetString(strings.TrimSuffix(strSize, unit.suffix))
			if !ok {
				return "", errUnableToParseSize
			}
			r.Mul(r, new(big.Rat).SetInt(big.NewInt(unit.multiplier)))
			if !r.IsInt() {
				f, _ := r.Float64()
				return fmt.Sprintf("%d", int64(f)), nil
			}
			return r.RatString(), nil
		}
	}

	return "", errUnableToParseSize
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

	period = strings.ToLower(period)
	for _, unit := range units {
		if strings.HasSuffix(period, unit.suffix) {
			var base float64
			_, err := fmt.Sscan(strings.TrimSuffix(period, unit.suffix), &base)
			if err != nil {
				return "", errUnableToParseSize
			}
			blocks := int(base * unit.multiplier)
			return fmt.Sprint(blocks), nil
		}
	}

	return "", errUnableToParseSize
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

// parseCurrency converts a siacoin amount to base units.
func parseCurrency(amount string) (string, error) {
	units := []string{"pS", "nS", "uS", "mS", "SC", "KS", "MS", "GS", "TS"}
	for i, unit := range units {
		if strings.HasSuffix(amount, unit) {
			// scan into big.Rat
			r, ok := new(big.Rat).SetString(strings.TrimSuffix(amount, unit))
			if !ok {
				return "", errors.New("malformed amount")
			}
			// convert units
			exp := 24 + 3*(int64(i)-4)
			mag := new(big.Int).Exp(big.NewInt(10), big.NewInt(exp), nil)
			r.Mul(r, new(big.Rat).SetInt(mag))
			// r must be an integer at this point
			if !r.IsInt() {
				return "", errors.New("non-integer number of hastings")
			}
			return r.RatString(), nil
		}
	}
	// check for hastings separately
	if strings.HasSuffix(amount, "H") {
		return strings.TrimSuffix(amount, "H"), nil
	}

	return "", errors.New("amount is missing units; run 'wallet --help' for a list of units")
}

// parseRatelimit converts a ratelimit input string to an int64.
func parseRatelimit(rateLimitStr string) (int64, error) {
	// Check for 0 values signifying that the no limit is being set
	if rateLimitStr == "0" {
		return 0, nil
	}
	units := []string{"Bps", "Kbps", "Mbps", "Gbps", "Tbps"}
	for i, unit := range units {
		if !strings.HasSuffix(rateLimitStr, unit) {
			continue
		}

		// trim units and spaces
		rateLimitStr = strings.TrimSuffix(rateLimitStr, unit)
		rateLimitStr = strings.TrimSpace(rateLimitStr)

		// Check for empty string meaning only the units were provided
		if rateLimitStr == "" {
			return 0, errUnableToParseRateLimit
		}

		// convert string to float for exponation
		rateLimitFloat, err := strconv.ParseFloat(rateLimitStr, 64)
		if err != nil {
			return 0, errors.Compose(errUnableToParseRateLimit, err)
		}

		// Determine factor and convert to in64 for bps
		factor := math.Pow(float64(1e3), float64(i))
		rateLimit := int64(factor * rateLimitFloat)

		return rateLimit, nil
	}

	return 0, errUnableToParseRateLimit
}

// ratelimitUnits converts an int64 to a string with human-readable
// units. The unit used will be the largest unit that results in a value
// greater than 1. The value is rounded to 4 significant digits.
func ratelimitUnits(ratelimit int64) string {
	// Check for bps
	if ratelimit < 1e3 {
		return fmt.Sprintf("%v %s", ratelimit, "Bps")
	}
	// iterate until we find a unit greater than c
	mag := 1e3
	unit := ""
	for _, unit = range []string{"Kbps", "Mbps", "Gbps", "Tbps"} {
		if float64(ratelimit) < mag*1e3 {
			break
		} else if unit != "Tbps" {
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
