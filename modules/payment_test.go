package modules

import (
	"bytes"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestAccountID_FromSPK tests the FromSPK method.
func TestAccountID_FromSPK(t *testing.T) {
	t.Parallel()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       fastrand.Bytes(32),
	}
	var aid AccountID
	aid.FromSPK(spk)
	if string(aid) != spk.String() {
		t.Fatalf("AccountID should be %v but was %v", spk.String(), aid)
	}
}

// TestAccountID_LoadString tests the LoadString method.
func TestAccountID_LoadString(t *testing.T) {
	t.Parallel()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       fastrand.Bytes(32),
	}
	// Load valid string
	var aid AccountID
	if err := aid.LoadString(spk.String()); err != nil {
		t.Fatal(err)
	}
	if string(aid) != spk.String() {
		t.Fatalf("AccountID should be %v but was %v", spk.String(), aid)
	}
	// Load invalid string.
	if err := aid.LoadString("invalidaccountid"); err == nil {
		t.Fatal("Expected failure")
	}
	// Load invalid specifier
	if err := aid.LoadString("specifierthatiswaytolongtobevalid:invalidaccountid"); err == nil {
		t.Fatal("Expected failure")
	}
	// Load empty string.
	if err := aid.LoadString(""); err == nil {
		t.Fatal("Expected failure")
	}
}

// TestAccountID_IsZeroAccount tests the IsZeroAccount method.
func TestAccountID_IsZeroAccount(t *testing.T) {
	t.Parallel()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       fastrand.Bytes(32),
	}
	// Load key.
	var aid AccountID
	aid.FromSPK(spk)
	// Shouldn't be zero account.
	if aid.IsZeroAccount() {
		t.Fatal("Expected 'false'")
	}
	// Check if the ZeroAccount constant returns 'true'.
	if !ZeroAccountID.IsZeroAccount() {
		t.Fatal("Expected 'true'")
	}
}

// TestAccountID_SPK tests the SPK method.
func TestAccountID_SPK(t *testing.T) {
	t.Parallel()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       fastrand.Bytes(32),
	}
	// Load key.
	var aid AccountID
	aid.FromSPK(spk)
	// Check SPK.
	if !reflect.DeepEqual(aid.SPK(), spk) {
		t.Fatal("Expected keys to be equal")
	}
}

// TestAccountID_PK tests the PK method.
func TestAccountID_PK(t *testing.T) {
	t.Parallel()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       fastrand.Bytes(32),
	}
	// Load key.
	var aid AccountID
	aid.FromSPK(spk)
	// Check PK.
	var pk crypto.PublicKey
	copy(pk[:], spk.Key)
	if !reflect.DeepEqual(aid.PK(), pk) {
		t.Fatal("Expected keys to be equal")
	}
}

// TestAccountID_MarshalSia tests the SiaMarshaler implementation.
func TestAccountID_MarshalSia(t *testing.T) {
	t.Parallel()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       fastrand.Bytes(32),
	}
	// Load key.
	var aid, aid2 AccountID
	aid.FromSPK(spk)
	// Marshal und Unmarshal
	b := encoding.Marshal(aid)
	if err := encoding.Unmarshal(b, &aid2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(aid, aid2) {
		t.Fatal("id's don't match")
	}
	// Marshal und Unmarshal zero id.
	aid = ZeroAccountID
	b = encoding.Marshal(aid)
	if err := encoding.Unmarshal(b, &aid2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(aid, aid2) {
		t.Fatal("id's don't match")
	}
	// Marshal und Unmarshal zero id.
	aid = ZeroAccountID
	b = encoding.Marshal(aid)
	if err := encoding.Unmarshal(b, &aid2); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(aid, aid2) {
		t.Fatal("id's don't match")
	}
}

// TestAccountIDCompatSiaMarshal makes sure that the persistence data of a
// SiaPublicKey matches the data of a AccountID.
func TestAccountIDCompatSiaMarhsal(t *testing.T) {
	t.Parallel()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       fastrand.Bytes(32),
	}
	// Load key.
	var aid AccountID
	aid.FromSPK(spk)
	// Marshal und Unmarshal
	b := encoding.Marshal(aid)
	b2 := encoding.Marshal(spk)
	if !bytes.Equal(b, b2) {
		t.Log(b)
		t.Log(b2)
		t.Fatal("persistence doesn't match")
	}
}
