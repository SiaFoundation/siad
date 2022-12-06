package main

// TODO: If you run siac from a non-existent directory, the abs() function does
// not handle this very gracefully.

import (
	"bufio"
	"encoding/json"
	"fmt"
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
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/renter/filesystem"
	"go.sia.tech/siad/node/api"
	"go.sia.tech/siad/node/api/client"
	"go.sia.tech/siad/types"
)

const (
	fileSizeUnits = "B, KB, MB, GB, TB, PB, EB, ZB, YB"

	// truncateErrLength is the length at which an error string gets truncated
	truncateErrLength = 24

	// colourful strings for the console UI
	pBarJobProcess = "\x1b[34;1mpinning   \x1b[0m" // blue
	pBarJobUpload  = "\x1b[33;1muploading \x1b[0m" // yellow
	pBarJobDone    = "\x1b[32;1mpinned!   \x1b[0m" // green
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

	renterBubbleCmd = &cobra.Command{
		Use:   "bubble [directory]",
		Short: "Call bubble on a directory.",
		Long: `Call bubble on a directory to manually force an update of the directories metadata.
To bubble the root directory pass in '.' as the directory.`,
		Run: wrap(renterbubblecmd),
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

	renterCleanCmd = &cobra.Command{
		Use:   "clean",
		Short: "Cleans up lost files",
		Long: `WARNING: This action will permanently delete any files associated with
the renter that do not have a local copy on disk and have a redundancy of < 1.

You will be asked if you want to see these lost files. Additionally you can use
the command 'siac renter lost' to see the renter's lost files.`,
		Run: wrap(rentercleancmd),
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
		Long:    "Delete a file or folder. Does not delete the file/folder on disk.  Multiple files may be deleted with space separation.",
		Run:     renterfilesdeletecmd,
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
		Long: `Upload a file or folder to [path] on the Sia network. The --data-pieces and --parity-pieces
flags can be used to set a custom redundancy for the file.`,
		Run: wrap(renterfilesuploadcmd),
	}

	renterFilesUploadPauseCmd = &cobra.Command{
		Use:   "pause [duration]",
		Short: "Pause renter uploads for a duration",
		Long: `Temporarily pause renter uploads for the duration specified.
Available durations include "s" for seconds, "m" for minutes, and "h" for hours.
For Example: 'siac renter upload pause 3h' would pause uploads for 3 hours.`,
		Run: wrap(renterfilesuploadpausecmd),
	}

	renterFilesUploadResumeCmd = &cobra.Command{
		Use:   "resume",
		Short: "Resume renter uploads",
		Long:  "Resume renter uploads that were previously paused.",
		Run:   wrap(renterfilesuploadresumecmd),
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
		Short: "Set maxdownloadspeed and maxuploadspeed",
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

	renterWorkersCmd = &cobra.Command{
		Use:   "workers",
		Short: "View the Renter's workers",
		Long:  "View the status of the Renter's workers",
		Run:   wrap(renterworkerscmd),
	}

	renterWorkersAccountsCmd = &cobra.Command{
		Use:   "ea",
		Short: "View the workers' ephemeral account",
		Long:  "View detailed information of the workers' ephemeral account",
		Run:   wrap(renterworkerseacmd),
	}

	renterWorkersDownloadsCmd = &cobra.Command{
		Use:   "dj",
		Short: "View the workers' download jobs",
		Long:  "View detailed information of the workers' download jobs",
		Run:   wrap(renterworkersdownloadscmd),
	}

	renterWorkersHasSectorJobSCmd = &cobra.Command{
		Use:   "hsj",
		Short: "View the workers' has sector jobs",
		Long:  "View detailed information of the workers' has sector jobs",
		Run:   wrap(renterworkershsjcmd),
	}

	renterWorkersPriceTableCmd = &cobra.Command{
		Use:   "pt",
		Short: "View the workers's price table",
		Long:  "View detailed information of the workers' price table",
		Run:   wrap(renterworkersptcmd),
	}

	renterWorkersReadJobsCmd = &cobra.Command{
		Use:   "rj",
		Short: "View the workers' read jobs",
		Long:  "View detailed information of the workers' read jobs",
		Run:   wrap(renterworkersrjcmd),
	}

	renterWorkersUploadsCmd = &cobra.Command{
		Use:   "uj",
		Short: "View the workers' upload jobs",
		Long:  "View detailed information of the workers' upload jobs",
		Run:   wrap(renterworkersuploadscmd),
	}

	renterWorkersReadRegistryCmd = &cobra.Command{
		Use:   "rrj",
		Short: "View the workers' read registry jobs",
		Long:  "View detailed information of the workers' read registry jobs",
		Run:   wrap(renterworkersreadregistrycmd),
	}

	renterWorkersUpdateRegistryCmd = &cobra.Command{
		Use:   "urj",
		Short: "View the workers' update registry jobs",
		Long:  "View detailed information of the workers' update registry jobs",
		Run:   wrap(renterworkersupdateregistrycmd),
	}

	renterHealthSummaryCmd = &cobra.Command{
		Use:   "health",
		Short: "Display a health summary of uploaded files",
		Long:  "Display a health summary of uploaded files",
		Run:   wrap(renterhealthsummarycmd),
	}

	renterLostCmd = &cobra.Command{
		Use:   "lost",
		Short: "Display the renter's lost files",
		Long:  "Display the renter's lost files",
		Run:   wrap(renterlostcmd),
	}
)

// rentercleancmd cleans any lost files from the renter.
func rentercleancmd() {
	// Print initial warning
	fmt.Println("WARNING: This command will delete lost files and cannot be undone!")

	// Ask user if they want to see the lost files they are about to delete.
	confirmed := askForConfirmation("Would you like see the lost files that will be deleted?")
	if confirmed {
		renterlostcmd()
	}

	// Confirm user wants to proceed
	confirmed = askForConfirmation("Are you sure you want to continue and delete the lost files?")
	if !confirmed {
		return
	}

	// Clean up lost files
	fmt.Println("Cleaning lost files...")
	err := httpClient.RenterCleanPost()
	if err != nil {
		die("Unable to clean renter's lost files:", err)
	}
	fmt.Println("Successfully cleaned lost files!")
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
	rate, err := types.ParseExchangeRate(build.ExchangeRate())
	if err != nil {
		fmt.Printf("Warning: ignoring exchange rate - %s\n", err)
	}

	fmt.Println()
	fmt.Printf(`Allowance:`)
	if rg.Settings.Allowance.Funds.IsZero() {
		fmt.Printf("      0 SC (No current allowance)\n")
	} else {
		fm := rg.FinancialMetrics
		totalSpent := fm.ContractFees.Add(fm.UploadSpending).
			Add(fm.DownloadSpending).Add(fm.StorageSpending).Add(fm.FundAccountSpending).Add(fm.MaintenanceSpending.Sum())
		fmt.Printf(`       %v
  Spent Funds:     %v
  Unspent Funds:   %v
`, currencyUnitsWithExchangeRate(rg.Settings.Allowance.Funds, rate),
			currencyUnitsWithExchangeRate(totalSpent, rate),
			currencyUnitsWithExchangeRate(fm.Unspent, rate))
	}

	// detailed allowance spending for current period
	if verbose {
		renterallowancespending(rg)
	}

	// File and Contract Data
	fmt.Println()
	fmt.Println(`Data Storage:`)
	err = renterFilesAndContractSummary()
	if err != nil {
		die(err)
	}

	if !verbose {
		return
	}

	// Print out the memory information for the renter
	ms := rg.MemoryStatus
	ud := ms.UserDownload
	uu := ms.UserUpload
	reg := ms.Registry
	sys := ms.System
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	fmt.Fprintf(w, "\nMemory Status\tUser Download\tUser Upload\tRegistry\tSystem\tTotal\n")
	fmt.Fprintf(w, "  Available Memory\t%v\t%v\t%v\t%v\t%v\n", sizeString(ud.Available), sizeString(uu.Available), sizeString(reg.Available), sizeString(sys.Available), sizeString(ms.Available))
	fmt.Fprintf(w, "  Starting Memory\t%v\t%v\t%v\t%v\t%v\n", sizeString(ud.Base), sizeString(uu.Base), sizeString(reg.Base), sizeString(sys.Base), sizeString(ms.Base))
	fmt.Fprintf(w, "  Requested Memory\t%v\t%v\t%v\t%v\t%v\n", sizeString(ud.Requested), sizeString(uu.Requested), sizeString(reg.Requested), sizeString(sys.Requested), sizeString(ms.Requested))
	fmt.Fprintf(w, " \t \t \t \t \t \n")
	fmt.Fprintf(w, "  Available Priority Memory\t%v\t%v\t%v\t%v\t%v\n", sizeString(ud.PriorityAvailable), sizeString(uu.PriorityAvailable), sizeString(reg.PriorityAvailable), sizeString(sys.PriorityAvailable), sizeString(ms.PriorityAvailable))
	fmt.Fprintf(w, "  Starting Priority Memory\t%v\t%v\t%v\t%v\t%v\n", sizeString(ud.PriorityBase), sizeString(uu.PriorityBase), sizeString(reg.PriorityBase), sizeString(sys.PriorityBase), sizeString(ms.PriorityBase))
	fmt.Fprintf(w, "  Requested Priority Memory\t%v\t%v\t%v\t%v\t%v\n", sizeString(ud.PriorityRequested), sizeString(uu.PriorityRequested), sizeString(reg.PriorityRequested), sizeString(sys.PriorityRequested), sizeString(ms.PriorityRequested))
	fmt.Fprintln(w, "")

	// Print out if the uploads are paused
	if verbose {
		var pauseEndTime time.Duration
		if rg.Settings.UploadsStatus.PauseEndTime.After(time.Now()) {
			pauseEndTime = time.Until(rg.Settings.UploadsStatus.PauseEndTime)
		}
		fmt.Fprintf(w, "\nUploads Status\n")
		fmt.Fprintf(w, "  Paused:\t%v\n", yesNo(rg.Settings.UploadsStatus.Paused))
		fmt.Fprintf(w, "  Pause End Time:\t%v\n", pauseEndTime)
	}

	// Flush the writer
	err = w.Flush()
	if err != nil {
		die(err)
	}

	// Print out ratelimit info about the renter
	fmt.Println()
	rateLimitSummary(rg.Settings.MaxDownloadSpeed, rg.Settings.MaxUploadSpeed)
}

// renterlostcmd is the handler for displaying the renter's lost files.
func renterlostcmd() {
	// Print out the lost files of the renter
	dirs := getDir(modules.RootSiaPath(), true, true)
	_, _, err := fileHealthBreakdown(dirs, true)
	if err != nil {
		die("Unable to display lost files:", err)
	}
}

// renterhealthsummarycmd is the handler for displaying the overall health
// summary for uploaded files.
func renterhealthsummarycmd() {
	// Print out file health summary for the renter
	dirs := getDir(modules.RootSiaPath(), true, true)
	renterFileHealthSummary(dirs)
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

// renterallowancecmd is the handler for the command `siac renter allowance`.
// displays the current allowance.
func renterallowancecmd() {
	rg, err := httpClient.RenterGet()
	if err != nil {
		die("Could not get allowance:", err)
	}
	allowance := rg.Settings.Allowance

	// Show allowance info
	rate, err := types.ParseExchangeRate(build.ExchangeRate())
	if err != nil {
		fmt.Printf("Warning: ignoring exchange rate - %s\n", err)
	}

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

Price Protections:
  MaxRPCPrice:               %v per million requests
  MaxContractPrice:          %v
  MaxDownloadBandwidthPrice: %v per TB
  MaxSectorAccessPrice:      %v per million accesses
  MaxStoragePrice:           %v per TB per Month
  MaxUploadBandwidthPrice:   %v per TB
`, currencyUnitsWithExchangeRate(allowance.Funds, rate), allowance.Period, allowance.RenewWindow,
		allowance.Hosts,
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

`, currencyUnitsWithExchangeRate(fm.PreviousSpending, rate),
			currencyUnitsWithExchangeRate(fm.WithheldFunds, rate), fm.ReleaseBlock)
	}
}

// renterallowancecancelcmd is the handler for `siac renter allowance cancel`.
// cancels the current allowance.
func renterallowancecancelcmd() {
	fmt.Println(`Canceling your allowance will disable uploading new files,
repairing existing files, and renewing existing files. All files will cease
to be accessible after a short period of time.`)
	confirmed := askForConfirmation("Do you want to continue?")
	if !confirmed {
		return
	}
	err := httpClient.RenterAllowanceCancelPost()
	if err != nil {
		die("error canceling allowance:", err)
	}
	fmt.Println("Allowance canceled.")
}

// rentersetallowancecmd is the handler for `siac renter setallowance`.
// set the allowance or modify individual allowance fields.
func rentersetallowancecmd(_ *cobra.Command, _ []string) {
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
		hastings, err := types.ParseCurrency(allowanceFunds)
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
		priceStr, err := types.ParseCurrency(allowanceMaxRPCPrice)
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
		priceStr, err := types.ParseCurrency(allowanceMaxContractPrice)
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
		priceStr, err := types.ParseCurrency(allowanceMaxDownloadBandwidthPrice)
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
		priceStr, err := types.ParseCurrency(allowanceMaxSectorAccessPrice)
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
		priceStr, err := types.ParseCurrency(allowanceMaxStoragePrice)
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
		priceStr, err := types.ParseCurrency(allowanceMaxUploadBandwidthPrice)
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

// rentersetallowancecmdInteractive is the interactive handler for `siac renter
// setallowance`.
func rentersetallowancecmdInteractive(req *client.AllowanceRequestPost, allowance modules.Allowance) *client.AllowanceRequestPost {
	br := bufio.NewReader(os.Stdin)
	readString := func() string {
		str, _ := br.ReadString('\n')
		return strings.TrimSpace(str)
	}

	fmt.Println("Interactive tool for setting the 8 allowance options.")

	// funds
	fmt.Println()
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
	for {
		fmt.Print("Funds: ")
		allowanceFunds := readString()
		if allowanceFunds == "" {
			break
		}

		hastings, err := types.ParseCurrency(allowanceFunds)
		if err != nil {
			fmt.Printf("Could not parse currency in '%v': %v\n", allowanceFunds, err)
			continue
		}
		_, err = fmt.Sscan(hastings, &funds)
		if err != nil {
			fmt.Printf("Could not parse currency in '%v': %v\n", allowanceFunds, err)
			continue
		}
		if funds.IsZero() {
			fmt.Println("Allowance funds cannot be 0")
			continue
		}
		break
	}
	req = req.WithFunds(funds)

	// period
	fmt.Println()
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
	for {
		fmt.Print("Period: ")
		allowancePeriod := readString()
		if allowancePeriod == "" {
			break
		}

		blocks, err := parsePeriod(allowancePeriod)
		if err != nil {
			fmt.Printf("Could not parse period in '%v': %v\n", allowancePeriod, err)
			continue
		}
		_, err = fmt.Sscan(blocks, &period)
		if err != nil {
			fmt.Printf("Could not parse period in '%v': %v\n", allowancePeriod, err)
			continue
		}
		if period == 0 {
			fmt.Println("Period cannot be 0")
			continue
		}
		break
	}
	req = req.WithPeriod(period)

	// hosts
	fmt.Println()
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
	for {
		fmt.Print("Hosts: ")
		allowanceHosts := readString()
		if allowanceHosts == "" {
			break
		}

		hostsInt, err := strconv.Atoi(allowanceHosts)
		if err != nil {
			fmt.Printf("Could not parse host count in '%v': %v\n", allowanceHosts, err)
			continue
		}
		hosts = uint64(hostsInt)
		if hosts == 0 {
			fmt.Println("Must have at least 1 host")
			continue
		}
		break
	}
	req = req.WithHosts(hosts)

	// renewWindow
	fmt.Println()
	fmt.Println(`4/8: Renew Window
The renew window is how long the user has to renew their contracts. At the end
of the period, all of the contracts expire. The contracts need to be renewed
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
	for {
		fmt.Print("Renew Window: ")
		allowanceRenewWindow := readString()
		if allowanceRenewWindow == "" {
			break
		}

		rw, err := parsePeriod(allowanceRenewWindow)
		if err != nil {
			fmt.Printf("Could not parse renew window in '%v': %v\n", allowanceRenewWindow, err)
			continue
		}
		_, err = fmt.Sscan(rw, &renewWindow)
		if err != nil {
			fmt.Printf("Could not parse renew window in '%v': %v\n", allowanceRenewWindow, err)
			continue
		}
		if renewWindow == 0 {
			fmt.Println("Cannot set renew window to zero")
			continue
		}
		break
	}
	req = req.WithRenewWindow(renewWindow)

	// expectedStorage
	fmt.Println()
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
and bandwidth needs while spending significantly less than the overall allowance.

The following units can be used to set the expected storage:`)
	fmt.Println()
	fmt.Printf("    %v\n", fileSizeUnits)
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
	for {
		fmt.Print("Expected Storage: ")
		allowanceExpectedStorage := readString()
		if allowanceExpectedStorage == "" {
			break
		}

		es, err := parseFilesize(allowanceExpectedStorage)
		if err != nil {
			fmt.Printf("Could not parse expected storage in '%v': %v\n", allowanceExpectedStorage, err)
			continue
		}
		_, err = fmt.Sscan(es, &expectedStorage)
		if err != nil {
			fmt.Printf("Could not parse expected storage in '%v': %v\n", allowanceExpectedStorage, err)
			continue
		}
		break
	}
	req = req.WithExpectedStorage(expectedStorage)

	// expectedUpload
	fmt.Println()
	fmt.Println(`6/8: Expected Upload
Expected upload tells siad how much uploading the user expects to do each
period. If this value is high, siad will more strongly prefer hosts that have a
low upload bandwidth price. If this value is low, siad will focus on other
metrics than upload bandwidth pricing, because even if the host charges a lot
for upload bandwidth, it will not impact the total cost to the user very much.

The user should not consider upload bandwidth used during repairs, siad will
consider repair bandwidth separately.

The following units can be used to set the expected upload:`)
	fmt.Println()
	fmt.Printf("    %v\n", fileSizeUnits)
	fmt.Println()
	euCurrentPeriod := allowance.ExpectedUpload * uint64(allowance.Period)
	euDefaultPeriod := modules.DefaultAllowance.ExpectedUpload * uint64(modules.DefaultAllowance.Period)
	fmt.Println("Current value:", modules.FilesizeUnits(euCurrentPeriod))
	fmt.Println("Default value:", modules.FilesizeUnits(euDefaultPeriod))

	if allowance.ExpectedUpload == 0 {
		fmt.Println("Enter desired value below, or leave blank to use default value")
	} else {
		fmt.Println("Enter desired value below, or leave blank to use current value")
	}
	var expectedUpload uint64
	for {
		fmt.Print("Expected Upload: ")
		allowanceExpectedUpload := readString()
		if allowanceExpectedUpload == "" {
			// The user did not enter a value so use either the default or the
			// current value, as appropriate.
			if allowance.ExpectedUpload == 0 {
				expectedUpload = euDefaultPeriod
			} else {
				expectedUpload = euCurrentPeriod
			}
			break
		}

		eu, err := parseFilesize(allowanceExpectedUpload)
		if err != nil {
			fmt.Printf("Could not parse expected upload in '%v': %v\n", allowanceExpectedUpload, err)
			continue
		}
		_, err = fmt.Sscan(eu, &expectedUpload)
		if err != nil {
			fmt.Printf("Could not parse expected upload in '%v': %v\n", allowanceExpectedUpload, err)
			continue
		}
		break
	}
	// User set field in terms of period, need to normalize to per-block.
	expectedUpload /= uint64(period)
	req = req.WithExpectedUpload(expectedUpload)

	// expectedDownload
	fmt.Println()
	fmt.Println(`7/8: Expected Download
Expected download tells siad how much downloading the user expects to do each
period. If this value is high, siad will more strongly prefer hosts that have a
low download bandwidth price. If this value is low, siad will focus on other
metrics than download bandwidth pricing, because even if the host charges a lot
for downloads, it will not impact the total cost to the user very much.

The user should not consider download bandwidth used during repairs, siad will
consider repair bandwidth separately.

The following units can be used to set the expected download:`)
	fmt.Println()
	fmt.Printf("    %v\n", fileSizeUnits)
	fmt.Println()
	edCurrentPeriod := allowance.ExpectedDownload * uint64(allowance.Period)
	edDefaultPeriod := modules.DefaultAllowance.ExpectedDownload * uint64(modules.DefaultAllowance.Period)
	fmt.Println("Current value:", modules.FilesizeUnits(edCurrentPeriod))
	fmt.Println("Default value:", modules.FilesizeUnits(edDefaultPeriod))

	if allowance.ExpectedDownload == 0 {
		fmt.Println("Enter desired value below, or leave blank to use default value")
	} else {
		fmt.Println("Enter desired value below, or leave blank to use current value")
	}
	var expectedDownload uint64
	for {
		fmt.Print("Expected Download: ")
		allowanceExpectedDownload := readString()
		if allowanceExpectedDownload == "" {
			// The user did not enter a value so use either the default or the
			// current value, as appropriate.
			if allowance.ExpectedDownload == 0 {
				expectedDownload = edDefaultPeriod
			} else {
				expectedDownload = edCurrentPeriod
			}
			break
		}

		ed, err := parseFilesize(allowanceExpectedDownload)
		if err != nil {
			fmt.Printf("Could not parse expected download in '%v': %v\n", allowanceExpectedDownload, err)
			continue
		}
		_, err = fmt.Sscan(ed, &expectedDownload)
		if err != nil {
			fmt.Printf("Could not parse expected download in '%v': %v\n", allowanceExpectedDownload, err)
			continue
		}
		break
	}
	// User set field in terms of period, need to normalize to per-block.
	expectedDownload /= uint64(period)
	req = req.WithExpectedDownload(expectedDownload)

	// expectedRedundancy
	fmt.Println()
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
	for {
		fmt.Print("Expected Redundancy: ")
		allowanceExpectedRedundancy := readString()
		if allowanceExpectedRedundancy == "" {
			break
		}

		expectedRedundancy, err = strconv.ParseFloat(allowanceExpectedRedundancy, 64)
		if err != nil {
			fmt.Printf("Could not parse expected redundancy in '%v': %v\n", allowanceExpectedRedundancy, err)
			continue
		}
		if expectedRedundancy < 1 {
			fmt.Println("Expected redundancy must be at least 1")
			continue
		}
		break
	}
	req = req.WithExpectedRedundancy(expectedRedundancy)
	fmt.Println()

	return req
}

// renterbubblecmd is the handler for the command `siac renter
// bubble`.
func renterbubblecmd(directory string) {
	// Parse the siapath
	var siaPath modules.SiaPath
	if directory == "." {
		directory = "root" // For UX
		siaPath = modules.RootSiaPath()
	} else {
		err := siaPath.LoadString(directory)
		if err != nil {
			die("Unable to load siapath:", err)
		}
	}
	fmt.Println("Calling bubble on:", directory)

	// Bubble Directory
	err := httpClient.RenterBubblePost(siaPath, true, renterBubbleAll)
	if err != nil {
		die("Unable to bubble", directory, ":", err)
	}
	fmt.Println("Bubble successful!")
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
	if err := w.Flush(); err != nil {
		die("failed to flush writer:", err)
	}
}

// rentercontractscmd is the handler for the command `siac renter contracts`.
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
	rc, err := httpClient.RenterAllContractsGet()
	if err != nil {
		die("Could not get contract details: ", err)
	}

	contracts := append(rc.ActiveContracts, rc.PassiveContracts...)
	contracts = append(contracts, rc.RefreshedContracts...)
	contracts = append(contracts, rc.DisabledContracts...)
	contracts = append(contracts, rc.ExpiredContracts...)
	contracts = append(contracts, rc.ExpiredRefreshedContracts...)

	err = printContractInfo(cid, contracts)
	if err != nil {
		die(err)
	}
}

// renterfilesdownload downloads the dir at the given path from the Sia network
// to the local specified destination.
func renterdirdownload(path, destination string) {
	destination = abs(destination)
	// Parse SiaPath.
	siaPath, err := modules.NewSiaPath(path)
	if err != nil {
		die("Couldn't parse SiaPath:", err)
	}
	// If root is not set we need to rebase.
	if !renterDownloadRoot {
		siaPath, err = siaPath.Rebase(modules.RootSiaPath(), modules.UserFolder)
		if err != nil {
			die("Couldn't rebase SiaPath:", err)
		}
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
	failedDownloads := downloadProgress(tfs)
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
func renterfilesdeletecmd(cmd *cobra.Command, paths []string) {
	for _, path := range paths {
		// Parse SiaPath.
		siaPath, err := modules.NewSiaPath(path)
		if err != nil {
			die("Couldn't parse SiaPath:", err)
		}

		// Try to delete file.
		//
		// In the case where the path points to a dir, this will fail and we
		// silently move on to deleting it as a dir. This is more efficient than
		// querying the renter first to see if it is a file or a dir, as that is
		// guaranteed to always be two renter calls.
		var errFile error
		if renterDeleteRoot {
			errFile = httpClient.RenterFileDeleteRootPost(siaPath)
		} else {
			errFile = httpClient.RenterFileDeletePost(siaPath)
		}
		if errFile == nil {
			fmt.Printf("Deleted file '%v'\n", path)
			continue
		} else if !(strings.Contains(errFile.Error(), filesystem.ErrNotExist.Error()) || strings.Contains(errFile.Error(), filesystem.ErrDeleteFileIsDir.Error())) {
			die(fmt.Sprintf("Failed to delete file %v: %v", path, errFile))
		}
		// Try to delete dir.
		var errDir error
		if renterDeleteRoot {
			errDir = httpClient.RenterDirDeleteRootPost(siaPath)
		} else {
			errDir = httpClient.RenterDirDeletePost(siaPath)
		}
		if errDir == nil {
			fmt.Printf("Deleted directory '%v'\n", path)
			continue
		} else if !strings.Contains(errDir.Error(), filesystem.ErrNotExist.Error()) {
			die(fmt.Sprintf("Failed to delete directory %v: %v", path, errDir))
		}

		// Unknown file/dir.
		die(fmt.Sprintf("Unknown path '%v'", path))
	}
	return
}

// renterfilesdownload is the handler for the command `siac renter download
// [path] [destination]`. It determines whether a file or a folder is downloaded
// and calls the corresponding sub-handler.
func renterfilesdownloadcmd(path, destination string) {
	// Parse SiaPath.
	siaPath, err := modules.NewSiaPath(path)
	if err != nil {
		die("Couldn't parse SiaPath:", err)
	}
	// If root is not set we need to rebase.
	if !renterDownloadRoot {
		siaPath, err = siaPath.Rebase(modules.RootSiaPath(), modules.UserFolder)
		if err != nil {
			die("Couldn't rebase SiaPath:", err)
		}
	}
	_, err = httpClient.RenterFileRootGet(siaPath)
	if err == nil {
		renterFilesDownload(path, destination)
		return
	} else if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		die("Failed to download file:", err)
	}
	_, err = httpClient.RenterDirRootGet(siaPath)
	if err == nil {
		renterdirdownload(path, destination)
		return
	} else if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
		die("Failed to download folder:", err)
	}
	die(fmt.Sprintf("Unknown path '%v'", path))
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

// renterfileslistcmd is the handler for the command `siac renter ls`. Lists
// files known to the renter on the network.
func renterfileslistcmd(cmd *cobra.Command, args []string) {
	var path string
	switch len(args) {
	case 0:
		path = "."
	case 1:
		path = args[0]
	default:
		_ = cmd.UsageFunc()(cmd)
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
		var rf api.RenterFile
		if renterListRoot {
			rf, err = httpClient.RenterFileRootGet(sp)
		} else {
			rf, err = httpClient.RenterFileGet(sp)
		}
		if err == nil {
			json, err := json.MarshalIndent(rf.File, "", "  ")
			if err != nil {
				log.Fatal(err)
			}

			fmt.Println()
			fmt.Println(string(json))
			fmt.Println()
			return
		} else if !strings.Contains(err.Error(), filesystem.ErrNotExist.Error()) {
			die(fmt.Sprintf("Error getting file %v: %v", path, err))
		}
	}

	// Get dirs with their corresponding files.
	dirs := getDir(sp, renterListRoot, renterListRecursive)

	// Sort the directories and the files.
	sort.Sort(byDirectoryInfo(dirs))
	for i := 0; i < len(dirs); i++ {
		sort.Sort(bySiaPathDir(dirs[i].subDirs))
		sort.Sort(bySiaPathFile(dirs[i].files))
	}

	// Get the total number of listings (subdirs and files).
	root := dirs[0] // Root directory we are querying.
	totalStored := root.dir.AggregateSize
	var numFilesDirs uint64
	if renterListRecursive {
		numFilesDirs = root.dir.AggregateNumFiles + root.dir.AggregateNumSubDirs
	} else {
		numFilesDirs = root.dir.NumFiles + root.dir.NumSubDirs
	}

	// Print totals for both verbose and not verbose output.
	totalStoredStr := modules.FilesizeUnits(totalStored)
	fmt.Printf("\nListing %v files/dirs:\t%9s\n\n", numFilesDirs, totalStoredStr)

	// Handle the non verbose output.
	if !verbose {
		for _, dir := range dirs {
			fmt.Printf("%v/\n", dir.dir.SiaPath)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for _, subDir := range dir.subDirs {
				name := subDir.SiaPath.Name() + "/"
				size := modules.FilesizeUnits(subDir.AggregateSize)
				fmt.Fprintf(w, "  %v\t%9v\n", name, size)
			}

			for _, file := range dir.files {
				name := file.SiaPath.Name()
				size := modules.FilesizeUnits(file.Filesize)
				fmt.Fprintf(w, "  %v\t%9v\n", name, size)
			}
			if err := w.Flush(); err != nil {
				die("failed to flush writer:", err)
			}
			fmt.Println()
		}
		return
	}

	// Handle the verbose output.
	for _, dir := range dirs {
		fmt.Println(dir.dir.SiaPath.String() + "/")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "  Name\tFile size\tAvailable\t Uploaded\tProgress\tRedundancy\tHealth\tStuck Health\tStuck\tRenewing\tOn Disk\tRecoverable\n")
		for _, subDir := range dir.subDirs {
			name := subDir.SiaPath.Name() + "/"
			size := modules.FilesizeUnits(subDir.AggregateSize)
			redundancyStr := fmt.Sprintf("%.2f", subDir.AggregateMinRedundancy)
			if subDir.AggregateMinRedundancy == -1 {
				redundancyStr = "-"
			}
			healthStr := fmt.Sprintf("%.2f%%", modules.HealthPercentage(subDir.AggregateHealth))
			stuckHealthStr := fmt.Sprintf("%.2f%%", modules.HealthPercentage(subDir.AggregateStuckHealth))
			stuckStr := yesNo(subDir.AggregateNumStuckChunks > 0)
			fmt.Fprintf(w, "  %v\t%9v\t%9s\t%9s\t%8s\t%10s\t%7s\t%7s\t%5s\t%8s\t%7s\t%11s\n", name, size, "-", "-", "-", redundancyStr, healthStr, stuckHealthStr, stuckStr, "-", "-", "-")
		}

		for _, file := range dir.files {
			name := file.SiaPath.Name()
			size := modules.FilesizeUnits(file.Filesize)
			availStr := yesNo(file.Available)
			bytesUploaded := modules.FilesizeUnits(file.UploadedBytes)
			uploadStr := fmt.Sprintf("%.2f%%", file.UploadProgress)
			if file.UploadProgress == -1 {
				uploadStr = "-"
			}
			redundancyStr := fmt.Sprintf("%.2f", file.Redundancy)
			if file.Redundancy == -1 {
				redundancyStr = "-"
			}

			healthStr := fmt.Sprintf("%.2f%%", modules.HealthPercentage(file.Health))
			stuckHealthStr := fmt.Sprintf("%.2f%%", modules.HealthPercentage(file.StuckHealth))
			stuckStr := yesNo(file.Stuck)
			renewStr := yesNo(file.Renewing)
			onDiskStr := yesNo(file.OnDisk)
			recoverStr := yesNo(file.Recoverable)
			fmt.Fprintf(w, "  %v\t%9v\t%9s\t%9s\t%8s\t%10s\t%7s\t%7s\t%5s\t%8s\t%7s\t%11s\n", name, size, availStr, bytesUploaded, uploadStr, redundancyStr, healthStr, stuckHealthStr, stuckStr, renewStr, onDiskStr, recoverStr)
		}
		if err := w.Flush(); err != nil {
			die("failed to flush writer:", err)
		}
		fmt.Println()
	}
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
	err := httpClient.RenterRenamePost(siaPath, newSiaPath, renterRenameRoot)
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
	if err := w.Flush(); err != nil {
		die("failed to flush writer:", err)
	}
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
	opts := modules.MountOptions{
		ReadOnly:   true,
		AllowOther: renterFuseMountAllowOther,
	}
	err = httpClient.RenterFuseMount(path, siaPath, opts)
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

// rentersetlocalpathcmd is the handler for the command `siac renter setlocalpath [siapath] [newlocalpath]`
// Changes the trackingpath of the file
// through API Endpoint
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
	// Get all dirs and their files recursively.
	dirs := getDir(modules.RootSiaPath(), true, true)

	// Count all files.
	totalFiles := 0
	for _, d := range dirs {
		totalFiles += len(d.files)
	}

	// Declare a worker function to mark files as not stuck.
	var atomicFilesDone uint64
	toUnstuck := make(chan modules.SiaPath)
	worker := func() {
		for siaPath := range toUnstuck {
			err := httpClient.RenterSetFileStuckPost(siaPath, true, false)
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
	for _, d := range dirs {
		for _, f := range d.files {
			if !f.Stuck && f.NumStuckChunks == 0 {
				// Nothing to do. Count as set for progress.
				atomic.AddUint64(&atomicFilesDone, 1)
				continue
			}
			toUnstuck <- f.SiaPath
			if time.Since(lastStatusUpdate) > time.Second {
				fmt.Printf("\r%v of %v files set to 'unstuck'",
					atomic.LoadUint64(&atomicFilesDone), totalFiles)
				lastStatusUpdate = time.Now()
			}
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

	// Check for and parse any redundancy settings
	numDataPieces, numParityPieces, err := api.ParseDataAndParityPieces(dataPieces, parityPieces)
	if err != nil {
		die("Could not parse data and parity pieces:", err)
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
			err = httpClient.RenterUploadPost(abs(file), fSiaPath, uint64(numDataPieces), uint64(numParityPieces))
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
		err = httpClient.RenterUploadPost(abs(source), siaPath, uint64(numDataPieces), uint64(numParityPieces))
		if err != nil {
			die("Could not upload file:", err)
		}
		fmt.Printf("Uploaded '%s' as '%s'.\n", abs(source), path)
	}
}

// renterfilesuploadpausecmd is the handler for the command `siac renter upload
// pause`.  It pauses all renter uploads for the duration (in minutes)
// passed in.
func renterfilesuploadpausecmd(dur string) {
	pauseDuration, err := time.ParseDuration(dur)
	if err != nil {
		die("Couldn't parse duration:", err)
	}
	err = httpClient.RenterUploadsPausePost(pauseDuration)
	if err != nil {
		die("Could not pause renter uploads:", err)
	}
	fmt.Println("Renter uploads have been paused for", dur)
}

// renterfilesuploadresumecmd is the handler for the command `siac renter upload
// resume`.  It resumes all renter uploads that have been paused.
func renterfilesuploadresumecmd() {
	err := httpClient.RenterUploadsResumePost()
	if err != nil {
		die("Could not resume renter uploads:", err)
	}
	fmt.Println("Renter uploads have been resumed")
}

// renterpricescmd is the handler for the command `siac renter prices`, which
// displays the prices of various storage operations. The user can submit an
// allowance to have the estimate reflect those settings or the user can submit
// nothing
func renterpricescmd(cmd *cobra.Command, args []string) {
	allowance := modules.Allowance{}

	if len(args) != 0 && len(args) != 4 {
		_ = cmd.UsageFunc()(cmd)
		os.Exit(exitCodeUsage)
	}
	if len(args) > 0 {
		hastings, err := types.ParseCurrency(args[0])
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
	rate, err := types.ParseExchangeRate(build.ExchangeRate())
	if err != nil {
		fmt.Printf("Warning: ignoring exchange rate - %s\n", err)
	}

	fmt.Println("Renter Prices (estimated):")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "\tFees for Creating a Set of Contracts:\t", currencyUnitsWithExchangeRate(rpg.FormContracts, rate))
	fmt.Fprintln(w, "\tDownload 1 TB:\t", currencyUnitsWithExchangeRate(rpg.DownloadTerabyte, rate))
	fmt.Fprintln(w, "\tStore 1 TB for 1 Month:\t", currencyUnitsWithExchangeRate(rpg.StorageTerabyteMonth, rate))
	fmt.Fprintln(w, "\tStore 1 TB for Allowance Period:\t", currencyUnitsWithExchangeRate(rpg.StorageTerabyteMonth.Mul64(periodFactor), rate))
	fmt.Fprintln(w, "\tUpload 1 TB:\t", currencyUnitsWithExchangeRate(rpg.UploadTerabyte, rate))
	if err := w.Flush(); err != nil {
		die("failed to flush writer:", err)
	}

	// Display allowance used for estimate
	fmt.Println("\nAllowance used for estimate:")
	fmt.Fprintln(w, "\tFunds:\t", currencyUnitsWithExchangeRate(rpg.Allowance.Funds, rate))
	fmt.Fprintln(w, "\tPeriod:\t", rpg.Allowance.Period)
	fmt.Fprintln(w, "\tHosts:\t", rpg.Allowance.Hosts)
	fmt.Fprintln(w, "\tRenew Window:\t", rpg.Allowance.RenewWindow)
	if err := w.Flush(); err != nil {
		die("failed to flush writer:", err)
	}
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

// renterworkerscmd is the handler for the command `siac renter workers`.
// It lists the Renter's workers.
func renterworkerscmd() {
	rw, err := httpClient.RenterWorkersGet()
	if err != nil {
		die("Could not get contracts:", err)
	}

	// Sort workers by public key.
	sort.Slice(rw.Workers, func(i, j int) bool {
		return rw.Workers[i].HostPubKey.String() < rw.Workers[j].HostPubKey.String()
	})

	// Print Worker Pool Summary
	fmt.Println("Worker Pool Summary")
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	fmt.Fprintf(w, "  Total Workers:\t%v\n", rw.NumWorkers)
	fmt.Fprintf(w, "  Workers On Download Cooldown:\t%v\n", rw.TotalDownloadCoolDown)
	fmt.Fprintf(w, "  Workers On Upload Cooldown:\t%v\n", rw.TotalUploadCoolDown)
	fmt.Fprintf(w, "  Workers On Maintenance Cooldown:\t%v\n", rw.TotalMaintenanceCoolDown)
	if err := w.Flush(); err != nil {
		die("failed to flush writer:", err)
	}

	// Split Workers into GoodForUpload and !GoodForUpload
	var goodForUpload, notGoodForUpload []modules.WorkerStatus
	for _, worker := range rw.Workers {
		if worker.ContractUtility.GoodForUpload {
			goodForUpload = append(goodForUpload, worker)
			continue
		}
		notGoodForUpload = append(notGoodForUpload, worker)
	}

	// List out GoorForUpload workers
	fmt.Println("GoodForUpload Workers:")
	if len(goodForUpload) == 0 {
		fmt.Println("  No GoodForUpload workers.")
	} else {
		writeWorkers(goodForUpload)
	}

	// List out !GoorForUpload workers
	fmt.Println("\nNot GoodForUpload Workers:")
	if len(notGoodForUpload) == 0 {
		fmt.Println("  All workers are GoodForUpload.")
	} else {
		writeWorkers(notGoodForUpload)
	}
}

// renterworkerseacmd is the handler for the command `siac renter workers ea`.
// It lists the status of the account of every worker.
func renterworkerseacmd() {
	rw, err := httpClient.RenterWorkersGet()
	if err != nil {
		die("Could not get worker statuses:", err)
	}

	// Sort workers by public key.
	sort.Slice(rw.Workers, func(i, j int) bool {
		return rw.Workers[i].HostPubKey.String() < rw.Workers[j].HostPubKey.String()
	})

	// collect some overal account stats
	var nfw uint64
	for _, worker := range rw.Workers {
		if worker.AccountStatus.AvailableBalance.IsZero() {
			nfw++
		}
	}
	fmt.Println("Worker Accounts Summary")

	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	defer func() {
		err := w.Flush()
		if err != nil {
			die("Could not flush tabwriter:", err)
		}
	}()

	// print summary
	fmt.Fprintf(w, "Total Workers: \t%v\n", rw.NumWorkers)
	fmt.Fprintf(w, "Non Funded Workers: \t%v\n", nfw)

	// print header
	hostInfo := "Host PubKey"
	accountInfo := "\tAvailBal\tNegBal\tTargetBal"
	errorInfo := "\tSucceededAt\tErrorAt\tError"
	header := hostInfo + accountInfo + errorInfo
	fmt.Fprintln(w, "\nWorker Accounts Detail  \n\n"+header)

	// print rows
	for _, worker := range rw.Workers {
		as := worker.AccountStatus

		// Host Info
		fmt.Fprintf(w, "%v", worker.HostPubKey.String())

		// Account Info
		fmt.Fprintf(w, "\t%s\t%s\t%s",
			as.AvailableBalance.HumanString(),
			as.NegativeBalance.HumanString(),
			worker.AccountBalanceTarget.HumanString())

		// Error Info
		fmt.Fprintf(w, "\t%v\t%v\t%v\n",
			sanitizeTime(as.RecentSuccessTime, as.RecentSuccessTime != time.Time{}),
			sanitizeTime(as.RecentErrTime, as.RecentErr != ""),
			sanitizeErr(as.RecentErr))
	}
}

// renterworkersdownloadscmd is the handler for the command `siac renter workers
// dj`.  It lists the status of the download jobs of every worker.
func renterworkersdownloadscmd() {
	rw, err := httpClient.RenterWorkersGet()
	if err != nil {
		die("Could not get worker statuses:", err)
	}

	// Sort workers by public key.
	sort.Slice(rw.Workers, func(i, j int) bool {
		return rw.Workers[i].HostPubKey.String() < rw.Workers[j].HostPubKey.String()
	})

	// Create tab writer
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	defer func() {
		err := w.Flush()
		if err != nil {
			die("Could not flush tabwriter:", err)
		}
	}()
	// Write Download Info
	writeWorkerDownloadUploadInfo(true, w, rw)
}

// renterworkersptcmd is the handler for the command `siac renter workers pt`.
// It lists the status of the price table of every worker.
func renterworkersptcmd() {
	rw, err := httpClient.RenterWorkersGet()
	if err != nil {
		die("Could not get worker statuses:", err)
	}

	// Sort workers by public key.
	sort.Slice(rw.Workers, func(i, j int) bool {
		return rw.Workers[i].HostPubKey.String() < rw.Workers[j].HostPubKey.String()
	})

	// collect some overal account stats
	var workersWithoutPTs uint64
	for _, worker := range rw.Workers {
		if !worker.PriceTableStatus.Active {
			workersWithoutPTs++
		}
	}
	fmt.Println("Worker Price Tables Summary")

	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	defer func() {
		err := w.Flush()
		if err != nil {
			die("Could not flush tabwriter:", err)
		}
	}()

	// print summary
	fmt.Fprintf(w, "Total Workers: \t%v\n", rw.NumWorkers)
	fmt.Fprintf(w, "Workers Without Price Table: \t%v\n", workersWithoutPTs)

	// print header
	hostInfo := "Host PubKey"
	priceTableInfo := "\tActive\tExpiry\tUpdate"
	queueInfo := "\tErrorAt\tError"
	header := hostInfo + priceTableInfo + queueInfo
	fmt.Fprintln(w, "\nWorker Price Tables Detail  \n\n"+header)

	// print rows
	for _, worker := range rw.Workers {
		pts := worker.PriceTableStatus

		// Host Info
		fmt.Fprintf(w, "%v", worker.HostPubKey.String())

		// Price Table Info
		fmt.Fprintf(w, "\t%t\t%s\t%s",
			pts.Active,
			sanitizeTime(pts.ExpiryTime, pts.Active),
			sanitizeTime(pts.UpdateTime, pts.Active))

		// Error Info
		fmt.Fprintf(w, "\t%v\t%v\n",
			sanitizeTime(pts.RecentErrTime, pts.RecentErr != ""),
			sanitizeErr(pts.RecentErr))
	}
}

// renterworkersrjcmd is the handler for the command `siac renter workers rj`.
// It lists the status of the read job queue for every worker.
func renterworkersrjcmd() {
	rw, err := httpClient.RenterWorkersGet()
	if err != nil {
		die("Could not get worker statuses:", err)
	}

	// Sort workers by public key.
	sort.Slice(rw.Workers, func(i, j int) bool {
		return rw.Workers[i].HostPubKey.String() < rw.Workers[j].HostPubKey.String()
	})

	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	defer func() {
		err := w.Flush()
		if err != nil {
			die("Could not flush tabwriter:", err)
		}
	}()

	// print header
	hostInfo := "Host PubKey"
	queueInfo := "\tJobs\tAvgJobTime64k (ms)\tAvgJobTime1m (ms)\tAvgJobTime4m (ms)\tConsecFail\tErrorAt\tError"
	header := hostInfo + queueInfo
	fmt.Fprintln(w, "\nWorker Read Jobs  \n\n"+header)

	// print rows
	for _, worker := range rw.Workers {
		rjs := worker.ReadJobsStatus

		// Host Info
		fmt.Fprintf(w, "%v", worker.HostPubKey.String())

		// ReadJobs Info
		fmt.Fprintf(w, "\t%v\t%v\t%v\t%v\t%v\t%v\t%v\n",
			rjs.JobQueueSize,
			rjs.AvgJobTime64k,
			rjs.AvgJobTime1m,
			rjs.AvgJobTime4m,
			rjs.ConsecutiveFailures,
			sanitizeTime(rjs.RecentErrTime, rjs.RecentErr != ""),
			sanitizeErr(rjs.RecentErr))
	}
}

// renterworkershsjcmd is the handler for the command `siac renter workers hs`.
// It lists the status of the has sector job queue for every worker.
func renterworkershsjcmd() {
	rw, err := httpClient.RenterWorkersGet()
	if err != nil {
		die("Could not get worker statuses:", err)
	}

	// Sort workers by public key.
	sort.Slice(rw.Workers, func(i, j int) bool {
		return rw.Workers[i].HostPubKey.String() < rw.Workers[j].HostPubKey.String()
	})

	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	defer func() {
		err := w.Flush()
		if err != nil {
			die("Could not flush tabwriter:", err)
		}
	}()

	// print header
	hostInfo := "Host PubKey"
	queueInfo := "\tJobs\tAvgJobTime (ms)\tConsecFail\tErrorAt\tError"
	header := hostInfo + queueInfo
	fmt.Fprintln(w, "\nWorker Has Sector Jobs  \n\n"+header)

	// print rows
	for _, worker := range rw.Workers {
		hsjs := worker.HasSectorJobsStatus

		// Host Info
		fmt.Fprintf(w, "%v", worker.HostPubKey.String())

		// HasSector Jobs Info
		fmt.Fprintf(w, "\t%v\t%v\t%v\t%v\t%v\n",
			hsjs.JobQueueSize,
			hsjs.AvgJobTime,
			hsjs.ConsecutiveFailures,
			sanitizeTime(hsjs.RecentErrTime, hsjs.RecentErr != ""),
			sanitizeErr(hsjs.RecentErr))
	}
}

// renterworkersuploadscmd is the handler for the command `siac renter workers
// uj`.  It lists the status of the upload jobs of every worker.
func renterworkersuploadscmd() {
	rw, err := httpClient.RenterWorkersGet()
	if err != nil {
		die("Could not get worker statuses:", err)
	}

	// Sort workers by public key.
	sort.Slice(rw.Workers, func(i, j int) bool {
		return rw.Workers[i].HostPubKey.String() < rw.Workers[j].HostPubKey.String()
	})

	// Create tab writer
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	defer func() {
		err := w.Flush()
		if err != nil {
			die("Could not flush tabwriter:", err)
		}
	}()
	// Write Upload Info
	writeWorkerDownloadUploadInfo(false, w, rw)
}

// writeWorkers is a helper function to display workers
func writeWorkers(workers []modules.WorkerStatus) {
	fmt.Println("  Number of Workers:", len(workers))
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	contractHeader := "Worker Contract\t \t \t "
	contractInfo := "Host PubKey\tContract ID\tGood For Renew\tGood For Upload"
	downloadHeader := "\tWorker Downloads\t "
	downloadInfo := "\tOn Cooldown\tQueue"
	uploadHeader := "\tWorker Uploads\t "
	uploadInfo := "\tOn Cooldown\tQueue"
	maintenanceHeader := "\tWorker Maintenance\t \t "
	maintenanceInfo := "\tOn Cooldown\tCooldown Time\tLast Error"
	jobHeader := "\tWorker Jobs\t \t "
	jobInfo := "\tHas Sector\tRead Sector\tSnapshot UL\tSnapshot DL"
	fmt.Fprintln(w, "\n  "+contractHeader+downloadHeader+uploadHeader+maintenanceHeader+jobHeader)
	fmt.Fprintln(w, "  "+contractInfo+downloadInfo+uploadInfo+maintenanceInfo+jobInfo)

	for _, worker := range workers {
		// Contract Info
		fmt.Fprintf(w, "  %v\t%v\t%v\t%v",
			worker.HostPubKey.String(),
			worker.ContractID,
			worker.ContractUtility.GoodForRenew,
			worker.ContractUtility.GoodForUpload)

		// Download Info
		fmt.Fprintf(w, "\t%v\t%v",
			worker.DownloadOnCoolDown,
			worker.DownloadQueueSize)

		// Upload Info
		fmt.Fprintf(w, "\t%v\t%v",
			worker.UploadOnCoolDown,
			worker.UploadQueueSize)

		// Maintenance Info
		fmt.Fprintf(w, "\t%t\t%v\t%v",
			worker.MaintenanceOnCooldown,
			worker.MaintenanceCoolDownTime,
			sanitizeErr(worker.MaintenanceCoolDownError))

		// Job Info
		fmt.Fprintf(w, "\t%v\t%v\t%v\t%v\n",
			worker.HasSectorJobsStatus.JobQueueSize,
			worker.ReadJobsStatus.JobQueueSize,
			worker.DownloadSnapshotJobQueueSize,
			worker.UploadSnapshotJobQueueSize)
	}
	if err := w.Flush(); err != nil {
		die("failed to flush writer:", err)
	}
}

// renterworkerreadregistrycmd is the handler for the command `siac renter workers
// rrj`.  It lists the status of the read registry jobs of every worker.
func renterworkersreadregistrycmd() {
	rw, err := httpClient.RenterWorkersGet()
	if err != nil {
		die("Could not get worker statuses:", err)
	}

	// Sort workers by public key.
	sort.Slice(rw.Workers, func(i, j int) bool {
		return rw.Workers[i].HostPubKey.String() < rw.Workers[j].HostPubKey.String()
	})

	// Create tab writer
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	defer func() {
		err := w.Flush()
		if err != nil {
			die("Could not flush tabwriter:", err)
		}
	}()
	// Write Upload Info
	writeWorkerReadUpdateRegistryInfo(true, w, rw)
}

// renterworkerupdateregistrycmd is the handler for the command `siac renter
// workers urj`. It lists the status of the update registry jobs of every
// worker.
func renterworkersupdateregistrycmd() {
	rw, err := httpClient.RenterWorkersGet()
	if err != nil {
		die("Could not get worker statuses:", err)
	}

	// Sort workers by public key.
	sort.Slice(rw.Workers, func(i, j int) bool {
		return rw.Workers[i].HostPubKey.String() < rw.Workers[j].HostPubKey.String()
	})

	// Create tab writer
	w := tabwriter.NewWriter(os.Stdout, 2, 0, 2, ' ', 0)
	defer func() {
		err := w.Flush()
		if err != nil {
			die("Could not flush tabwriter:", err)
		}
	}()
	// Write Upload Info
	writeWorkerReadUpdateRegistryInfo(false, w, rw)
}
