package main

// TODO: If you run siac from a non-existent directory, the abs() function does
// not handle this very gracefully.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"

	"github.com/spf13/cobra"
	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	renterAllowanceCancelCmd = &cobra.Command{
		Use:   "cancel",
		Short: "Cancel the current allowance",
		Long:  "Cancel the current allowance, which controls how much money is spent on file contracts.",
		Run:   wrap(renterallowancecancelcmd),
	}

	renterAllowanceCmd = &cobra.Command{
		Use:   "allowance",
		Short: "View the current allowance",
		Long:  "View the current allowance, which controls how much money is spent on file contracts.",
		Run:   wrap(renterallowancecmd),
	}

	renterBackupCreateCmd = &cobra.Command{
		Use:   "createbackup [path]",
		Short: "Create a backup of the renter's siafiles",
		Long:  "Create a backup of the renter's siafiles at the specified path",
		Run:   wrap(renterbackupcreatecmd),
	}

	renterBackupLoadCmd = &cobra.Command{
		Use:   "loadbackup [path]",
		Short: "Load a backup of the renter's siafiles",
		Long:  "Load a backup of the renter's siafiles from the specified path",
		Run:   wrap(renterbackuploadcmd),
	}

	renterCmd = &cobra.Command{
		Use:   "renter",
		Short: "Perform renter actions",
		Long:  "Upload, download, rename, delete, load, or share files.",
		Run:   wrap(rentercmd),
	}

	renterContractsCmd = &cobra.Command{
		Use:   "contracts",
		Short: "View the Renter's contracts",
		Long:  "View the contracts that the Renter has formed with hosts.",
		Run:   wrap(rentercontractscmd),
	}

	renterContractsRecoveryScanProgressCmd = &cobra.Command{
		Use:   "recoveryscanprogress",
		Short: "Returns the recovery scan progress.",
		Long:  "Returns the progress of a potentially ongoing recovery scan.",
		Run:   wrap(rentercontractrecoveryscanprogresscmd),
	}

	renterContractsViewCmd = &cobra.Command{
		Use:   "view [contract-id]",
		Short: "View details of the specified contract",
		Long:  "View all details available of the specified contract.",
		Run:   wrap(rentercontractsviewcmd),
	}

	renterDownloadsCmd = &cobra.Command{
		Use:   "downloads",
		Short: "View the download queue",
		Long:  "View the list of files currently downloading.",
		Run:   wrap(renterdownloadscmd),
	}

	renterFilesDeleteCmd = &cobra.Command{
		Use:     "delete [path]",
		Aliases: []string{"rm"},
		Short:   "Delete a file or folder",
		Long:    "Delete a file or folder. Does not delete the file/folder on disk.",
		Run:     wrap(renterfilesdeletecmd),
	}

	renterFilesDownloadCmd = &cobra.Command{
		Use:   "download [path] [destination]",
		Short: "Download a file or folder",
		Long:  "Download a previously-uploaded file or folder to a specified destination.",
		Run:   wrap(renterfilesdownloadcmd),
	}

	renterFilesListCmd = &cobra.Command{
		Use:   "ls [path]",
		Short: "List the status of all files within specified dir",
		Long:  "List the status of all files known to the renter within the specified folder on the Sia network. To query the root dir either '\"\"', '/' or '.' can be supplied",
		Run:   wrap(renterfileslistcmd),
	}

	renterFilesRenameCmd = &cobra.Command{
		Use:     "rename [path] [newpath]",
		Aliases: []string{"mv"},
		Short:   "Rename a file",
		Long:    "Rename a file.",
		Run:     wrap(renterfilesrenamecmd),
	}

	renterFilesUnstuckCmd = &cobra.Command{
		Use:   "unstuckall",
		Short: "Set all files to unstuck",
		Long:  "Set the 'stuck' status of every chunk in every file uploaded to the renter to 'false'.",
		Run:   wrap(renterfilesunstuckcmd),
	}

	renterFilesUploadCmd = &cobra.Command{
		Use:   "upload [source] [path]",
		Short: "Upload a file or folder",
		Long:  "Upload a file or folder to [path] on the Sia network.",
		Run:   wrap(renterfilesuploadcmd),
	}

	renterPricesCmd = &cobra.Command{
		Use:   "prices [amount] [period] [hosts] [renew window]",
		Short: "Display the price of storage and bandwidth",
		Long: `Display the estimated prices of storing files, retrieving files, and creating a set of contracts.

An allowance can be provided for a more accurate estimate, if no allowance is provided the current set allowance will be used,
and if no allowance is set an allowance of 500SC, 12w period, 50 hosts, and 4w renew window will be used.`,
		Run: renterpricescmd,
	}

	renterSetAllowanceCmd = &cobra.Command{
		Use:   "setallowance --amount [amount] --period [period] --hosts [hosts] --renew-window [renew window]",
		Short: "Set the allowance",
		Long: `Set the amount of money that can be spent over a given period.

Allowance can be automatically renewed periodically. If the current
blockheight + the renew window >= the end height the contract,
then the contract is renewed automatically.

Note that setting the allowance will cause siad to immediately begin forming
contracts! You should only set the allowance once you are fully synced and you
have a reasonable number (>30) of hosts in your hostdb.`,
		Run: rentersetallowancecmd,
	}

	renterTriggerContractRecoveryScanCmd = &cobra.Command{
		Use:   "triggerrecoveryscan",
		Short: "Triggers a recovery scan.",
		Long:  "Triggers a scan of the whole blockchain to find recoverable contracts.",
		Run:   wrap(rentertriggercontractrecoveryrescancmd),
	}

	renterUploadsCmd = &cobra.Command{
		Use:   "uploads",
		Short: "View the upload queue",
		Long:  "View the list of files currently uploading.",
		Run:   wrap(renteruploadscmd),
	}
)

// abs returns the absolute representation of a path.
// TODO: bad things can happen if you run siac from a non-existent directory.
// Implement some checks to catch this problem.
func abs(path string) string {
	abspath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abspath
}

