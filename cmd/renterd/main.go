package main

import (
	"context"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/siad/v2/p2p"
	"golang.org/x/crypto/ssh/terminal"
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
	genesis       = consensus.Checkpoint{Block: genesisBlock, Context: genesisUpdate.Context}
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
	mine := flag.Bool("mine", false, "run CPU miner")
	checkpoint := flag.String("checkpoint", "", "checkpoint to bootstrap from")
	bootstrap := flag.String("bootstrap", "", "peer address or explorer URL to bootstrap from")
	flag.Parse()

	log.Println("renterd v0.0.1")
	if flag.Arg(0) == "version" {
		return
	}

	apiPassword := os.Getenv("RENTERD_API_PASSWORD")
	if len(apiPassword) == 0 {
		fmt.Print("Enter API password: ")
		pw, err := terminal.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			log.Fatal(err)
		}
		apiPassword = string(pw)
	}

	initCheckpoint := genesis
	if *checkpoint != "" {
		index, ok := parseIndex(*checkpoint)
		if !ok {
			log.Fatal("Invalid checkpoint")
		}
		if *bootstrap == "" {
			log.Fatal("Must specify -bootstrap to download checkpoint from")
		}
		fmt.Printf("Downloading checkpoint %v from %v...", index, *bootstrap)
		c, err := p2p.DownloadCheckpoint(context.Background(), *bootstrap, genesisBlock.ID(), index)
		if err != nil {
			fmt.Println()
			log.Fatal(err)
		}
		fmt.Println("Success!")
		initCheckpoint = c

		// overwrite existing stores
		// TODO: if existing stores are present, require -force flag
		if err := os.RemoveAll(filepath.Join(*dir, "chain")); err != nil {
			log.Fatal(err)
		}
		if err := os.RemoveAll(filepath.Join(*dir, "wallet")); err != nil {
			log.Fatal(err)
		}
	}

	n, err := newNode(*gatewayAddr, *dir, initCheckpoint)
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

	if *mine {
		go n.mine()
	}

	l, err := net.Listen("tcp", *apiAddr)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("api: Listening on", l.Addr())
	go startWeb(l, n, apiPassword)

	signalCh := make(chan os.Signal, 1)
	signal.Notify(signalCh, os.Interrupt)
	<-signalCh
	log.Println("Shutting down...")
	n.Close()
	l.Close()
}

func parseIndex(s string) (types.ChainIndex, bool) {
	parts := strings.Split(s, "::")
	if len(parts) != 2 {
		return types.ChainIndex{}, false
	}
	height, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil {
		return types.ChainIndex{}, false
	}
	var id types.BlockID
	if n, err := hex.Decode(id[:], []byte(parts[1])); n != len(id) || err != nil {
		return types.ChainIndex{}, false
	}
	return types.ChainIndex{
		Height: height,
		ID:     id,
	}, true
}
