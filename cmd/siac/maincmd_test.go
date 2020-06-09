package main

import (
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
)

// TestRootSiacCmd tests root siac command for expected outputs. The test
// runs its own node and requires no service running at port 5555.
func TestRootSiacCmd(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a test node for this test group
	groupDir := siacTestDir(t.Name())
	n, err := newTestNode(groupDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := n.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	root := getRootCmdForSiacCmdsTests(groupDir)

	// define test constants:
	// regular expressions to check siac output
	begin := "^"
	nl := `
` // platform agnostic new line
	end := "$"

	IPv6addr := n.Address
	IPv4Addr := strings.ReplaceAll(n.Address, "[::]", "localhost")

	rootCmdOutPattern := `Consensus:
  Synced: (No|Yes)
  Height: [\d]+

Wallet:
(  Status: Locked|  Status:          unlocked
  Siacoin Balance: [\d]+(\.[\d]*|) (SC|KS|MS))

Renter:
  Files:               [\d]+
  Total Stored:        [\d]+(\.[\d]+|) ( B|kB|MB|GB|TB)
  Total Contract Data: [\d]+(\.[\d]+|) ( B|kB|MB|GB|TB)
  Min Redundancy:      ([\d]+.[\d]{2}|-)
  Active Contracts:    [\d]+
  Passive Contracts:   [\d]+
  Disabled Contracts:  [\d]+`

	rootCmdUsagePattern := `Usage:
  .*siac(\.test|) \[flags\]
  .*siac(\.test|) \[command\]

Available Commands:
  alerts      view daemon alerts
  consensus   Print the current state of consensus
  gateway     Perform gateway actions
  help        Help about any command
  host        Perform host actions
  hostdb      Interact with the renter's host database\.
  miner       Perform miner actions
  ratelimit   set the global maxdownloadspeed and maxuploadspeed
  renter      Perform renter actions
  skykey      Perform actions related to Skykeys
  skynet      Perform actions related to Skynet
  stop        Stop the Sia daemon
  update      Update Sia
  utils       various utilities for working with Sia's types
  version     Print version information
  wallet      Perform wallet actions

Flags:
  -a, --addr string            which host/port to communicate with \(i.e. the host/port siad is listening on\) \(default "localhost:9980"\)
      --apipassword string     the password for the API's http authentication
  -h, --help                   help for .*siac(\.test|)
  -d, --sia-directory string   location of the sia directory
      --useragent string       the useragent used by siac to connect to the daemon's API \(default "Sia-Agent"\)
  -v, --verbose                Display additional siac information

Use ".*siac(\.test|) \[command\] --help" for more information about a command\.`

	siaClientVersionPattern := "Sia Client v" + strings.ReplaceAll(build.Version, ".", `\.`)
	connectionRefusedPattern := `Could not get consensus status: \[failed to get reader response; GET request failed; Get http://localhost:5555/consensus: dial tcp \[::1\]:5555: connect: connection refused\]`

	// Define subtests
	// We can't test siad on default address (port) when test node has
	// dynamically allocated port, we have to use node address.
	subTests := []siacCmdSubTest{
		{
			name:               "TestRootCmdWithShortAddressFlagIPv6",
			test:               testGenericSiacCmd,
			cmd:                root,
			cmdStrs:            []string{"-a", IPv6addr},
			expectedOutPattern: begin + rootCmdOutPattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithShortAddressFlagIPv4",
			test:               testGenericSiacCmd,
			cmd:                root,
			cmdStrs:            []string{"-a", IPv4Addr},
			expectedOutPattern: begin + rootCmdOutPattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithLongAddressFlagIPv6",
			test:               testGenericSiacCmd,
			cmd:                root,
			cmdStrs:            []string{"--addr", IPv6addr},
			expectedOutPattern: begin + rootCmdOutPattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithLongAddressFlagIPv4",
			test:               testGenericSiacCmd,
			cmd:                root,
			cmdStrs:            []string{"--addr", IPv4Addr},
			expectedOutPattern: begin + rootCmdOutPattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithInvalidFlag",
			test:               testGenericSiacCmd,
			cmd:                root,
			cmdStrs:            []string{"-x"},
			expectedOutPattern: begin + "Error: unknown shorthand flag: 'x' in -x" + nl + rootCmdUsagePattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithInvalidAddress",
			test:               testGenericSiacCmd,
			cmd:                root,
			cmdStrs:            []string{"-a", "localhost:5555"},
			expectedOutPattern: begin + connectionRefusedPattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithHelpFlag",
			test:               testGenericSiacCmd,
			cmd:                root,
			cmdStrs:            []string{"-h"},
			expectedOutPattern: begin + siaClientVersionPattern + nl + nl + rootCmdUsagePattern + nl + end,
		},
	}

	// run tests
	err = runSiacCmdSubTests(t, subTests)
	if err != nil {
		t.Fatal(err)
	}
}
