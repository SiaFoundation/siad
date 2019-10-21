package host

import (
	"bytes"
	"testing"

	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

// TestHostGetPubKey confirms that the pubkey is returned through the API
func TestHostGetPubKey(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Host
	testDir := hostTestDir(t.Name())

	// Create a new server
	hostParams := node.Host(testDir)
	testNode, err := siatest.NewCleanNode(hostParams)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := testNode.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Call HostGet, confirm public key is not a blank key
	hg, err := testNode.HostGet()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(hg.PublicKey.Key, []byte{}) {
		t.Fatal("Host has empty pubkey key", hg.PublicKey.Key)
	}

	// Read public key from disk and compare to value from API
	p, err := readHostPersistance(testDir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.PublicKey.Key, hg.PublicKey.Key) {
		t.Log("HostGet PubKey:", hg.PublicKey)
		t.Log("Persistance PubKey:", p.PublicKey)
		t.Fatal("Publics Keys don't match")
	}
}
