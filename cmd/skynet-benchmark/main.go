package main

import (
	"fmt"
	"os"
	"strconv"

	"gitlab.com/NebulousLabs/Sia/node/api/client"
)

var (
	c *client.Client
)

func main() {
	fmt.Printf("Skynet performance analysis tool.\n\n")

	// Determine which port to use when talking to siad.
	args := os.Args
	port := 9980
	var cmd string
	if len(args) == 1 {
		cmd = "dl"
	} else if len(args) == 2 {
		cmd = args[1]
	} else if len(args) == 3 {
		cmd = args[1]

		// Parse port.
		num, err := strconv.Atoi(args[2])
		if err != nil {
			fmt.Println("Error parsing port:", err)
		}
		if num > 65535 {
			fmt.Println("Invalid port number")
		}
		port = num
	} else if len(args) > 3 {
		fmt.Println("Usage: ./skynet-benchmark [optional: which test to run] [optional: port for siad api]\n\tTest options: 'dl' and 'basic'")
		return
	}
	addr := fmt.Sprintf("localhost:%d", port)

	// Create the client that will be used to talk to siad.
	opts, err := client.DefaultOptions()
	if err != nil {
		fmt.Println("Unable to get Sia client options:", err)
		return
	}
	opts.Address = addr
	c = client.New(opts)

	// Parse the options.
	if cmd == "basic" {
		basicCheck()
		return
	}
	if cmd == "dl" {
		dl()
		return
	}

	fmt.Printf("Command '%v' not recognized. Options are 'dl' and 'basic'\n", cmd)
}
