package main

import (
	"fmt"
	"math"
	"os"
	"reflect"

	"github.com/spf13/cobra"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/node/api"
	"go.sia.tech/siad/node/api/client"
)

var (
	// General Flags
	alertSuppress bool
	siaDir        string // Path to sia data dir
	verbose       bool   // Display additional information

	// Module Specific Flags
	//
	// Daemon Flags
	daemonStackOutputFile  string // The file that the stack trace will be written to
	daemonCPUProfile       bool   // Indicates that the CPU profile should be started
	daemonMemoryProfile    bool   // Indicates that the Memory profile should be started
	daemonProfileDirectory string // The Directory where the profile logs are saved
	daemonTraceProfile     bool   // Indicates that the Trace profile should be started

	// Host Flags
	hostContractOutputType string // output type for host contracts
	hostFolderRemoveForce  bool   // force folder remove

	// Renter Flags
	dataPieces                string // the number of data pieces a file should be uploaded with
	parityPieces              string // the number of parity pieces a file should be uploaded with
	renterAllContracts        bool   // Show all active and expired contracts
	renterBubbleAll           bool   // Bubble the entire directory tree
	renterDeleteRoot          bool   // Delete path start from root instead of the UserFolder.
	renterDownloadAsync       bool   // Downloads files asynchronously
	renterDownloadRecursive   bool   // Downloads folders recursively.
	renterDownloadRoot        bool   // Download path start from root instead of the UserFolder.
	renterFuseMountAllowOther bool   // Mount fuse with 'AllowOther' set to true.
	renterListRecursive       bool   // List files of folder recursively.
	renterListRoot            bool   // List path start from root instead of the UserFolder.
	renterRenameRoot          bool   // Rename files relative to root instead of the UserFolder.
	renterShowHistory         bool   // Show download history in addition to download queue.

	// Renter Allowance Flags
	allowanceFunds       string // amount of money to be used within a period
	allowanceHosts       string // number of hosts to form contracts with
	allowancePeriod      string // length of period
	allowanceRenewWindow string // renew window of allowance

	allowanceExpectedDownload   string // expected data downloaded within period
	allowanceExpectedRedundancy string // expected redundancy of most uploaded files
	allowanceExpectedStorage    string // expected storage stored on hosts before redundancy
	allowanceExpectedUpload     string // expected data uploaded within period

	allowanceMaxContractPrice          string // maximum allowed price to form a contract
	allowanceMaxDownloadBandwidthPrice string // max allowed price to download data from a host
	allowanceMaxRPCPrice               string // maximum allowed base price for RPCs
	allowanceMaxSectorAccessPrice      string // max allowed price to access a sector on a host
	allowanceMaxStoragePrice           string // max allowed price to store data on a host
	allowanceMaxUploadBandwidthPrice   string // max allowed price to upload data to a host

	// Skykey Flags
	skykeyID              string // ID used to identify a Skykey.
	skykeyName            string // Name used to identify a Skykey.
	skykeyRenameAs        string // Optional parameter to rename a Skykey while adding it.
	skykeyShowPrivateKeys bool   // Set to true to show private key data.
	skykeyType            string // Type used to create a new Skykey.

	// Utils Flags
	dictionaryLanguage string // dictionary for seed utils

	// Wallet Flags
	initForce            bool   // destroy and re-encrypt the wallet on init if it already exists
	initPassword         bool   // supply a custom password when creating a wallet
	walletRawTxn         bool   // Encode/decode transactions in base64-encoded binary.
	walletStartHeight    uint64 // Start height for transaction search.
	walletEndHeight      uint64 // End height for transaction search.
	walletTxnFeeIncluded bool   // include the fee in the balance being sent
	insecureInput        bool   // Insecure password/seed input. Disables the shoulder-surfing and Mac secure input feature.
)

var (
	// Globals.
	rootCmd    *cobra.Command // Root command cobra object, used by bash completion cmd.
	httpClient client.Client
)

// Exit codes.
// inspired by sysexits.h
const (
	exitCodeGeneral = 1  // Not in sysexits.h, but is standard practice.
	exitCodeUsage   = 64 // EX_USAGE in sysexits.h
)

