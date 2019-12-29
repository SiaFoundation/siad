package modules

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestLinkFormat checks that the linkformat is correctly encoding to and
// decoding from a string.
func TestLinkFormat(t *testing.T) {
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

	// Create a bunch of random values and run the same test.
	if testing.Short() {
		t.SkipNow()
	}
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
	arb = arb + "a"
	err = ld.LoadString(arb)
	if err != nil {
		t.Error(err)
	}
	arb = arb + "a"
	err = ld.LoadString(arb)
	if err == nil {
		t.Error("expecting error when loading string that is too large")
	}
}
