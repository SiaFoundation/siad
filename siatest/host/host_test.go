package host

import (
	"bytes"
	"testing"

	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

func TestHostGet(t *testing.T) {
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

	hg, err := testNode.HostGet()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(hg.PublicKey.Key, []byte{}) {
		t.Fatal("Host has empty pubkey key", hg.PublicKey.Key)
	}
}
