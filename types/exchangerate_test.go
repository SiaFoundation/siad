package types

import (
	"testing"
)

// TestParseInvalidExchangeRate checks that invalid exchange rate
// expressions are rejected correctly.
func TestParseInvalidExchangeRate(t *testing.T) {
	tests := []struct {
		s   string
		err error
	}{
		{"", nil},
		{"1", ErrUnexpectedFormat},
		{"123", ErrUnexpectedFormat},
		{"USD", ErrUnexpectedFormat},
		{" USD", ErrUnexpectedFormat},
		{"USD123", ErrUnexpectedFormat},
		{"1.2.3 USD", ErrUnexpectedFormat},
		{"1,23 EUR", ErrUnexpectedFormat},
		{"-1 USD", ErrUnexpectedFormat},
		{"0 USD", ErrZeroNotAllowed},
		{"0.00 USD", ErrZeroNotAllowed},
		{"1 USD EUR", ErrUnexpectedFormat},
		{"1asdf EUR", ErrUnexpectedFormat},
		{"10 10", ErrUnexpectedFormat},
		{"1 1", ErrUnexpectedFormat},
		{"USD USD", ErrUnexpectedFormat},
	}
	for _, test := range tests {
		rate, err := ParseExchangeRate(test.s)

		if rate != nil || err != test.err {
			t.Errorf("ParseExchangeRate(%#v): expected nil %v, got %+v %v", test.s, test.err, rate, err)
		}
	}
}

// TestParseValidExchangeRate checks that valid exchange rate
// expressions are parsed correctly.
func TestParseValidExchangeRate(t *testing.T) {
	tests := []struct {
		s      string
		value  string
		symbol string
	}{
		{"1 USD", "1", "USD"},
		{"1 usd", "1", "usd"},
		{"1USD", "1", "USD"},
		{"1.23 USD", "1.23", "USD"},
		{"1.23USD", "1.23", "USD"},
		{"10 X", "10", "X"},
		{"10 x", "10", "x"},
		{"100 LONG_SYMBOL", "100", "LONG_SYMBOL"},
		{"123.456789 EUR", "123.456789", "EUR"},
		{"123.456789EUR", "123.456789", "EUR"},
		{" 1 USD", "1", "USD"},
		{"1 USD ", "1", "USD"},
		{" 1 USD ", "1", "USD"},
		{"   1 USD   ", "1", "USD"},
	}
	for _, test := range tests {
		rate, err := ParseExchangeRate(test.s)
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		} else if rate == nil {
			t.Errorf("ParseExchangeRate(%#v): expected %v %v, got nil",
				test.s, test.value, test.symbol)
		} else {
			if rate.staticValue.String() != test.value || rate.staticSymbol != test.symbol {
				t.Errorf("ParseExchangeRate(%#v): expected %v %v, got %v %v",
					test.s, test.value, test.symbol, rate.staticValue.String(), rate.staticSymbol)
			}
		}
	}
}

// TestApplyAndFormat checks that an exchange rate is correctly
// applied and formatted.
func TestApplyAndFormat(t *testing.T) {
	mustParse := func(s string) *ExchangeRate {
		rate, err := ParseExchangeRate(s)
		if err != nil {
			t.Fatalf("test case uses invalid exchange rate: %v", err)
		}

		return rate
	}
	tests := []struct {
		rate   *ExchangeRate
		c      Currency
		result string
	}{
		{mustParse("1 USD"), SiacoinPrecision, "~ 1.00 USD"},
		{mustParse("1 USD"), SiacoinPrecision.Div64(2), "~ 0.50 USD"},
		{mustParse("1 USD"), SiacoinPrecision.Mul64(10), "~ 10.00 USD"},
		{mustParse("0.12 USD"), SiacoinPrecision, "~ 0.12 USD"},
		{mustParse("0.1234 USD"), SiacoinPrecision, "~ 0.12 USD"},
		{mustParse("0.009 USD"), SiacoinPrecision, "~ 0.009 USD"},
		{mustParse("0.0094 USD"), SiacoinPrecision, "~ 0.009 USD"},
		{mustParse("0.0002 USD"), SiacoinPrecision, "~ 0.0002 USD"},
		{mustParse("0.00005 USD"), SiacoinPrecision, "~ 0.0001 USD"},
		{mustParse("0.00004 USD"), SiacoinPrecision, "~ 0.0000 USD"},
		{mustParse("0.0000001 USD"), SiacoinPrecision, "~ 0.0000 USD"},
		{mustParse("10.00004 USD"), SiacoinPrecision, "~ 10.00 USD"},
		{mustParse("0.0039 USD"), SiacoinPrecision.Mul64(1000), "~ 3.90 USD"},
		{mustParse("0.0039 USD"), SiacoinPrecision.Mul64(10000), "~ 39.00 USD"},
		{mustParse("1.23 EUR"), SiacoinPrecision, "~ 1.23 EUR"},
		{mustParse("1 EUR"), NewCurrency64(1), "~ 0.0000 EUR"},
		{mustParse("1000000000000000000000000 EUR"), NewCurrency64(1), "~ 1.00 EUR"},
		{mustParse("1 USD"), NewCurrency64(0), "0.00 USD"},
		{mustParse("9.99999 USD"), SiacoinPrecision, "~ 10.00 USD"},
		{mustParse("1.11111 USD"), SiacoinPrecision, "~ 1.11 USD"},
	}
	for _, test := range tests {
		result := test.rate.ApplyAndFormat(test.c)

		if test.result != result {
			t.Errorf("TestApplyAndFormat with %v %v: expected %#v, got %#v",
				test.rate.staticValue, test.rate.staticSymbol, test.result, result)
		}
	}
}
