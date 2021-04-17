package mdm

import (
	"bytes"
	"encoding/binary"
	"testing"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestInstructionReadRegistryEID tests the ReadRegistryEID instruction.
func TestInstructionReadRegistryEID(t *testing.T) {
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
	rv := modules.NewRegistryValue(tweak, data, rev).Sign(sk)
	_, err := host.RegistryUpdate(rv, spk, types.BlockHeight(fastrand.Uint64n(1000)))
	if err != nil {
		t.Fatal(err)
	}

	so := host.newTestStorageObligation(true)
	pt := newTestPriceTable()
	tb := newTestProgramBuilder(pt, 0)
	tb.AddReadRegistryEIDInstruction(modules.DeriveRegistryEntryID(spk, tweak), false, true)

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
	rv2 := modules.NewSignedRegistryValue(tweak2, data2, rev2, sig2)
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
	refund := tb.AddReadRegistryEIDInstruction(modules.DeriveRegistryEntryID(spk, crypto.Hash{}), true, true)

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
	rv := modules.NewRegistryValue(tweak, data, rev).Sign(sk)
	_, err := host.RegistryUpdate(rv, spk, types.BlockHeight(fastrand.Uint64n(1000)))
	if err != nil {
		t.Fatal(err)
	}

	so := host.newTestStorageObligation(true)
	pt := newTestPriceTable()
	tb := newTestProgramBuilder(pt, 0)
	tb.AddReadRegistryEIDInstruction(modules.DeriveRegistryEntryID(spk, tweak), false, false)

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
	rv2 := modules.NewSignedRegistryValue(tweak, data2, rev2, sig2)
	if err := rv2.Verify(pk); err != nil {
		t.Fatal("verification failed", err)
	}
}
