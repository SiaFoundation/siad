package host

import (
	"encoding/json"
	"net"
	"reflect"
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
	pt := modules.RPCPriceTable{
		Expiry:               time.Now().Add(1).Unix(),
		UpdatePriceTableCost: types.SiacoinPrecision,
		InitBaseCost:         types.SiacoinPrecision,
		MemoryTimeCost:       types.SiacoinPrecision,
		ReadBaseCost:         types.SiacoinPrecision,
		ReadLengthCost:       types.SiacoinPrecision,
	}

	bytes, err := json.Marshal(pt)
	if err != nil {
		t.Fatal("Failed to marshal RPC price table", err)
	}

	var ptUmar modules.RPCPriceTable
	err = json.Unmarshal(bytes, &ptUmar)
	if err != nil {
		t.Fatal("Failed to unmarshal RPC price table", err)
	}

	if !reflect.DeepEqual(pt, ptUmar) {
		t.Log("expected:", pt)
		t.Log("actual:", ptUmar)
		t.Fatal("Unmarshaled table doesn't match expected one")
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

	ptc := pt.UpdatePriceTableCost
	if ptc.Equals(types.ZeroCurrency) {
		t.Log(ptc)
		t.Fatal("Expected the cost of the updatePriceTableRPC to be set")
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
