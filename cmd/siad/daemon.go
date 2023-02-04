package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"gitlab.com/NebulousLabs/errors"
	"golang.org/x/term"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/node/api/server"
	"go.sia.tech/siad/profile"
)

// passwordPrompt securely reads a password from stdin.
func passwordPrompt(prompt string) (string, error) {
	fmt.Print(prompt)
	pw, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println()
	return string(pw), err
}

// verifyAPISecurity checks that the security values are consistent with a
// sane, secure system.
func verifyAPISecurity(config Config) error {
	// Make sure that only the loopback address is allowed unless the
	// --disable-api-security flag has been used.
	if !config.Siad.AllowAPIBind {
		addr := modules.NetAddress(config.Siad.APIaddr)
		if !addr.IsLoopback() {
			if addr.Host() == "" {
				return fmt.Errorf("a blank host will listen on all interfaces, did you mean localhost:%v?\nyou must pass --disable-api-security to bind Siad to a non-localhost address", addr.Port())
			}
			return errors.New("you must pass --disable-api-security to bind Siad to a non-localhost address")
		}
		return nil
	}

	// If the --disable-api-security flag is used, enforce that
	// --authenticate-api must also be used.
	if config.Siad.AllowAPIBind && !config.Siad.AuthenticateAPI {
		return errors.New("cannot use --disable-api-security without setting an api password")
	}
	return nil
}

// processNetAddr adds a ':' to a bare integer, so that it is a proper port
// number.
func processNetAddr(addr string) string {
	_, err := strconv.Atoi(addr)
	if err == nil {
		return ":" + addr
	}
	return addr
}

// processModules makes the modules string lowercase to make checking if a
// module in the string easier, and returns an error if the string contains an
// invalid module character.
func processModules(modules string) (string, error) {
	modules = strings.ToLower(modules)
	// Check for module names
	switch modules {
	case "gateway":
		return "g", nil
	case "consensus":
		return "gc", nil
	case "tpool":
		return "gct", nil
	case "wallet":
		// Add Accounting and FeeManager modules to all nodes that have at least
		// a wallet
		return "gctwaf", nil
	case "renter":
		return "gctwafr", nil
	case "host":
		return "gctwafh", nil
	case "miner":
		return "gctwafm", nil
	case "feemanager":
		return "gctwaf", nil
	case "accounting":
		return "gctwaf", nil
	case "explorer":
		return "gce", nil
	}

	// Check module letters provided
	validModules := "acghmrtwef"
	invalidModules := modules
	for _, m := range validModules {
		invalidModules = strings.Replace(invalidModules, string(m), "", 1)
	}
	if len(invalidModules) > 0 {
		return "", errors.New("Unable to parse --modules flag, unrecognized or duplicate modules: " + invalidModules)
	}
	return modules, nil
}

// processConfig checks the configuration values and performs cleanup on
// incorrect-but-allowed values.
func processConfig(config Config) (Config, error) {
	var err1, err2 error
	config.Siad.APIaddr = processNetAddr(config.Siad.APIaddr)
	config.Siad.RPCaddr = processNetAddr(config.Siad.RPCaddr)
	config.Siad.HostAddr = processNetAddr(config.Siad.HostAddr)
	config.Siad.Modules, err1 = processModules(config.Siad.Modules)
	if config.Siad.Profile != "" {
		config.Siad.Profile, err2 = profile.ProcessProfileFlags(config.Siad.Profile)
	}
	err3 := verifyAPISecurity(config)
	err := build.JoinErrors([]error{err1, err2, err3}, ", and ")
	if err != nil {
		return Config{}, err
	}
	return config, nil
}

// loadAPIPassword determines whether to use an API password from disk or a
// temporary one entered by the user according to the provided config.
func loadAPIPassword(config Config) (_ Config, err error) {
	if config.Siad.AuthenticateAPI {
		if config.Siad.TempPassword {
			config.APIPassword, err = passwordPrompt("Enter API password: ")
			if err != nil {
				return Config{}, err
			} else if config.APIPassword == "" {
				return Config{}, errors.New("password cannot be blank")
			}
		} else {
			// load API password from environment variable or file.
			config.APIPassword, err = build.APIPassword()
			if err != nil {
				return Config{}, err
			}
		}
	}
	return config, nil
}

