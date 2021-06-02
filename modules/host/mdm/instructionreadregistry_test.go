package mdm

import (
	"encoding/binary"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestInstructionReadRegistry tests the ReadRegistry instruction.
func TestInstructionReadRegistry(t *testing.T) {
	t.Run("NoTypeV156", func(t *testing.T) {
		testInstructionReadRegistry(t, func(tb *testProgramBuilder, spk types.SiaPublicKey, tweak crypto.Hash) modules.ReadRegistryVersion {
			tb.AddReadRegistryInstructionV156(spk, tweak, false)
			return modules.ReadRegistryVersionNoType
		})
	})
	t.Run("NoType", func(t *testing.T) {
		testInstructionReadRegistry(t, func(tb *testProgramBuilder, spk types.SiaPublicKey, tweak crypto.Hash) modules.ReadRegistryVersion {
			tb.AddReadRegistryInstruction(spk, tweak, false, modules.ReadRegistryVersionNoType)
			return modules.ReadRegistryVersionNoType
		})
	})
	t.Run("WithType", func(t *testing.T) {
		testInstructionReadRegistry(t, func(tb *testProgramBuilder, spk types.SiaPublicKey, tweak crypto.Hash) modules.ReadRegistryVersion {
			tb.AddReadRegistryInstruction(spk, tweak, false, modules.ReadRegistryVersionWithType)
			return modules.ReadRegistryVersionWithType
		})
	})
}

// testInstructionReadRegistry tests the ReadRegistry instruction.
func testInstructionReadRegistry(t *testing.T, addReadRegistryInstruction func(tb *testProgramBuilder, spk types.SiaPublicKey, tweak crypto.Hash) modules.ReadRegistryVersion) {
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
	version := addReadRegistryInstruction(tb, spk, tweak)

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	// Assert output.
	output := outputs[0]
	revBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(revBytes, rev)
	expectedOutput := append(rv.Signature[:], append(revBytes, rv.Data...)...)
	if version == modules.ReadRegistryVersionWithType {
		expectedOutput = append(expectedOutput, byte(rv.Type))
	}
	err = output.assert(0, crypto.Hash{}, []crypto.Hash{}, expectedOutput, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the signature.
	var sig2 crypto.Signature
	copy(sig2[:], output.Output[:crypto.SignatureSize])
	rev2 := binary.LittleEndian.Uint64(output.Output[crypto.SignatureSize:])
	data2 := output.Output[crypto.SignatureSize+8:]
	if version == modules.ReadRegistryVersionWithType {
		// The last byte might be the entry type.
		if data2[len(data2)-1] != byte(rv.Type) {
			t.Fatal("wrong type")
		}
		data2 = data2[:len(data2)-1]
	}
	rv2 := modules.NewSignedRegistryValue(tweak, data2, rev2, sig2, rv.Type)
	if err := rv2.Verify(pk); err != nil {
		t.Log(rv)
		t.Log(rv2)
		t.Fatal("verification failed", err)
	}
}

// TestInstructionReadRegistryNotFound tests the ReadRegistry instruction for
// when an entry isn't found.
func TestInstructionReadRegistryNotFound(t *testing.T) {
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
	refund := tb.AddReadRegistryInstruction(spk, crypto.Hash{}, true, modules.ReadRegistryVersionWithType)

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
