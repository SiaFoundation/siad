package mdm

import (
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestInstructionUpdateRegistry tests the update registry instruction.
func TestInstructionUpdateRegistry(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Create a program to update a registry value that doesn't exist yet.
	sk, pk := crypto.GenerateKeyPair()
	tweak := crypto.Hash{1, 2, 3}
	data := fastrand.Bytes(modules.RegistryDataSize)
	rev := uint64(0)
	rv := modules.NewRegistryValue(tweak, data, rev).Sign(sk)
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	pt := newTestPriceTable()
	tb := newTestProgramBuilder(pt, 0)
	tb.AddUpdateRegistryInstruction(spk, rv)

	// Execute it.
	so := host.newTestStorageObligation(true)
	finalizeFn, budget, outputs, err := mdm.ExecuteProgramWithBuilderManualFinalize(tb, so, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	// Assert the outputs.
	for _, output := range outputs {
		err = output.assert(0, crypto.Hash{}, []crypto.Hash{}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	// Finalize the program.
	if finalizeFn != nil {
		t.Fatal("registry updates don't require finalizing")
	}

	// Budget should be empty now.
	if !budget.Remaining().IsZero() {
		t.Fatal("budget wasn't completely depleted")
	}
	// Registry should contain correct value.
	rv2, ok := host.RegistryGet(spk, rv.Tweak)
	if !ok {
		t.Fatal("registry doesn't contain entry")
	}
	if err := rv2.Verify(pk); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rv2, rv) {
		t.Fatal("registry returned wrong data")
	}
}