// wrap wraps a generic command with a check that the command has been
// passed the correct number of arguments. The command must take only strings
// as arguments.
func wrap(fn interface{}) func(*cobra.Command, []string) {
	fnVal, fnType := reflect.ValueOf(fn), reflect.TypeOf(fn)
	if fnType.Kind() != reflect.Func {
		panic("wrapped function has wrong type signature")
	}
	for i := 0; i < fnType.NumIn(); i++ {
		if fnType.In(i).Kind() != reflect.String {
			panic("wrapped function has wrong type signature")
		}
	}

	return func(cmd *cobra.Command, args []string) {
		if len(args) != fnType.NumIn() {
			_ = cmd.UsageFunc()(cmd)
			os.Exit(exitCodeUsage)
		}
		argVals := make([]reflect.Value, fnType.NumIn())
		for i := range args {
			argVals[i] = reflect.ValueOf(args[i])
		}
		fnVal.Call(argVals)
	}
}

// die prints its arguments to stderr, in production exits the program with the
// default error code, during tests it passes panic so that tests can catch the
// panic and check printed errors
func die(args ...interface{}) {
	fmt.Fprintln(os.Stderr, args...)

	if build.Release == "testing" {
		// In testing pass panic that can be catched and the test can continue
		panic(errors.New("die panic for testing"))
	}
	// In production exit
	os.Exit(exitCodeGeneral)
}

// statuscmd is the handler for the command `siac`
// prints basic information about Sia.
func statuscmd() {
	// For UX formating
	defer fmt.Println()

	// Consensus Info
	cg, err := httpClient.ConsensusGet()
	if errors.Contains(err, api.ErrAPICallNotRecognized) {
		// Assume module is not loaded if status command is not recognized.
		fmt.Printf("Consensus:\n  Status: %s\n\n", moduleNotReadyStatus)
	} else if err != nil {
		die("Could not get consensus status:", err)
	} else {
		fmt.Printf(`Consensus:
  Synced: %v
  Height: %v

`, yesNo(cg.Synced), cg.Height)
	}

	// Wallet Info
	walletStatus, err := httpClient.WalletGet()
	if errors.Contains(err, api.ErrAPICallNotRecognized) {
		// Assume module is not loaded if status command is not recognized.
		fmt.Printf("Wallet:\n  Status: %s\n\n", moduleNotReadyStatus)
	} else if err != nil {
		die("Could not get wallet status:", err)
	} else if walletStatus.Unlocked {
		fmt.Printf(`Wallet:
  Status:          unlocked
  Siacoin Balance: %v

`, currencyUnits(walletStatus.ConfirmedSiacoinBalance))
	} else {
		fmt.Printf(`Wallet:
  Status: Locked

`)
	}

	// Renter Info
	fmt.Println(`Renter:`)
	err = renterFilesAndContractSummary()
	if err != nil {
		die(err)
	}

	if !verbose {
		return
	}

	// Global Daemon Rate Limits
	dg, err := httpClient.DaemonSettingsGet()
	if err != nil {
		die("Could not get daemon:", err)
	}
	fmt.Printf(`
Global `)
	rateLimitSummary(dg.MaxDownloadSpeed, dg.MaxUploadSpeed)

	// Gateway Rate Limits
	gg, err := httpClient.GatewayGet()
	if err != nil {
		die("Could not get gateway:", err)
	}
	fmt.Printf(`
Gateway `)
	rateLimitSummary(gg.MaxDownloadSpeed, gg.MaxUploadSpeed)

	// Renter Rate Limits
	rg, err := httpClient.RenterGet()
	if err != nil {
		die("Error getting renter:", err)
	}
	fmt.Printf(`
Renter `)
	rateLimitSummary(rg.Settings.MaxDownloadSpeed, rg.Settings.MaxUploadSpeed)
}

// rateLimitSummary displays the a summary of the provided rate limits
func rateLimitSummary(download, upload int64) {
	fmt.Printf(`Rate limits: `)
	if download == 0 {
		fmt.Printf(`
  Download Speed: %v`, "no limit")
	} else {
		fmt.Printf(`
  Download Speed: %v`, ratelimitUnits(download))
	}
	if upload == 0 {
		fmt.Printf(`
  Upload Speed:   %v
`, "no limit")
	} else {
		fmt.Printf(`
  Upload Speed:   %v
`, ratelimitUnits(upload))
	}
}

