package host

import (
	"encoding/json"
	"io/ioutil"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/siamux/mux"
)

// TestMarshalUnmarshalRPCPriceTable tests we can properly marshal and unmarshal
// the RPC price table.
func TestMarshalUnmarshalJSONRPCPriceTable(t *testing.T) {
	pt := modules.RPCPriceTable{
		Expiry:               time.Now().Add(1).Unix(),
		UpdatePriceTableCost: types.SiacoinPrecision,
		InitBaseCost:         types.SiacoinPrecision,
		MemoryTimeCost:       types.SiacoinPrecision,
		ReadBaseCost:         types.SiacoinPrecision,
		ReadLengthCost:       types.SiacoinPrecision,
	}

	// marshal & unmarshal it
	ptMar, err := json.Marshal(pt)
	if err != nil {
		t.Fatal("Failed to marshal RPC price table", err)
	}
	var ptUmar modules.RPCPriceTable
	err = json.Unmarshal(ptMar, &ptUmar)
	if err != nil {
		t.Fatal("Failed to unmarshal RPC price table", err)
	}

	if !reflect.DeepEqual(pt, ptUmar) {
		t.Log("expected:", pt)
		t.Log("actual:", ptUmar)
		t.Fatal("Unmarshaled table doesn't match expected one")
	}
}

// TestUpdatePriceTableRPC tests the UpdatePriceTableRPC by manually calling the
// RPC handler.
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

	// setup a client and server mux
	client, server := createTestingMuxs()
	defer client.Close()
	defer server.Close()

	var atomicErrors uint64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()

		// open a new stream
		stream, err := client.NewStream()
		if err != nil {
			t.Log(err)
		}
		defer stream.Close()

		// write the rpc id
		err = encoding.WriteObject(stream, modules.RPCUpdatePriceTable)
		if err != nil {
			t.Log("Failed to write rpc id", err)
			atomic.AddUint64(&atomicErrors, 1)
			return
		}

		// read the updated RPC price table
		var update modules.RPCUpdatePriceTableResponse
		if err = modules.RPCRead(stream, &update); err != nil {
			t.Log("Failed to read updated price table from the stream", err)
			atomic.AddUint64(&atomicErrors, 1)
			return
		}

		// unmarshal the JSON into a price table
		var pt modules.RPCPriceTable
		if err = json.Unmarshal(update.PriceTableJSON, &pt); err != nil {
			t.Log("Failed to unmarshal the JSON encoded RPC price table")
			atomic.AddUint64(&atomicErrors, 1)
			return
		}

		ptc := pt.UpdatePriceTableCost
		if ptc.Equals(types.ZeroCurrency) {
			t.Log(ptc)
			t.Log("Expected the cost of the updatePriceTableRPC to be set")
			atomic.AddUint64(&atomicErrors, 1)
			return
		}

		// Note: we do not verify if the host process payment properly. This
		// would require a paymentprovider, this functionality should be tested
		// renter-side.
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()

		// wait for the incoming stream
		stream, err := server.AcceptStream()
		if err != nil {
			t.Log(err)
			atomic.AddUint64(&atomicErrors, 1)
			return
		}

		// read the rpc id
		var id types.Specifier
		encoding.ReadObject(stream, &id, 4096)

		// call the update price table RPC, we purposefully ignore the error
		// here because the client is not providing payment. This RPC call will
		// end up with a closed stream, which will end up with a payment error.
		_ = ht.host.managedRPCUpdatePriceTable(stream)

	}()
	wg.Wait()

	if atomic.LoadUint64(&atomicErrors) > 0 {
		t.Fatal("Update price table failed")
	}
}

// createTestingMuxs creates a connected pair of type Mux which has already
// completed the encryption handshake and is ready to go.
func createTestingMuxs() (clientMux, serverMux *mux.Mux) {
	// Prepare tcp connections.
	clientConn, serverConn := createTestingConns()
	// Generate server keypair.
	serverPrivKey, serverPubKey := mux.GenerateED25519KeyPair()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		clientMux, err = mux.NewClientMux(clientConn, serverPubKey, persist.NewLogger(ioutil.Discard), func() {})
		if err != nil {
			panic(err)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		serverMux, err = mux.NewServerMux(serverConn, serverPubKey, serverPrivKey, persist.NewLogger(ioutil.Discard), func() {})
		if err != nil {
			panic(err)
		}
	}()
	wg.Wait()
	return
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
