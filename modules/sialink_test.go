package modules

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestSialink checks that the linkformat is correctly encoding to and decoding
// from a string.
func TestSialink(t *testing.T) {
	// Create a linkdata struct that is all 0's, check that the resulting
	// sialink is 52 bytes, and check that the struct encodes and decodes
	// without problems.
	var ldMin LinkData
	sialink := ldMin.Sialink()
	if len(sialink) != 52 {
		t.Error("sialink is not 52 bytes")
	}
	var ldMinDecoded LinkData
	err := ldMinDecoded.LoadSialink(sialink)
	if err != nil {
		t.Fatal(err)
	}
	if ldMinDecoded != ldMin {
		t.Error("encoding and decoding is not symmetric")
	}

	// Create a linkdata struct that is all 1's, check that the resulting
	// sialink is 52 bytes, and check that the struct encodes and decodes
	// without problems.
	ldMax := LinkData{
		vdp:            255,
		fetchMagnitude: 255,
	}
	for i := 0; i < len(ldMax.merkleRoot); i++ {
		ldMax.merkleRoot[i] = 255
	}
	sialink = ldMax.Sialink()
	if len(sialink) != 52 {
		t.Error("sialink is not 52 bytes")
	}
	var ldMaxDecoded LinkData
	err = ldMaxDecoded.LoadSialink(sialink)
	if err != nil {
		t.Fatal(err)
	}
	if ldMaxDecoded != ldMax {
		t.Error("encoding and decoding is not symmetric")
	}

	// Try setting bad version numbers on the LinkData.
	err = ldMax.SetVersion(0)
	if err == nil {
		t.Error("should not be able to set an invalid version")
	}
	err = ldMax.SetVersion(5)
	if err == nil {
		t.Error("should not be able to set an invalid version")
	}

	// Some longer fuzzing sorts of tests below, skip for short tests.
	if testing.Short() {
		t.SkipNow()
	}
	// Create a bunch of random values and run the same test.
	for i := 0; i < 100e3; i++ {
		ldRand := LinkData{
			vdp:            uint8(fastrand.Intn(256)),
			fetchMagnitude: uint8(fastrand.Intn(256)),
			merkleRoot:     crypto.HashObject(i),
		}
		sialink = ldRand.Sialink()
		if len(sialink) != 52 {
			t.Error("sialink is not 52 bytes")
			t.Log(ldRand.String())
			t.Log(len(ldRand.String()))
			t.Log(ldRand)
		}
		var ldRandDecoded LinkData
		err = ldRandDecoded.LoadSialink(sialink)
		if err != nil {
			t.Fatal(err)
		}
		if ldRandDecoded != ldRand {
			t.Error("encoding and decoding is not symmetric")
			t.Log(ldRand.String())
			t.Log(len(ldRand.String()))
			t.Log(ldRand)
			t.Log(ldRandDecoded)
		}

		// Test the setters and getters of the LinkData when the rest of the
		// values are randomized.
		ldChanged := ldRand
		err = ldChanged.SetVersion(1)
		if err != nil {
			t.Error(err)
		}
		if ldChanged.Version() != 1 {
			t.Error("version setting and getting is incorrect")
			t.Log(ldRand)
			t.Log(ldChanged)
		}
		err = ldChanged.SetVersion(2)
		if err != nil {
			t.Error(err)
		}
		if ldChanged.Version() != 2 {
			t.Error("version setting and getting is incorrect")
			t.Log(ldRand)
			t.Log(ldChanged)
		}
		err = ldChanged.SetVersion(3)
		if err != nil {
			t.Error(err)
		}
		if ldChanged.Version() != 3 {
			t.Error("version setting and getting is incorrect")
			t.Log(ldRand)
			t.Log(ldChanged)
		}
		err = ldChanged.SetVersion(4)
		if err != nil {
			t.Error(err)
		}
		if ldChanged.Version() != 4 {
			t.Error("version setting and getting is incorrect")
			t.Log(ldRand)
			t.Log(ldChanged)
		}
		// Reset to original.
		err = ldChanged.SetVersion(ldRand.Version())
		if err != nil {
			t.Error(err)
		}
		if ldChanged != ldRand {
			t.Error("ldChanged should match ldRand after reverting version changes")
		}

		// Set and fetch a random fetch size. Ensure that fetch constraints are
		// followed correctly.
		randFetchSize := fastrand.Intn(int(SialinkMaxFetchSize)) + 1
		ldChanged.SetFetchSize(uint64(randFetchSize))
		resultFetchSize := ldChanged.FetchSize()
		if resultFetchSize < uint64(randFetchSize) {
			t.Error("FetchSize() should never return a value lower than what was submitted to SetFetchSize()", resultFetchSize, randFetchSize)
		}
		// The resulting fetch size should be no more than 16384 bytes larger
		// than the input fetch size.
		if resultFetchSize > uint64(randFetchSize)+(SialinkMaxFetchSize/256) {
			t.Error("resulting fetch size is too large!")
		}
		// Check that setting and getting the fetch size with the compressed
		// value returns the same compressed value.
		ldChanged.SetFetchSize(resultFetchSize)
		if ldChanged.FetchSize() != resultFetchSize {
			t.Error("setting and getting a fetch size is not always consistent")
		}
		// Check that resetting the fetch magnitude to the original value
		// results in the same struct - meaning that no other values were
		// incorrectly changed.
		ldChanged.SetFetchSize(ldRand.FetchSize())
		if ldChanged != ldRand {
			t.Error("resetting fetch size didn't result in original value")
			t.Log(ldRand.fetchMagnitude)
			t.Log(ldChanged.fetchMagnitude)
		}
	}

	// Try loading an arbitrary string that is too small.
	var ld LinkData
	var arb string
	for i := 0; i < encodedLinkDataSize-1; i++ {
		arb = arb + "a"
	}
	err = ld.LoadString(arb)
	if err == nil {
		t.Error("expecting error when loading string that is too small")
	}
	// Try loading a siafile that's just arbitrary/meaningless data.
	arb = arb + "a"
	err = ld.LoadString(arb)
	if err != nil {
		t.Error(err)
	}
	// Try loading a siafile that's too large.
	long := arb + "a"
	err = ld.LoadString(long)
	if err == nil {
		t.Error("expecting error when loading string that is too large")
	}
	// Try loading a blank siafile.
	blank := ""
	err = ld.LoadString(blank)
	if err == nil {
		t.Error("expecting an error when loading a blank sialink")
	}
	// Try adding some extra params after a valid siafile.
	params := arb + "&asdfasdfasdf"
	err = ld.LoadString(params)
	if err != nil {
		t.Error("should be no issues loading a sialink with params")
	}

	// Test a bunch of different packet sizes with fuzz for the set fetch size
	// function.
	fetchIncrement := uint64(SialinkMaxFetchSize / 256)
	for i := uint64(0); i < 255; i++ {
		var ld LinkData
		// Try one less byte less than i packets.
		ld.SetFetchSize((i * fetchIncrement) - 1)
		fs := ld.FetchSize()
		ld.SetFetchSize(fs)
		if ld.FetchSize() != fs {
			t.Error("inconsistency")
		}

		// Try exactly i packets.
		ld.SetFetchSize(i * fetchIncrement)
		fs = ld.FetchSize()
		ld.SetFetchSize(fs)
		if ld.FetchSize() != fs {
			t.Error("inconsistency")
		}

		// Try one more byte than i packets.
		ld.SetFetchSize((i * fetchIncrement) + 1)
		fs = ld.FetchSize()
		ld.SetFetchSize(fs)
		if ld.FetchSize() != fs {
			t.Error("inconsistency")
		}
	}
}
