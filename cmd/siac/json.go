package main

// json.go implements commands which provide json output rather than human
// output.

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"gitlab.com/NebulousLabs/Sia/modules"
)

var (
	jsonCmd = &cobra.Command{
		Use: "json",
		Short: "provide a json dump of siad's current status",
		Long: "queries a large number of endpoints in the siad api and produces a json dump with all of the information",
		Run: wrap(jsoncmd),
	}
)

// jsoncmd queries a large number of endpoints in the siad api and aggregates
// them together to produce a single dump of information.
//
// If this ever gets split into multiple subcommands, the current implementation
// would specifically be 'json renter' as the focus of the current implementation
// is on pulling together a large amount of renter information.
func jsoncmd() {
	var rs modules.RenterStats

	// Grab the contract statistics.
	rc, err := httpClient.RenterDisabledContractsGet()
	if err != nil {
		die("Could not fetch contract status:", err)
	}

	// Grab the statistics on the various classes of contracts.
	activeSize, activeSpent, activeRemaining, activeFees := contractStats(rc.ActiveContracts)
	passiveSize, passiveSpent, passiveRemaining, passiveFees := contractStats(rc.PassiveContracts)
	_, refreshedSpent, refreshedRemaining, refreshedFees := contractStats(rc.RefreshedContracts)
	disabledSize, disabledSpent, disabledRemaining, disabledFees := contractStats(rc.DisabledContracts)
	// Sum up the appropriate totals.
	rs.ActiveContractData = activeSize
	rs.PassiveContractData = passiveSize
	rs.WastedContractData = disabledSize
	spentToHost := activeSpent.Add(passiveSpent).Add(refreshedSpent).Add(disabledSpent)
	spentToFees := activeFees.Add(passiveFees).Add(refreshedFees).Add(disabledFees)
	rs.TotalContractSpentFunds = spentToHost.Add(spentToFees)
	rs.TotalContractRemainingFunds = activeRemaining.Add(passiveRemaining).Add(refreshedRemaining).Add(disabledRemaining)

	// Get the number of files on the system.
	rf, err := httpClient.RenterDirRootGet(modules.RootSiaPath())
	if err != nil {
		die("Cound not get the renter root dir:", err)
	}
	rs.TotalSiafiles = rf.Directories[0].AggregateNumFiles

	// Get the wallet balance.
	wg, err := httpClient.WalletGet()
	if err != nil {
		die("could not get the wallet balance:", err)
	}
	rs.TotalWalletFunds = wg.ConfirmedSiacoinBalance.Add(wg.UnconfirmedIncomingSiacoins).Sub(wg.UnconfirmedOutgoingSiacoins)

	// Convert the rs to marshalled json.
	json, err := json.MarshalIndent(rs, "", "\t")
	if err != nil {
		die("Cound not marshal the json output:", err)
	}
	fmt.Println(string(json))
}