// rentercmd displays the renter's financial metrics and high level renter info
func rentercmd() {
	rg, err := httpClient.RenterGet()
	if err != nil {
		die("Could not get renter info:", err)
	}
	fm := rg.FinancialMetrics
	totalSpent := fm.ContractFees.Add(fm.UploadSpending).
		Add(fm.DownloadSpending).Add(fm.StorageSpending)

	rf, err := httpClient.RenterGetDir(modules.RootSiaPath())
	if err != nil {
		die("Could not get renter files:", err)
	}
	rc, err := httpClient.RenterInactiveContractsGet()
	if err != nil {
		die("Could not get contracts:", err)
	}

	fmt.Printf(`Allowance:`)

	if rg.Settings.Allowance.Funds.IsZero() {
		fmt.Printf("      0 SC (No current allowance)\n")
	} else {
		fmt.Printf(`       %v
  Spent Funds:     %v
  Unspent Funds:   %v
`, currencyUnits(rg.Settings.Allowance.Funds),
			currencyUnits(totalSpent), currencyUnits(fm.Unspent))
	}
	fmt.Printf(`Total Files:    %v
Total Stored:   %v
Min Redundancy: %v
Contracts:      %v
`, rf.Directories[0].AggregateNumFiles, filesizeUnits(rf.Directories[0].AggregateSize), rf.Directories[0].MinRedundancy, len(rc.ActiveContracts))
}

// renteruploadscmd is the handler for the command `siac renter uploads`.
// Lists files currently uploading.
func renteruploadscmd() {
	rf, err := httpClient.RenterFilesGet(false)
	if err != nil {
		die("Could not get upload queue:", err)
	}

	// TODO: add a --history flag to the uploads command to mirror the --history
	//       flag in the downloads command. This hasn't been done yet because the
	//       call to /renter/files includes files that have been shared with you,
	//       not just files you've uploaded.

	// Filter out files that have been uploaded.
	var filteredFiles []modules.FileInfo
	for _, fi := range rf.Files {
		if !fi.Available {
			filteredFiles = append(filteredFiles, fi)
		}
	}
	if len(filteredFiles) == 0 {
		fmt.Println("No files are uploading.")
		return
	}
	fmt.Println("Uploading", len(filteredFiles), "files:")
	for _, file := range filteredFiles {
		fmt.Printf("%13s  %s (uploading, %0.2f%%)\n", filesizeUnits(file.Filesize), file.SiaPath, file.UploadProgress)
	}
}

// renterdownloadscmd is the handler for the command `siac renter downloads`.
// Lists files currently downloading, and optionally previously downloaded
// files if the -H or --history flag is specified.
func renterdownloadscmd() {
	queue, err := httpClient.RenterDownloadsGet()
	if err != nil {
		die("Could not get download queue:", err)
	}
	// Filter out files that have been downloaded.
	var downloading []api.DownloadInfo
	for _, file := range queue.Downloads {
		if !file.Completed {
			downloading = append(downloading, file)
		}
	}
	if len(downloading) == 0 {
		fmt.Println("No files are downloading.")
	} else {
		fmt.Println("Downloading", len(downloading), "files:")
		for _, file := range downloading {
			fmt.Printf("%s: %5.1f%% %s -> %s\n", file.StartTime.Format("Jan 02 03:04 PM"), 100*float64(file.Received)/float64(file.Filesize), file.SiaPath, file.Destination)
		}
	}
	if !renterShowHistory {
		return
	}
	fmt.Println()
	// Filter out files that are downloading.
	var downloaded []api.DownloadInfo
	for _, file := range queue.Downloads {
		if file.Completed {
			downloaded = append(downloaded, file)
		}
	}
	if len(downloaded) == 0 {
		fmt.Println("No files downloaded.")
	} else {
		fmt.Println("Downloaded", len(downloaded), "files:")
		for _, file := range downloaded {
			fmt.Printf("%s: %s -> %s\n", file.StartTime.Format("Jan 02 03:04 PM"), file.SiaPath, file.Destination)
		}
	}
}

// renterallowancecmd is the handler for the command `siac renter allowance`.
// displays the current allowance.
func renterallowancecmd() {
	rg, err := httpClient.RenterGet()
	if err != nil {
		die("Could not get allowance:", err)
	}
	allowance := rg.Settings.Allowance

	// Normalize the expectations over the period.
	allowance.ExpectedUpload *= uint64(allowance.Period)
	allowance.ExpectedDownload *= uint64(allowance.Period)

	// Show allowance info
	fmt.Printf(`Allowance:
	Amount:               %v
	Period:               %v blocks
	Renew Window:         %v blocks
	Hosts:                %v

Expectations for period:
	Expected Storage:     %v
	Expected Upload:      %v
	Expected Download:    %v
	Expected Redundancy:  %v
`, currencyUnits(allowance.Funds), allowance.Period, allowance.RenewWindow, allowance.Hosts, filesizeUnits(allowance.ExpectedStorage),
		filesizeUnits(allowance.ExpectedUpload), filesizeUnits(allowance.ExpectedDownload), allowance.ExpectedRedundancy)

	// Show spending detail
	fm := rg.FinancialMetrics
	totalSpent := fm.ContractFees.Add(fm.UploadSpending).
		Add(fm.DownloadSpending).Add(fm.StorageSpending)
	// Calculate unspent allocated
	unspentAllocated := types.ZeroCurrency
	if fm.TotalAllocated.Cmp(totalSpent) >= 0 {
		unspentAllocated = fm.TotalAllocated.Sub(totalSpent)
	}
	// Calculate unspent unallocated
	unspentUnallocated := types.ZeroCurrency
	if fm.Unspent.Cmp(unspentAllocated) >= 0 {
		unspentUnallocated = fm.Unspent.Sub(unspentAllocated)
	}

	fmt.Printf(`
Spending:
  Current Period Spending:`)

	if rg.Settings.Allowance.Funds.IsZero() {
		fmt.Printf("\n    No current period spending.\n")
	} else {
		fmt.Printf(`
    Spent Funds:     %v
      Storage:       %v
      Upload:        %v
      Download:      %v
      Fees:          %v
    Unspent Funds:   %v
      Allocated:     %v
      Unallocated:   %v
`, currencyUnits(totalSpent), currencyUnits(fm.StorageSpending),
			currencyUnits(fm.UploadSpending), currencyUnits(fm.DownloadSpending),
			currencyUnits(fm.ContractFees), currencyUnits(fm.Unspent),
			currencyUnits(unspentAllocated), currencyUnits(unspentUnallocated))
	}

	fmt.Printf("\n  Previous Spending:")
	if fm.PreviousSpending.IsZero() && fm.WithheldFunds.IsZero() {
		fmt.Printf("\n    No previous spending.\n\n")
	} else {
		fmt.Printf(` %v
    Withheld Funds:  %v
    Release Block:   %v

`, currencyUnits(fm.PreviousSpending), currencyUnits(fm.WithheldFunds), fm.ReleaseBlock)
	}
}

