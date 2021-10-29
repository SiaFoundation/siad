package main

import (
	"fmt"
	"log"
	"runtime"
	"strings"

	"go.sia.tech/siad/v2/api"
	"lukechampine.com/flagg"
)

var (
	// to be supplied at build time
	githash   = "?"
	builddate = "?"
)

var (
	rootUsage = `Usage:
    siac [flags] [action|subcommand]

Actions:
    version         display version information

Subcommands:
    wallet          manage the wallet
    host            manage the host
    renter          manage the renter
    miner           manage the miner
`
	versionUsage = rootUsage

	walletUsage = `Usage:
    siac [flags] wallet [flags] [action]

Actions:
    balance         display current balance
`

	walletBalanceUsage = `Usage:
siac [flags] wallet [flags] balance

Displays the current wallet balance in siacoins.
`
)

func check(ctx string, err error) {
	if err != nil {
		log.Fatalln(ctx, err)
	}
}

var siadAddr string

func getClient() *api.Client {
	if !strings.HasPrefix(siadAddr, "https://") && !strings.HasPrefix(siadAddr, "http://") {
		siadAddr = "http://" + siadAddr
	}
	c := api.NewClient(siadAddr)
	_, err := c.WalletBalance()
	check("couldn't connect to siad:", err)
	return c
}

func main() {
	log.SetFlags(0)

	rootCmd := flagg.Root
	rootCmd.Usage = flagg.SimpleUsage(rootCmd, rootUsage)
	rootCmd.StringVar(&siadAddr, "a", "localhost:9980", "siad API server address")
	verbose := rootCmd.Bool("v", false, "print verbose output")

	versionCmd := flagg.New("version", versionUsage)
	walletCmd := flagg.New("wallet", walletUsage)
	walletBalanceCmd := flagg.New("balance", walletBalanceUsage)
	exactBalance := walletBalanceCmd.Bool("exact", false, "print balance in Hastings")

	_, _ = verbose, exactBalance

	cmd := flagg.Parse(flagg.Tree{
		Cmd: rootCmd,
		Sub: []flagg.Tree{
			{Cmd: versionCmd},
			{
				Cmd: walletCmd,
				Sub: []flagg.Tree{
					{Cmd: walletBalanceCmd},
				},
			},
		},
	})
	args := cmd.Args()

	switch cmd {
	case rootCmd:
		if len(args) != 0 {
			cmd.Usage()
			return
		}
		fallthrough
	case versionCmd:
		log.Printf("siac v2.0.0\nCommit:     %s\nGo version: %s %s/%s\nBuild Date: %s\n",
			githash, runtime.Version(), runtime.GOOS, runtime.GOARCH, builddate)

	case walletCmd:
		cmd.Usage()

	case walletBalanceCmd:
		if len(args) != 0 {
			cmd.Usage()
			return
		}
		balance, err := getClient().WalletBalance()
		check("couldn't get balance:", err)
		if *exactBalance {
			fmt.Printf("%d H\n", balance.Siacoins)
		} else {
			fmt.Printf("%s SC\n", balance.Siacoins)
		}
	}
}