func main() {
	// initialize commands
	rootCmd = initCmds()

	// initialize client
	initClient(rootCmd, &verbose, &httpClient, &siaDir, &alertSuppress)

	// Perform some basic actions after cobra has initialized.
	cobra.OnInitialize(func() {
		// set API password if it was not set
		setAPIPasswordIfNotSet()

		// Check if the siaDir is set.
		if siaDir == "" {
			// No siaDir passed in, fetch the siaDir
			siaDir = build.SiaDir()
		}

		// Check for Critical Alerts
		alerts, err := httpClient.DaemonAlertsGet()
		if err == nil && len(alerts.CriticalAlerts) > 0 && !alertSuppress {
			printAlerts(alerts.CriticalAlerts, modules.SeverityCritical)
			fmt.Println("------------------")
			fmt.Printf("\n  The above %v critical alerts should be resolved ASAP\n\n", len(alerts.CriticalAlerts))
		}
	})

	// run
	if err := rootCmd.Execute(); err != nil {
		// Since no commands return errors (all commands set Command.Run instead of
		// Command.RunE), Command.Execute() should only return an error on an
		// invalid command or flag. Therefore Command.Usage() was called (assuming
		// Command.SilenceUsage is false) and we should exit with exitCodeUsage.
		os.Exit(exitCodeUsage)
	}
}

