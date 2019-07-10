package main

import (
	"encoding/json"
	"fmt"
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/gateway"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type NodeScanner struct {
	//The node scanner uses a dummy gateway to connect to nodes and
	//		requests peers from nodes across the network using the
	//		ShareNodes RPC.
	gateway *gateway.Gateway

	// Multiple workers are given addresses to scan using workCh.
	// The workers always send a result back to the main goroutine
	// using the resultCh
	workCh   chan WorkAssignment
	resultCh chan ShareNodesResult

	/*
		Count the total number of work assignments sent down workCh
		 and the total number of results received through resultCh.
		 Since every work assignment sent always sends a result back
		 (even in case of failure), the main goroutine can tell if the
		 node scan has finished by checking that:
			- there are no assignments outstanding in workCh
			- there are no unprocessed results in resultCh
			- there are no unassigned addresses in queue
			- all workers are done with their assignements (totalWorkAssignments = totalResults)
	*/
	totalWorkAssignments int
	totalResults         int

	// The seen set keeps track of all the addresses seen by the
	// scanner so far.
	seen map[modules.NetAddress]struct{}
	// The queue holds nodes to be added to workCh.
	queue []modules.NetAddress

	// Counters for successful/unsuccesful connections.
	successfulConns int
	failConns       int

	// Counters for common failure types.
	unacceptableVersions  int
	networkIsUnreachables int
	noRouteToHosts        int
	connectionRefuseds    int

	// scanFile holds all the results for this scan.
	scanFile *os.File
	// encoder is used to store NodeScanResult structs
	// as JSON objects in the scanFile.
	encoder *json.Encoder
}

// WorkAssignment tells a worker which node it should scan,
// and the number of times it should send the ShareNodes RPC.
// The ShareNodes RPC is used multiple times because nodes will
// only return 10 random peers, but we want as many as possible.
type WorkAssignment struct {
	node           modules.NetAddress
	maxRPCAttempts int
}

// ShareNodesResult gives the set of nodes received from ShareNodes
// RPCs sent to a specific node. err is nil, an error from connecting,
// or an error from ShareNodes.
type ShareNodesResult struct {
	node  modules.NetAddress
	err   error
	nodes map[modules.NetAddress]struct{}
}

// NodeScanResult stores the data we got from the scan of a single address.
type NodeScanResult struct {
	Addr      modules.NetAddress
	Timestamp int64
	Err       string
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

	for {
		select {
		case <-ticker.C:
			fmt.Printf("Seen: %d,  Queued: %d, In WorkCh: %d, In ResultCh :%d\n", len(ns.seen), len(ns.queue), len(ns.workCh), len(ns.resultCh))
			fmt.Printf("Number assigned: %d, Number of results :%d\n", ns.totalWorkAssignments, ns.totalResults)
			fmt.Printf("Successful Connections: %d, Unsuccesful: %d\n\t(unacceptableVersions: %d, networkIsUnreachables: %d, noRouteToHosts: %d, connectionRefuseds: %d)\n\n", ns.successfulConns, ns.failConns, ns.unacceptableVersions, ns.networkIsUnreachables, ns.noRouteToHosts, ns.connectionRefuseds)

		case res := <-ns.resultCh:
			ns.totalResults++

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
			ns.totalWorkAssignments++
			ns.workCh <- WorkAssignment{
				node:           node,
				maxRPCAttempts: numRPCAttempts,
			}
		}

		// Check ending condition.
		if (len(ns.workCh) == 0) && (len(ns.resultCh) == 0) && (len(ns.queue) == 0) && (ns.totalWorkAssignments == ns.totalResults) {
			fmt.Printf("Seen: %d,  Queued: %d, In WorkCh: %d, In ResultCh :%d\n", len(ns.seen), len(ns.queue), len(ns.workCh), len(ns.resultCh))
			fmt.Printf("Number assigned: %d, Number of results :%d\n", ns.totalWorkAssignments, ns.totalResults)
			fmt.Printf("Successful Connections: %d, Unsuccesful: %d\n\t(unacceptableVersions: %d, networkIsUnreachables: %d, noRouteToHosts: %d, connectionRefuseds: %d)\n\n", ns.successfulConns, ns.failConns, ns.unacceptableVersions, ns.networkIsUnreachables, ns.noRouteToHosts, ns.connectionRefuseds)
			ns.scanFile.Close()
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

	// Setup the node scanner's directories.
	scannerDirPath := filepath.Join(os.TempDir(), "SiaNodeScanner")
	scannerGatewayDirPath := filepath.Join(scannerDirPath, "gateway")
	if _, err := os.Stat(scannerDirPath); os.IsNotExist(err) {
		err := os.Mkdir(scannerDirPath, 0777)
		if err != nil {
			log.Fatal("Error creating scan directory: ", err)
		}
	}
	if _, err := os.Stat(scannerGatewayDirPath); os.IsNotExist(err) {
		err := os.Mkdir(scannerGatewayDirPath, 0777)
		if err != nil {
			log.Fatal("Error creating scanner gateway directory: ", err)
		}
	}
	log.Println(scannerDirPath)

	// Create the file for this scan.
	startTime := time.Now().Format("01-02:15:04")
	scanFileName := scannerDirPath + "/scan-" + startTime + ".json"
	scanFile, err := os.Create(scanFileName)
	if err != nil {
		log.Fatal("Error creating scan file: ", err)
	}
	ns.scanFile = scanFile
	ns.encoder = json.NewEncoder(ns.scanFile)

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
		ns.totalWorkAssignments++
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
	scanRes := NodeScanResult{
		Addr:      res.node,
		Timestamp: time.Now().Unix(),
		Err:       "",
	}
	if res.err != nil {
		scanRes.Err = res.err.Error()
	}
	err := ns.encoder.Encode(scanRes)
	if err != nil {
		log.Println("Error writing NodeScanResult to file! - ", err)
	}

	if res.err == nil {
		ns.successfulConns++
		return
	}

	ns.failConns++
	log.Printf("Cannot connect to local node at address %s: %s\n", res.node, res.err)

	if strings.Contains(res.err.Error(), "unacceptable version") {
		ns.unacceptableVersions++
	} else if strings.Contains(res.err.Error(), "unreachable") {
		ns.networkIsUnreachables++
	} else if strings.Contains(res.err.Error(), "no route to host") {
		ns.noRouteToHosts++
	} else if strings.Contains(res.err.Error(), "connection refused") {
		ns.connectionRefuseds++
	} else {
		log.Printf("Cannot connect to local node at address %s: %s\n", res.node, res.err)
	}
}

// startWorker starts a worker that continually receives from the workCh,
// connect to the node it has been assigned, and returns all results
// using resultCh.
func startWorker(g *gateway.Gateway, workCh <-chan WorkAssignment, resultCh chan<- ShareNodesResult) {
	for {
		work := <-workCh

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

	return result
}