// renterallowancecancelcmd is the handler for `siac renter allowance cancel`.
// cancels the current allowance.
func renterallowancecancelcmd() {
	fmt.Println(`Canceling your allowance will disable uploading new files,
repairing existing files, and renewing existing files. All files will cease
to be accessible after a short period of time.`)
again:
	fmt.Print("Do you want to continue? [y/n] ")
	var resp string
	fmt.Scanln(&resp)
	switch strings.ToLower(resp) {
	case "y", "yes":
		// continue below
	case "n", "no":
		return
	default:
		goto again
	}
	err := httpClient.RenterCancelAllowance()
	if err != nil {
		die("error canceling allowance:", err)
	}
	fmt.Println("Allowance canceled.")
}

// rentersetallowancecmd is the handler for `siac renter setallowance`.
// set the allowance or modify individual allowance fields.
func rentersetallowancecmd(cmd *cobra.Command, args []string) {
	req := httpClient.RenterPostPartialAllowance()
	changedFields := 0

	// Get the current period setting.
	rg, err := httpClient.RenterGet()
	if err != nil {
		die("Could not get renter settings")
	}
	period := rg.Settings.Allowance.Period

	// parse funds
	if allowanceFunds != "" {
		hastings, err := parseCurrency(allowanceFunds)
		if err != nil {
			die("Could not parse amount:", err)
		}
		var funds types.Currency
		_, err = fmt.Sscan(hastings, &funds)
		if err != nil {
			die("Could not parse amount:", err)
		}
		req = req.WithFunds(funds)
		changedFields++
	}
	// parse period
	if allowancePeriod != "" {
		blocks, err := parsePeriod(allowancePeriod)
		if err != nil {
			die("Could not parse period:", err)
		}
		_, err = fmt.Sscan(blocks, &period)
		if err != nil {
			die("Could not parse period:", err)
		}
		req = req.WithPeriod(period)
		changedFields++
	}
	// parse hosts
	if allowanceHosts != "" {
		hosts, err := strconv.Atoi(allowanceHosts)
		if err != nil {
			die("Could not parse host count")
		}
		req = req.WithHosts(uint64(hosts))
		changedFields++
	}
	// parse renewWindow
	if allowanceRenewWindow != "" {
		rw, err := parsePeriod(allowanceRenewWindow)
		if err != nil {
			die("Could not parse renew window")
		}
		var renewWindow types.BlockHeight
		_, err = fmt.Sscan(rw, &renewWindow)
		if err != nil {
			die("Could not parse renew window:", err)
		}
		req = req.WithRenewWindow(renewWindow)
		changedFields++
	}
	// parse expectedStorage
	if allowanceExpectedStorage != "" {
		es, err := parseFilesize(allowanceExpectedStorage)
		if err != nil {
			die("Could not parse expected storage")
		}
		var expectedStorage uint64
		_, err = fmt.Sscan(es, &expectedStorage)
		if err != nil {
			die("Could not parse expected storage")
		}
		req = req.WithExpectedStorage(expectedStorage)
		changedFields++
	}
	// parse expectedUpload
	if allowanceExpectedUpload != "" {
		eu, err := parseFilesize(allowanceExpectedUpload)
		if err != nil {
			die("Could not parse expected upload")
		}
		var expectedUpload uint64
		_, err = fmt.Sscan(eu, &expectedUpload)
		if err != nil {
			die("Could not parse expected upload")
		}
		req = req.WithExpectedUpload(expectedUpload / uint64(period))
		changedFields++
	}
	// parse expectedDownload
	if allowanceExpectedDownload != "" {
		ed, err := parseFilesize(allowanceExpectedDownload)
		if err != nil {
			die("Could not parse expected download")
		}
		var expectedDownload uint64
		_, err = fmt.Sscan(ed, &expectedDownload)
		if err != nil {
			die("Could not parse expected download")
		}
		req = req.WithExpectedDownload(expectedDownload / uint64(period))
		changedFields++
	}
	// parse expectedRedundancy
	if allowanceExpectedRedundancy != "" {
		er, err := parseFilesize(allowanceExpectedRedundancy)
		if err != nil {
			die("Could not parse expected redundancy")
		}
		var expectedRedundancy float64
		_, err = fmt.Sscan(er, &expectedRedundancy)
		if err != nil {
			die("Could not parse expected redundancy")
		}
		req = req.WithExpectedRedundancy(expectedRedundancy)
		changedFields++
	}
	// check if any fields were updated.
	if changedFields == 0 {
		fmt.Println("No flags specified. Allowance not updated.")
		return
	}
	if err := req.Send(); err != nil {
		die("Could not set allowance:", err)
	}
	fmt.Printf("Allowance updated. %v setting(s) changed.\n", changedFields)
}

// byValue sorts contracts by their value in siacoins, high to low. If two
// contracts have the same value, they are sorted by their host's address.
type byValue []api.RenterContract

func (s byValue) Len() int      { return len(s) }
func (s byValue) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s byValue) Less(i, j int) bool {
	cmp := s[i].RenterFunds.Cmp(s[j].RenterFunds)
	if cmp == 0 {
		return s[i].NetAddress < s[j].NetAddress
	}
	return cmp > 0
}

// renterbackcreatecmd is the handler for the command `siac renter
// createbackup`.
func renterbackupcreatecmd(path string) {
	path = abs(path)
	// If the destination is a folder, create the backup in the folder.
	fi, err := os.Stat(path)
	if err == nil && fi.IsDir() {
		path = filepath.Join(path, fmt.Sprintf("%v.backup", time.Now().Unix()))
	}
	// Create backup.
	err = httpClient.RenterCreateBackupPost(path)
	if err != nil {
		die("Failed to create backup", err)
	}
}

// renterbackloadcmd is the handler for the command `siac renter
// loadbackup`.
func renterbackuploadcmd(path string) {
	path = abs(path)

	err := httpClient.RenterRecoverBackupPost(path)
	if err != nil {
		die("Failed to load backup", err)
	}
}

