package main

import (
	"fmt"
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/gateway"
	"log"
	"strings"
	"time"
)

type NodeScanner struct {
	gateway *gateway.Gateway

	workCh   chan WorkAssignment
	resultCh chan ShareNodesResult

	queue []modules.NetAddress
	seen  map[modules.NetAddress]struct{}

	// Stats
	successfulConns int
	failConns       int
	oldVersionFails int
	// TODO: file to store other stats
}

type WorkAssignment struct {
	node           modules.NetAddress
	maxRPCAttempts int
}

type ShareNodesResult struct {
	node  modules.NetAddress
	err   error
	nodes map[modules.NetAddress]struct{}
}

// NodeScanResult stores the data we got from the scan of a single address.
type NodeScanResult struct {
	addr      modules.NetAddress
	timestamp time.Time
	err       error
}

const maxSharedNodes = uint64(1000)
const maxRPCs = 10
const maxWorkers = 10
const workChSize = 1000

func main() {
	// TODO: take starting address from commandline
	addr := modules.NetAddress("192.168.1.161:9981")

	ns := setupNodeScanner(addr)

	// Start all the workers.
	for i := 0; i < maxWorkers; i++ {
		go startWorker(ns.gateway, ns.workCh, ns.resultCh)
	}

	// Print out stats periodically.
	ticker := time.NewTicker(1 * time.Second)
	numRPCAttempts := 3

	// TODO: fix this hacky end condition
	checkTime := time.Now().Add(15 * time.Minute)

	for {
		select {
		case <-ticker.C:
			log.Printf("MAIN LOOP:\n\t SeenLen: %d\n\tQueueLen: %d, WorkCh len: %d ResultCh len: %d\n", len(ns.seen), len(ns.queue), len(ns.workCh), len(ns.resultCh))
			fmt.Printf("\nConnection counts: Success: %d All fails: %d, Old version: %d\n", ns.successfulConns, ns.failConns, ns.oldVersionFails)

		case res := <-ns.resultCh:
			// Add any new nodes from this set of results.
			for node := range res.nodes {
				if _, alreadySeen := ns.seen[node]; !alreadySeen {
					ns.seen[node] = struct{}{}
					ns.queue = append(ns.queue, node)
				}
			}

			// Log the result and any errors.
			ns.logWorkerResult(res)
		}

		// Fill up workCh with nodes from queue.
		var node modules.NetAddress
		for i := len(ns.workCh); i < workChSize; i++ {
			if len(ns.queue) == 0 {
				break
			}
			node, ns.queue = ns.queue[len(ns.queue)-1], ns.queue[:len(ns.queue)-1]
			ns.workCh <- WorkAssignment{
				node:           node,
				maxRPCAttempts: numRPCAttempts,
			}
		}

		// Check ending condition.
		// TODO: this is wrong... but mostly works.
		if len(ns.queue) == 0 && len(ns.workCh) == 0 && len(ns.resultCh) == 0 && time.Now().After(checkTime) {
			log.Printf("MAIN LOOP:\n\t SeenLen: %d\n\tQueueLen: %d, WorkCh len: %d ResultCh len: %d\n", len(ns.seen), len(ns.queue), len(ns.workCh), len(ns.resultCh))
			fmt.Printf("\nConnection counts: Success: %d All fails: %d, Old version: %d\n", ns.successfulConns, ns.failConns, ns.oldVersionFails)
			return
		}
	}
}

