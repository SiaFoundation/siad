package main

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
)

// TestRootSiacCmd tests root siac command for expected outputs. The test
// requires siad running at port 9980 and no service running at port 5555.
func TestRootSiacCmd(t *testing.T) {
	if !build.VLONG {
		t.SkipNow()
	}
	
	// define test constants: regular expressions to check siac output
	begin := "^"
	nl := `
` // platform agnostic new line
	end := "$"

	rootCmdOutPattern := `Consensus:
  Synced: (No|Yes)
  Height: [\d]+

Wallet:
  Status: (Locked|Unlocked)

Renter:
  Files:               [\d]+
  Total Stored:        [\d]+.[\d]+ (kB|MB|GB|TB)
  Total Contract Data: [\d]+.[\d]+ (kB|MB|GB|TB)
  Min Redundancy:      [\d]+.[\d]{2}
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
		{
			name:               "TestRootCmd",
			test:               testGenericCobraCmd,
			cmd:                []string{},
			expectedOutPattern: begin + rootCmdOutPattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithShortAddressFlag",
			test:               testGenericCobraCmd,
			cmd:                []string{"-a", "localhost:9980"},
			expectedOutPattern: begin + rootCmdOutPattern + nl + nl + end,
		},
		{
			name:               "TestRootCmdWithLongAddressFlag",
			test:               testGenericCobraCmd,
			cmd:                []string{"--addr", "localhost:9980"},
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
	err := runCobraCmdSubTests(t, subTests)
	if err != nil {
		t.Fatal(err)
	}
}
