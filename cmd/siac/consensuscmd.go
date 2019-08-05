package main

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

var (
	consensusCmd = &cobra.Command{
		Use:   "consensus",
		Short: "Print the current state of consensus",
		Long:  "Print the current state of consensus such as current block, block height, and target.",
		Run:   wrap(consensuscmd),
	}

	consensusCmdVerbose bool
)

// consensuscmd is the handler for the command `siac consensus`.
// Prints the current state of consensus.
func consensuscmd() {
	cg, err := httpClient.ConsensusGet()
	if errors.Contains(err, api.ErrAPICallNotRecognized) {
		// Assume module is not loaded if status command is not recognized.
		fmt.Printf("Consensus:\n  Status: %s\n\n", moduleNotReadyStatus)
		return
	} else if err != nil {
		die("Could not get current consensus state:", err)
	}

	if cg.Synced {
		fmt.Printf(`Synced: %v
Block:      %v
Height:     %v
Target:     %v
Difficulty: %v
`, yesNo(cg.Synced), cg.CurrentBlock, cg.Height, cg.Target, cg.Difficulty)
	} else {
		estimatedHeight := estimatedHeightAt(time.Now(), cg)
		estimatedProgress := float64(cg.Height) / float64(estimatedHeight) * 100
		if estimatedProgress > 100 {
			estimatedProgress = 99.9
		}
		if estimatedProgress == 100 && !cg.Synced {
			estimatedProgress = 99.9
		}
		fmt.Printf(`Synced: %v
Height: %v
Progress (estimated): %.1f%%
`, yesNo(cg.Synced), cg.Height, estimatedProgress)
	}
	if consensusCmdVerbose {
		fmt.Println()
		fmt.Println("Block Frequency:", cg.BlockFrequency)
		fmt.Println("Block Size Limit:", cg.BlockSizeLimit)
		fmt.Println("Maturity Delay:", cg.MaturityDelay)
		fmt.Println("Genesis Timestamp:", time.Unix(int64(cg.GenesisTimestamp), 0))
	}
}

// estimatedHeightAt returns the estimated block height for the given time.
// Block height is estimated by calculating the minutes since a known block in
// the past and dividing by 10 minutes (the block time).
func estimatedHeightAt(t time.Time, cg api.ConsensusGET) types.BlockHeight {
	gt := cg.GenesisTimestamp
	bf := cg.BlockFrequency
	return types.BlockHeight(types.Timestamp(t.Unix())-gt) / bf
}
