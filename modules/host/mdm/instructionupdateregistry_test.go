package mdm

import (
	"encoding/binary"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestInstructionUpdateRegistry tests the update registry instruction with
// different types of entries.
func TestInstructionUpdateRegistry(t *testing.T) {
	t.Run("NoPubkeyV156", func(t *testing.T) {
		entryType := modules.RegistryTypeWithoutPubkey
		testInstructionUpdateRegistry(t, entryType, false, func(tb *testProgramBuilder, spk types.SiaPublicKey, rv modules.SignedRegistryValue) {
			tb.AddUpdateRegistryInstructionV156(spk, rv)
		})
	})
	t.Run("NoPubkey", func(t *testing.T) {
		entryType := modules.RegistryTypeWithoutPubkey
		testInstructionUpdateRegistry(t, entryType, true, func(tb *testProgramBuilder, spk types.SiaPublicKey, rv modules.SignedRegistryValue) {
			tb.AddUpdateRegistryInstruction(spk, rv)
		})
	})
	t.Run("WithPubkey", func(t *testing.T) {
		entryType := modules.RegistryTypeWithPubkey
		testInstructionUpdateRegistry(t, entryType, true, func(tb *testProgramBuilder, spk types.SiaPublicKey, rv modules.SignedRegistryValue) {
			tb.AddUpdateRegistryInstruction(spk, rv)
		})
	})
	t.Run("Invalid", func(t *testing.T) {
		entryType := modules.RegistryTypeInvalid
		testInstructionUpdateRegistry(t, entryType, true, func(tb *testProgramBuilder, spk types.SiaPublicKey, rv modules.SignedRegistryValue) {
			tb.AddUpdateRegistryInstruction(spk, rv)
		})
	})
}

// testInstructionUpdateRegistry tests the update registry instruction.
func testInstructionUpdateRegistry(t *testing.T, entryType modules.RegistryEntryType, expectType bool, addUpdateRegistryInstruction func(tb *testProgramBuilder, spk types.SiaPublicKey, rv modules.SignedRegistryValue)) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Create a program to update a registry value that doesn't exist yet.
	sk, pk := crypto.GenerateKeyPair()
	tweak := crypto.Hash{1, 2, 3}
	data := fastrand.Bytes(modules.RegistryDataSize)
	rev := uint64(0)
	rv := modules.NewRegistryValue(tweak, data, rev, entryType).Sign(sk)
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	pt := newTestPriceTable()
	tb := newTestProgramBuilder(pt, 0)
	addUpdateRegistryInstruction(tb, spk, rv)

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
	_, rv2, ok := host.RegistryGet(modules.DeriveRegistryEntryID(spk, rv.Tweak))
	if !ok {
		t.Fatal("registry doesn't contain entry")
	}
	err = rv2.Verify(pk)
	if entryType == modules.RegistryTypeInvalid && !errors.Contains(err, modules.ErrInvalidRegistryEntryType) {
		t.Fatal("wrong error")
	} else if entryType != modules.RegistryTypeInvalid && err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rv2, rv) {
		t.Fatal("registry returned wrong data")
	}

	if entryType == modules.RegistryTypeInvalid {
		return // test for invalid entry is over
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
	if expectType {
		expectedOutput = append(expectedOutput, byte(entryType))
	}
	// Assert output.
	output = outputs[0]
	err = output.assert(0, crypto.Hash{}, []crypto.Hash{}, expectedOutput, modules.ErrSameRevNum)
	if err != nil {
		t.Fatal(err)
	}

	// Update the revision to 1. This should work.
	tb = newTestProgramBuilder(pt, 0)
	rv.Revision++
	rv = rv.Sign(sk)
	addUpdateRegistryInstruction(tb, spk, rv)
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
	_, rv2, ok = host.RegistryGet(modules.DeriveRegistryEntryID(spk, rv.Tweak))
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
	oldRevision := rv.Revision
	rv.Revision = 0
	rv = rv.Sign(sk)
	addUpdateRegistryInstruction(tb, spk, rv)
	outputs, err = mdm.ExecuteProgramWithBuilder(tb, so, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	// Construct expected output.
	revBytes = make([]byte, 8)
	binary.LittleEndian.PutUint64(revBytes, rv2.Revision)
	expectedOutput = append(rv2.Signature[:], append(revBytes, rv2.Data...)...)
	if expectType {
		expectedOutput = append(expectedOutput, byte(entryType))
	}
	// Assert output.
	output = outputs[0]
	err = output.assert(0, crypto.Hash{}, []crypto.Hash{}, expectedOutput, modules.ErrLowerRevNum)
	if err != nil {
		t.Fatal(err)
	}

	// Update the value again but with an empty registry entry.
	tb = newTestProgramBuilder(pt, 0)
	rv.Revision = oldRevision + 1
	rv.Data = []byte{}
	rv = rv.Sign(sk)
	addUpdateRegistryInstruction(tb, spk, rv)

	// Execute it.
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
	_, rv2, ok = host.RegistryGet(modules.DeriveRegistryEntryID(spk, rv.Tweak))
	if !ok {
		t.Fatal("registry doesn't contain entry")
	}
	err = rv2.Verify(pk)
	if entryType == modules.RegistryTypeWithPubkey && !errors.Contains(err, modules.ErrRegistryEntryDataMalformed) {
		t.Fatal("wrong error", err)
	} else if entryType != modules.RegistryTypeWithPubkey && err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rv2, rv) {
		t.Fatal("registry returned wrong data")
	}
}