// initCmds initializes root command and its subcommands
func initCmds() *cobra.Command {
	root := &cobra.Command{
		Use:   os.Args[0],
		Short: "siac v" + build.NodeVersion,
		Long:  "siac v" + build.NodeVersion,
		Run:   wrap(statuscmd),
	}

	// create command tree (alphabetized by root command)
	root.AddCommand(consensusCmd)
	root.AddCommand(jsonCmd)

	root.AddCommand(gatewayCmd)
	gatewayCmd.AddCommand(gatewayAddressCmd, gatewayBandwidthCmd, gatewayBlocklistCmd, gatewayConnectCmd, gatewayDisconnectCmd, gatewayListCmd, gatewayRatelimitCmd)
	gatewayBlocklistCmd.AddCommand(gatewayBlocklistAppendCmd, gatewayBlocklistClearCmd, gatewayBlocklistRemoveCmd, gatewayBlocklistSetCmd)

	root.AddCommand(hostCmd)
	hostCmd.AddCommand(hostAnnounceCmd, hostConfigCmd, hostContractCmd, hostFolderCmd, hostSectorCmd)
	hostFolderCmd.AddCommand(hostFolderAddCmd, hostFolderRemoveCmd, hostFolderResizeCmd)
	hostSectorCmd.AddCommand(hostSectorDeleteCmd)
	hostContractCmd.Flags().StringVarP(&hostContractOutputType, "type", "t", "value", "Select output type")
	hostFolderRemoveCmd.Flags().BoolVarP(&hostFolderRemoveForce, "force", "f", false, "Force the removal of the folder and its data")

	root.AddCommand(hostdbCmd)
	hostdbCmd.AddCommand(hostdbFiltermodeCmd, hostdbSetFiltermodeCmd, hostdbViewCmd)
	hostdbCmd.Flags().IntVarP(&hostdbNumHosts, "numhosts", "n", 0, "Number of hosts to display from the hostdb")

	root.AddCommand(minerCmd)
	minerCmd.AddCommand(minerStartCmd, minerStopCmd)

	root.AddCommand(renterCmd)
	renterCmd.AddCommand(renterAllowanceCmd, renterBubbleCmd, renterBackupCreateCmd, renterBackupListCmd, renterBackupLoadCmd,
		renterCleanCmd, renterContractsCmd, renterContractsRecoveryScanProgressCmd, renterDownloadCancelCmd,
		renterDownloadsCmd, renterExportCmd, renterFilesDeleteCmd, renterFilesDownloadCmd,
		renterFilesListCmd, renterFilesRenameCmd, renterFilesUnstuckCmd, renterFilesUploadCmd,
		renterFuseCmd, renterLostCmd, renterPricesCmd, renterRatelimitCmd, renterSetAllowanceCmd,
		renterSetLocalPathCmd, renterTriggerContractRecoveryScanCmd, renterUploadsCmd, renterWorkersCmd,
		renterHealthSummaryCmd)
	renterWorkersCmd.AddCommand(renterWorkersAccountsCmd, renterWorkersDownloadsCmd, renterWorkersPriceTableCmd, renterWorkersReadJobsCmd, renterWorkersHasSectorJobSCmd, renterWorkersUploadsCmd, renterWorkersReadRegistryCmd, renterWorkersUpdateRegistryCmd)

	renterAllowanceCmd.AddCommand(renterAllowanceCancelCmd)
	renterBubbleCmd.Flags().BoolVarP(&renterBubbleAll, "all", "A", false, "Bubble the entire directory tree")
	renterContractsCmd.AddCommand(renterContractsViewCmd)
	renterFilesUploadCmd.AddCommand(renterFilesUploadPauseCmd, renterFilesUploadResumeCmd)

	renterContractsCmd.Flags().BoolVarP(&renterAllContracts, "all", "A", false, "Show all expired contracts in addition to active contracts")
	renterDownloadsCmd.Flags().BoolVarP(&renterShowHistory, "history", "H", false, "Show download history in addition to the download queue")
	renterFilesDeleteCmd.Flags().BoolVar(&renterDeleteRoot, "root", false, "Delete files and folders from root instead of from the user home directory")
	renterFilesDownloadCmd.Flags().BoolVarP(&renterDownloadAsync, "async", "A", false, "Download file asynchronously")
	renterFilesDownloadCmd.Flags().BoolVarP(&renterDownloadRecursive, "recursive", "R", false, "Download folder recursively")
	renterFilesDownloadCmd.Flags().BoolVar(&renterDownloadRoot, "root", false, "Download files and folders from root instead of from the user home directory")
	renterFilesListCmd.Flags().BoolVarP(&renterListRecursive, "recursive", "R", false, "Recursively list files and folders")
	renterFilesListCmd.Flags().BoolVar(&renterListRoot, "root", false, "List files and folders from root instead of from the user home directory")
	renterFilesUploadCmd.Flags().StringVar(&dataPieces, "data-pieces", "", "the number of data pieces a files should be uploaded with")
	renterFilesUploadCmd.Flags().StringVar(&parityPieces, "parity-pieces", "", "the number of parity pieces a files should be uploaded with")
	renterExportCmd.AddCommand(renterExportContractTxnsCmd)
	renterFilesRenameCmd.Flags().BoolVar(&renterRenameRoot, "root", false, "Rename files relative to root instead of the user homedir")

	renterSetAllowanceCmd.Flags().StringVar(&allowanceFunds, "amount", "", "amount of money in allowance, specified in currency units")
	renterSetAllowanceCmd.Flags().StringVar(&allowancePeriod, "period", "", "period of allowance in blocks (b), hours (h), days (d) or weeks (w)")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceHosts, "hosts", "", "number of hosts the renter will spread the uploaded data across")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceRenewWindow, "renew-window", "", "renew window in blocks (b), hours (h), days (d) or weeks (w)")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceExpectedStorage, "expected-storage", "", "expected storage in bytes (B), kilobytes (KB), megabytes (MB) etc. up to yottabytes (YB)")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceExpectedUpload, "expected-upload", "", "expected upload in period in bytes (B), kilobytes (KB), megabytes (MB) etc. up to yottabytes (YB)")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceExpectedDownload, "expected-download", "", "expected download in period in bytes (B), kilobytes (KB), megabytes (MB) etc. up to yottabytes (YB)")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceExpectedRedundancy, "expected-redundancy", "", "expected redundancy of most uploaded files")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceMaxRPCPrice, "max-rpc-price", "", "the maximum RPC base price that is allowed for a host")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceMaxContractPrice, "max-contract-price", "", "the maximum price that the renter will pay to form a contract with a host")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceMaxDownloadBandwidthPrice, "max-download-bandwidth-price", "", "the maximum price that the renter will pay to download from a host")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceMaxSectorAccessPrice, "max-sector-access-price", "", "the maximum price that the renter will pay to access a sector on a host")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceMaxStoragePrice, "max-storage-price", "", "the maximum price that the renter will pay to store data on a host")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceMaxUploadBandwidthPrice, "max-upload-bandwidth-price", "", "the maximum price that the renter will pay to upload data to a host")

	renterFuseCmd.AddCommand(renterFuseMountCmd, renterFuseUnmountCmd)
	renterFuseMountCmd.Flags().BoolVarP(&renterFuseMountAllowOther, "allow-other", "", false, "Allow users other than the user that mounted the fuse directory to access and use the fuse directory")

	// Daemon Commands
	root.AddCommand(alertsCmd, globalRatelimitCmd, profileCmd, stackCmd, stopCmd, updateCmd, versionCmd)
	profileCmd.AddCommand(profileStartCmd, profileStopCmd)
	profileStartCmd.Flags().BoolVarP(&daemonCPUProfile, "cpu", "c", false, "Start the CPU profile")
	profileStartCmd.Flags().BoolVarP(&daemonMemoryProfile, "memory", "m", false, "Start the Memory profile")
	profileStartCmd.Flags().StringVar(&daemonProfileDirectory, "profileDir", "", "Specify the directory where the profile logs are to be saved")
	profileStartCmd.Flags().BoolVarP(&daemonTraceProfile, "trace", "t", false, "Start the Trace profile")
	stackCmd.Flags().StringVarP(&daemonStackOutputFile, "filename", "f", "stack.txt", "Specify the output file for the stack trace")
	updateCmd.AddCommand(updateCheckCmd)

	root.AddCommand(utilsCmd)
	utilsCmd.AddCommand(bashcomplCmd, mangenCmd, utilsBruteForceSeedCmd, utilsCheckSigCmd,
		utilsDecodeRawTxnCmd, utilsDisplayAPIPasswordCmd, utilsEncodeRawTxnCmd, utilsHastingsCmd,
		utilsSigHashCmd, utilsUploadedsizeCmd, utilsVerifySeedCmd)

	utilsVerifySeedCmd.Flags().StringVarP(&dictionaryLanguage, "language", "l", "english", "which dictionary you want to use")

	root.AddCommand(walletCmd)
	walletCmd.AddCommand(walletAddressCmd, walletAddressesCmd, walletBalanceCmd, walletBroadcastCmd, walletChangepasswordCmd,
		walletInitCmd, walletInitSeedCmd, walletLoadCmd, walletLockCmd, walletSeedsCmd, walletSendCmd,
		walletSignCmd, walletSweepCmd, walletTransactionsCmd, walletUnlockCmd)
	walletInitCmd.Flags().BoolVarP(&initPassword, "password", "p", false, "Prompt for a custom password")
	walletInitCmd.Flags().BoolVarP(&initForce, "force", "", false, "destroy the existing wallet and re-encrypt")
	walletInitSeedCmd.Flags().BoolVarP(&initForce, "force", "", false, "destroy the existing wallet")
	walletLoadCmd.AddCommand(walletLoad033xCmd, walletLoadSeedCmd, walletLoadSiagCmd)
	walletSendCmd.AddCommand(walletSendSiacoinsCmd, walletSendSiafundsCmd)
	walletSendSiacoinsCmd.Flags().BoolVarP(&walletTxnFeeIncluded, "fee-included", "", false, "Take the transaction fee out of the balance being submitted instead of the fee being additional")
	walletUnlockCmd.Flags().BoolVarP(&insecureInput, "insecure-input", "", false, "Disable shoulder-surf protection (echoing passwords and seeds)")
	walletUnlockCmd.Flags().BoolVarP(&initPassword, "password", "p", false, "Display interactive password prompt even if SIA_WALLET_PASSWORD is set")
	walletBroadcastCmd.Flags().BoolVarP(&walletRawTxn, "raw", "", false, "Decode transaction as base64 instead of JSON")
	walletSignCmd.Flags().BoolVarP(&walletRawTxn, "raw", "", false, "Encode signed transaction as base64 instead of JSON")
	walletTransactionsCmd.Flags().Uint64Var(&walletStartHeight, "startheight", 0, " Height of the block where transaction history should begin.")
	walletTransactionsCmd.Flags().Uint64Var(&walletEndHeight, "endheight", math.MaxUint64, " Height of the block where transaction history should end.")

	return root
}

