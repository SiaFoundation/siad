package modules

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"

	"gitlab.com/NebulousLabs/fastrand"
)

// TestSialinkManualExamples checks a pile of manual examples using table driven
// tests.
func TestSialinkManualExamples(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Good Examples.
	var sialinkExamples = []struct {
		offset         uint64
		length         uint64
		expectedLength uint64
	}{
		// Try a valid offset for each mode.
		{4096, 1, 4096},
		{4096 * 2, (32 * 1024) + 1, 32*1024 + 4096},
		{4096 * 4, (64 * 1024) + 1, 64*1024 + 4096*2},
		{4096 * 8, (128 * 1024) + 1, 128*1024 + 4096*4},
		{4096 * 16, (256 * 1024) + 1, 256*1024 + 4096*8},
		{4096 * 32, (512 * 1024) + 1, 512*1024 + 4096*16},
		{4096 * 64, (1024 * 1024) + 1, 1024*1024 + 4096*32},
		{4096 * 128, (2048 * 1024) + 1, 2048*1024 + 4096*64},
		// Smattering of random examples.
		{4096, 0, 4096},
		{4096 * 2, 0, 4096},
		{4096 * 3, 0, 4096},
		{4096 * 3, 4096 * 8, 4096 * 8},
		{0, 1, 4096},
		{0, 4095, 4096},
		{0, 4096, 4096},
		{0, 4097, 8192},
		{0, 8192, 8192},
		{4096 * 45, 0, 4096},
		{0, 10e3, 4096 * 3},
		{0, 33e3, 4096 * 9},
		{0, 39e3, 4096 * 10},
		{8192 * 350, 39e3, 4096 * 10},
		{0, 71 * 1024, 72 * 1024},
		{0, (32 * 1024) - 1, 32 * 1024},
		{0, 32 * 1024, 32 * 1024},
		{0, (32 * 1024) + 1, 36 * 1024},
		{0, (64 * 1024) - 1, 64 * 1024},
		{8 * 1024, (64 * 1024) - 1, 64 * 1024},
		{16 * 1024, (64 * 1024) - 1, 64 * 1024},
		{0, (64 * 1024), 64 * 1024},
		{24 * 1024, (64 * 1024), 64 * 1024},
		{56 * 1024, (64 * 1024), 64 * 1024},
		{0, (64 * 1024) + 1, 72 * 1024},
		{16 * 1024, (64 * 1024) - 1, 64 * 1024},
		{48 * 1024, (64 * 1024) - 1, 64 * 1024},
		{16 * 1024, (64 * 1024), 64 * 1024},
		{48 * 1024, (64 * 1024), 64 * 1024},
		{16 * 1024, (64 * 1024) + 1, 72 * 1024},
		{48 * 1024, (64 * 1024) + 1, 72 * 1024},
		{16 * 1024, (72 * 1024) - 1, 72 * 1024},
		{48 * 1024, (72 * 1024) - 1, 72 * 1024},
		{16 * 1024, (72 * 1024), 72 * 1024},
		{48 * 1024, (72 * 1024), 72 * 1024},
		{16 * 1024, (72 * 1024) + 1, 80 * 1024},
		{48 * 1024, (72 * 1024) + 1, 80 * 1024},
		{192 * 1024, (288 * 1024) - 1, 288 * 1024},
		{128 * 2 * 1024, 1025 * 1024, (1024 + 128) * 1024},
		{512 * 1024, 2050 * 1024, (2048 + 256) * 1024},
	}
	// Try each example.
	for _, example := range sialinkExamples {
		sl, err := NewSialinkV1(crypto.Hash{}, example.offset, example.length)
		if err != nil {
			t.Error(err)
		}
		offset, length, err := sl.OffsetAndFetchSize()
		if err != nil {
			t.Fatal(err)
		}
		if offset != example.offset {
			t.Error("bad offset:", example.offset, example.length, example.expectedLength, offset)
		}
		if length != example.expectedLength {
			t.Error("bad length:", example.offset, example.length, example.expectedLength, length)
		}
		if sl.Version() != 1 {
			t.Error("bad version:", sl.Version())
		}
	}

	// Invalid Examples.
	var badSialinkExamples = []struct {
		offset uint64
		length uint64
	}{
		// Try an invalid offset for each mode.
		{2048, 4096},
		{4096, (4096 * 8) + 1},
		{4096 * 2, (4096 * 2 * 8) + 1},
		{4096 * 4, (4096 * 4 * 8) + 1},
		{4096 * 8, (4096 * 8 * 8) + 1},
		{4096 * 16, (4096 * 16 * 8) + 1},
		{4096 * 32, (4096 * 32 * 8) + 1},
		{4096 * 64, (4096 * 64 * 8) + 1},
		// Try some invalid inputs.
		{1024 * 1024 * 3, 1024 * 1024 * 2},
	}
	// Try each example.
	for _, example := range badSialinkExamples {
		_, err := NewSialinkV1(crypto.Hash{}, example.offset, example.length)
		if err == nil {
			t.Error("expecting a failure:", example.offset, example.length)
		}
	}
}

