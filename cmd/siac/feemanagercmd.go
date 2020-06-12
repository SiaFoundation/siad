package main

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	feeManagerCmd = &cobra.Command{
		Use:   "feemanager",
		Short: "View information about the FeeManager",
		Long:  "View information about the FeeManager such as pending fees and the next fee payout height",
		Run:   wrap(feemanagercmd),
	}

	feeManagerCancelFeeCmd = &cobra.Command{
		Use:   "cancel <feeUID>",
		Short: "Cancel a fee",
		Long:  "Cancel a pending fee. If a transaction has already been created the fee cannot be cancelled",
		Run:   wrap(feemanagercancelfeecmd),
	}
)

// feeInfo is a helper struct for gathering some information about the fees
type feeInfo struct {
	appUID      modules.AppUID
	fees        []modules.AppFee
	totalAmount types.Currency
}

// feemanagercmd prints out the basic information about the FeeManager and lists
// any pending fees
func feemanagercmd() {
	// Get the basic information about the FeeManager
	fmg, err := httpClient.FeeManagerGet()
	if err != nil {
		die(err)
	}

	// Get the pending fees
	pendingFees, err := httpClient.FeeManagerPendingFeesGet()
	if err != nil {
		die(err)
	}

	// Parse the pending fees
	fees, pendingTotal := parseFees(pendingFees.PendingFees)

	// Print out the high level information about the FeeManager
	fmt.Println("FeeManager")
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  Next FeePayoutHeight:\t%v\n", fmg.PayoutHeight)
	fmt.Fprintf(w, "  Number Pending Fees:\t%v\n", len(pendingFees.PendingFees))
	fmt.Fprintf(w, "  Total Amount Pending:\t%v\n", pendingTotal.HumanString())
	w.Flush()

	// Print Pending Fees
	if len(pendingFees.PendingFees) == 0 {
		fmt.Println("No Pending Fees")
		return
	}
	fmt.Fprintln(w, "\nPending Fees:")
	fmt.Fprintln(w, "  AppUID\tFeeUID\tAmount\tRecurring\tPayout Height\tTxn Created")
	for _, feeInfo := range fees {
		for _, fee := range feeInfo.fees {
			fmt.Fprintf(w, "  %v\t%v\t%v\t%v\t%v\t%v\n",
				fee.AppUID, fee.FeeUID, fee.Amount, fee.Recurring, fee.PayoutHeight, fee.TransactionCreated)
		}
	}
	w.Flush()

	// Check if verbose output was requested
	if !feeManagerVerbose {
		return
	}

	// Get the Paid Fees
	paidFees, err := httpClient.FeeManagerPaidFeesGet()
	if err != nil {
		die(err)
	}
	if len(paidFees.PaidFees) == 0 {
		fmt.Println("\nNo Paid Fees")
		return
	}

	// Parse the paid fees
	fees, paidTotal := parseFees(paidFees.PaidFees)

	// Print the paid fees
	fmt.Fprintln(w, "\nPaid Fees:")
	fmt.Fprintf(w, "  Total Amount Paid:\t%v\n", paidTotal.HumanString())
	fmt.Fprintln(w, "  AppUID\tFeeUID\tAmount\tPayout Height")
	for _, feeInfo := range fees {
		for _, fee := range feeInfo.fees {
			fmt.Fprintf(w, "  %v\t%v\t%v\t%v\n",
				fee.AppUID, fee.FeeUID, fee.Amount, fee.PayoutHeight)
		}
	}
	w.Flush()
}

// feemanagercancelfeecmd cancels a fee
func feemanagercancelfeecmd(feeUIDStr string) {
	feeUID := modules.FeeUID(feeUIDStr)
	err := httpClient.FeeManagerCancelPost(feeUID)
	if err != nil {
		die(err)
	}
	fmt.Println("Fee successfully cancelled")
}

// parseFees takes a slice of AppFess and returns a slice of feeInfos sorted by
// total amount by AppUID and amount per fee
func parseFees(fees []modules.AppFee) ([]feeInfo, types.Currency) {
	appToFeesMap := make(map[modules.AppUID]feeInfo)
	var totalAmount types.Currency

	// Create a map of the fees by AppUID
	for _, fee := range fees {
		// Grab the entry from the map or create it
		fi, ok := appToFeesMap[fee.AppUID]
		if !ok {
			fi = feeInfo{appUID: fee.AppUID}
		}

		// Update the totalAmount and the entry information
		totalAmount = totalAmount.Add(fee.Amount)
		fi.totalAmount = fi.totalAmount.Add(fee.Amount)
		fi.fees = append(fi.fees, fee)

		// Update Map
		appToFeesMap[fee.AppUID] = fi
	}

	// Covert map to slice for sorting
	var feeInfos []feeInfo
	for _, fi := range appToFeesMap {
		// Sort to slice of fees for each AppUID
		sort.Sort(byAmount(fi.fees))
		feeInfos = append(feeInfos, fi)
	}

	// Sort Slice and return
	sort.Sort(byTotalAmount(feeInfos))
	return feeInfos, totalAmount
}

// byAmount is an implementation of a sort interface to sort the Fees by amount
type byAmount []modules.AppFee

func (s byAmount) Len() int      { return len(s) }
func (s byAmount) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s byAmount) Less(i, j int) bool {
	cmp := s[i].Amount.Cmp(s[j].Amount)
	if cmp == 0 {
		return s[i].PayoutHeight > s[j].PayoutHeight
	}
	return cmp > 0
}

// byTotalAmount is an implementation of a sort interface to sort the feeInfo by
// totalAmount
type byTotalAmount []feeInfo

func (s byTotalAmount) Len() int      { return len(s) }
func (s byTotalAmount) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s byTotalAmount) Less(i, j int) bool {
	cmp := s[i].totalAmount.Cmp(s[j].totalAmount)
	return cmp > 0
}