// printVersionAndRevision prints the daemon's version and revision numbers.
func printVersionAndRevision() {
	fmt.Println("siad v" + build.NodeVersion)
	if build.Release == "testnet" {
		fmt.Println("Testnet -- only for testing purposes")
	}
	if build.GitRevision == "" {
		fmt.Println("WARN: compiled without build commit or version. To compile correctly, please use the makefile")
	} else {
		fmt.Println("Git Revision " + build.GitRevision)
	}
}

// installMmapSignalHandler installs a signal handler for Mmap related signals
// and exits when such a signal is received.
func installMmapSignalHandler() {
	// NOTE: ideally we would catch SIGSEGV here too, since that signal can
	// also be thrown by an mmap I/O error. However, SIGSEGV can occur under
	// other circumstances as well, and in those cases, we will want a full
	// stack trace.
	mmapChan := make(chan os.Signal, 1)
	signal.Notify(mmapChan, syscall.SIGBUS)
	go func() {
		<-mmapChan
		fmt.Println("A fatal I/O exception (SIGBUS) has occurred.")
		fmt.Println("Please check your disk for errors.")
		os.Exit(1)
	}()
}

// installKillSignalHandler installs a signal handler for os.Interrupt, os.Kill
// and syscall.SIGTERM and returns a channel that is closed when one of them is
// caught.
func installKillSignalHandler() chan os.Signal {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, os.Kill, syscall.SIGTERM)
	return sigChan
}

// startDaemon uses the config parameters to initialize Sia modules and start
// siad.
func startDaemon(config Config) (err error) {
	loadStart := time.Now()

	// Load API password.
	config, err = loadAPIPassword(config)
	if err != nil {
		return errors.AddContext(err, "failed to get API password")
	}

	// Print the siad Version and GitRevision
	printVersionAndRevision()

	// Install a signal handler that will catch exceptions thrown by mmap'd
	// files.
	installMmapSignalHandler()

	// Print a startup message.
	fmt.Println("Loading...")

	// Create the node params by parsing the modules specified in the config.
	nodeParams := parseModules(config)
	// set the wallet password from the environment variable
	nodeParams.WalletPassword = build.WalletPassword()

	// Start and run the server.
	srv, err := server.New(config.Siad.APIaddr, config.Siad.RequiredUserAgent, config.APIPassword, nodeParams, loadStart)
	if err != nil {
		return err
	}

	// listen for kill signals
	sigChan := installKillSignalHandler()

	// Print a 'startup complete' message.
	startupTime := time.Since(loadStart)
	fmt.Printf("Finished full setup in %s\n", startupTime.Truncate(time.Second).String())

	// wait for Serve to return or for kill signal to be caught
	err = func() error {
		select {
		case err := <-srv.ServeErr():
			return err
		case <-sigChan:
			fmt.Println("\rCaught stop signal, quitting...")
			return srv.Close()
		}
	}()
	if err != nil {
		build.Critical(err)
	}

	// Wait for server to complete shutdown.
	srv.WaitClose()

	return nil
}

// startDaemonCmd is a passthrough function for startDaemon.
func startDaemonCmd(cmd *cobra.Command, _ []string) {
	// Process the config variables after they are parsed by cobra.
	config, err := processConfig(globalConfig)
	if err != nil {
		die(errors.AddContext(err, "failed to parse input parameter"))
	}

	// Parse profile flags
	profileCPU := strings.Contains(config.Siad.Profile, "c")
	profileMem := strings.Contains(config.Siad.Profile, "m")
	profileTrace := strings.Contains(config.Siad.Profile, "t")

	// Always run CPU and memory profiles on debug
	if build.DEBUG {
		profileCPU = true
		profileMem = true
	}

	// Launch any profiles
	if profileCPU || profileMem || profileTrace {
		var profileDir string
		if cmd.Root().Flag("profile-directory").Changed {
			profileDir = config.Siad.ProfileDir
		} else {
			profileDir = filepath.Join(config.Siad.SiaDir, config.Siad.ProfileDir)
		}
		go profile.StartContinuousProfile(profileDir, profileCPU, profileMem, profileTrace)
	}

	// Start siad. startDaemon will only return when it is shutting down.
	err = startDaemon(config)
	if err != nil {
		die(err)
	}

	// Daemon seems to have closed cleanly. Print a 'closed' message.
	fmt.Println("Shutdown complete.")
}
