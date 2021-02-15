package types

// exchangerate.go contains the exchange rate struct and related helper
// functions

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"
)

type (
	// ExchangeRate is represented as a float and an associated symbol.
	ExchangeRate struct {
		staticValue  *big.Float
		staticSymbol string
	}
)

var (
	// ErrUnexpectedFormat is the error that is returned when the exchange rate
	// is in an unexpected format.
	ErrUnexpectedFormat = errors.New("exchange rate has unexpected format")

	// ErrZeroNotAllowed is returned when attempting to set the exchange rate to
	// zero.
	ErrZeroNotAllowed = errors.New("exchange rate cannot be zero")

	// exchangeRateRegExp describes the format of an exchange rate as a regular
	// expression.
	exchangeRateRegExp = regexp.MustCompile(`^\s*([0-9.]+) ?([A-Za-z_]+)\s*$`)
)

// ParseExchangeRate parses a string into an exchange rate. It returns nil if no
// valid exchange rate could be detected. If s is not an empty string and the
// contents could not be parsed, an error will be returned that describes the
// parse error.
func ParseExchangeRate(s string) (*ExchangeRate, error) {
	if s == "" {
		return nil, nil
	}

	matches := exchangeRateRegExp.FindStringSubmatch(s)
	if len(matches) != 3 {
		return nil, ErrUnexpectedFormat
	}

	value, ok := new(big.Float).SetString(matches[1])
	if !ok {
		return nil, ErrUnexpectedFormat
	}

	if value.Sign() == 0 { // don't allow (near) zero
		return nil, ErrZeroNotAllowed
	}

	rate := &ExchangeRate{
		staticValue:  value,
		staticSymbol: matches[2],
	}
	return rate, nil
}

// ApplyAndFormat applies the exchange rate to a currency amount and formats the
// result. Assumes that c cannot be negative. The output will use two decimal
// places, expect for small values where three or four decimal places are used.
func (r *ExchangeRate) ApplyAndFormat(c Currency) string {
	// deal with zero as a special case
	if c.IsZero() {
		return fmt.Sprintf("0.00 %s", r.staticSymbol)
	}

	asRatio, _ := r.staticValue.Rat(nil)
	cRat := new(big.Rat).SetInt(c.Big())
	precisionRat := new(big.Rat).SetInt(SiacoinPrecision.Big())

	// calculate (cRat * asRatio) / precisionRat
	resultRat := new(big.Rat).Quo(new(big.Rat).Mul(cRat, asRatio), precisionRat)

	// use two digits of precision by default
	result := resultRat.FloatString(2)

	// unless the amount is very small
	if resultRat.Cmp(big.NewRat(1, 100)) == -1 {
		result = resultRat.FloatString(3)
	}
	if resultRat.Cmp(big.NewRat(1, 1000)) == -1 {
		result = resultRat.FloatString(4)
	}

	result = fmt.Sprintf("~ %s %s", result, r.staticSymbol)
	return result
}