// TestSialink checks that the linkformat is correctly encoding to and decoding
// from a string.
func TestSialink(t *testing.T) {
	// Create a linkdata struct that is all 0's, check that the resulting
	// sialink is 52 bytes, and check that the struct encodes and decodes
	// without problems.
	var slMin Sialink
	str, err := slMin.String()
	if err != nil {
		t.Fatal(err)
	}
	if len(str) != 52 {
		t.Error("sialink is not 52 bytes")
	}
	var slMinDecoded Sialink
	err = slMinDecoded.LoadString(str)
	if err != nil {
		t.Fatal(err)
	}
	if slMinDecoded != slMin {
		t.Error("encoding and decoding is not symmetric")
	}

	// Create a linkdata struct that is all 1's, check that the resulting
	// sialink is 52 bytes, and check that the struct encodes and decodes
	// without problems.
	slMax := Sialink{
		bitfield: 65535,
	}
	slMax.bitfield -= 7175 // set the final three bits to 0, and also bits 10, 11, 12 to zer oto make this a valid sialink.
	for i := 0; i < len(slMax.merkleRoot); i++ {
		slMax.merkleRoot[i] = 255
	}
	str, err = slMax.String()
	if err != nil {
		t.Fatal(err)
	}
	if len(str) != 52 {
		t.Error("str is not 52 bytes")
	}
	var slMaxDecoded Sialink
	err = slMaxDecoded.LoadString(str)
	if err != nil {
		t.Fatal(err)
	}
	if slMaxDecoded != slMax {
		t.Error("encoding and decoding is not symmetric")
	}

	// Try loading an arbitrary string that is too small.
	var sl Sialink
	var arb string
	for i := 0; i < encodedSialinkSize-1; i++ {
		arb = arb + "a"
	}
	err = sl.LoadString(arb)
	if err == nil {
		t.Error("expecting error when loading string that is too small")
	}
	// Try loading a siafile that's just arbitrary/meaningless data.
	arb = arb + "a"
	err = sl.LoadString(arb)
	if err == nil {
		t.Error("arbitrary string should not decode")
	}
	// Try loading a siafile that's too large.
	long := arb + "a"
	err = sl.LoadString(long)
	if err == nil {
		t.Error("expecting error when loading string that is too large")
	}
	// Try loading a blank siafile.
	blank := ""
	err = sl.LoadString(blank)
	if err == nil {
		t.Error("expecting an error when loading a blank sialink")
	}

	// Try giving a sialink extra params and loading that.
	slStr, err := sl.String()
	if err != nil {
		t.Fatal(err)
	}
	params := slStr + "&fdsafdsafdsa"
	err = sl.LoadString(params)
	if err != nil {
		t.Error("should be no issues loading a sialink with params")
	}
	// Add more ampersands
	params = params + "&fffffdsafdsafdsa"
	err = sl.LoadString(params)
	if err != nil {
		t.Error("should be no issues loading a sialink with params")
	}

	// Try loading a non base64 string.
	nonb64 := "sia://%" + slStr
	err = sl.LoadString(nonb64[:len(slStr)])
	if err == nil {
		t.Error("should not be able to load non base64 string")
	}

	// Try parsing a linkfile that's got a bad version.
	var slBad Sialink
	slBad.bitfield = 1
	str, err = slBad.String()
	if err == nil {
		t.Error("expecting an error when marshalling a sialink with a bad version")
	}
	_, _, err = slBad.OffsetAndFetchSize()
	if err == nil {
		t.Error("should not be able to get offset and fetch size of bad sialink")
	}
	// Try setting invalid mode bits.
	slBad.bitfield = ^uint16(0) - 3
	_, _, err = slBad.OffsetAndFetchSize()
	if err == nil {
		t.Error("should not be able to get offset and fetch size of bad sialink")
	}

	// Check the MerkleRoot() function.
	mr := crypto.HashObject("fdsa")
	sl, err = NewSialinkV1(mr, 4096, 4096)
	if err != nil {
		t.Fatal(err)
	}
	if sl.MerkleRoot() != mr {
		t.Fatal("root mismatch")
	}
}

