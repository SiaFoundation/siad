package host

import (
	"container/heap"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
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

// TestPriceTableMinHeap verifies the working of the min heap
func TestPriceTableMinHeap(t *testing.T) {
	t.Parallel()

	now := time.Now()
	pth := priceTableHeap{heap: make([]*modules.RPCPriceTable, 0)}

	// add 4 price tables (out of order) that expire somewhere in the future
	pt1 := modules.RPCPriceTable{Expiry: now.Add(9 * time.Minute).Unix()}
	pt2 := modules.RPCPriceTable{Expiry: now.Add(-3 * time.Minute).Unix()}
	pt3 := modules.RPCPriceTable{Expiry: now.Add(-6 * time.Minute).Unix()}
	pt4 := modules.RPCPriceTable{Expiry: now.Add(-1 * time.Minute).Unix()}
	pth.Push(&pt1)
	pth.Push(&pt2)
	pth.Push(&pt3)
	pth.Push(&pt4)

	// verify it considers 3 to be expired if we pass it a threshold 7' from now
	expired := pth.PopExpired()
	if len(expired) != 3 {
		t.Fatalf("Expected 3 price tables to be expired, yet managedExpired returned %d price tables", len(expired))
	}

	// verify 'pop' returns the last remaining price table
	pth.mu.Lock()
	expectedPt1 := heap.Pop(&pth.heap)
	pth.mu.Unlock()
	if expectedPt1 != &pt1 {
		t.Fatal("Expected the last price table to be equal to pt1, which is the price table with the highest expiry")
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
	err = ht.host.rpcUpdatePriceTable(stream)
	if err != nil {
		t.Fatal(err)
	}

	// verify there's at least one price table
	ht.host.staticPriceTables.mu.RLock()
	numPTs := len(ht.host.staticPriceTables.guaranteed)
	ht.host.staticPriceTables.mu.RUnlock()
	if numPTs == 0 {
		t.Fatal("Expected at least one price table to be set in the host's price table map")
	}

	// get its uuid
	ht.host.staticPriceTables.mu.RLock()
	var uuid types.Specifier
	for uuid = range ht.host.staticPriceTables.guaranteed {
		break
	}
	ht.host.staticPriceTables.mu.RUnlock()

	// sleep for the duration of the epxiry frequency, seeing as that is greater
	// than the price guarantee period, it is the worst case
	time.Sleep(pruneExpiredRPCPriceTableFrequency)

	// verify it was expired
	ht.host.staticPriceTables.mu.RLock()
	_, exists := ht.host.staticPriceTables.guaranteed[uuid]
	ht.host.staticPriceTables.mu.RUnlock()
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
		_ = ht.host.rpcUpdatePriceTable(stream)
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
		clientMux, err = mux.NewClientMux(clientConn, serverPubKey, persist.NewLogger(ioutil.Discard), func(*mux.Mux) {}, func(*mux.Mux) {})
		if err != nil {
			panic(err)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		serverMux, err = mux.NewServerMux(serverConn, serverPubKey, serverPrivKey, persist.NewLogger(ioutil.Discard), func(*mux.Mux) {}, func(*mux.Mux) {})
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
	muxAddress := fmt.Sprintf("%s:%s", hes.NetAddress.Host(), hes.SiaMuxPort)
	mux := h.staticMux

	// fetch a stream from the mux
	stream, err := mux.NewStream(modules.HostSiaMuxSubscriberName, muxAddress, modules.SiaPKToMuxPK(h.publicKey))
	if err != nil {
		return nil, err
	}
	return stream, nil
}
