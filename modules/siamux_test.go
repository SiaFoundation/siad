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

	persistDir := filepath.Join(os.TempDir(), t.Name())

	// create a new siamux, seeing as there won't be a host persistence file, it
	// will act as if this is a fresh new node and create a new key pair
	mux, err := NewSiaMux(persistDir, "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	pubKey := mux.staticPubKey
	privKey := mux.staticPrivKey
	mux.Close()

	// re-open the mux and verify it uses the same keys
	mux, err = NewSiaMux(persistDir, "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mux.staticPubKey, pubKey) {
		t.Log(mux.staticPubKey)
		t.Log(pubKey)
		t.Fatal("SiaMux's public key was different after reloading the mux")
	}
	if !bytes.Equal(mux.staticPrivKey, privKey) {
		t.Log(mux.staticPrivKey)
		t.Log(privKey)
		t.Fatal("SiaMux's private key was different after reloading the mux")
	}
	mux.Close()

	// prepare a host's persistence file with v1.4.2 and verify the mux is now
	// initialised using the host's key pair
	persistPath := filepath.Join(persistDir, HostDir, HostDir, ".json")
	sk, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	persistence := hostKeys{
		PublicKey: spk,
		SecretKey: sk,
	}
	persist.SaveJSON(v120PersistMetadata, persistence, persistPath)

	// create a new siamux
	mux, err = NewSiaMux(persistDir, "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(mux.staticPubKey, spk.Key) {
		t.Log(mux.staticPubKey)
		t.Log(spk.Key)
		t.Fatal("SiaMux's public key was not equal to the host's pubkey")
	}
	if !bytes.Equal(mux.staticPrivKey, sk[:]) {
		t.Log(mux.staticPrivKey)
		t.Log(spk.Key)
		t.Fatal("SiaMux's public key was not equal to the host's pubkey")
	}
}