// TestSialinkAutoExamples performs a brute force test over lots of values for
// the sialink bitfield to ensure correctness.
func TestSialinkAutoExamples(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Helper function to try some values.
	tryValues := func(offset, length, expectedLength uint64) {
		sl, err := NewSialinkV1(crypto.Hash{}, offset, length)
		if err != nil {
			t.Error(err)
		}
		offsetOut, lengthOut, err := sl.OffsetAndFetchSize()
		if err != nil {
			t.Fatal(err)
		}
		if offset != offsetOut {
			t.Error("bad offset:", offset, length, expectedLength, offsetOut)
		}
		if expectedLength != lengthOut {
			t.Error("bad length:", offset, length, expectedLength, lengthOut)
		}

		// Encode the sialink and then decode the sialink. There should be no
		// errors in doing so, and the result should equal the initial.
		str, err := sl.String()
		if err != nil {
			t.Fatal(err)
		}
		var slDecode Sialink
		err = slDecode.LoadString(str)
		if err != nil {
			t.Error(err)
		}
		if slDecode != sl {
			t.Log(sl)
			t.Error("linkdata does not maintain its fields when encoded and decoded")
		}
	}

	// Check every length in the first row. The first row must be offset by 4
	// kib.
	for i := uint64(0); i < 8; i++ {
		// Check every possible offset for each length.
		for j := uint64(0); j < 1024-i; j++ {
			// Try the edge cases. One byte into the length, one byte before the
			// end of the length, the very end of the length.
			shift := uint64(0)
			offsetAlign := uint64(4096)
			lengthAlign := uint64(4096)
			tryValues(offsetAlign*j, shift+((lengthAlign*i)+1), shift+(lengthAlign*(i+1)))
			tryValues(offsetAlign*j, shift+((lengthAlign*(i+1))-1), shift+(lengthAlign*(i+1)))
			tryValues(offsetAlign*j, shift+(lengthAlign*(i+1)), shift+(lengthAlign*(i+1)))

			// Try some random values.
			for k := uint64(0); k < 5; k++ {
				rand := uint64(fastrand.Intn(int(lengthAlign)))
				rand++                            // move range from [0, lengthAlign) to [1, lengthAlign].
				rand += shift + (lengthAlign * i) // Move range into the range being tested.
				tryValues(offsetAlign*j, rand, shift+(lengthAlign*(i+1)))
			}
		}
	}

	// The first row is a special case, a general loop can be used for the
	// remaining 7 rows.
	for r := uint64(1); r < 7; r++ {
		// Check every length in the second row.
		for i := uint64(0); i < 8; i++ {
			// Check every possible offset for each length.
			offsets := uint64(1024 >> r)
			for j := uint64(0); j < offsets-4-(i/2); j++ {
				// Try the edge cases. One byte into the length, one byte before the
				// end of the length, the very end of the length.
				shift := uint64(1 << (14 + r))
				offsetAlign := uint64(1 << (12 + r))
				lengthAlign := uint64(1 << (11 + r))
				tryValues(offsetAlign*j, shift+((lengthAlign*i)+1), shift+(lengthAlign*(i+1)))
				tryValues(offsetAlign*j, shift+((lengthAlign*(i+1))-1), shift+(lengthAlign*(i+1)))
				tryValues(offsetAlign*j, shift+(lengthAlign*(i+1)), shift+(lengthAlign*(i+1)))

				// Try some random values for the length.
				for k := uint64(0); k < 25; k++ {
					rand := uint64(fastrand.Intn(int(lengthAlign)))
					rand++                            // move range from [0, lengthAlign) to [1, lengthAlign].
					rand += shift + (lengthAlign * i) // Move range into the range being tested.
					tryValues(offsetAlign*j, rand, shift+(lengthAlign*(i+1)))
				}
			}
		}
	}
}
