package proto

import (
	"bytes"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestEphemeralRenterSeed tests the ephemeralRenterSeed methods.
func TestEphemeralRenterSeed(t *testing.T) {
	// Create random wallet seed.
	var walletSeed modules.Seed
	fastrand.Read(walletSeed[:])

	// Test for blockheights 0 to ephemeralSeedInterval-1
	for bh := types.BlockHeight(0); bh < ephemeralSeedInterval; bh++ {
		expectedSeed := crypto.HashAll(walletSeed, renterSeedSpecifier, 0)
		seed := EphemeralRenterSeed(walletSeed, bh)
		if !bytes.Equal(expectedSeed[:], seed[:]) {
			t.Fatal("Seeds don't match for blockheight", bh)
		}
	}
	// Test for blockheights ephemeralSeedInterval to 2*ephemeralSeedInterval-1
	for bh := ephemeralSeedInterval; bh < 2*ephemeralSeedInterval; bh++ {
		expectedSeed := crypto.HashAll(walletSeed, renterSeedSpecifier, 1)
		seed := EphemeralRenterSeed(walletSeed, bh)
		if !bytes.Equal(expectedSeed[:], seed[:]) {
			t.Fatal("Seeds don't match for blockheight", bh)
		}
	}
}