// setupNodeScanner creates a dummy gateway at localhost and initializes all the
// data structures used for scanning the network. It starts by connecting to the
// Sia node at addr. It then creates a queue of node addresses using the set of
// bootstrap nodes and also by asking the initial node for peers using the
// ShareNodes RPC.
func setupNodeScanner(addr modules.NetAddress) (ns *NodeScanner) {
	ns = new(NodeScanner)

	// Create dummy gateway at localhost.
	g, err := gateway.New("localhost:0", true, build.TempDir("node-scanner")) // TODO: make own dir
	if err != nil {
		log.Fatal("Error making new gateway: ", err)
	}
	log.Println("Set up gateway at address: ", g.Address())
	ns.gateway = g

	// Connect to the starting node.
	err = g.Connect(addr)
	if err != nil {
		log.Fatalf("Cannot connect to local node at address %s: %s\n", addr, err)
	}

	// Get some of the starting node's peers.
	ns.queue = make([]modules.NetAddress, 100)
	err = g.RPC(addr, "ShareNodes", func(conn modules.PeerConn) error {
		return encoding.ReadObject(conn, &ns.queue, maxSharedNodes*modules.MaxEncodedNetAddressLength)
	})
	if err != nil {
		log.Fatal("ShareNodes err: ", err)
	}

	// TODO: add bootstrap nodes to queue.

	// Mark all starting nodes as seen.
	ns.seen = make(map[modules.NetAddress]struct{})
	ns.seen[addr] = struct{}{}
	for _, n := range ns.queue {
		ns.seen[n] = struct{}{}
	}

	ns.workCh = make(chan WorkAssignment, workChSize)
	ns.resultCh = make(chan ShareNodesResult, workChSize)

	for _, node := range ns.queue {
		ns.workCh <- WorkAssignment{
			node:           node,
			maxRPCAttempts: maxRPCs,
		}
	}
	ns.queue = ns.queue[:1]

	log.Printf("Starting with %d nodes in workCh.\n", len(ns.workCh))
	return
}

func (ns *NodeScanner) logWorkerResult(res ShareNodesResult) {
	if res.err == nil {
		// TODO: log IP, timestamp, etc. in file.
		log.Printf("success %s\n", res.node)
		ns.successfulConns++
		return
	}
	ns.failConns++

	if strings.Contains(res.err.Error(), "unacceptable version") {
		log.Printf("Cannot connect to local node at address %s: %s\n", res.node, res.err)
		ns.oldVersionFails++
	}

	/*

	   TODO: add more failure types:
	   	- "network is unreachable"
	   	- "no route to host"
	   	- "connection refused"
	*/

}

func startWorker(g *gateway.Gateway, workCh <-chan WorkAssignment, resultCh chan<- ShareNodesResult) {
	for {
		work, ok := <-workCh
		if !ok {
			fmt.Println("ayy", len(workCh))
			return
		}
		fmt.Printf("\taccepted work: %s\n", work.node)

		// Try connecting to the node at this address.
		// If the connection fails, return the error message.
		err := g.Connect(work.node)
		if err != nil {
			resultCh <- ShareNodesResult{err: err}
			continue
		}

		// Try 1 or more ShareNodes RPCs with this node. Return any errors.
		resultCh <- sendShareNodesRequests(g, work)
		g.Disconnect(work.node)
	}
}

const timeBetweenRequests = 50 * time.Millisecond

// Send ShareNodesRequest(s) to a node and return the set of nodes received.
func sendShareNodesRequests(g *gateway.Gateway, work WorkAssignment) ShareNodesResult {
	result := ShareNodesResult{
		node:  work.node,
		err:   nil,
		nodes: make(map[modules.NetAddress]struct{}),
	}

	// The ShareNodes RPC gives at most 10 random peers from the node, so
	// we repeatedly call ShareNodes in an attempt to get more peers quickly.
	for i := 0; i < work.maxRPCAttempts; i++ {
		var newNodes []modules.NetAddress
		result.err = g.RPC(work.node, "ShareNodes", func(conn modules.PeerConn) error {
			return encoding.ReadObject(conn, &newNodes, maxSharedNodes*modules.MaxEncodedNetAddressLength)
		})
		if result.err != nil {
			return result
		}
		for _, n := range newNodes {
			result.nodes[n] = struct{}{}
		}

		// Avoid spamming nodes by adding time between RPCs.
		time.Sleep(timeBetweenRequests)
	}

	fmt.Printf("\tWorker found %d peers\n", len(result.nodes))
	return result
}
