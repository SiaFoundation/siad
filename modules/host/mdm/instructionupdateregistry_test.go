package mdm

import (
	"encoding/binary"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/host/registry"
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
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	// Assert output.
	output := outputs[0]
	err = output.assert(0, crypto.Hash{}, []crypto.Hash{}, []byte{}, nil)
	if err != nil {
		t.Fatal(err)
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

	// Execute it again. This should fail with ErrSameRevNum.
	outputs, err = mdm.ExecuteProgramWithBuilder(tb, so, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	// Construct expected output.
	revBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(revBytes, rv.Revision)
	expectedOutput := append(rv.Signature[:], append(revBytes, rv.Data...)...)
	// Assert output.
	output = outputs[0]
	err = output.assert(0, crypto.Hash{}, []crypto.Hash{}, expectedOutput, registry.ErrSameRevNum)
	if err != nil {
		t.Fatal(err)
	}

	// Update the revision to 1. This should work.
	tb = newTestProgramBuilder(pt, 0)
	rv.Revision++
	rv = rv.Sign(sk)
	tb.AddUpdateRegistryInstruction(spk, rv)
	outputs, err = mdm.ExecuteProgramWithBuilder(tb, so, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	// Assert output.
	output = outputs[0]
	err = output.assert(0, crypto.Hash{}, []crypto.Hash{}, []byte{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Registry should contain correct value.
	rv2, ok = host.RegistryGet(spk, rv.Tweak)
	if !ok {
		t.Fatal("registry doesn't contain entry")
	}
	if err := rv2.Verify(pk); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rv2, rv) {
		t.Fatal("registry returned wrong data")
	}

	// Update the revision to 0. This should fail again but provide the right
	// proof.
	tb = newTestProgramBuilder(pt, 0)
	rv.Revision = 0
	rv = rv.Sign(sk)
	tb.AddUpdateRegistryInstruction(spk, rv)
	outputs, err = mdm.ExecuteProgramWithBuilder(tb, so, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	// Construct expected output.
	revBytes = make([]byte, 8)
	binary.LittleEndian.PutUint64(revBytes, rv2.Revision)
	expectedOutput = append(rv2.Signature[:], append(revBytes, rv2.Data...)...)
	// Assert output.
	output = outputs[0]
	err = output.assert(0, crypto.Hash{}, []crypto.Hash{}, expectedOutput, registry.ErrLowerRevNum)
	if err != nil {
		t.Fatal(err)
	}
}
