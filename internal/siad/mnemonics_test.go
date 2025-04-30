package siad

import (
	"bytes"
	"encoding/hex"
	"testing"

	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/wallet"
)

func TestMnemonic(t *testing.T) {
	phrase := "touchy inroads aptitude perfect seventh tycoon zinger madness firm cause diode owls meant knife nuisance skirting umpire sapling reruns batch molten urchins jaded nodes"

	expectedSeed, err := hex.DecodeString("9d233ac253210d671f96a2bfb187455b88204eabd602742b09b7525460595194")
	if err != nil {
		t.Fatal(err)
	}

	expectedAddresses := map[uint64]string{
		0:                    "64be0ec80f9b55675d526e6b3d5384e2950aa1d638995ea46920ba49fb137eca8088acf8dcc9",
		4294967295:           "6191fece67324fde53e7f9e9ae50a485523e4af76988fb57306650fcfc11d480af785f3aebeb",
		18446744073709551615: "55d7eed1d48d81aa99f26833c0e594dba17c93727db913eca68a4c3a9466b4f8453f1d97dc1d",
	}

	var seed [32]byte
	if err = SeedFromPhrase(&seed, phrase); err != nil {
		t.Fatal(err)
	} else if !bytes.Equal(seed[:], expectedSeed) {
		t.Fatalf("unexpected seed: expected %x, got %x", expectedSeed, seed)
	}

	for i, expected := range expectedAddresses {
		addr := types.StandardUnlockHash(wallet.KeyFromSeed(&seed, i).PublicKey())
		if addr.String() != expected {
			t.Fatalf("unexpected address: expected %q, got %q", expected, addr.String())
		}
	}
}
