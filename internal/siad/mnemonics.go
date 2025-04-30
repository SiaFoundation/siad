package siad

import (
	"errors"
	"math/big"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	// DictionarySize specifies the size of the dictionaries that are used by
	// the mnemonics package. All dictionaries are the same length so that the
	// same []byte can be encoded into multiple languages and all results will
	// resemble eachother.
	dictionarySize = 1626
)

var (
	errEmptyInput        = errors.New("input has len 0 - not valid for conversion")
	errUnknownDictionary = errors.New("language not recognized")
	errUnknownWord       = errors.New("word not found in dictionary for given language: ")
)

// The conversion functions can be seen as changing the base of a number. A
// []byte can actually be viewed as a slice of base-256 numbers, and a []dict
// can be viewed as a slice of base-1626 numbers. The conversions are a little
// strange because leading 0's need to be preserved.
//
// For example, in base 256:
//
//		{0} -> 0
//		{255} -> 255
//		{0, 0} -> 256
//		{1, 0} -> 257
//		{0, 1} -> 512
//
// Every possible []byte has a unique big.Int which represents it, and every
// big.Int represents a unique []byte.

// bytesToInt converts a byte slice to a big.Int in a way that preserves
// leading 0s, and ensures there is a perfect 1:1 mapping between Int's and
// []byte's.
func bytesToInt(bs *[32]byte) *big.Int {
	base := big.NewInt(256)
	exp := big.NewInt(1)
	result := big.NewInt(-1)
	for i := 0; i < len(bs); i++ {
		tmp := big.NewInt(int64(bs[i]))
		tmp.Add(tmp, big.NewInt(1))
		tmp.Mul(tmp, exp)
		exp.Mul(exp, base)
		result.Add(result, tmp)
	}
	return result
}

// intToBytes conversts a big.Int to a []byte, following the conventions
// documented at bytesToInt.
func intToBytes(bi *big.Int) (bs []byte) {
	base := big.NewInt(256)
	for bi.Cmp(base) >= 0 {
		i := new(big.Int).Mod(bi, base).Int64()
		bs = append(bs, byte(i))
		bi.Sub(bi, base)
		bi.Div(bi, base)
	}
	bs = append(bs, byte(bi.Int64()))
	return bs
}

// phraseToInt coverts a phrase into a big.Int, using logic similar to
// bytesToInt.
func phraseToInt(p string) (*big.Int, error) {
	const prefixLen = 3

	base := big.NewInt(1626)
	exp := big.NewInt(1)
	result := big.NewInt(-1)
	for _, word := range strings.Fields(p) {
		// Normalize the input.
		word = norm.NFC.String(word)

		// Get the first prefixLen runes from the string.
		var prefix []byte
		var runeCount int
		for _, r := range word {
			encR := make([]byte, utf8.RuneLen(r))
			utf8.EncodeRune(encR, r)
			prefix = append(prefix, encR...)

			runeCount++
			if runeCount == prefixLen {
				break
			}
		}

		// Find the index associated with the phrase.
		var tmp *big.Int
		found := false
		for j, word := range dict {
			if strings.HasPrefix(word, string(prefix)) {
				tmp = big.NewInt(int64(j))
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New(errUnknownWord.Error() + word)
		}

		// Add the index to the int.
		tmp.Add(tmp, big.NewInt(1))
		tmp.Mul(tmp, exp)
		exp.Mul(exp, base)
		result.Add(result, tmp)
	}
	return result, nil
}

// intToPhrase converts a phrase into a big.Int, working in a fashion similar
// to bytesToInt.
func intToPhrase(bi *big.Int) string {
	var words []string
	base := big.NewInt(dictionarySize)
	for bi.Cmp(base) >= 0 {
		i := new(big.Int).Mod(bi, base).Int64()
		words = append(words, dict[i])
		bi.Sub(bi, base)
		bi.Div(bi, base)
	}
	words = append(words, dict[bi.Int64()])
	return strings.Join(words, " ")
}

// SeedFromPhrase derives a 32-byte seed from the supplied 28/29 word
// siad recovery phrase.
func SeedFromPhrase(seed *[32]byte, phrase string) error {
	b, err := phraseToInt(phrase)
	if err != nil {
		return err
	}
	bs := intToBytes(b)
	copy(seed[:], bs)
	return nil
}