// rentercontractscmd is the handler for the comand `siac renter contracts`.
// It lists the Renter's contracts.
func rentercontractscmd() {
	rc, err := httpClient.RenterInactiveContractsGet()
	if err != nil {
		die("Could not get contracts:", err)
	}

	fmt.Println("Active Contracts:")
	if len(rc.ActiveContracts) == 0 {
		fmt.Println("  No active contracts.")
	} else {
		// Display Active Contracts
		sort.Sort(byValue(rc.ActiveContracts))
		var activeTotalStored uint64
		var activeTotalRemaining, activeTotalSpent, activeTotalFees types.Currency
		for _, c := range rc.ActiveContracts {
			activeTotalStored += c.Size
			activeTotalRemaining = activeTotalRemaining.Add(c.RenterFunds)
			activeTotalSpent = activeTotalSpent.Add(c.TotalCost.Sub(c.RenterFunds).Sub(c.Fees))
			activeTotalFees = activeTotalFees.Add(c.Fees)
		}
		fmt.Printf(`  Number of Contracts:  %v
  Total stored:         %s
  Total Remaining:      %v
  Total Spent:          %v
  Total Fees:           %v

`, len(rc.ActiveContracts), filesizeUnits(activeTotalStored),
			currencyUnits(activeTotalRemaining), currencyUnits(activeTotalSpent), currencyUnits(activeTotalFees))
		w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  Host\tHost Version\tRemaining Funds\tSpent Funds\tSpent Fees\tData\tEnd Height\tID\tGoodForUpload\tGoodForRenew")
		for _, c := range rc.ActiveContracts {
			address := c.NetAddress
			hostVersion := c.HostVersion
			if address == "" {
				address = "Host Removed"
				hostVersion = ""
			}
			fmt.Fprintf(w, "  %v\t%v\t%8s\t%8s\t%8s\t%v\t%v\t%v\t%v\t%v\n",
				address,
				hostVersion,
				currencyUnits(c.RenterFunds),
				currencyUnits(c.TotalCost.Sub(c.RenterFunds).Sub(c.Fees)),
				currencyUnits(c.Fees),
				filesizeUnits(c.Size),
				c.EndHeight,
				c.ID,
				c.GoodForUpload,
				c.GoodForRenew)
		}
		w.Flush()
	}

	fmt.Println("\nInactive Contracts:")
	if len(rc.InactiveContracts) == 0 {
		fmt.Println("  No inactive contracts.")
	} else {
		// Display Inactive Contracts
		sort.Sort(byValue(rc.InactiveContracts))
		var inactiveTotalStored uint64
		var inactiveTotalRemaining, inactiveTotalSpent, inactiveTotalFees types.Currency
		for _, c := range rc.InactiveContracts {
			inactiveTotalStored += c.Size
			inactiveTotalRemaining = inactiveTotalRemaining.Add(c.RenterFunds)
			inactiveTotalSpent = inactiveTotalSpent.Add(c.TotalCost.Sub(c.RenterFunds).Sub(c.Fees))
			inactiveTotalFees = inactiveTotalFees.Add(c.Fees)
		}

		fmt.Printf(`
  Number of Contracts:  %v
  Total stored:         %s
  Total Remaining:      %v
  Total Spent:          %v
  Total Fees:           %v

`, len(rc.InactiveContracts), filesizeUnits(inactiveTotalStored), currencyUnits(inactiveTotalRemaining), currencyUnits(inactiveTotalSpent), currencyUnits(inactiveTotalFees))
		w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  Host\tHost Version\tRemaining Funds\tSpent Funds\tSpent Fees\tData\tEnd Height\tID\tGoodForUpload\tGoodForRenew")
		for _, c := range rc.InactiveContracts {
			address := c.NetAddress
			hostVersion := c.HostVersion
			if address == "" {
				address = "Host Removed"
				hostVersion = ""
			}
			fmt.Fprintf(w, "  %v\t%v\t%8s\t%8s\t%8s\t%v\t%v\t%v\t%v\t%v\n",
				address,
				hostVersion,
				currencyUnits(c.RenterFunds),
				currencyUnits(c.TotalCost.Sub(c.RenterFunds).Sub(c.Fees)),
				currencyUnits(c.Fees),
				filesizeUnits(c.Size),
				c.EndHeight,
				c.ID,
				c.GoodForUpload,
				c.GoodForRenew)
		}
		w.Flush()
	}

	if renterAllContracts {
		fmt.Println("\nExpired Contracts:")
		rce, err := httpClient.RenterExpiredContractsGet()
		if err != nil {
			die("Could not get expired contracts:", err)
		}
		if len(rce.ExpiredContracts) == 0 {
			fmt.Println("  No expired contracts.")
		} else {
			sort.Sort(byValue(rce.ExpiredContracts))
			var expiredTotalStored uint64
			var expiredTotalWithheld, expiredTotalSpent, expiredTotalFees types.Currency
			for _, c := range rce.ExpiredContracts {
				expiredTotalStored += c.Size
				expiredTotalWithheld = expiredTotalWithheld.Add(c.RenterFunds)
				expiredTotalSpent = expiredTotalSpent.Add(c.TotalCost.Sub(c.RenterFunds).Sub(c.Fees))
				expiredTotalFees = expiredTotalFees.Add(c.Fees)
			}
			fmt.Printf(`
	Number of Contracts:  %v
	Total stored:         %9s
	Total Remaining:      %v
	Total Spent:          %v
	Total Fees:           %v
			
	`, len(rce.ExpiredContracts), filesizeUnits(expiredTotalStored), currencyUnits(expiredTotalWithheld), currencyUnits(expiredTotalSpent), currencyUnits(expiredTotalFees))
			w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
			fmt.Fprintln(w, "  Host\tHost Version\tWithheld Funds\tSpent Funds\tSpent Fees\tData\tEnd Height\tID\tGoodForUpload\tGoodForRenew")
			for _, c := range rce.ExpiredContracts {
				address := c.NetAddress
				hostVersion := c.HostVersion
				if address == "" {
					address = "Host Removed"
					hostVersion = ""
				}
				fmt.Fprintf(w, "  %v\t%v\t%8s\t%8s\t%8s\t%v\t%v\t%v\t%v\t%v\n",
					address,
					hostVersion,
					currencyUnits(c.RenterFunds),
					currencyUnits(c.TotalCost.Sub(c.RenterFunds).Sub(c.Fees)),
					currencyUnits(c.Fees),
					filesizeUnits(c.Size),
					c.EndHeight,
					c.ID,
					c.GoodForUpload,
					c.GoodForRenew)
			}
			w.Flush()
		}
	}
}

