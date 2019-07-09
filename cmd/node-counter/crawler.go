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

/*

TODO: add more failure types:
	- "network is unreachable"
	- "no route to host"
	- "connection refused"
*/

type ResultChans struct {
	newAddrs            chan modules.NetAddress
	successfulConns     chan struct{}
	oldVersionConnFails chan struct{}
	otherConnFails      chan struct{}
}

const MAX_SHARED_NODES = uint64(1000)
const MAX_RPCS = 10
const MAX_WORKERS = 10

func main() {
	// TODO: take starting address from commandline
	addr := modules.NetAddress("192.168.1.161:9981")

	// Create dummy gateway at localhost.
	g, err := gateway.New("localhost:0", true, build.TempDir("node-counter"))
	if err != nil {
		log.Fatal("Error making new gateway: ", err)
	}
	log.Println("Set up gateway at address: ", g.Address())

	// Connect to the starting node.
	err = g.Connect(addr)
	if err != nil {
		log.Fatalf("Cannot connect to local node at address %s: %s\n", addr, err)
	}

	var nodeQueue []modules.NetAddress
	err = g.RPC(addr, "ShareNodes", func(conn modules.PeerConn) error {
		return encoding.ReadObject(conn, &nodeQueue, MAX_SHARED_NODES*modules.MaxEncodedNetAddressLength)
	})
	if err != nil {
		log.Fatal("ShareNodes err: ", err)
	}

	n := len(nodeQueue)
	log.Printf("Starting with %d nodes in queue.\n", n)

	// Mark all starting node as seen.
	seen := make(map[modules.NetAddress]struct{})
	seen[addr] = struct{}{}
	for _, n := range nodeQueue {
		seen[n] = struct{}{}
	}

	results := ResultChans{
		newAddrs:            make(chan modules.NetAddress, 6000),
		successfulConns:     make(chan struct{}, 1000),
		oldVersionConnFails: make(chan struct{}, 1000),
		otherConnFails:      make(chan struct{}, 1000),
	}

	// Start the node counter in the background.
	counterKillCh := make(chan struct{})
	counterDone := make(chan struct{})
	go nodeCounter(&results, counterKillCh, counterDone)

	nWorkersAvailable := MAX_WORKERS
	workerDone := make(chan struct{}, MAX_WORKERS)

	// Print out some stats every now and then.
	ticker := time.NewTicker(5 * time.Second)

	for {
		select {
		// Add any new, unseen addresses to the work queue.
		case addr := <-results.newAddrs:
			if _, alreadySeen := seen[addr]; !alreadySeen {
				seen[addr] = struct{}{}
				nodeQueue = append(nodeQueue, addr)
			}
		// Check if any workers are done.
		case <-workerDone:
			nWorkersAvailable++

		case <-ticker.C:
			log.Printf("MAIN LOOP:\n\t FreeWorkers: %d, QueueLen: %d, Chan len: %d\n", nWorkersAvailable, len(nodeQueue), len(results.newAddrs))

		default:
		}

		// Assign work to any free workers.
		if len(nodeQueue) != 0 && nWorkersAvailable > 0 && nWorkersAvailable <= MAX_WORKERS {
			addr, nodeQueue = nodeQueue[len(nodeQueue)-1], nodeQueue[:len(nodeQueue)-1]
			go getNodes(g, addr, &results, &workerDone)
			nWorkersAvailable--
		}

		// If all workers are done, and there are no results/work assignments waiting then finish.
		if nWorkersAvailable == MAX_WORKERS && len(results.newAddrs) == 0 && len(nodeQueue) == 0 {
			counterKillCh <- struct{}{}
			<-counterDone
			return
		}

		if len(results.newAddrs) < 100 {
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// getNodes uses the gateway to connect to the node at addr, and then uses the ShareNodes RPC
// to some of that node's peers. It sends the results (succesfull/unsuccesful connections and
// the addresses of the peers) through ch.
func getNodes(g *gateway.Gateway, addr modules.NetAddress, ch *ResultChans, done *chan struct{}) {
	defer func() {
		*done <- struct{}{}
	}()

	// Try connecting to the node at this address, and increment
	// node counter or one of the fail counters.
	err := g.Connect(addr)
	if err != nil {
		log.Printf("Cannot connect to local node at address %s: %s\n", addr, err)
		if strings.Contains(err.Error(), "unacceptable version") {
			ch.oldVersionConnFails <- struct{}{}
		} else {
			ch.otherConnFails <- struct{}{}
		}
		return
	}
	go func() {
		ch.successfulConns <- struct{}{}
	}()

	// ShareNodes returns 10 random peers from the node. We'll make the RPC multiple times,
	// and deduplicate the results using a set before sending them back to be queued.
	nodesToSend := make(map[modules.NetAddress]struct{})

	for i := 0; i < MAX_RPCS; i++ {
		// Get the list of peers from this node.
		var newNodes []modules.NetAddress
		err = g.RPC(addr, "ShareNodes", func(conn modules.PeerConn) error {
			return encoding.ReadObject(conn, &newNodes, MAX_SHARED_NODES*modules.MaxEncodedNetAddressLength)
		})
		if err != nil {
			log.Fatal("ShareNodes err: ", err)
		}
		for _, n := range newNodes {
			nodesToSend[n] = struct{}{}
		}
		// Don't spam your friends.
		time.Sleep(50 * time.Millisecond)
	}

	fmt.Printf("\tworker done %d peers\n", len(nodesToSend))

	// Add peers to the queue.
	for n := range nodesToSend {
		ch.newAddrs <- n
	}

	g.Disconnect(addr)
}

func (ch *ResultChans) connChansEmpty() bool {
	return (len(ch.successfulConns) == 0) && (len(ch.oldVersionConnFails) == 0) && (len(ch.otherConnFails) == 0)
}

// nodeCounter reads from the results channels and counts successful/unsuccessful connections.
func nodeCounter(ch *ResultChans, kill chan struct{}, done chan struct{}) {
	// Keep track of successful connections, unsuccessful connections
	// that failed due to version mismatch, and other failed connections.
	// (1 successful connection from our starting node)
	nSuccesfulConns := 1
	nOldVersionFails := 0
	nOtherFails := 0

	ticker := time.NewTicker(1 * time.Second)
	for {
		select {
		case <-kill:
			// Drain the counter channels.
			for empty := ch.connChansEmpty(); !empty; empty = ch.connChansEmpty() {
				select {
				case <-ch.successfulConns:
					nSuccesfulConns++
				case <-ch.oldVersionConnFails:
					nOldVersionFails++
				case <-ch.otherConnFails:
					nOtherFails++
				default:
				}
			}
			log.Printf("NODE COUNTER:\n\tSuccesful Connections: %d, Old version failures: %d, Other failures: %d\n", nSuccesfulConns, nOldVersionFails, nOtherFails)
			done <- struct{}{}
			return

		case <-ch.successfulConns:
			nSuccesfulConns++
		case <-ch.oldVersionConnFails:
			nOldVersionFails++
		case <-ch.otherConnFails:
			nOtherFails++
		case <-ticker.C:
			log.Printf("NODE COUNTER:\n\tSuccesful Connections: %d, Old version failures: %d, Other failures: %d\n", nSuccesfulConns, nOldVersionFails, nOtherFails)
		}
	}
}