// initClient initializes client cmd flags and default values
func initClient(root *cobra.Command, verbose *bool, client *client.Client, siaDir *string, alertSuppress *bool) {
	var defaultAPIAddr string
	switch build.Release {
	case "testnet":
		defaultAPIAddr = "localhost:9880"
	default:
		defaultAPIAddr = "localhost:9980"
	}
	root.PersistentFlags().BoolVarP(verbose, "verbose", "v", false, "Display additional information")
	root.PersistentFlags().StringVarP(&client.Address, "addr", "a", defaultAPIAddr, "which host/port to communicate with (i.e. the host/port siad is listening on)")
	root.PersistentFlags().StringVarP(&client.Password, "apipassword", "", "", "the password for the API's http authentication")
	root.PersistentFlags().StringVarP(siaDir, "sia-directory", "d", "", "location of the sia directory")
	root.PersistentFlags().StringVarP(&client.UserAgent, "useragent", "", "Sia-Agent", "the useragent used by siac to connect to the daemon's API")
	root.PersistentFlags().BoolVarP(alertSuppress, "alert-suppress", "s", false, "suppress siac alerts")
}

// setAPIPasswordIfNotSet sets API password if it was not set
func setAPIPasswordIfNotSet() {
	// Check if the API Password is set
	if httpClient.Password == "" {
		// No password passed in, fetch the API Password
		pw, err := build.APIPassword()
		if err != nil {
			fmt.Println("Exiting: Error getting API Password:", err)
			os.Exit(exitCodeGeneral)
		}
		httpClient.Password = pw
	}
}
