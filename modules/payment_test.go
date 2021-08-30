package modules

import (
	"bytes"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/types"
)

// TestNewAccountID tests the NewAccountID utility method.
func TestNewAccountID(t *testing.T) {
	t.Parallel()
	aid, _ := NewAccountID()

	// verify it's not the zero account
	if aid.IsZeroAccount() {
		t.Fatal("NewAccountID should not return the ZeroAccount")
	}

	// verify the account ID is valid by converting it to a SiaPublicKey
	spk := aid.SPK()
	if spk.String() != aid.spk {
		t.Fatal("expected SiaPublicKey to match account id string")
	}
}

// TestAccountID_FromSPK tests the FromSPK method.
func TestAccountID_FromSPK(t *testing.T) {
	t.Parallel()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       fastrand.Bytes(32),
	}
	var aid AccountID
	aid.FromSPK(spk)
	if aid.spk != spk.String() {
		t.Fatalf("AccountID should be %v but was %v", spk.String(), aid)
	}
	aid.FromSPK(types.SiaPublicKey{})
	if !aid.IsZeroAccount() {
		t.Fatal("AccountID should be ZeroAccount")
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
	if aid.spk != spk.String() {
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

func TestWithdrawalMessageValidate(t *testing.T) {
	t.Parallel()
	aid, sk := NewAccountID()
	sk2, _ := crypto.GenerateKeyPair()

	// verify it's not the zero account
	if aid.IsZeroAccount() {
		t.Fatal("NewAccountID should not return the ZeroAccount")
	}

	var nonce [WithdrawalNonceSize]byte
	fastrand.Read(nonce[:])

	tests := []struct {
		desc    string
		sk      crypto.SecretKey
		message WithdrawalMessage
		current types.BlockHeight
		wantErr bool
	}{
		{
			"already expired",
			sk,
			WithdrawalMessage{
				Account: aid,
				Expiry:  3,
				Nonce:   nonce,
			},
			6,
			true,
		},
		{
			"extreme future",
			sk,
			WithdrawalMessage{
				Account: aid,
				Expiry:  50,
			},
			5,
			true,
		},
		{
			"zero account",
			sk,
			WithdrawalMessage{
				Account: ZeroAccountID,
				Expiry:  10,
			},
			5,
			true,
		},
		{
			"pubkey length",
			sk,
			WithdrawalMessage{
				Account: AccountID{
					spk: aid.spk[:len(aid.spk)-2],
				},
				Expiry: 10,
			},
			5,
			true,
		},
		{
			"invalid signature",
			sk2,
			WithdrawalMessage{
				Account: aid,
				Expiry:  10,
				Amount:  types.NewCurrency64(1),
				Nonce:   nonce,
			},
			5,
			true,
		},
		{
			"valid withdrawal message",
			sk,
			WithdrawalMessage{
				Account: aid,
				Expiry:  10,
				Amount:  types.NewCurrency64(1),
				Nonce:   nonce,
			},
			5,
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			fingerprint := crypto.HashAll(tt.message)
			sig := crypto.SignHash(fingerprint, tt.sk)
			err := tt.message.Validate(tt.current, tt.current+20, fingerprint, sig)
			if (err != nil) != tt.wantErr {
				t.Fatalf("WithdrawalMessage.Validate() error = %v, wantErr %t", err, tt.wantErr)
			}
		})
	}
}