// rentercontractsviewcmd is the handler for the command `siac renter contracts <id>`.
// It lists details of a specific contract.
func rentercontractsviewcmd(cid string) {
	rc, err := httpClient.RenterInactiveContractsGet()
	if err != nil {
		die("Could not get contract details: ", err)
	}
	rce, err := httpClient.RenterExpiredContractsGet()
	if err != nil {
		die("Could not get expired contract details: ", err)
	}

	contracts := append(rc.ActiveContracts, rc.InactiveContracts...)
	contracts = append(contracts, rce.ExpiredContracts...)

	for _, rc := range contracts {
		if rc.ID.String() == cid {
			hostInfo, err := httpClient.HostDbHostsGet(rc.HostPublicKey)
			if err != nil {
				die("Could not fetch details of host: ", err)
			}
			fmt.Printf(`
Contract %v
	Host: %v (Public Key: %v)
	Host Version: %v

  Start Height: %v
  End Height:   %v

  Total cost:        %v (Fees: %v)
  Funds Allocated:   %v
  Upload Spending:   %v
  Storage Spending:  %v
  Download Spending: %v
  Remaining Funds:   %v

  File Size: %v
`, rc.ID, rc.NetAddress, rc.HostVersion, rc.HostPublicKey.String(), rc.StartHeight, rc.EndHeight,
				currencyUnits(rc.TotalCost),
				currencyUnits(rc.Fees),
				currencyUnits(rc.TotalCost.Sub(rc.Fees)),
				currencyUnits(rc.UploadSpending),
				currencyUnits(rc.StorageSpending),
				currencyUnits(rc.DownloadSpending),
				currencyUnits(rc.RenterFunds),
				filesizeUnits(rc.Size))

			printScoreBreakdown(&hostInfo)
			return
		}
	}

	fmt.Println("Contract not found")
}

// downloadDir downloads the dir at the specified siaPath to the specified
// location. It returns all the files for which a download was initialized as
// tracked files and the ones which were ignored as skipped. Errors are composed
// into a single error.
func downloadDir(siaPath modules.SiaPath, destination string) (tfs []trackedFile, skipped []string, err error) {
	// Get dir info.
	rd, err := httpClient.RenterGetDir(siaPath)
	if err != nil {
		err = errors.AddContext(err, "failed to get dir info")
		return
	}
	// Create destination on disk.
	if err = os.MkdirAll(destination, 0755); err != nil {
		err = errors.AddContext(err, "failed to create destination dir")
		return
	}
	// Download files.
	for _, file := range rd.Files {
		// Skip files that already exist.
		dst := filepath.Join(destination, file.SiaPath.Name())
		if _, err = os.Stat(dst); err == nil {
			skipped = append(skipped, dst)
			continue
		} else if !os.IsNotExist(err) {
			err = errors.AddContext(err, "failed to get file stats")
			return
		}
		// Download file.
		err = httpClient.RenterDownloadFullGet(file.SiaPath, dst, true)
		if err != nil {
			err = errors.AddContext(err, "Failed to start download")
			return
		}
		// Append file to tracked files.
		tfs = append(tfs, trackedFile{
			siaPath: file.SiaPath,
			dst:     dst,
		})
	}
	// If the download isn't recursive we are done.
	if !renterDownloadRecursive {
		return
	}
	// Call downloadDir on all subdirs.
	for i := 1; i < len(rd.Directories); i++ {
		subDir := rd.Directories[i]
		rtfs, rskipped, rerr := downloadDir(subDir.SiaPath, filepath.Join(destination, subDir.SiaPath.Name()))
		tfs = append(tfs, rtfs...)
		skipped = append(skipped, rskipped...)
		err = errors.Compose(err, rerr)
	}
	return
}

// renterfilesdownload downloads the dir at the given path from the Sia network
// to the local specified destination.
func renterdirdownload(path, destination string) {
	destination = abs(destination)
	// Parse SiaPath.
	siaPath, err := modules.NewSiaPath(path)
	if err != nil {
		die("Failed to parse SiaPath:", err)
	}
	// Download dir.
	tfs, skipped, downloadErr := downloadDir(siaPath, destination)
	if renterDownloadAsync && downloadErr != nil {
		fmt.Println("At least one error occured when initializing the download:", downloadErr)
	}
	// If the download is async, report success.
	if renterDownloadAsync {
		fmt.Printf("Queued Download '%s' to %s.\n", siaPath.String(), abs(destination))
		return
	}
	// If the download is blocking, display progress as the file downloads.
	failedDownloads := downloadprogress(tfs)
	// Print skipped files.
	for _, s := range skipped {
		fmt.Printf("Skipped file '%v' since it already exists\n", s)
	}
	// Handle potential errors.
	if len(failedDownloads) == 0 {
		fmt.Printf("\nDownloaded '%s' to '%s'.\n", path, abs(destination))
		os.Exit(0)
	}
	// Print errors.
	if downloadErr != nil {
		fmt.Println("At least one error occured when initializing the download:", downloadErr)
	}
	for _, fd := range failedDownloads {
		fmt.Printf("Download of file '%v' to destination '%v' failed: %v\n", fd.SiaPath, fd.Destination, fd.Error)
	}
	os.Exit(1)
}

// renterfilesdeletecmd is the handler for the command `siac renter delete [path]`.
// Removes the specified path from the Sia network.
func renterfilesdeletecmd(path string) {
	// Parse SiaPath.
	siaPath, err := modules.NewSiaPath(path)
	if err != nil {
		die("Couldn't parse SiaPath:", err)
	}
	// Try to delete file.
	errFile := httpClient.RenterDeletePost(siaPath)
	if errFile == nil {
		fmt.Printf("Deleted file '%v'\n", path)
		return
	} else if !strings.Contains(errFile.Error(), siafile.ErrUnknownPath.Error()) {
		die(fmt.Sprintf("Failed to delete file %v: %v", path, errFile))
	}
	// Try to delete folder.
	errDir := httpClient.RenterDirDeletePost(siaPath)
	if errDir == nil {
		fmt.Printf("Deleted directory '%v'\n", path)
		return
	} else if !strings.Contains(errDir.Error(), siadir.ErrUnknownPath.Error()) {
		die(fmt.Sprintf("Failed to delete directory %v: %v", path, errDir))
	}
	// Unknown file/folder.
	die(fmt.Sprintf("Unknown path '%v'", path))
}

