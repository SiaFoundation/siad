package gateway

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
)

func TestLoad(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Start new gateway
	g := newTestingGateway(t)

	// Add node and persist node and gateway
	g.mu.Lock()
	g.addNode(dummyNode)
	if err := g.saveSync(); err != nil {
		t.Fatal(err)
	}
	if err := g.saveSyncNodes(); err != nil {
		t.Fatal(err)
	}
	g.mu.Unlock()
	g.Close()

	// Start second gateway
	g2, err := New("localhost:0", false, g.persistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm node from gateway 1 is in gateway 2
	if _, ok := g2.nodes[dummyNode]; !ok {
		t.Fatal("gateway did not load old peer list:", g2.nodes)
	}

	// Confirm the persisted gateway information is the same between the two
	// gateways
	if !reflect.DeepEqual(g.persist, g2.persist) {
		t.Log("g.persit:", g.persist)
		t.Log("g2.persit:", g2.persist)
		t.Fatal("Gateway not persisted")
	}
}

// TestLoadv033 tests that the gateway can load a v033 persist file for the node
// persistence.
func TestLoadv033(t *testing.T) {
	var buf bytes.Buffer
	log := persist.NewLogger(&buf)
	buf.Reset()
	g := &Gateway{
		nodes:      make(map[modules.NetAddress]*node),
		persistDir: filepath.Join("testdata", t.Name()),
		log:        log,
	}
	if err := g.load(); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	// All nodes should have been loaded
	if len(g.nodes) != 10 {
		t.Error("expected 10 nodes, got", len(g.nodes))
	}
	// All nodes should be marked as non-outbound
	for _, node := range g.nodes {
		if node.WasOutboundPeer {
			t.Error("v033 nodes should not be marked as outbound peers")
		}
	}

	// The log should be empty
	if buf.Len() != 0 {
		t.Error("expected empty log, got", buf.String())
	}
}
