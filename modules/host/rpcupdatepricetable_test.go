package host

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestMarshalUnmarshalRPCPriceTable tests the MarshalJSON and UnmarshalJSON
// function of the RPC price table
func TestMarshalUnmarshalJSONRPCPriceTable(t *testing.T) {
	pt := modules.NewRPCPriceTable(time.Now().Add(1).Unix())
	pt.Costs[types.NewSpecifier("RPC1")] = types.NewCurrency64(1)
	pt.Costs[types.NewSpecifier("RPC2")] = types.NewCurrency64(2)

	bytes, err := json.Marshal(pt)
	if err != nil {
		t.Fatal("Failed to marshal RPC price table", err)
	}

	var ptUmar modules.RPCPriceTable
	err = json.Unmarshal(bytes, &ptUmar)
	if err != nil {
		t.Fatal("Failed to unmarshal RPC price table", err)
	}

	if pt.Expiry != ptUmar.Expiry {
		t.Log("expected:", pt.Expiry)
		t.Log("actual:", ptUmar.Expiry)
		t.Fatal("Unexpected Expiry after marshal unmarshal")
	}

	if len(pt.Costs) != len(ptUmar.Costs) {
		t.Log("expected:", len(pt.Costs))
		t.Log("actual:", len(ptUmar.Costs))
		t.Fatal("Unexpected # of Costs after marshal unmarshal")
	}

	for r, c := range pt.Costs {
		actual, exists := ptUmar.Costs[r]
		if !exists {
			t.Log(r)
			t.Fatal("Failed to find cost of RPC after marshal unmarshal")
		}
		if !c.Equals(actual) {
			t.Log("expected:", c)
			t.Log("actual:", actual)
			t.Fatal("Unexpected cost of RPC after marshal unmarshal")
		}
	}
}

// TestUpdatePriceTableRPC verifies the update price table RPC, it does this by
// manually calling the RPC handler.
func TestUpdatePriceTableRPC(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a blank host tester
	ht, err := blankHostTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer ht.Close()

	// call the update price table RPC directly
	cc, sc := createTestingConns()
	err = ht.host.managedRPCUpdatePriceTable(sc)
	if err != nil {
		t.Fatal("Failed to update the RPC price table", err)
	}

	// read the updated RPC price table
	var update modules.RPCUpdatePriceTableResponse
	if err = encoding.ReadObject(cc, &update, modules.RPCMinLen); err != nil {
		t.Fatal("Failed to read updated price table from the stream", err)
	}

	// unmarshal the JSON into a price table
	var pt modules.RPCPriceTable
	if err = json.Unmarshal(update.PriceTableJSON, &pt); err != nil {
		t.Fatal("Failed to unmarshal the JSON encoded RPC price table")
	}

	_, exists := pt.Costs[modules.RPCUpdatePriceTable]
	if !exists {
		t.Log(pt)
		t.Fatal("Expected the cost of the updatePriceTableRPC to be defined")
	}
}

// createTestingConns is a helper method to create a pair of connected tcp
// connection ready to use.
func createTestingConns() (clientConn, serverConn net.Conn) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		serverConn, _ = ln.Accept()
		wg.Done()
	}()
	clientConn, _ = net.Dial("tcp", ln.Addr().String())
	wg.Wait()
	return
}
