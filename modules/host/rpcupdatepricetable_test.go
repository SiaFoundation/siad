package host

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/siamux"
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

// TestPruneExpiredPriceTables verifies the rpc price tables get pruned from the
// host's price table map if they have expired.
func TestPruneExpiredPriceTables(t *testing.T) {
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

	// create a test stream
	stream, err := createTestStream(ht.host)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	// call the update price table rpc
	err = ht.host.managedRPCUpdatePriceTable(stream)
	if err != nil {
		t.Fatal(err)
	}

	// verify there's at least one price table
	ht.host.mu.Lock()
	numPTs := len(ht.host.priceTableMap)
	ht.host.mu.Unlock()

	if numPTs == 0 {
		t.Fatal("Expected at least one price table to be set in the host's price table map")
	}

	// get its uuid
	var uuid types.Specifier
	for uuid = range ht.host.priceTableMap {
		break
	}

	// sleep for the duration of the price guarantee + the epxiry frequency,
	// this is the worst case of how long it can take before the price table
	// gets expired
	time.Sleep(pruneExpiredRPCPriceTableFrequency + rpcPriceGuaranteePeriod)

	// verify it was expired
	ht.host.mu.Lock()
	_, exists := ht.host.priceTableMap[uuid]
	ht.host.mu.Unlock()
	if exists {
		t.Fatal("Expected RPC price table to be pruned because it should have expired")
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
		err = modules.RPCWrite(stream, modules.RPCUpdatePriceTable)
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
		err = modules.RPCRead(stream, &id)
		if err != nil {
			t.Log(err)
			atomic.AddUint64(&atomicErrors, 1)
			return
		}

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

// createTestStream is a helper method to create a stream
func createTestStream(h *Host) (siamux.Stream, error) {
	hes := h.ExternalSettings()
	muxAddress := fmt.Sprintf("%s:%d", hes.NetAddress.Host(), hes.SiaMuxPort)
	mux := h.staticMux

	// fetch a stream from the mux
	stream, err := mux.NewStream(modules.HostSiaMuxSubscriberName, muxAddress, modules.SiaPKToMuxPK(h.publicKey))
	if err != nil {
		return nil, err
	}
	return stream, nil
}
