package modules

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestSiaMuxCompat verifies the SiaMux is initialized in compatibility mode
// when the host's persitence metadata version is v1.4.2
func TestSiaMuxCompat(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// ensure the host's persistence file does not exist
	persistDir := filepath.Join(os.TempDir(), t.Name())
	persistPath := filepath.Join(persistDir, HostDir, settingsFile)
	os.Remove(persistPath)

	// create a new siamux, seeing as there won't be a host persistence file, it
	// will act as if this is a fresh new node and create a new key pair
	mux, err := NewSiaMux(persistDir, "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	expectedPK := mux.PublicKey()
	expectedSK := mux.PrivateKey()
	mux.Close()

	// re-open the mux and verify it uses the same keys
	mux, err = NewSiaMux(persistDir, "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	actualPK := mux.PublicKey()
	actualSK := mux.PrivateKey()
	if !bytes.Equal(actualPK[:], expectedPK[:]) {
		t.Log(actualPK)
		t.Log(expectedPK)
		t.Fatal("SiaMux's public key was different after reloading the mux")
	}
	if !bytes.Equal(actualSK[:], expectedSK[:]) {
		t.Log(actualSK)
		t.Log(expectedSK)
		t.Fatal("SiaMux's private key was different after reloading the mux")
	}
	mux.Close()

	// prepare a host's persistence file with v1.4.2 and verify the mux is now
	// initialised using the host's key pair

	// create the host directory if it doesn't exist.
	err = os.MkdirAll(filepath.Join(persistDir, HostDir), 0700)
	if err != nil {
		t.Fatal(err)
	}

	sk, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	persistence := hostKeys{
		PublicKey: spk,
		SecretKey: sk,
	}
	err = persist.SaveJSON(v120PersistMetadata, persistence, persistPath)
	if err != nil {
		t.Fatal(err)
	}

	// create a new siamux
	mux, err = NewSiaMux(persistDir, "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	actualPK = mux.PublicKey()
	actualSK = mux.PrivateKey()
	if !bytes.Equal(actualPK[:], spk.Key) {
		t.Log(actualPK)
		t.Log(spk.Key)
		t.Fatal("SiaMux's public key was not equal to the host's pubkey")
	}
	if !bytes.Equal(actualSK[:], sk[:]) {
		t.Log(mux.PrivateKey())
		t.Log(spk.Key)
		t.Fatal("SiaMux's public key was not equal to the host's pubkey")
	}
}
