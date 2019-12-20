package main

// TODO: If you run siac from a non-existent directory, the abs() function does
// not handle this very gracefully.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
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
		Use:   "createbackup [name]",
		Short: "Create a backup of the renter's siafiles",
		Long:  "Create a backup of the renter's siafiles, using the specified name.",
		Run:   wrap(renterbackupcreatecmd),
	}

	renterBackupLoadCmd = &cobra.Command{
		Use:   "restorebackup [name]",
		Short: "Restore a backup of the renter's siafiles",
		Long:  "Restore the backup of the renter's siafiles with the given name.",
		Run:   wrap(renterbackuprestorecmd),
	}

	renterBackupListCmd = &cobra.Command{
		Use:   "listbackups",
		Short: "List backups stored on hosts",
		Long:  "List backups stored on hosts",
		Run:   wrap(renterbackuplistcmd),
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

	renterShareCmd = &cobra.Command{
		Use:   "share [siapath] [dst]",
		Short: "Export the file or folder at the specified siapath to a location on disk.",
		Long:  "Export the file or folder at the specified siapath to a location on disk be shared with other people/nodes",
		Run:   wrap(rentersharecmd),
	}

	renterDownloadCancelCmd = &cobra.Command{
		Use:   "canceldownload [cancelID]",
		Short: "Cancel async download",
		Long:  "Cancels an ongoing async download.",
		Run:   wrap(renterdownloadcancelcmd),
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
		Short: "List the status of a specific file or all files within specified dir",
		Long:  "List the status of a specific file or all files known to the renter within the specified folder on the Sia network. To query the root dir either '\"\"', '/' or '.' can be supplied",
		Run:   renterfileslistcmd,
	}

	renterFilesRenameCmd = &cobra.Command{
		Use:     "rename [path] [newpath]",
		Aliases: []string{"mv"},
		Short:   "Rename a file",
		Long:    "Rename a file.",
		Run:     wrap(renterfilesrenamecmd),
	}

	renterFuseCmd = &cobra.Command{
		Use:   "fuse",
		Short: "Perform fuse actions.",
		Long:  "List the set of fuse directories that are mounted",
		Run:   wrap(renterfusecmd),
	}

	renterFuseMountCmd = &cobra.Command{
		Use:   "mount [path] [siapath]",
		Short: "Mount a Sia folder to your disk",
		Long: `Mount a Sia folder to your disk. Applications will be able to see this folder
as though it is a normal part of your filesystem.  Currently experimental, and
read-only. When Sia is ready to support read-write fuse mounting, siac will be
updated to mount in read-write mode as the default. If you must guarantee that
read-only mode is used, you must use the API.`,
		Run: wrap(renterfusemountcmd),
	}

	renterFuseUnmountCmd = &cobra.Command{
		Use:   "unmount [path]",
		Short: "Unmount a Sia folder",
		Long: `Unmount a Sia folder that has previously been mounted. Unmount by specifying the
local path where the Sia folder is mounted.`,
		Run: wrap(renterfuseunmountcmd),
	}

	renterSetLocalPathCmd = &cobra.Command{
		Use:   "setlocalpath [siapath] [newlocalpath]",
		Short: "Changes the local path of the file",
		Long:  "Changes the local path of the file",
		Run:   wrap(rentersetlocalpathcmd),
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
		Long: `Display the estimated prices of storing files, retrieving files, and creating a
set of contracts.

An allowance can be provided for a more accurate estimate, if no allowance is
provided the current set allowance will be used, and if no allowance is set an
allowance of 500SC, 12w period, 50 hosts, and 4w renew window will be used.`,
		Run: renterpricescmd,
	}

	renterRatelimitCmd = &cobra.Command{
		Use:   "ratelimit [maxdownloadspeed] [maxuploadspeed]",
		Short: "set maxdownloadspeed and maxuploadspeed",
		Long: `Set the maxdownloadspeed and maxuploadspeed in 
Bytes per second: B/s, KB/s, MB/s, GB/s, TB/s
or
Bits per second: Bps, Kbps, Mbps, Gbps, Tbps
Set them to 0 for no limit.`,
		Run: wrap(renterratelimitcmd),
	}

	renterSetAllowanceCmd = &cobra.Command{
		Use:   "setallowance",
		Short: "Set the allowance",
		Long: `Set the amount of money that can be spent over a given period.

If no flags are set you will be walked through the interactive allowance
setting. To update only certain fields, pass in those values with the
corresponding field flag, for example '--amount 500SC'.

Allowance can be automatically renewed periodically. If the current
blockheight + the renew window >= the end height the contract, then the contract
is renewed automatically.

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
	// For UX formating
	defer fmt.Println()

	// Get Renter
	rg, err := httpClient.RenterGet()
	if errors.Contains(err, api.ErrAPICallNotRecognized) {
		// Assume module is not loaded if status command is not recognized.
		fmt.Printf("Renter:\n  Status: %s\n\n", moduleNotReadyStatus)
		return
	} else if err != nil {
		die("Could not get renter info:", err)
	}

	// Print Allowance info
	fmt.Println()
	fmt.Printf(`Allowance:`)
	if rg.Settings.Allowance.Funds.IsZero() {
		fmt.Printf("      0 SC (No current allowance)\n")
	} else {
		fm := rg.FinancialMetrics
		totalSpent := fm.ContractFees.Add(fm.UploadSpending).
			Add(fm.DownloadSpending).Add(fm.StorageSpending)
		fmt.Printf(`       %v
  Spent Funds:     %v
  Unspent Funds:   %v
`, currencyUnits(rg.Settings.Allowance.Funds), currencyUnits(totalSpent), currencyUnits(fm.Unspent))
	}

	// detailed allowance spending for current period
	if renterVerbose {
		renterallowancespending(rg)
	}

	// File and Contract Data
	fmt.Println()
	fmt.Printf(`Data Storage:`)
	err = renterFilesAndContractSummary()
	if err != nil {
		die(err)
	}

	if !renterListVerbose {
		return
	}

	// Print out ratelimit info about the renter
	fmt.Println()
	rateLimitSummary(rg.Settings.MaxDownloadSpeed, rg.Settings.MaxUploadSpeed)
}

// rentersharecmd exports a SiaFile or SiaDir and saves it to destination.
func rentersharecmd(path, destination string) {
	// Parse SiaPath.
	siaPath, err := modules.NewSiaPath(path)
	if err != nil {
		die("Couldn't parse SiaPath:", err)
	}
	_, err = httpClient.RenterFileGet(siaPath)
	if err == nil {
		renterfilesshare(siaPath, destination)
		return
	} else if !strings.Contains(err.Error(), siafile.ErrUnknownPath.Error()) {
		die("Failed to export file:", err)
	}
	_, err = httpClient.RenterGetDir(siaPath)
	if err == nil {
		die("Exporting folders is not supported yet.")
		return
	} else if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		die("Failed to export folder:", err)
	}
	die(fmt.Sprintf("Unknown file/folder '%v'", path))
}

// renterFilesAndContractSummary prints out a summary of what the renter is
// storing
func renterFilesAndContractSummary() error {
	rf, err := httpClient.RenterGetDir(modules.RootSiaPath())
	if errors.Contains(err, api.ErrAPICallNotRecognized) {
		// Assume module is not loaded if status command is not recognized.
		fmt.Printf("\n  Status: %s\n\n", moduleNotReadyStatus)
		return nil
	} else if err != nil {
		return errors.AddContext(err, "unable to get root dir with RenterGetDir")
	}

	rc, err := httpClient.RenterContractsGet()
	if err != nil {
		return err
	}
	redundancyStr := fmt.Sprintf("%.2f", rf.Directories[0].AggregateMinRedundancy)
	if rf.Directories[0].AggregateMinRedundancy == -1 {
		redundancyStr = "-"
	}

	fmt.Printf(`
  Files:          %v
  Total Stored:   %v
  Min Redundancy: %v
  Contracts:      %v
`, rf.Directories[0].AggregateNumFiles, modules.FilesizeUnits(rf.Directories[0].AggregateSize), redundancyStr, len(rc.ActiveContracts))

	return nil
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
		fmt.Printf("%13s  %s (uploading, %0.2f%%)\n", modules.FilesizeUnits(file.Filesize), file.SiaPath, file.UploadProgress)
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

// renterallowancespending prints info about the current period spending
// this also get called by 'siac renter -v' which is why it's in its own
// function
func renterallowancespending(rg api.RenterGET) {
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
}

// renterallowancecmd is the handler for the command `siac renter allowance`.
// displays the current allowance.
func renterallowancecmd() {
	rg, err := httpClient.RenterGet()
	if err != nil {
		die("Could not get allowance:", err)
	}
	allowance := rg.Settings.Allowance

	// Show allowance info
	fmt.Printf(`Allowance:
  Amount:               %v
  Period:               %v blocks
  Renew Window:         %v blocks
  Hosts:                %v

Viewnode Per-Contract Budget: %v

Expectations for period:
  Expected Storage:     %v
  Expected Upload:      %v
  Expected Download:    %v
  Expected Redundancy:  %v

Price Protections:
  MaxRPCPrice:               %v per million requests
  MaxContractPrice:          %v
  MaxDownloadBandwidthPrice: %v per TB
  MaxSectorAccessPrice:      %v per million accesses
  MaxStoragePrice:           %v per TB per Month
  MaxUploadBandwidthPrice:   %v per TB
`, currencyUnits(allowance.Funds), allowance.Period, allowance.RenewWindow,
		allowance.Hosts,
		currencyUnits(allowance.ViewContractInitialPrice),
		modules.FilesizeUnits(allowance.ExpectedStorage),
		modules.FilesizeUnits(allowance.ExpectedUpload*uint64(allowance.Period)),
		modules.FilesizeUnits(allowance.ExpectedDownload*uint64(allowance.Period)),
		allowance.ExpectedRedundancy,
		currencyUnits(allowance.MaxRPCPrice.Mul64(1e6)),
		currencyUnits(allowance.MaxContractPrice),
		currencyUnits(allowance.MaxDownloadBandwidthPrice.Mul(modules.BytesPerTerabyte)),
		currencyUnits(allowance.MaxSectorAccessPrice.Mul64(1e6)),
		currencyUnits(allowance.MaxStoragePrice.Mul(modules.BlockBytesPerMonthTerabyte)),
		currencyUnits(allowance.MaxUploadBandwidthPrice.Mul(modules.BytesPerTerabyte)))

	// Show detailed current Period spending metrics
	renterallowancespending(rg)

	fm := rg.FinancialMetrics

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
	err := httpClient.RenterAllowanceCancelPost()
	if err != nil {
		die("error canceling allowance:", err)
	}
	fmt.Println("Allowance canceled.")
}

// rentersetallowancecmd is the handler for `siac renter setallowance`.
// set the allowance or modify individual allowance fields.
func rentersetallowancecmd(cmd *cobra.Command, args []string) {
	// Get the current period setting.
	rg, err := httpClient.RenterGet()
	if err != nil {
		die("Could not get renter settings")
	}

	req := httpClient.RenterPostPartialAllowance()
	changedFields := 0
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
			die("Could not parse host count:", err)
		}
		req = req.WithHosts(uint64(hosts))
		changedFields++
	}
	// parse renewWindow
	if allowanceRenewWindow != "" {
		rw, err := parsePeriod(allowanceRenewWindow)
		if err != nil {
			die("Could not parse renew window:", err)
		}
		var renewWindow types.BlockHeight
		_, err = fmt.Sscan(rw, &renewWindow)
		if err != nil {
			die("Could not parse renew window:", err)
		}
		req = req.WithRenewWindow(renewWindow)
		changedFields++
	}
	// parse viewcontractinitialprice
	if allowanceViewContractInitialPrice != "" {
		priceStr, err := parseCurrency(allowanceViewContractInitialPrice)
		if err != nil {
			die("Could not parse view contract initial price:", err)
		}
		var price types.Currency
		_, err = fmt.Sscan(priceStr, &price)
		if err != nil {
			die("could not read view contract initial price:", err)
		}
		req = req.WithViewContractInitialPrice(price)
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
		expectedRedundancy, err := strconv.ParseFloat(allowanceExpectedRedundancy, 64)
		if err != nil {
			die("Could not parse expected redundancy")
		}
		req = req.WithExpectedRedundancy(expectedRedundancy)
		changedFields++
	}
	// parse maxrpcprice
	if allowanceMaxRPCPrice != "" {
		priceStr, err := parseCurrency(allowanceMaxRPCPrice)
		if err != nil {
			die("Could not parse max rpc price:", err)
		}
		var price types.Currency
		_, err = fmt.Sscan(priceStr, &price)
		if err != nil {
			die("Could not read max rpc price:", err)
		}
		price = price.Div64(1e6)
		req = req.WithMaxRPCPrice(price)
		changedFields++
	}
	// parse maxcontractprice
	if allowanceMaxContractPrice != "" {
		priceStr, err := parseCurrency(allowanceMaxContractPrice)
		if err != nil {
			die("Could not parse max contract price:", err)
		}
		var price types.Currency
		_, err = fmt.Sscan(priceStr, &price)
		if err != nil {
			die("Could not read max contract price:", err)
		}
		req = req.WithMaxContractPrice(price)
		changedFields++
	}
	// parse maxdownloadbandwidthprice
	if allowanceMaxDownloadBandwidthPrice != "" {
		priceStr, err := parseCurrency(allowanceMaxDownloadBandwidthPrice)
		if err != nil {
			die("Could not parse max download bandwidth price:", err)
		}
		var price types.Currency
		_, err = fmt.Sscan(priceStr, &price)
		if err != nil {
			die("Could not read max download bandwidth price:", err)
		}
		price = price.Div(modules.BytesPerTerabyte)
		req = req.WithMaxDownloadBandwidthPrice(price)
		changedFields++
	}
	// parse maxsectoraccessprice
	if allowanceMaxSectorAccessPrice != "" {
		priceStr, err := parseCurrency(allowanceMaxSectorAccessPrice)
		if err != nil {
			die("Could not parse max sector access price:", err)
		}
		var price types.Currency
		_, err = fmt.Sscan(priceStr, &price)
		if err != nil {
			die("Could not read max sector access price:", err)
		}
		price = price.Div64(1e6)
		req = req.WithMaxSectorAccessPrice(price)
		changedFields++
	}
	// parse maxstorageprice
	if allowanceMaxStoragePrice != "" {
		priceStr, err := parseCurrency(allowanceMaxStoragePrice)
		if err != nil {
			die("Could not parse max storage price:", err)
		}
		var price types.Currency
		_, err = fmt.Sscan(priceStr, &price)
		if err != nil {
			die("Could not read max storage price:", err)
		}
		price = price.Div(modules.BlockBytesPerMonthTerabyte)
		req = req.WithMaxStoragePrice(price)
		changedFields++
	}
	// parse maxuploadbandwidthprice
	if allowanceMaxUploadBandwidthPrice != "" {
		priceStr, err := parseCurrency(allowanceMaxUploadBandwidthPrice)
		if err != nil {
			die("Could not parse max upload bandwidth price:", err)
		}
		var price types.Currency
		_, err = fmt.Sscan(priceStr, &price)
		if err != nil {
			die("Could not read max upload bandwidth price:", err)
		}
		price = price.Div(modules.BytesPerTerabyte)
		req = req.WithMaxUploadBandwidthPrice(price)
		changedFields++
	}

	// check if any fields were updated.
	if changedFields == 0 {
		// If no fields were set then walk the user through the interactive
		// allowance setting
		req = rentersetallowancecmdInteractive(req, rg.Settings.Allowance)
		if err := req.Send(); err != nil {
			die("Could not set allowance:", err)
		}
		fmt.Println("Allowance updated")
		return
	}
	// check for required initial fields
	if rg.Settings.Allowance.Funds.IsZero() && allowanceFunds == "" {
		die("Funds must be set in initial allowance")
	}
	if rg.Settings.Allowance.ExpectedStorage == 0 && allowanceExpectedStorage == "" {
		die("Expected storage must be set in initial allowance")
	}

	if err := req.Send(); err != nil {
		die("Could not set allowance:", err)
	}
	fmt.Printf("Allowance updated. %v setting(s) changed.\n", changedFields)
}

func rentersetallowancecmdInteractive(req *client.AllowanceRequestPost, allowance modules.Allowance) *client.AllowanceRequestPost {
	br := bufio.NewReader(os.Stdin)
	readString := func() string {
		str, _ := br.ReadString('\n')
		return strings.TrimSpace(str)
	}

	fmt.Println("Interactive tool for setting the 8 allowance options.")
	fmt.Println()

	// funds
	fmt.Println(`1/8: Funds
Funds determines the number of siacoins that the renter will spend when forming
contracts with hosts. The renter will not allocate more than this amount of
siacoins into the set of contracts each billing period. If the renter spends all
of the funds but then needs to form new contracts, the renter will wait until
either until the user increase the allowance funds, or until a new billing
period is reached. If there are not enough funds to repair all files, then files
may be at risk of getting lost.

Once the allowance is set, the renter will begin forming contracts. This will
immediately spend a large portion of the allowance, while also leaving a large
portion for forming additional contracts throughout the billing period. Most of
the funds that are spent immediately are not actually spent, but instead locked
up into state channels. In the allowance reports, these funds will typically be
reported as 'unspent allocated'. The funds that have been set aside for forming
contracts later in the billing cycle will be reported as 'unspent unallocated'.

The command 'siac renter allowance' can be used to see a breakdown of spending.

The following units can be used to set the allowance:
    H  (10^24 H per siacoin)
    SC (1 siacoin per SC)
    KS (1000 siacoins per KS)`)
	fmt.Println()
	fmt.Println("Current value:", currencyUnits(allowance.Funds))
	fmt.Println("Default value:", currencyUnits(modules.DefaultAllowance.Funds))

	var funds types.Currency
	if allowance.Funds.IsZero() {
		funds = modules.DefaultAllowance.Funds
		fmt.Println("Enter desired value below, or leave blank to use default value")
	} else {
		funds = allowance.Funds
		fmt.Println("Enter desired value below, or leave blank to use current value")
	}
	fmt.Print("Funds: ")
	allowanceFunds := readString()
	if allowanceFunds != "" {
		hastings, err := parseCurrency(allowanceFunds)
		if err != nil {
			die("Could not parse amount:", err)
		}
		_, err = fmt.Sscan(hastings, &funds)
		if err != nil {
			die("Could not parse amount:", err)
		}
	}
	if funds.IsZero() {
		die("Allowance cannot be 0")
	}
	req = req.WithFunds(funds)

	// period
	fmt.Println(`2/8: Period
The period is equivalent to the billing cycle length. The renter will not spend
more than the full balance of its funds every billing period. When the billing
period is over, the contracts will be renewed and the spending will be reset.

The following units can be used to set the period:

    b (blocks - 10 minutes)
    d (days - 144 blocks or 1440 minutes)
    w (weeks - 1008 blocks or 10080 minutes)`)
	fmt.Println()
	fmt.Println("Current value:", periodUnits(allowance.Period), "weeks")
	fmt.Println("Default value:", periodUnits(modules.DefaultAllowance.Period), "weeks")

	var period types.BlockHeight
	if allowance.Period == 0 {
		period = modules.DefaultAllowance.Period
		fmt.Println("Enter desired value below, or leave blank to use default value")
	} else {
		period = allowance.Period
		fmt.Println("Enter desired value below, or leave blank to use current value")
	}
	fmt.Print("Period: ")
	allowancePeriod := readString()
	if allowancePeriod != "" {
		blocks, err := parsePeriod(allowancePeriod)
		if err != nil {
			die("Could not parse period:", err)
		}
		_, err = fmt.Sscan(blocks, &period)
		if err != nil {
			die("Could not parse period:", err)
		}
	}
	if period == 0 {
		die("Period cannot be 0")
	}
	req = req.WithPeriod(period)

	// hosts
	fmt.Println(`3/8: Hosts
Hosts sets the number of hosts that will be used to form the allowance. Sia
gains most of its resiliancy from having a large number of hosts. More hosts
will mean both more robustness and higher speeds when using the network, however
will also result in more memory consumption and higher blockchain fees. It is
recommended that the default number of hosts be treated as a minimum, and that
double the default number of default hosts be treated as a maximum.`)
	fmt.Println()
	fmt.Println("Current value:", allowance.Hosts)
	fmt.Println("Default value:", modules.DefaultAllowance.Hosts)

	var hosts uint64
	if allowance.Hosts == 0 {
		hosts = modules.DefaultAllowance.Hosts
		fmt.Println("Enter desired value below, or leave blank to use default value")
	} else {
		hosts = allowance.Hosts
		fmt.Println("Enter desired value below, or leave blank to use current value")
	}
	fmt.Print("Hosts: ")
	allowanceHosts := readString()
	if allowanceHosts != "" {
		hostsInt, err := strconv.Atoi(allowanceHosts)
		if err != nil {
			die("Could not parse host count")
		}
		hosts = uint64(hostsInt)
	}
	if hosts == 0 {
		die("Must have at least 1 host")
	}
	req = req.WithHosts(uint64(hosts))

	// renewWindow
	fmt.Println(`4/8: Renew Window
The renew window is how long the user has to renew their contracts. At the end
of the period, all of the contracts expire. The contracts need to be renewewd
before they expire, otherwise the user will lose all of their files. The renew
window is the window of time at the end of the period during which the renter
will renew the users contracts. For example, if the renew window is 1 week long,
then during the final week of each period the user will renew their contracts.
If the user is offline for that whole week, the user's data will be lost.

Each billing period begins at the beginning of the renew window for the previous
period. For example, if the period is 12 weeks long and the renew window is 4
weeks long, then the first billing period technically begins at -4 weeks, or 4
weeks before the allowance is created. And the second billing period begins at
week 8, or 8 weeks after the allowance is created. The third billing period will
begin at week 20.

The following units can be used to set the renew window:

    b (blocks - 10 minutes)
    d (days - 144 blocks or 1440 minutes)
    w (weeks - 1008 blocks or 10080 minutes)`)
	fmt.Println()
	fmt.Println("Current value:", periodUnits(allowance.RenewWindow), "weeks")
	fmt.Println("Default value:", periodUnits(modules.DefaultAllowance.RenewWindow), "weeks")

	var renewWindow types.BlockHeight
	if allowance.RenewWindow == 0 {
		renewWindow = modules.DefaultAllowance.RenewWindow
		fmt.Println("Enter desired value below, or leave blank to use default value")
	} else {
		renewWindow = allowance.RenewWindow
		fmt.Println("Enter desired value below, or leave blank to use current value")
	}
	fmt.Print("Renew Window: ")
	allowanceRenewWindow := readString()
	if allowanceRenewWindow != "" {
		rw, err := parsePeriod(allowanceRenewWindow)
		if err != nil {
			die("Could not parse renew window")
		}
		_, err = fmt.Sscan(rw, &renewWindow)
		if err != nil {
			die("Could not parse renew window:", err)
		}
	}
	if renewWindow == 0 {
		die("Cannot set renew window to zero")
	}
	req = req.WithRenewWindow(renewWindow)

	// expectedStorage
	fmt.Println(`5/8: Expected Storage
Expected storage is the amount of storage that the user expects to keep on the
Sia network. This value is important to calibrate the spending habits of siad.
Because Sia is decentralized, there is no easy way for siad to know what the
real world cost of storage is, nor what the real world price of a siacoin is. To
overcome this deficiency, siad depends on the user for guidance.

If the user has a low allowance and a high amount of expected storage, siad will
more heavily prioritize cheaper hosts, and will also be more comfortable with
hosts that post lower amounts of collateral. If the user has a high allowance
and a low amount of expected storage, siad will prioritize hosts that post more
collateral, as well as giving preference to hosts better overall traits such as
uptime and age.

Even when the user has a large allowance and a low amount of expected storage,
siad will try to optimize for saving money; siad tries to meet the users storage
and bandwidth needs while spending significantly less than the overall allowance.`)
	fmt.Println()
	fmt.Println("Current value:", modules.FilesizeUnits(allowance.ExpectedStorage))
	fmt.Println("Default value:", modules.FilesizeUnits(modules.DefaultAllowance.ExpectedStorage))

	var expectedStorage uint64
	if allowance.ExpectedStorage == 0 {
		expectedStorage = modules.DefaultAllowance.ExpectedStorage
		fmt.Println("Enter desired value below, or leave blank to use default value")
	} else {
		expectedStorage = allowance.ExpectedStorage
		fmt.Println("Enter desired value below, or leave blank to use current value")
	}
	fmt.Print("Expected Storage: ")
	allowanceExpectedStorage := readString()
	if allowanceExpectedStorage != "" {
		es, err := parseFilesize(allowanceExpectedStorage)
		if err != nil {
			die("Could not parse expected storage")
		}
		_, err = fmt.Sscan(es, &expectedStorage)
		if err != nil {
			die("Could not parse expected storage")
		}
	}
	req = req.WithExpectedStorage(expectedStorage)

	// expectedUpload
	fmt.Println(`6/8: Expected Upload
Expected upload tells siad how much uploading the user expects to do each
period. If this value is high, siad will more strongly prefer hosts that have a
low upload bandwidth price. If this value is low, siad will focus on other
metrics than upload bandwidth pricing, because even if the host charges a lot
for upload bandwidth, it will not impact the total cost to the user very much.

The user should not consider upload bandwidth used during repairs, siad will
consider repair bandwidth separately.`)
	fmt.Println()
	fmt.Println("Current value:", modules.FilesizeUnits(allowance.ExpectedUpload*uint64(allowance.Period)))
	fmt.Println("Default value:", modules.FilesizeUnits(modules.DefaultAllowance.ExpectedUpload*uint64(allowance.Period)))

	var expectedUpload uint64
	if allowance.ExpectedUpload == 0 {
		expectedUpload = modules.DefaultAllowance.ExpectedUpload
		fmt.Println("Enter desired value below, or leave blank to use default value")
	} else {
		expectedUpload = allowance.ExpectedUpload
		fmt.Println("Enter desired value below, or leave blank to use current value")
	}
	fmt.Print("Expected Upload: ")
	allowanceExpectedUpload := readString()
	if allowanceExpectedUpload != "" {
		eu, err := parseFilesize(allowanceExpectedUpload)
		if err != nil {
			die("Could not parse expected upload")
		}
		_, err = fmt.Sscan(eu, &expectedUpload)
		if err != nil {
			die("Could not parse expected upload")
		}
		// User provdies setting in terms of period, need to normalize to
		// per-block.
		expectedUpload /= uint64(allowance.Period)
	}
	req = req.WithExpectedUpload(expectedUpload)

	// expectedDownload
	fmt.Println(`7/8: Expected Download
Expected download tells siad how much downloading the user expects to do each
period. If this value is high, siad will more strongly prefer hosts that have a
low download bandwidth price. If this value is low, siad will focus on other
metrics than download bandwidth pricing, because even if the host charges a lot
for downloads, it will not impact the total cost to the user very much.

The user should not consider download bandwidth used during repairs, siad will
consider repair bandwidth separately.`)
	fmt.Println()
	fmt.Println("Current value:", modules.FilesizeUnits(allowance.ExpectedDownload*uint64(allowance.Period)))
	fmt.Println("Default value:", modules.FilesizeUnits(modules.DefaultAllowance.ExpectedDownload*uint64(allowance.Period)))

	var expectedDownload uint64
	if allowance.ExpectedDownload == 0 {
		expectedDownload = modules.DefaultAllowance.ExpectedDownload
		fmt.Println("Enter desired value below, or leave blank to use default value")
	} else {
		expectedDownload = allowance.ExpectedDownload
		fmt.Println("Enter desired value below, or leave blank to use current value")
	}
	fmt.Print("Expected Download: ")
	allowanceExpectedDownload := readString()
	if allowanceExpectedDownload != "" {
		ed, err := parseFilesize(allowanceExpectedDownload)
		if err != nil {
			die("Could not parse expected download")
		}
		_, err = fmt.Sscan(ed, &expectedDownload)
		if err != nil {
			die("Could not parse expected download")
		}
		// User set field in terms of period, need to normalize to per-block.
		expectedDownload /= uint64(allowance.Period)
	}
	req = req.WithExpectedDownload(expectedDownload)

	// expectedRedundancy
	fmt.Println(`8/8: Expected Redundancy
Expected redundancy is used in conjunction with expected storage to determine
the total amount of raw storage that will be stored on hosts. If the expected
storage is 1 TB and the expected redundancy is 3, then the renter will calculate
that the total amount of storage in the user's contracts will be 3 TiB.

This value does not need to be changed from the default unless the user is
manually choosing redundancy settings for their file. If different files are
being given different redundancy settings, then the average of all the
redundancies should be used as the value for expected redundancy, weighted by
how large the files are.`)
	fmt.Println()
	fmt.Println("Current value:", allowance.ExpectedRedundancy)
	fmt.Println("Default value:", modules.DefaultAllowance.ExpectedRedundancy)

	var expectedRedundancy float64
	var err error
	if allowance.ExpectedRedundancy == 0 {
		expectedRedundancy = modules.DefaultAllowance.ExpectedRedundancy
		fmt.Println("Enter desired value below, or leave blank to use default value")
	} else {
		expectedRedundancy = allowance.ExpectedRedundancy
		fmt.Println("Enter desired value below, or leave blank to use current value")
	}
	fmt.Print("Expected Redundancy: ")
	allowanceExpectedRedundancy := readString()
	if allowanceExpectedRedundancy != "" {
		expectedRedundancy, err = strconv.ParseFloat(allowanceExpectedRedundancy, 64)
		if err != nil {
			die("Could not parse expected redundancy")
		}
	}
	if expectedRedundancy < 1 {
		die("Expected redundancy must be at least 1")
	}
	req = req.WithExpectedRedundancy(expectedRedundancy)
	fmt.Println()

	return req
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
func renterbackupcreatecmd(name string) {
	// Create backup.
	err := httpClient.RenterCreateBackupPost(name)
	if err != nil {
		die("Failed to create backup", err)
	}
	fmt.Println("Backup initiated. Monitor progress with the 'listbackups' command.")
}

// renterbackuprestorecmd is the handler for the command `siac renter
// restorebackup`.
func renterbackuprestorecmd(name string) {
	err := httpClient.RenterRecoverBackupPost(name)
	if err != nil {
		die("Failed to restore backup", err)
	}
}

// renterbackuplistcmd is the handler for the command `siac renter listbackups`.
func renterbackuplistcmd() {
	ubs, err := httpClient.RenterBackups()
	if err != nil {
		die("Failed to retrieve backups", err)
	} else if len(ubs.Backups) == 0 {
		fmt.Println("No uploaded backups.")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  Name\tCreation Date\tUpload Progress")
	for _, ub := range ubs.Backups {
		date := time.Unix(int64(ub.CreationDate), 0)
		fmt.Fprintf(w, "  %v\t%v\t%v\n", ub.Name, date.Format(time.ANSIC), ub.UploadProgress)
	}
	w.Flush()
}

// contractStats is a helper function to pull information out of the renter
// contracts to be displayed
func contractStats(contracts []api.RenterContract) (size uint64, spent, remaining, fees types.Currency) {
	for _, c := range contracts {
		size += c.Size
		remaining = remaining.Add(c.RenterFunds)
		fees = fees.Add(c.Fees)
		// Negative Currency Check
		var contractTotalSpent types.Currency
		if c.TotalCost.Cmp(c.RenterFunds.Add(c.Fees)) < 0 {
			contractTotalSpent = c.RenterFunds.Add(c.Fees)
		} else {
			contractTotalSpent = c.TotalCost.Sub(c.RenterFunds).Sub(c.Fees)
		}
		spent = spent.Add(contractTotalSpent)
	}
	return
}

// writeContracts is a helper function to display contracts
func writeContracts(contracts []api.RenterContract) {
	fmt.Println("  Number of Contracts:", len(contracts))
	sort.Sort(byValue(contracts))
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	fmt.Fprintln(w, "  \nHost\tHost PubKey\tHost Version\tRemaining Funds\tSpent Funds\tSpent Fees\tData\tEnd Height\tContract ID\tGoodForUpload\tGoodForRenew\tBadContract")
	for _, c := range contracts {
		address := c.NetAddress
		hostVersion := c.HostVersion
		if address == "" {
			address = "Host Removed"
			hostVersion = ""
		}
		// Negative Currency Check
		var contractTotalSpent types.Currency
		if c.TotalCost.Cmp(c.RenterFunds.Add(c.Fees)) < 0 {
			contractTotalSpent = c.RenterFunds.Add(c.Fees)
		} else {
			contractTotalSpent = c.TotalCost.Sub(c.RenterFunds).Sub(c.Fees)
		}
		fmt.Fprintf(w, "  %v\t%v\t%v\t%8s\t%8s\t%8s\t%v\t%v\t%v\t%v\t%v\t%v\n",
			address,
			c.HostPublicKey.String(),
			hostVersion,
			currencyUnits(c.RenterFunds),
			currencyUnits(contractTotalSpent),
			currencyUnits(c.Fees),
			modules.FilesizeUnits(c.Size),
			c.EndHeight,
			c.ID,
			c.GoodForUpload,
			c.GoodForRenew,
			c.BadContract)
	}
	w.Flush()
}

// rentercontractscmd is the handler for the comand `siac renter contracts`.
// It lists the Renter's contracts.
func rentercontractscmd() {
	rc, err := httpClient.RenterDisabledContractsGet()
	if err != nil {
		die("Could not get contracts:", err)
	}

	// Build Current Period summary
	fmt.Println("Current Period Summary")
	// Active Contracts are all good data
	activeSize, activeSpent, activeRemaining, activeFees := contractStats(rc.ActiveContracts)
	// Passive Contracts are all good data
	passiveSize, passiveSpent, passiveRemaining, passiveFees := contractStats(rc.PassiveContracts)
	// Refreshed Contracts are duplicate data
	_, refreshedSpent, refreshedRemaining, refreshedFees := contractStats(rc.RefreshedContracts)
	// Disabled Contracts are wasted data
	disabledSize, disabledSpent, disabledRemaining, disabledFees := contractStats(rc.DisabledContracts)
	// Sum up the appropriate totals
	totalStored := activeSize + passiveSize
	totalWasted := disabledSize
	totalSpent := activeSpent.Add(passiveSpent).Add(refreshedSpent).Add(disabledSpent)
	totalRemaining := activeRemaining.Add(passiveRemaining).Add(refreshedRemaining).Add(disabledRemaining)
	totalFees := activeFees.Add(passiveFees).Add(refreshedFees).Add(disabledFees)

	fmt.Printf(`  Total Good Data:    %s
  Total Wasted Data:  %s
  Total Remaining:    %v
  Total Spent:        %v
  Total Fees:         %v

`, modules.FilesizeUnits(totalStored), modules.FilesizeUnits(totalWasted), currencyUnits(totalRemaining), currencyUnits(totalSpent), currencyUnits(totalFees))

	// List out contracts
	fmt.Println("Active Contracts:")
	if len(rc.ActiveContracts) == 0 {
		fmt.Println("  No active contracts.")
	} else {
		// Display Active Contracts
		writeContracts(rc.ActiveContracts)
	}

	fmt.Println("\nPassive Contracts:")
	if len(rc.PassiveContracts) == 0 {
		fmt.Println("  No passive contracts.")
	} else {
		// Display Passive Contracts
		writeContracts(rc.PassiveContracts)
	}

	fmt.Println("\nRefreshed Contracts:")
	if len(rc.RefreshedContracts) == 0 {
		fmt.Println("  No refreshed contracts.")
	} else {
		// Display Refreshed Contracts
		writeContracts(rc.RefreshedContracts)
	}

	fmt.Println("\nDisabled Contracts:")
	if len(rc.DisabledContracts) == 0 {
		fmt.Println("  No disabled contracts.")
	} else {
		// Display Disabled Contracts
		writeContracts(rc.DisabledContracts)
	}

	if renterAllContracts {
		rce, err := httpClient.RenterExpiredContractsGet()
		if err != nil {
			die("Could not get expired contracts:", err)
		}
		// Build Historical summary
		fmt.Println("\nHistorical Summary")
		// Expired Contracts are all good data
		expiredSize, expiredSpent, expiredRemaining, expiredFees := contractStats(rce.ExpiredContracts)
		// Expired Refreshed Contracts are duplicate data
		_, expiredRefreshedSpent, expiredRefreshedRemaining, expiredRefreshedFees := contractStats(rce.ExpiredRefreshedContracts)
		// Sum up the appropriate totals
		totalStored := expiredSize
		totalSpent := expiredSpent.Add(expiredRefreshedSpent)
		totalRemaining := expiredRemaining.Add(expiredRefreshedRemaining)
		totalFees := expiredFees.Add(expiredRefreshedFees)

		fmt.Printf(`  Total Expired Data:  %s
  Total Remaining:     %v
  Total Spent:         %v
  Total Fees:          %v

`, modules.FilesizeUnits(totalStored), currencyUnits(totalRemaining), currencyUnits(totalSpent), currencyUnits(totalFees))
		fmt.Println("\nExpired Contracts:")
		if len(rce.ExpiredContracts) == 0 {
			fmt.Println("  No expired contracts.")
		} else {
			writeContracts(rce.ExpiredContracts)
		}

		fmt.Println("\nExpired Refresh Contracts:")
		if len(rce.ExpiredRefreshedContracts) == 0 {
			fmt.Println("  No expired refreshed contracts.")
		} else {
			writeContracts(rce.ExpiredRefreshedContracts)
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
				modules.FilesizeUnits(rc.Size))

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
func downloadDir(siaPath modules.SiaPath, destination string) (tfs []trackedFile, skipped []string, totalSize uint64, err error) {
	// Get dir info.
	rd, err := httpClient.RenterGetDir(siaPath)
	if err != nil {
		err = errors.AddContext(err, "failed to get dir info")
		return
	}
	// Create destination on disk.
	if err = os.MkdirAll(destination, 0750); err != nil {
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
		totalSize += file.Filesize
		_, err = httpClient.RenterDownloadFullGet(file.SiaPath, dst, true)
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
		rtfs, rskipped, totalSubSize, rerr := downloadDir(subDir.SiaPath, filepath.Join(destination, subDir.SiaPath.Name()))
		tfs = append(tfs, rtfs...)
		skipped = append(skipped, rskipped...)
		totalSize += totalSubSize
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
	start := time.Now()
	tfs, skipped, totalSize, downloadErr := downloadDir(siaPath, destination)
	if renterDownloadAsync && downloadErr != nil {
		fmt.Println("At least one error occurred when initializing the download:", downloadErr)
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
		fmt.Printf("\nDownloaded '%s' to '%s - %v in %v'.\n", path, abs(destination), modules.FilesizeUnits(totalSize), time.Since(start).Round(time.Millisecond))
		return
	}
	// Print errors.
	if downloadErr != nil {
		fmt.Println("At least one error occurred when initializing the download:", downloadErr)
	}
	for _, fd := range failedDownloads {
		fmt.Printf("Download of file '%v' to destination '%v' failed: %v\n", fd.SiaPath, fd.Destination, fd.Error)
	}
	os.Exit(1)
}

// renterdownloadcancelcmd is the handler for the command `siac renter download cancel [cancelID]`
// Cancels the ongoing download.
func renterdownloadcancelcmd(cancelID modules.DownloadID) {
	if err := httpClient.RenterCancelDownloadPost(cancelID); err != nil {
		die("Couldn't cancel download:", err)
	}
	fmt.Println("Download canceled successfully")
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
	} else if !strings.Contains(errFile.Error(), filesystem.ErrNotExist.Error()) {
		die(fmt.Sprintf("Failed to delete file %v: %v", path, errFile))
	}
	// Try to delete folder.
	errDir := httpClient.RenterDirDeletePost(siaPath)
	if errDir == nil {
		fmt.Printf("Deleted directory '%v'\n", path)
		return
	} else if !strings.Contains(errDir.Error(), filesystem.ErrNotExist.Error()) {
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
	} else if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		die("Failed to download file:", err)
	}
	_, err = httpClient.RenterGetDir(siaPath)
	if err == nil {
		renterdirdownload(path, destination)
		return
	} else if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		die("Failed to download folder:", err)
	}
	die(fmt.Sprintf("Unknown file '%v'", path))
}

// renterfilesshare exports a single SiaFile and saves it to disk.
func renterfilesshare(siaPath modules.SiaPath, destination string) {
	destination = abs(destination)
	exportedFile, err := httpClient.RenterExportSiafile(siaPath)
	if err != nil {
		die("Failed to export SiaFile:", err)
	}
	if err := ioutil.WriteFile(destination, exportedFile, 0750); err != nil {
		die("Failed to write exported SiaFile to destination:", err)
	}
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
	start := time.Now()
	cancelID, err := httpClient.RenterDownloadFullGet(siaPath, destination, true)
	if err != nil {
		die("Download could not be started:", err)
	}

	// If the download is async, report success.
	if renterDownloadAsync {
		fmt.Printf("Queued Download '%s' to %s.\n", siaPath.String(), abs(destination))
		fmt.Printf("ID to cancel download: '%v'\n", cancelID)
		return
	}

	// If the download is blocking, display progress as the file downloads.
	file, err := httpClient.RenterFileGet(siaPath)
	if err != nil {
		die("Error getting file after download has started:", err)
	}

	failedDownloads := downloadprogress([]trackedFile{{siaPath: siaPath, dst: destination}})
	if len(failedDownloads) > 0 {
		die("\nDownload could not be completed:", failedDownloads[0].Error)
	}
	fmt.Printf("\nDownloaded '%s' to '%s - %v in %v'.\n", path, abs(destination), modules.FilesizeUnits(file.File.Filesize), time.Since(start).Round(time.Millisecond))
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
	failedDownloads := func() (fd []api.DownloadInfo) {
		for _, di := range errMap {
			fd = append(fd, di)
		}
		return
	}
	for range time.Tick(OutputRefreshRate) {
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
		// Clear terminal.
		clearStr := fmt.Sprint("\033[H\033[2J")
		// Take new measurements for each tracked file.
		progressStr := clearStr
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
					return failedDownloads()
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

			progressLine := fmt.Sprintf("Downloading %v... %5.1f%% of %v, %v elapsed, %s    ", tf.siaPath.String(), pct, modules.FilesizeUnits(d.Filesize), elapsed, speed)
			if tfIdx < len(tfs)-1 {
				progressStr += fmt.Sprintln(progressLine)
			} else {
				progressStr += fmt.Sprint(progressLine)
			}
		}
		print(progressStr)
		progressStr = clearStr
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
	if err != nil {
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
func renterfileslistcmd(cmd *cobra.Command, args []string) {
	var path string
	switch len(args) {
	case 0:
		path = "."
	case 1:
		path = args[0]
	default:
		cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}
	// Parse the input siapath.
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

	// Check for file first
	if !sp.IsRoot() {
		rf, err := httpClient.RenterFileGet(sp)
		if err == nil {
			fmt.Println()
			json, err := json.MarshalIndent(rf.File, "", "  ")
			if err != nil {
				log.Fatal(err)
			}

			fmt.Println(string(json))
			fmt.Println()
			return
		} else if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
			die(fmt.Sprintf("Error getting file %v: %v", path, err))
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
	if numFiles+len(dirs) < 1 {
		fmt.Println("No files/dirs have been uploaded.")
		return
	}
	fmt.Printf("\nListing %v files/dirs:", numFiles+len(dirs)-1)
	fmt.Printf(" %9s\n", modules.FilesizeUnits(totalStored))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if renterListVerbose {
		fmt.Fprintln(w, "  Name\tFile size\tAvailable\t Uploaded\tProgress\tRedundancy\t Health\tStuck\tRenewing\tOn Disk\tRecoverable")
	}
	sort.Sort(byDirectoryInfo(dirs))
	// Print dirs.
	for _, dir := range dirs {
		fmt.Fprintf(w, "%v/\t\t\t\t\t\t\t\t\t\t\n", dir.dir.SiaPath)
		// Print subdirs.
		sort.Sort(bySiaPathDir(dir.subDirs))
		for _, subDir := range dir.subDirs {
			fmt.Fprintf(w, "  %s", subDir.SiaPath.Name()+"/")
			fmt.Fprintf(w, "\t%9s", modules.FilesizeUnits(subDir.AggregateSize))
			if renterListVerbose {
				redundancyStr := fmt.Sprintf("%.2f", subDir.AggregateMinRedundancy)
				if subDir.AggregateMinRedundancy == -1 {
					redundancyStr = "-"
				}
				healthStr := fmt.Sprintf("%.2f%%", subDir.AggregateMaxHealthPercentage)
				stuckStr := yesNo(subDir.AggregateNumStuckChunks > 0)
				fmt.Fprintf(w, "\t%9s\t%9s\t%8s\t%10s\t%7s\t%5s\t%8s\t%7s\t%11s", "-", "-", "-", redundancyStr, healthStr, stuckStr, "-", "-", "-")
			}
			fmt.Fprintln(w, "\t\t\t\t\t\t\t\t\t\t")
		}

		// Print files.
		sort.Sort(bySiaPathFile(dir.files))
		for _, file := range dir.files {
			name := file.SiaPath.Name()
			fmt.Fprintf(w, "  %s", name)
			fmt.Fprintf(w, "\t%9s", modules.FilesizeUnits(file.Filesize))
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
				fmt.Fprintf(w, "\t%9s\t%9s\t%8s\t%10s\t%7s\t%5s\t%8s\t%7s\t%11s", availableStr, modules.FilesizeUnits(file.UploadedBytes), uploadProgressStr, redundancyStr, healthStr, stuckStr, renewingStr, onDiskStr, recoverableStr)
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

// renterfusecmd displays the list of directories that are currently mounted via
// fuse.
func renterfusecmd() {
	// Get the list of mountpoints.
	fuseInfo, err := httpClient.RenterFuse()
	if err != nil {
		die("Unable to fetch fuse information:", err)
	}
	mountPoints := fuseInfo.MountPoints

	// Special message if nothing is mounted.
	if len(mountPoints) == 0 {
		fmt.Println("Nothing mounted.")
		return
	}

	// Sort the mountpoints.
	sort.Slice(mountPoints, func(i, j int) bool {
		return strings.Compare(mountPoints[i].MountPoint, mountPoints[j].MountPoint) < 0
	})

	// Print out the sorted set of mountpoints.
	fmt.Println("Mounted folders:")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "\t%s\t%s\n", "Mount Point", "SiaPath")
	for _, mp := range mountPoints {
		siaPathStr := mp.SiaPath.String()
		if siaPathStr == "" {
			siaPathStr = "{root}"
		}

		fmt.Fprintf(w, "\t%s\t%s\n", mp.MountPoint, siaPathStr)
	}
	w.Flush()
	fmt.Println()

}

// renterfusemountcmd is the handler for the command `siac renter fuse mount [path] [siapath]`.
func renterfusemountcmd(path, siaPathStr string) {
	// TODO: Once read-write is supported on the backend, the 'true' flag can be
	// set to 'false' - siac will support mounting read-write by default. Need
	// to update the help string of the command to indicate that mounting will
	// mount in read-write mode.
	path = abs(path)
	var siaPath modules.SiaPath
	var err error
	if siaPathStr == "" || siaPathStr == "/" {
		siaPath = modules.RootSiaPath()
	} else {
		siaPath, err = modules.NewSiaPath(siaPathStr)
		if err != nil {
			die("Unable to parse the siapath that should be mounted:", err)
		}
	}
	err = httpClient.RenterFuseMount(path, siaPath, true)
	if err != nil {
		die("Unable to mount the directory:", err)
	}
	fmt.Printf("mounted %s to %s\n", siaPathStr, path)
}

// renterfuseunmountcmd is the handler for the command `siac renter fuse unmount [path]`.
func renterfuseunmountcmd(path string) {
	path = abs(path)
	err := httpClient.RenterFuseUnmount(path)
	if err != nil {
		s := fmt.Sprintf("Unable to unmount %s:", path)
		die(s, err)
	}
	fmt.Printf("Unmounted %s successfully\n", path)
}

//rentersetlocalpathcmd is the handler for the command `siac renter setlocalpath [siapath] [newlocalpath]`
//Changes the trackingpath of the file
//through API Endpoint
func rentersetlocalpathcmd(siapath, newlocalpath string) {
	//Parse Siapath
	siaPath, err := modules.NewSiaPath(siapath)
	if err != nil {
		die("Couldn't parse Siapath:", err)
	}
	err = httpClient.RenterSetRepairPathPost(siaPath, newlocalpath)
	if err != nil {
		die("Could not Change the path of the file:", err)
	}
	fmt.Printf("Updated %s localpath to %s\n", siapath, newlocalpath)
}

// renterfilesunstuckcmd is the handler for the command `siac renter
// unstuckall`. Sets all files to unstuck.
func renterfilesunstuckcmd() {
	rfg, err := httpClient.RenterFilesGet(true)
	if err != nil {
		die("Couldn't get list of all files:", err)
	}
	// Declare a worker function to mark files as not stuck.
	var atomicFilesDone uint64
	toUnstuck := make(chan modules.SiaPath)
	worker := func() {
		for siaPath := range toUnstuck {
			err = httpClient.RenterSetFileStuckPost(siaPath, false)
			if err != nil {
				die(fmt.Sprintf("Couldn't set %v to unstuck: %v", siaPath, err))
			}
			atomic.AddUint64(&atomicFilesDone, 1)
		}
	}
	// Spin up some workers.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker()
		}()
	}
	// Pass the files on to the workers.
	lastStatusUpdate := time.Now()
	for _, f := range rfg.Files {
		toUnstuck <- f.SiaPath
		if time.Since(lastStatusUpdate) > time.Second {
			fmt.Printf("\r%v of %v files set to 'unstuck'",
				atomic.LoadUint64(&atomicFilesDone), len(rfg.Files))
			lastStatusUpdate = time.Now()
		}
	}
	close(toUnstuck)
	wg.Wait()
	fmt.Println("\nSet all files to 'unstuck'")
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
	periodFactor := uint64(rpg.Allowance.Period / types.BlocksPerMonth)

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

// renterratelimitcmd is the handler for the command `siac renter ratelimit`
// which sets the maxuploadspeed and maxdownloadspeed in bytes-per-second for
// the renter module
func renterratelimitcmd(downloadSpeedStr, uploadSpeedStr string) {
	downloadSpeedInt, err := parseRatelimit(downloadSpeedStr)
	if err != nil {
		die(errors.AddContext(err, "unable to parse download speed"))
	}
	uploadSpeedInt, err := parseRatelimit(uploadSpeedStr)
	if err != nil {
		die(errors.AddContext(err, "unable to parse upload speed"))
	}

	err = httpClient.RenterRateLimitPost(downloadSpeedInt, uploadSpeedInt)
	if err != nil {
		die(errors.AddContext(err, "Could not set renter ratelimit speed"))
	}
	fmt.Println("Set renter maxdownloadspeed to ", downloadSpeedInt, " and maxuploadspeed to ", uploadSpeedInt)
}