// renterfilesdownload is the handler for the comand `siac renter download [path] [destination]`.
// It determines whether a file or a folder is downloaded and calls the corresponding sub-handler.
func renterfilesdownloadcmd(path, destination string) {
	// Parse SiaPath.
	siaPath, err := modules.NewSiaPath(path)
	if err != nil {
		die("Couldn't parse SiaPath:", err)
	}
	_, err = httpClient.RenterFileGet(siaPath)
	if err == nil {
		renterfilesdownload(path, destination)
		return
	} else if !strings.Contains(err.Error(), siafile.ErrUnknownPath.Error()) {
		die("Failed to download file:", err)
	}
	_, err = httpClient.RenterGetDir(siaPath)
	if err == nil {
		renterdirdownload(path, destination)
		return
	} else if !strings.Contains(err.Error(), siadir.ErrUnknownPath.Error()) {
		die("Failed to download folder:", err)
	}
	die(fmt.Sprintf("Unknown file '%v'", path))
}

// renterfilesdownload downloads the file at the specified path from the Sia
// network to the local specified destination.
func renterfilesdownload(path, destination string) {
	destination = abs(destination)
	// Parse SiaPath.
	siaPath, err := modules.NewSiaPath(path)
	if err != nil {
		die("Couldn't parse SiaPath:", err)
	}
	// If the destination is a folder, download the file to that folder.
	fi, err := os.Stat(destination)
	if err == nil && fi.IsDir() {
		destination = filepath.Join(destination, siaPath.Name())
	}
	// Queue the download. An error will be returned if the queueing failed, but
	// the call will return before the download has completed. The call is made
	// as an async call.
	err = httpClient.RenterDownloadFullGet(siaPath, destination, true)
	if err != nil {
		die("Download could not be started:", err)
	}

	// If the download is async, report success.
	if renterDownloadAsync {
		fmt.Printf("Queued Download '%s' to %s.\n", siaPath.String(), abs(destination))
		return
	}

	// If the download is blocking, display progress as the file downloads.
	failedDownloads := downloadprogress([]trackedFile{{siaPath: siaPath, dst: destination}})
	if len(failedDownloads) > 0 {
		die("\nDownload could not be completed:", failedDownloads[0].Error)
	}
	fmt.Printf("\nDownloaded '%s' to '%s'.\n", path, abs(destination))
}

// rentertriggercontractrecoveryrescancmd starts a new scan for recoverable
// contracts on the blockchain.
func rentertriggercontractrecoveryrescancmd() {
	crpg, err := httpClient.RenterContractRecoveryProgressGet()
	if err != nil {
		die("Failed to get recovery status", err)
	}
	if crpg.ScanInProgress {
		fmt.Println("Scan already in progress")
		fmt.Println("Scanned height:\t", crpg.ScannedHeight)
		return
	}
	if err := httpClient.RenterInitContractRecoveryScanPost(); err != nil {
		die("Failed to trigger recovery scan", err)
	}
	fmt.Println("Successfully triggered contract recovery scan.")
}

// rentercontractrecoveryscanprogresscmd returns the current progress of a
// potentially ongoing recovery scan.
func rentercontractrecoveryscanprogresscmd() {
	crpg, err := httpClient.RenterContractRecoveryProgressGet()
	if err != nil {
		die("Failed to get recovery status", err)
	}
	if crpg.ScanInProgress {
		fmt.Println("Scan in progress")
		fmt.Println("Scanned height:\t", crpg.ScannedHeight)
	} else {
		fmt.Println("No scan in progress")
	}
}

// bandwidthUnit takes bps (bits per second) as an argument and converts
// them into a more human-readable string with a unit.
func bandwidthUnit(bps uint64) string {
	units := []string{"Bps", "Kbps", "Mbps", "Gbps", "Tbps", "Pbps", "Ebps", "Zbps", "Ybps"}
	mag := uint64(1)
	unit := ""
	for _, unit = range units {
		if bps < 1e3*mag {
			break
		} else if unit != units[len(units)-1] {
			// don't want to perform this multiply on the last iter; that
			// would give us 1.235 Ybps instead of 1235 Ybps
			mag *= 1e3
		}

	}
	return fmt.Sprintf("%.2f %s", float64(bps)/float64(mag), unit)
}

type trackedFile struct {
	siaPath modules.SiaPath
	dst     string
}

// helper type used for measurements.
type measurement struct {
	progress uint64
	time     time.Time
}

// downloadprogress will display the progress of the provided files and return a
// slice of DownloadInfos for failed downloads.
func downloadprogress(tfs []trackedFile) []api.DownloadInfo {
	// Nothing to do if no files are tracked.
	if len(tfs) == 0 {
		return nil
	}
	start := time.Now()

	// Create a map of all tracked files for faster lookups and also a measurement
	// map which is initialized with 0 progress for all tracked files.
	tfsMap := make(map[modules.SiaPath]trackedFile)
	measurements := make(map[modules.SiaPath][]measurement)
	for _, tf := range tfs {
		tfsMap[tf.siaPath] = tf
		measurements[tf.siaPath] = []measurement{{
			progress: 0,
			time:     time.Now(),
		}}
	}
	// Periodically print measurements until download is done.
	completed := make(map[string]struct{})
	errMap := make(map[string]api.DownloadInfo)
	for range time.Tick(OutputRefreshRate) {
		// Clear terminal.
		fmt.Print("\033[H\033[2J")
		// Get the list of downloads.
		rdg, err := httpClient.RenterDownloadsGet()
		if err != nil {
			continue // benign
		}
		// Create a map of downloads for faster lookups. To get unique keys we use
		// siaPath + destination as the key.
		queue := make(map[string]api.DownloadInfo)
		for _, d := range rdg.Downloads {
			key := d.SiaPath.String() + d.Destination
			if _, exists := queue[key]; !exists {
				queue[key] = d
			}
		}
		// Take new measurements for each tracked file.
		for tfIdx, tf := range tfs {
			// Search for the download in the list of downloads.
			mapKey := tf.siaPath.String() + tf.dst
			d, found := queue[mapKey]
			m, exists := measurements[tf.siaPath]
			if !exists {
				die("Measurement missing for tracked file. This should never happen.")
			}
			// If the download has not appeared in the queue yet, either continue or
			// give up.
			if !found {
				if time.Since(start) > RenterDownloadTimeout {
					die("Unable to find download in queue. This should never happen.")
				}
				continue
			}
			// Check whether the file has completed or otherwise errored out.
			if d.Error != "" {
				errMap[mapKey] = d
			}
			if d.Completed {
				completed[mapKey] = struct{}{}
				// Check if all downloads are done.
				if len(completed) == len(tfs) {
					return nil
				}
				continue
			}
			// Add the current progress to the measurements.
			m = append(m, measurement{
				progress: d.Received,
				time:     time.Now(),
			})
			// Shrink the measurements to only contain measurements from within the
			// SpeedEstimationWindow.
			for len(m) > 2 && m[len(m)-1].time.Sub(m[0].time) > SpeedEstimationWindow {
				m = m[1:]
			}
			// Update measurements in the map.
			measurements[tf.siaPath] = m
			// Compute the progress and timespan between the first and last
			// measurement to get the speed.
			received := float64(m[len(m)-1].progress - m[0].progress)
			timespan := m[len(m)-1].time.Sub(m[0].time)
			speed := bandwidthUnit(uint64((received * 8) / timespan.Seconds()))

			// Compuate the percentage of completion and time elapsed since the
			// start of the download.
			pct := 100 * float64(d.Received) / float64(d.Filesize)
			elapsed := time.Since(d.StartTime)
			elapsed -= elapsed % time.Second // round to nearest second

			progressStr := fmt.Sprintf("Downloading %v... %5.1f%% of %v, %v elapsed, %s    ", tf.siaPath.String(), pct, filesizeUnits(d.Filesize), elapsed, speed)
			if tfIdx < len(tfs)-1 {
				fmt.Println(progressStr)
			} else {
				fmt.Print(progressStr)
			}
		}
	}
	// This code is unreachable, but the compiler requires this to be here.
	return nil
}

