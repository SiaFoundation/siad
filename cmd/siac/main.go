package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"strings"

	"github.com/spf13/cobra"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// Flags.
	dictionaryLanguage      string // dictionary for seed utils
	hostContractOutputType  string // output type for host contracts
	hostVerbose             bool   // display additional host info
	initForce               bool   // destroy and re-encrypt the wallet on init if it already exists
	initPassword            bool   // supply a custom password when creating a wallet
	renterAllContracts      bool   // Show all active and expired contracts
	renterDownloadAsync     bool   // Downloads files asynchronously
	renterDownloadRecursive bool   // Downloads folders recursively.
	renterListVerbose       bool   // Show additional info about uploaded files.
	renterListRecursive     bool   // List files of folder recursively.
	renterShowHistory       bool   // Show download history in addition to download queue.
	siaDir                  string // Path to sia data dir
	walletRawTxn            bool   // Encode/decode transactions in base64-encoded binary.

	allowanceFunds              string // amount of money to be used within a period
	allowancePeriod             string // length of period
	allowanceHosts              string // number of hosts to form contracts with
	allowanceRenewWindow        string // renew window of allowance
	allowanceExpectedStorage    string // expected storage stored on hosts before redundancy
	allowanceExpectedUpload     string // expected data uploaded within period
	allowanceExpectedDownload   string // expected data downloaded within period
	allowanceExpectedRedundancy string // expected redundancy of most uploaded files
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
			cmd.UsageFunc()(cmd)
			os.Exit(exitCodeUsage)
		}
		argVals := make([]reflect.Value, fnType.NumIn())
		for i := range args {
			argVals[i] = reflect.ValueOf(args[i])
		}
		fnVal.Call(argVals)
	}
}

// die prints its arguments to stderr, then exits the program with the default
// error code.
func die(args ...interface{}) {
	fmt.Fprintln(os.Stderr, args...)
	os.Exit(exitCodeGeneral)
}

