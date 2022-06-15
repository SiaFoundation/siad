package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/api/explored"
)

var (
	// to be supplied at build time
	githash   = "?"
	builddate = "?"
)

var (
	genesisTxns  = []types.Transaction{}
	genesisBlock = types.Block{
		Header: types.BlockHeader{
			Timestamp: time.Unix(734600000, 0),
		},
		Transactions: genesisTxns,
	}
	genesisUpdate = consensus.GenesisUpdate(genesisBlock, types.Work{NumHashes: [32]byte{29: 1 << 4}})
	genesis       = consensus.Checkpoint{Block: genesisBlock, State: genesisUpdate.State}
)

func die(context string, err error) {
	if err != nil {
		log.Fatalf("%v: %v", context, err)
	}
}

func main() {
	log.SetFlags(0)
	gatewayAddr := flag.String("addr", ":0", "address to listen on")
	apiAddr := flag.String("http", "localhost:9980", "address to serve API on")
	dir := flag.String("dir", ".", "directory to store node state in")
	bootstrap := flag.String("bootstrap", "", "peer address or explorer URL to bootstrap from")
	flag.Parse()

	log.Println("explored v0.1.0")
	if flag.Arg(0) == "version" {
		log.Println("Commit:", githash)
		log.Println("Build Date:", builddate)
		return
	}

	n, err := newNode(*gatewayAddr, *dir, genesis)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := n.Close(); err != nil {
			log.Println("WARN: error shutting down:", err)
		}
	}()
	log.Println("p2p: Listening on", n.s.Addr())
	go func() {
		if err := n.run(); err != nil {
			die("fatal error", err)
		}
	}()

	if *bootstrap != "" {
		log.Println("Connecting to bootstrap peer...")
		if err := n.s.Connect(*bootstrap); err != nil {
			log.Println(err)
		} else {
			log.Println("Success!")
		}
	}

	l, err := net.Listen("tcp", *apiAddr)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("api: Listening on", l.Addr())
	go func() {
		api := explored.NewServer(n.c, n.s, n.tp)
		if err := http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api") {
				r.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
				api.ServeHTTP(w, r)
				return
			}
			http.NotFound(w, r)
		})); err != nil {
			log.Println(err)
		}
	}()

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)
	<-signalCh
	log.Println("Shutting down...")
	n.Close()
	l.Close()
}