// bySiaPathFile implements sort.Interface for [] modules.FileInfo based on the
// SiaPath field.
type bySiaPathFile []modules.FileInfo

func (s bySiaPathFile) Len() int           { return len(s) }
func (s bySiaPathFile) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s bySiaPathFile) Less(i, j int) bool { return s[i].SiaPath.String() < s[j].SiaPath.String() }

// bySiaPathDir implements sort.Interface for [] modules.DirectoryInfo based on the
// SiaPath field.
type bySiaPathDir []modules.DirectoryInfo

func (s bySiaPathDir) Len() int           { return len(s) }
func (s bySiaPathDir) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s bySiaPathDir) Less(i, j int) bool { return s[i].SiaPath.String() < s[j].SiaPath.String() }

type directoryInfo struct {
	dir     modules.DirectoryInfo
	files   []modules.FileInfo
	subDirs []modules.DirectoryInfo
}

// byDirectoryInfo implements sort.Interface for []directoryInfo based on the
// SiaPath field.
type byDirectoryInfo []directoryInfo

func (s byDirectoryInfo) Len() int      { return len(s) }
func (s byDirectoryInfo) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s byDirectoryInfo) Less(i, j int) bool {
	return s[i].dir.SiaPath.String() < s[j].dir.SiaPath.String()
}

// getDir returns the directory info for the directory at siaPath and its
// subdirs.
func getDir(siaPath modules.SiaPath) (dirs []directoryInfo) {
	rgd, err := httpClient.RenterGetDir(siaPath)
	if err != nil && !strings.Contains(err.Error(), siadir.ErrUnknownPath.Error()) {
		die("failed to get dir info:", err)
	}
	dir := rgd.Directories[0]
	subDirs := rgd.Directories[1:]

	// Append directory to dirs.
	dirs = append(dirs, directoryInfo{
		dir:     dir,
		files:   rgd.Files,
		subDirs: subDirs,
	})

	// If -R isn't set we are done.
	if !renterListRecursive {
		return
	}
	// Call getDir on subdirs.
	for _, subDir := range subDirs {
		rdirs := getDir(subDir.SiaPath)
		dirs = append(dirs, rdirs...)
	}
	return
}

// renterfileslistcmd is the handler for the command `siac renter list`.
// Lists files known to the renter on the network.
func renterfileslistcmd(path string) {
	var sp modules.SiaPath
	var err error
	if path == "." || path == "" || path == "/" {
		sp = modules.RootSiaPath()
	} else {
		sp, err = modules.NewSiaPath(path)
		if err != nil {
			die("could not parse siapath:", err)
		}
	}
	// Get dirs with their corresponding files.
	dirs := getDir(sp)
	numFiles := 0
	var totalStored uint64
	for _, dir := range dirs {
		for _, file := range dir.files {
			totalStored += file.Filesize
		}
		numFiles += len(dir.files)
	}
	if numFiles+len(dirs) <= 1 {
		fmt.Println("No files/dirs have been uploaded.")
		return
	}
	fmt.Printf("\nListing %v files/dirs:", numFiles+len(dirs)-1)
	fmt.Printf(" %9s\n", filesizeUnits(totalStored))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if renterListVerbose {
		fmt.Fprintln(w, "  Name\tFile size\tAvailable\tUploaded\tProgress\tRedundancy\tHealth\tStuck\tRenewing\tOn Disk\tRecoverable")
	}
	sort.Sort(byDirectoryInfo(dirs))
	// Print dirs.
	for _, dir := range dirs {
		fmt.Fprintf(w, "%v/\t\t\t\t\t\t\t\t\t\t\n", dir.dir.SiaPath)
		// Print subdirs.
		sort.Sort(bySiaPathDir(dir.subDirs))
		for _, subDir := range dir.subDirs {
			fmt.Fprintf(w, "  %v/\t\t\t\t\t\t\t\t\t\t\n", subDir.SiaPath.Name())
		}

		// Print files.
		sort.Sort(bySiaPathFile(dir.files))
		for _, file := range dir.files {
			name := file.SiaPath.Name()
			fmt.Fprintf(w, "  %s", name)
			fmt.Fprintf(w, "\t%9s", filesizeUnits(file.Filesize))
			if renterListVerbose {
				availableStr := yesNo(file.Available)
				renewingStr := yesNo(file.Renewing)
				redundancyStr := fmt.Sprintf("%.2f", file.Redundancy)
				if file.Redundancy == -1 {
					redundancyStr = "-"
				}
				healthStr := fmt.Sprintf("%.2f%%", file.MaxHealthPercent)
				uploadProgressStr := fmt.Sprintf("%.2f%%", file.UploadProgress)
				if file.UploadProgress == -1 {
					uploadProgressStr = "-"
				}
				onDiskStr := yesNo(file.OnDisk)
				recoverableStr := yesNo(file.Recoverable)
				stuckStr := yesNo(file.Stuck)
				fmt.Fprintf(w, "\t%s\t%9s\t%8s\t%10s\t%6s\t%s\t%s\t%s\t%s", availableStr, filesizeUnits(file.UploadedBytes), uploadProgressStr, redundancyStr, healthStr, stuckStr, renewingStr, onDiskStr, recoverableStr)
			}
			if !renterListVerbose && !file.Available {
				fmt.Fprintf(w, " (uploading, %0.2f%%)", file.UploadProgress)
			}
			fmt.Fprintln(w, "\t\t\t\t\t\t\t\t\t\t")
		}
		fmt.Fprintln(w, "\t\t\t\t\t\t\t\t\t\t")
	}
	w.Flush()
}

