package typesutil

import (
	"fmt"
	"strings"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

// PrintTxnWithObjectIDs prints the Transaction in human-readable form with all
// object IDs printed to allow for easy dependency matching (by humans) in
// debug-logs.
func PrintTxnWithObjectIDs(t types.Transaction) string {
	var str strings.Builder
	txIDString := crypto.Hash(t.ID()).String()
	fmt.Fprintf(&str, "\nTransaction ID: %s", txIDString)

	if len(t.SiacoinInputs) != 0 {
		fmt.Fprintf(&str, "\nSiacoinInputs:\n")
		for i, input := range t.SiacoinInputs {
			parentIDString := crypto.Hash(input.ParentID).String()
			fmt.Fprintf(&str, "\t%d: %s\n", i, parentIDString)
		}
	}
	if len(t.SiacoinOutputs) != 0 {
		fmt.Fprintf(&str, "SiacoinOutputs:\n")
		for i := range t.SiacoinOutputs {
			oidString := crypto.Hash(t.SiacoinOutputID(uint64(i))).String()
			fmt.Fprintf(&str, "\t%d: %s\n", i, oidString)
		}
	}
	if len(t.FileContracts) != 0 {
		fmt.Fprintf(&str, "FileContracts:\n")
		for i := range t.FileContracts {
			fcIDString := crypto.Hash(t.FileContractID(uint64(i))).String()
			fmt.Fprintf(&str, "\t%d: %s\n", i, fcIDString)
		}
	}
	if len(t.FileContractRevisions) != 0 {
		fmt.Fprintf(&str, "FileContractRevisions:\n")
		for _, fcr := range t.FileContractRevisions {
			parentIDString := crypto.Hash(fcr.ParentID).String()
			fmt.Fprintf(&str, "\t%d, %s\n", fcr.NewRevisionNumber, parentIDString)
		}
	}
	if len(t.StorageProofs) != 0 {
		fmt.Fprintf(&str, "StorageProofs:\n")
		for _, sp := range t.StorageProofs {
			parentIDString := crypto.Hash(sp.ParentID).String()
			fmt.Fprintf(&str, "\t%s\n", parentIDString)
		}
	}
	if len(t.SiafundInputs) != 0 {
		fmt.Fprintf(&str, "SiafundInputs:\n")
		for i, input := range t.SiafundInputs {
			parentIDString := crypto.Hash(input.ParentID).String()
			fmt.Fprintf(&str, "\t%d: %s\n", i, parentIDString)
		}
	}
	if len(t.SiafundOutputs) != 0 {
		fmt.Fprintf(&str, "SiafundOutputs:\n")
		for i := range t.SiafundOutputs {
			oidString := crypto.Hash(t.SiafundOutputID(uint64(i))).String()
			fmt.Fprintf(&str, "\t%d: %s\n", i, oidString)
		}
	}
	return str.String()
}
