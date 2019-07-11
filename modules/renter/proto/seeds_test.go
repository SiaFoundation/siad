package proto

import (
	"bytes"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestEphemeralRenterSeed tests the ephemeralRenterSeed methods.
func TestEphemeralRenterSeed(t *testing.T) {
	// Create random wallet seed.
	var walletSeed modules.Seed
	fastrand.Read(walletSeed[:])

	renterSeed := DeriveRenterSeed(walletSeed)
	fastrand.Read(renterSeed[:])

	// Test for blockheights 0 to ephemeralSeedInterval-1
	for bh := types.BlockHeight(0); bh < ephemeralSeedInterval; bh++ {
		expectedSeed := crypto.HashAll(renterSeed, 0)
		seed := renterSeed.EphemeralRenterSeed(bh)
		if !bytes.Equal(expectedSeed[:], seed[:]) {
			t.Fatal("Seeds don't match for blockheight", bh)
		}
	}
	// Test for blockheights ephemeralSeedInterval to 2*ephemeralSeedInterval-1
	for bh := ephemeralSeedInterval; bh < 2*ephemeralSeedInterval; bh++ {
		expectedSeed := crypto.HashAll(renterSeed, 1)
		seed := renterSeed.EphemeralRenterSeed(bh)
		if !bytes.Equal(expectedSeed[:], seed[:]) {
			t.Fatal("Seeds don't match for blockheight", bh)
		}
	}
}
