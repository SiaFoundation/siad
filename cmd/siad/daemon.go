package main

import (
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"golang.org/x/crypto/ssh/terminal"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api/server"
	"gitlab.com/NebulousLabs/Sia/profile"
)

// passwordPrompt securely reads a password from stdin.
func passwordPrompt(prompt string) (string, error) {
	fmt.Print(prompt)
	pw, err := terminal.ReadPassword(int(syscall.Stdin))
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
	validModules := "cghmrtwe"
	invalidModules := modules
	for _, m := range validModules {
		invalidModules = strings.Replace(invalidModules, string(m), "", 1)
	}
	if len(invalidModules) > 0 {
		return "", errors.New("Unable to parse --modules flag, unrecognized or duplicate modules: " + invalidModules)
	}
	return modules, nil
}

// processProfileFlags checks that the flags given for profiling are valid.
func processProfileFlags(profile string) (string, error) {
	profile = strings.ToLower(profile)
	validProfiles := "cmt"

	invalidProfiles := profile
	for _, p := range validProfiles {
		invalidProfiles = strings.Replace(invalidProfiles, string(p), "", 1)
	}
	if len(invalidProfiles) > 0 {
		return "", errors.New("Unable to parse --profile flags, unrecognized or duplicate flags: " + invalidProfiles)
	}
	return profile, nil
}

// processConfig checks the configuration values and performs cleanup on
// incorrect-but-allowed values.
func processConfig(config Config) (Config, error) {
	var err1, err2 error
	config.Siad.APIaddr = processNetAddr(config.Siad.APIaddr)
	config.Siad.RPCaddr = processNetAddr(config.Siad.RPCaddr)
	config.Siad.HostAddr = processNetAddr(config.Siad.HostAddr)
	config.Siad.Modules, err1 = processModules(config.Siad.Modules)
	config.Siad.Profile, err2 = processProfileFlags(config.Siad.Profile)
	err3 := verifyAPISecurity(config)
	err := build.JoinErrors([]error{err1, err2, err3}, ", and ")
	if err != nil {
		return Config{}, err
	}
	return config, nil
}

// apiPassword discovers the API password, which may be specified in an
// environment variable, stored in a file on disk, or supplied by the user via
// stdin.
func apiPassword(siaDir string) (string, error) {
	// Check the environment variable.
	pw := os.Getenv("SIA_API_PASSWORD")
	if pw != "" {
		fmt.Println("Using SIA_API_PASSWORD environment variable")
		return pw, nil
	}

	// Try to read the password from disk.
	path := build.APIPasswordFile(siaDir)
	pwFile, err := ioutil.ReadFile(path)
	if err == nil {
		// This is the "normal" case, so don't print anything.
		return strings.TrimSpace(string(pwFile)), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	// No password file; generate a secure one.
	// Generate a password file.
	if err := os.MkdirAll(siaDir, 0700); err != nil {
		return "", err
	}
	pw = hex.EncodeToString(fastrand.Bytes(16))
	if err := ioutil.WriteFile(path, []byte(pw+"\n"), 0600); err != nil {
		return "", err
	}
	fmt.Println("A secure API password has been written to", path)
	fmt.Println("This password will be used automatically the next time you run siad.")
	return pw, nil
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
			config.APIPassword, err = apiPassword(config.Siad.SiaDir)
			if err != nil {
				return Config{}, err
			}
		}
	}
	return config, nil
}

// printVersionAndRevision prints the daemon's version and revision numbers.
func printVersionAndRevision() {
	fmt.Println("Sia Daemon v" + build.Version)
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

// tryAutoUnlock will try to automatically unlock the server's wallet if the
// environment variable is set.
func tryAutoUnlock(srv *server.Server) {
	if password := os.Getenv("SIA_WALLET_PASSWORD"); password != "" {
		fmt.Println("Sia Wallet Password found, attempting to auto-unlock wallet")
		if err := srv.Unlock(password); err != nil {
			fmt.Println("Auto-unlock failed:", err)
		} else {
			fmt.Println("Auto-unlock successful.")
		}
	}
}

// startDaemon uses the config parameters to initialize Sia modules and start
// siad.
func startDaemon(config Config) (err error) {
	loadStart := time.Now()
	// Process the config variables after they are parsed by cobra.
	config, err = processConfig(config)
	if err != nil {
		return errors.AddContext(err, "failed to parse input parameter")
	}

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

	// Start and run the server.
	srv, err := server.New(config.Siad.APIaddr, config.Siad.RequiredUserAgent, config.APIPassword, nodeParams, loadStart)
	if err != nil {
		return err
	}

	// Attempt to auto-unlock the wallet using the SIA_WALLET_PASSWORD env variable
	tryAutoUnlock(srv)

	// listen for kill signals
	sigChan := installKillSignalHandler()

	// Print a 'startup complete' message.
	startupTime := time.Since(loadStart)
	fmt.Printf("Finished full setup in %.3f seconds\n", startupTime.Seconds())

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

	return nil
}

// startDaemonCmd is a passthrough function for startDaemon.
func startDaemonCmd(cmd *cobra.Command, _ []string) {
	var profileCPU, profileMem, profileTrace bool

	profileCPU = strings.Contains(globalConfig.Siad.Profile, "c")
	profileMem = strings.Contains(globalConfig.Siad.Profile, "m")
	profileTrace = strings.Contains(globalConfig.Siad.Profile, "t")

	if build.DEBUG {
		profileCPU = true
		profileMem = true
	}

	if profileCPU || profileMem || profileTrace {
		var profileDir string
		if cmd.Root().Flag("profile-directory").Changed {
			profileDir = globalConfig.Siad.ProfileDir
		} else {
			profileDir = filepath.Join(globalConfig.Siad.SiaDir, globalConfig.Siad.ProfileDir)
		}
		go profile.StartContinuousProfile(profileDir, profileCPU, profileMem, profileTrace)
	}

	// Start siad. startDaemon will only return when it is shutting down.
	err := startDaemon(globalConfig)
	if err != nil {
		die(err)
	}

	// Daemon seems to have closed cleanly. Print a 'closed' mesasge.
	fmt.Println("Shutdown complete.")
}
