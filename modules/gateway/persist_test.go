package gateway

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
)

// TestLoad probes loading a gateway from a persist file
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
	log, err := persist.NewLogger(&buf)
	if err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	g := &Gateway{
		nodes:      make(map[modules.NetAddress]*node),
		persistDir: filepath.Join("testdata", t.Name()),
		log:        log,
	}
	if err := g.load(); err != nil {
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

// TestLoadv135 tests that the gateway can load a v135 persist file.
func TestLoadv135(t *testing.T) {
	// Create testdir
	testDir := build.TempDir("gateway", t.Name())
	err := os.MkdirAll(testDir, persist.DefaultDiskPermissionsTest)
	if err != nil {
		t.Fatal(err)
	}

	// Copy the v135 persist file into the testdir
	v135PersistFile := filepath.Join("testdata", t.Name(), persistFilename)
	bytes, err := ioutil.ReadFile(v135PersistFile)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(testDir, persistFilename))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write(bytes)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Load a gateway from the testdata folder with the v135 persist file
	g := &Gateway{
		blocklist:  make(map[string]struct{}),
		persistDir: testDir,
	}
	if err := g.load(); err != nil {
		t.Fatal(err)
	}

	// Blocklist should have 1 IP in it
	if len(g.blocklist) != 1 {
		t.Fatalf("Expected %v ip in the blocklist but found %v", 1, len(g.blocklist))
	}
}
