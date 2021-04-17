package main

import (
	"fmt"
	"os"

	"go.sia.tech/siad/node/api/client"
)

var (
	c *client.Client
)

func main() {
	fmt.Printf("Skynet performance analysis tool.\n\n")

	// Determine which port to use when talking to siad.
	args := os.Args
	addr := "localhost:9980"
	var password string
	var cmd string
	if len(args) == 1 {
		cmd = "dl"
	} else if len(args) == 2 {
		cmd = args[1]
	} else if len(args) == 3 {
		cmd = args[1]
		addr = args[2]
	} else if len(args) == 4 {
		cmd = args[1]
		addr = args[2]
		password = args[3]
	} else if len(args) > 4 {
		fmt.Println("Usage: ./skynet-benchmark [optional: which test to run] [optional: endpoint for siad api, defaults to \"localhost:9980\"] [optional: api password]\n\tTest options: 'dl' and 'basic'")
		return
	}

	// Create the client that will be used to talk to siad.
	opts, err := client.DefaultOptions()
	if err != nil {
		fmt.Println("Unable to get Sia client options:", err)
		return
	}
	opts.Address = addr
	if password != "" {
		opts.Password = password
	}
	c = client.New(opts)

	// Parse the options.
	var benchmark benchmarkFn
	if cmd == "basic" {
		benchmark = basicCheck
	} else if cmd == "dl" {
		benchmark = dl
	} else {
		fmt.Printf("Command '%v' not recognized. Options are 'dl' and 'basic'\n", cmd)
		return
	}

	// Run the benchmark and capture the output
	output := captureOutput(benchmark)
	skylink, err := uploadBenchmarkOutput(output)
	if err != nil {
		fmt.Println("Failed to upload results to Skynet", err)
		return
	}
	fmt.Println("Uploaded output to skynet: ", skylink)
}