// statuscmd is the handler for the command `siac`
// prints basic information about Sia.
func statuscmd() {

	// Consensus Info
	cg, err := httpClient.ConsensusGet()
	if errors.Contains(err, api.ErrAPICallNotRecognized) {
		// Assume module is not loaded if status command is not recognized.
		fmt.Printf("Consensus: \n  Status: %s\n\n", moduleNotReadyStatus)
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
	fmt.Printf(`Renter:`)
	err = renterFilesAndContractSummary()
	if err != nil {
		die(err)
	}
}

func main() {
	root := &cobra.Command{
		Use:   os.Args[0],
		Short: "Sia Client v" + build.Version,
		Long:  "Sia Client v" + build.Version,
		Run:   wrap(statuscmd),
	}

	rootCmd = root

	// create command tree
	root.AddCommand(versionCmd)
	root.AddCommand(stopCmd)

	root.AddCommand(updateCmd)
	updateCmd.AddCommand(updateCheckCmd)

	root.AddCommand(hostCmd)
	hostCmd.AddCommand(hostConfigCmd, hostAnnounceCmd, hostFolderCmd, hostContractCmd, hostSectorCmd)
	hostFolderCmd.AddCommand(hostFolderAddCmd, hostFolderRemoveCmd, hostFolderResizeCmd)
	hostSectorCmd.AddCommand(hostSectorDeleteCmd)
	hostCmd.Flags().BoolVarP(&hostVerbose, "verbose", "v", false, "Display detailed host info")
	hostContractCmd.Flags().StringVarP(&hostContractOutputType, "type", "t", "value", "Select output type")

	root.AddCommand(hostdbCmd)
	hostdbCmd.AddCommand(hostdbViewCmd, hostdbFiltermodeCmd, hostdbSetFiltermodeCmd)
	hostdbCmd.Flags().IntVarP(&hostdbNumHosts, "numhosts", "n", 0, "Number of hosts to display from the hostdb")
	hostdbCmd.Flags().BoolVarP(&hostdbVerbose, "verbose", "v", false, "Display full hostdb information")

	root.AddCommand(minerCmd)
	minerCmd.AddCommand(minerStartCmd, minerStopCmd)

	root.AddCommand(walletCmd)
	walletCmd.AddCommand(walletAddressCmd, walletAddressesCmd, walletChangepasswordCmd, walletInitCmd, walletInitSeedCmd,
		walletLoadCmd, walletLockCmd, walletSeedsCmd, walletSendCmd, walletSweepCmd, walletSignCmd,
		walletBalanceCmd, walletBroadcastCmd, walletTransactionsCmd, walletUnlockCmd)
	walletInitCmd.Flags().BoolVarP(&initPassword, "password", "p", false, "Prompt for a custom password")
	walletInitCmd.Flags().BoolVarP(&initForce, "force", "", false, "destroy the existing wallet and re-encrypt")
	walletInitSeedCmd.Flags().BoolVarP(&initForce, "force", "", false, "destroy the existing wallet")
	walletLoadCmd.AddCommand(walletLoad033xCmd, walletLoadSeedCmd, walletLoadSiagCmd)
	walletSendCmd.AddCommand(walletSendSiacoinsCmd, walletSendSiafundsCmd)
	walletUnlockCmd.Flags().BoolVarP(&initPassword, "password", "p", false, "Display interactive password prompt even if SIA_WALLET_PASSWORD is set")
	walletBroadcastCmd.Flags().BoolVarP(&walletRawTxn, "raw", "", false, "Decode transaction as base64 instead of JSON")
	walletSignCmd.Flags().BoolVarP(&walletRawTxn, "raw", "", false, "Encode signed transaction as base64 instead of JSON")

	root.AddCommand(renterCmd)
	renterCmd.AddCommand(renterFilesDeleteCmd, renterFilesDownloadCmd,
		renterDownloadsCmd, renterAllowanceCmd, renterSetAllowanceCmd,
		renterContractsCmd, renterFilesListCmd, renterFilesRenameCmd,
		renterFilesUploadCmd, renterUploadsCmd, renterExportCmd,
		renterPricesCmd, renterBackupCreateCmd, renterBackupLoadCmd,
		renterBackupListCmd, renterTriggerContractRecoveryScanCmd, renterFilesUnstuckCmd,
		renterContractsRecoveryScanProgressCmd, renterDownloadCancelCmd)

	renterContractsCmd.AddCommand(renterContractsViewCmd)
	renterAllowanceCmd.AddCommand(renterAllowanceCancelCmd)

	renterCmd.Flags().BoolVarP(&renterListVerbose, "verbose", "v", false, "Show additional file info such as redundancy")
	renterContractsCmd.Flags().BoolVarP(&renterAllContracts, "all", "A", false, "Show all expired contracts in addition to active contracts")
	renterDownloadsCmd.Flags().BoolVarP(&renterShowHistory, "history", "H", false, "Show download history in addition to the download queue")
	renterFilesDownloadCmd.Flags().BoolVarP(&renterDownloadAsync, "async", "A", false, "Download file asynchronously")
	renterFilesDownloadCmd.Flags().BoolVarP(&renterDownloadRecursive, "recursive", "R", false, "Download folder recursively")
	renterFilesListCmd.Flags().BoolVarP(&renterListVerbose, "verbose", "v", false, "Show additional file info such as redundancy")
	renterFilesListCmd.Flags().BoolVarP(&renterListRecursive, "recursive", "R", false, "Recursively list files and folders")
	renterExportCmd.AddCommand(renterExportContractTxnsCmd)

	renterSetAllowanceCmd.Flags().StringVar(&allowanceFunds, "amount", "", "amount of money in allowance, specified in currency units")
	renterSetAllowanceCmd.Flags().StringVar(&allowancePeriod, "period", "", "period of allowance in blocks (b), hours (h), days (d) or weeks (w)")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceHosts, "hosts", "", "number of hosts the renter will spread the uploaded data across")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceRenewWindow, "renew-window", "", "renew window in blocks (b), hours (h), days (d) or weeks (w)")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceExpectedStorage, "expected-storage", "", "expected storage in bytes (B), kilobytes (KB), megabytes (MB) etc. up to yottabytes (YB)")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceExpectedUpload, "expected-upload", "", "expected upload in period in bytes (B), kilobytes (KB), megabytes (MB) etc. up to yottabytes (YB)")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceExpectedDownload, "expected-download", "", "expected download in period in bytes (B), kilobytes (KB), megabytes (MB) etc. up to yottabytes (YB)")
	renterSetAllowanceCmd.Flags().StringVar(&allowanceExpectedRedundancy, "expected-redundancy", "", "expected redundancy of most uploaded files")

	root.AddCommand(gatewayCmd)
	gatewayCmd.AddCommand(gatewayConnectCmd, gatewayDisconnectCmd, gatewayAddressCmd, gatewayListCmd, gatewayRatelimitCmd)

	root.AddCommand(consensusCmd)
	consensusCmd.Flags().BoolVarP(&consensusCmdVerbose, "verbose", "v", false, "Display full consensus information")

	utilsCmd.AddCommand(bashcomplCmd, mangenCmd, utilsHastingsCmd, utilsEncodeRawTxnCmd, utilsDecodeRawTxnCmd,
		utilsSigHashCmd, utilsCheckSigCmd, utilsVerifySeedCmd, utilsDisplayAPIPasswordCmd)
	utilsVerifySeedCmd.Flags().StringVarP(&dictionaryLanguage, "language", "l", "english", "which dictionary you want to use")
	root.AddCommand(utilsCmd)

	// initialize client
	root.PersistentFlags().StringVarP(&httpClient.Address, "addr", "a", "localhost:9980", "which host/port to communicate with (i.e. the host/port siad is listening on)")
	root.PersistentFlags().StringVarP(&httpClient.Password, "apipassword", "", "", "the password for the API's http authentication")
	root.PersistentFlags().StringVarP(&siaDir, "sia-directory", "d", build.DefaultSiaDir(), "location of the sia directory")
	root.PersistentFlags().StringVarP(&httpClient.UserAgent, "useragent", "", "Sia-Agent", "the useragent used by siac to connect to the daemon's API")

	// Check if the api password environment variable is set.
	apiPassword := os.Getenv("SIA_API_PASSWORD")
	if apiPassword != "" {
		httpClient.Password = apiPassword
		fmt.Println("Using SIA_API_PASSWORD environment variable")
	}

	// If the API password wasn't set we try to read it from the file. This must
	// be done only *after* we parse the sia-directory flag, which is why we do
	// it inside OnInitialize.
	cobra.OnInitialize(func() {
		if httpClient.Password == "" {
			pw, err := ioutil.ReadFile(build.APIPasswordFile(siaDir))
			if err != nil {
				fmt.Println("Could not read API password file:", err)
				httpClient.Password = ""
			} else {
				httpClient.Password = strings.TrimSpace(string(pw))
			}

		}
	})

	// run
	if err := root.Execute(); err != nil {
		// Since no commands return errors (all commands set Command.Run instead of
		// Command.RunE), Command.Execute() should only return an error on an
		// invalid command or flag. Therefore Command.Usage() was called (assuming
		// Command.SilenceUsage is false) and we should exit with exitCodeUsage.
		os.Exit(exitCodeUsage)
	}
}
