package mdm

import (
	"bytes"
	"encoding/binary"
	"testing"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestInstructionReadRegistryEID tests the ReadRegistryEID instruction.
func TestInstructionReadRegistryEID(t *testing.T) {
	t.Run("NoTypeV156", func(t *testing.T) {
		testInstructionReadRegistryEID(t, func(tb *testProgramBuilder, rid modules.RegistryEntryID) modules.ReadRegistryVersion {
			tb.AddReadRegistryEIDInstructionV156(rid, false, true)
			return modules.ReadRegistryVersionNoType
		})
	})
	t.Run("NoType", func(t *testing.T) {
		testInstructionReadRegistryEID(t, func(tb *testProgramBuilder, rid modules.RegistryEntryID) modules.ReadRegistryVersion {
			tb.AddReadRegistryEIDInstruction(rid, false, true, modules.ReadRegistryVersionNoType)
			return modules.ReadRegistryVersionNoType
		})
	})
	t.Run("WithType", func(t *testing.T) {
		testInstructionReadRegistryEID(t, func(tb *testProgramBuilder, rid modules.RegistryEntryID) modules.ReadRegistryVersion {
			tb.AddReadRegistryEIDInstruction(rid, false, true, modules.ReadRegistryVersionWithType)
			return modules.ReadRegistryVersionWithType
		})
	})
}

// TestInstructionReadRegistryEID tests the ReadRegistryEID instruction.
func testInstructionReadRegistryEID(t *testing.T, addReadRegistryEIDInstruction func(tb *testProgramBuilder, rid modules.RegistryEntryID) modules.ReadRegistryVersion) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Add a registry value for a given random key/tweak pair.
	sk, pk := crypto.GenerateKeyPair()
	var tweak crypto.Hash
	fastrand.Read(tweak[:])
	data := fastrand.Bytes(modules.RegistryDataSize)
	rev := fastrand.Uint64n(1000)
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	rv := modules.NewRegistryValue(tweak, data, rev, modules.RegistryTypeWithoutPubkey).Sign(sk)
	if fastrand.Intn(2) == 0 {
		rv.Type = modules.RegistryTypeWithPubkey
		rv = rv.Sign(sk)
	}
	_, err := host.RegistryUpdate(rv, spk, types.BlockHeight(fastrand.Uint64n(1000)))
	if err != nil {
		t.Fatal(err)
	}

	so := host.newTestStorageObligation(true)
	pt := newTestPriceTable()
	tb := newTestProgramBuilder(pt, 0)
	version := addReadRegistryEIDInstruction(tb, modules.DeriveRegistryEntryID(spk, tweak))

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	// Assert output.
	output := outputs[0]
	revBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(revBytes, rev)
	expectedOutput := append(encoding.Marshal(spk), rv.Tweak[:]...)
	expectedOutput = append(expectedOutput, rv.Signature[:]...)
	expectedOutput = append(expectedOutput, revBytes...)
	expectedOutput = append(expectedOutput, rv.Data...)
	if version == modules.ReadRegistryVersionWithType {
		expectedOutput = append(expectedOutput, byte(rv.Type))
	}
	err = output.assert(0, crypto.Hash{}, []crypto.Hash{}, expectedOutput, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the signature.
	dec := encoding.NewDecoder(bytes.NewReader(output.Output), encoding.DefaultAllocLimit)
	var spk2 types.SiaPublicKey
	var tweak2 crypto.Hash
	var sig2 crypto.Signature
	var rev2 uint64
	err = dec.DecodeAll(&spk2, &tweak2, &sig2, &rev2)
	if err != nil {
		t.Fatal(err)
	}
	data2 := make([]byte, len(output.Output))
	n, err := dec.Read(data2)
	if err != nil {
		t.Fatal(err)
	}
	data2 = data2[:n]
	if version == modules.ReadRegistryVersionWithType {
		// The last byte might be the entry type.
		if data2[len(data2)-1] != byte(rv.Type) {
			t.Fatal("wrong type")
		}
		data2 = data2[:len(data2)-1]
	}
	rv2 := modules.NewSignedRegistryValue(tweak2, data2, rev2, sig2, rv.Type)
	if err := rv2.Verify(spk2.ToPublicKey()); err != nil {
		t.Fatal("verification failed", err)
	}
}

// TestInstructionReadRegistryEIDNotFound tests the ReadRegistryEID instruction
// for when an entry isn't found.
func TestInstructionReadRegistryEIDNotFound(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Add a registry value for a given random key/tweak pair.
	_, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	so := host.newTestStorageObligation(true)
	pt := newTestPriceTable()
	tb := newTestProgramBuilder(pt, 0)
	refund := tb.AddReadRegistryEIDInstruction(modules.DeriveRegistryEntryID(spk, crypto.Hash{}), true, true, modules.ReadRegistryVersionWithType)

	// Execute it.
	outputs, remainingBudget, err := mdm.ExecuteProgramWithBuilderCustomBudget(tb, so, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if outputs[0].Error != nil {
		t.Fatal("error returned", outputs[0].Error)
	}
	if len(outputs[0].Output) != 0 {
		t.Fatal("expected empty output")
	}
	if !remainingBudget.Remaining().Equals(refund) {
		t.Fatal("remaining budget should equal refund", remainingBudget.Remaining().HumanString(), refund.HumanString())
	}
}

// TestInstructionReadRegistryEIDNoPubKeyAndTweak tests the ReadRegistryEID
// instruction with needPubkeyAndTweak set to false.
func TestInstructionReadRegistryEIDNoPubkeyAndTweak(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Add a registry value for a given random key/tweak pair.
	sk, pk := crypto.GenerateKeyPair()
	var tweak crypto.Hash
	fastrand.Read(tweak[:])
	data := fastrand.Bytes(modules.RegistryDataSize)
	rev := fastrand.Uint64n(1000)
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	rv := modules.NewRegistryValue(tweak, data, rev, modules.RegistryTypeWithoutPubkey).Sign(sk)
	_, err := host.RegistryUpdate(rv, spk, types.BlockHeight(fastrand.Uint64n(1000)))
	if err != nil {
		t.Fatal(err)
	}

	so := host.newTestStorageObligation(true)
	pt := newTestPriceTable()
	tb := newTestProgramBuilder(pt, 0)
	tb.AddReadRegistryEIDInstruction(modules.DeriveRegistryEntryID(spk, tweak), false, false, modules.ReadRegistryVersionNoType)

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	// Assert output.
	output := outputs[0]
	revBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(revBytes, rev)
	expectedOutput := rv.Signature[:]
	expectedOutput = append(expectedOutput, revBytes...)
	expectedOutput = append(expectedOutput, rv.Data...)
	err = output.assert(0, crypto.Hash{}, []crypto.Hash{}, expectedOutput, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the signature.
	dec := encoding.NewDecoder(bytes.NewReader(output.Output), encoding.DefaultAllocLimit)
	var sig2 crypto.Signature
	dec.Decode(&sig2)
	rev2 := dec.NextUint64()
	data2 := make([]byte, len(output.Output))
	n, err := dec.Read(data2)
	if err != nil {
		t.Fatal(err)
	}
	data2 = data2[:n]
	rv2 := modules.NewSignedRegistryValue(tweak, data2, rev2, sig2, rv.Type)
	if err := rv2.Verify(pk); err != nil {
		t.Fatal("verification failed", err)
	}
}