// renterfilesrenamecmd is the handler for the command `siac renter rename [path] [newpath]`.
// Renames a file on the Sia network.
func renterfilesrenamecmd(path, newpath string) {
	// Parse SiaPath.
	siaPath, err1 := modules.NewSiaPath(path)
	newSiaPath, err2 := modules.NewSiaPath(newpath)
	if err := errors.Compose(err1, err2); err != nil {
		die("Couldn't parse SiaPath:", err)
	}
	err := httpClient.RenterRenamePost(siaPath, newSiaPath)
	if err != nil {
		die("Could not rename file:", err)
	}
	fmt.Printf("Renamed %s to %s\n", path, newpath)
}

// renterfilesunstuckcmd is the handler for the command `siac renter
// unstuckall`. Sets all files to unstuck.
func renterfilesunstuckcmd() {
	rfg, err := httpClient.RenterFilesGet(false)
	if err != nil {
		die("Couldn't get list of all files:", err)
	}
	for _, f := range rfg.Files {
		err = httpClient.RenterSetFileStuckPost(f.SiaPath, false)
		if err != nil {
			die(fmt.Sprintf("Couldn't set %v to unstuck: %v", f.SiaPath, err))
		}
	}
	fmt.Printf("Set all files to 'unstuck'")
}

// renterfilesuploadcmd is the handler for the command `siac renter upload
// [source] [path]`. Uploads the [source] file to [path] on the Sia network.
// If [source] is a directory, all files inside it will be uploaded and named
// relative to [path].
func renterfilesuploadcmd(source, path string) {
	stat, err := os.Stat(source)
	if err != nil {
		die("Could not stat file or folder:", err)
	}

	if stat.IsDir() {
		// folder
		var files []string
		err := filepath.Walk(source, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				fmt.Println("Warning: skipping file:", err)
				return nil
			}
			if info.IsDir() {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			die("Could not read folder:", err)
		} else if len(files) == 0 {
			die("Nothing to upload.")
		}
		failed := 0
		for _, file := range files {
			fpath, _ := filepath.Rel(source, file)
			fpath = filepath.Join(path, fpath)
			fpath = filepath.ToSlash(fpath)
			// Parse SiaPath.
			fSiaPath, err := modules.NewSiaPath(fpath)
			if err != nil {
				die("Couldn't parse SiaPath:", err)
			}
			err = httpClient.RenterUploadDefaultPost(abs(file), fSiaPath)
			if err != nil {
				failed++
				fmt.Printf("Could not upload file %s :%v\n", file, err)
			}
		}
		fmt.Printf("\nUploaded %d of %d files into '%s'.\n", len(files)-failed, len(files), path)
	} else {
		// single file
		// Parse SiaPath.
		siaPath, err := modules.NewSiaPath(path)
		if err != nil {
			die("Couldn't parse SiaPath:", err)
		}
		err = httpClient.RenterUploadDefaultPost(abs(source), siaPath)
		if err != nil {
			die("Could not upload file:", err)
		}
		fmt.Printf("Uploaded '%s' as '%s'.\n", abs(source), path)
	}
}

// renterpricescmd is the handler for the command `siac renter prices`, which
// displays the prices of various storage operations. The user can submit an
// allowance to have the estimate reflect those settings or the user can submit
// nothing
func renterpricescmd(cmd *cobra.Command, args []string) {
	allowance := modules.Allowance{}

	if len(args) != 0 && len(args) != 4 {
		cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}
	if len(args) > 0 {
		hastings, err := parseCurrency(args[0])
		if err != nil {
			die("Could not parse amount:", err)
		}
		blocks, err := parsePeriod(args[1])
		if err != nil {
			die("Could not parse period:", err)
		}
		_, err = fmt.Sscan(hastings, &allowance.Funds)
		if err != nil {
			die("Could not set allowance funds:", err)
		}

		_, err = fmt.Sscan(blocks, &allowance.Period)
		if err != nil {
			die("Could not set allowance period:", err)
		}
		hosts, err := strconv.Atoi(args[2])
		if err != nil {
			die("Could not parse host count")
		}
		allowance.Hosts = uint64(hosts)
		renewWindow, err := parsePeriod(args[3])
		if err != nil {
			die("Could not parse renew window")
		}
		_, err = fmt.Sscan(renewWindow, &allowance.RenewWindow)
		if err != nil {
			die("Could not set allowance renew window:", err)
		}
	}

	rpg, err := httpClient.RenterPricesGet(allowance)
	if err != nil {
		die("Could not read the renter prices:", err)
	}
	periodFactor := uint64(rpg.Allowance.Period / types.BlockHeight(4032))

	// Display Estimate
	fmt.Println("Renter Prices (estimated):")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\tFees for Creating a Set of Contracts:\t", currencyUnits(rpg.FormContracts))
	fmt.Fprintln(w, "\tDownload 1 TB:\t", currencyUnits(rpg.DownloadTerabyte))
	fmt.Fprintln(w, "\tStore 1 TB for 1 Month:\t", currencyUnits(rpg.StorageTerabyteMonth))
	fmt.Fprintln(w, "\tStore 1 TB for Allowance Period:\t", currencyUnits(rpg.StorageTerabyteMonth.Mul64(periodFactor)))
	fmt.Fprintln(w, "\tUpload 1 TB:\t", currencyUnits(rpg.UploadTerabyte))
	w.Flush()

	// Display allowance used for estimate
	fmt.Println("\nAllowance used for estimate:")
	fmt.Fprintln(w, "\tFunds:\t", currencyUnits(rpg.Allowance.Funds))
	fmt.Fprintln(w, "\tPeriod:\t", rpg.Allowance.Period)
	fmt.Fprintln(w, "\tHosts:\t", rpg.Allowance.Hosts)
	fmt.Fprintln(w, "\tRenew Window:\t", rpg.Allowance.RenewWindow)
	w.Flush()
}
