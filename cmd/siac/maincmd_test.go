package main

import (
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
)

// TestRootSiacCmd tests root siac command for expected outputs. The test
// runs its own node and requires no service running at port 5555.
func TestRootSiacCmd(t *testing.T) {
	if !build.VLONG {
		t.SkipNow()
	}

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
	t.Log("siad api address:", n.APIAddress())

	// define test constants: regular expressions to check siac output
	begin := "^"
	nl := `
` // platform agnostic new line
	end := "$"

	IPv6addr := n.Address
	IPv4Addr := strings.Replace(n.Address, "[::]", "localhost", 1)

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

	siaClientVersionPattern := "Sia Client v" + build.Version
	connectionRefusedPattern := `Could not get consensus status: \[failed to get reader response; GET request failed; Get http://localhost:5555/consensus: dial tcp \[::1\]:5555: connect: connection refused\]`

	// define subtests
	subTests := []cobraCmdSubTest{
		// Can't test siad on default address (port) when test node has dynamically allocated port
		// {
		// 	name:               "TestRootCmd",
		// 	test:               testGenericCobraCmd,
		// 	cmd:                []string{},
		// 	expectedOutPattern: begin + rootCmdOutPattern + nl + nl + end,
		// },
		{
			name:               "TestRootCmdWithShortAddressFlagIPv6",
			test:               testGenericCobraCmd,
			cmd:                []string{"-a", IPv6addr},
			expectedOutPattern: begin + rootCmdOutPattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithShortAddressFlagIPv4",
			test:               testGenericCobraCmd,
			cmd:                []string{"-a", IPv4Addr},
			expectedOutPattern: begin + rootCmdOutPattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithLongAddressFlagIPv6",
			test:               testGenericCobraCmd,
			cmd:                []string{"--addr", IPv6addr},
			expectedOutPattern: begin + rootCmdOutPattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithLongAddressFlagIPv4",
			test:               testGenericCobraCmd,
			cmd:                []string{"--addr", IPv4Addr},
			expectedOutPattern: begin + rootCmdOutPattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithInvalidFlag",
			test:               testGenericCobraCmd,
			cmd:                []string{"-x"},
			expectedOutPattern: begin + "Error: unknown shorthand flag: 'x' in -x" + nl + rootCmdUsagePattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithInvalidAddress",
			test:               testGenericCobraCmd,
			cmd:                []string{"-a", "localhost:5555"},
			expectedOutPattern: begin + connectionRefusedPattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithHelpFlag",
			test:               testGenericCobraCmd,
			cmd:                []string{"-h"},
			expectedOutPattern: begin + siaClientVersionPattern + nl + nl + rootCmdUsagePattern + nl + end,
		},
	}

	// run tests
	err = runCobraCmdSubTests(t, subTests)
	if err != nil {
		t.Fatal(err)
	}
}
