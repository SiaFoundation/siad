package build

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gitlab.com/NebulousLabs/fastrand"
)

var (
	// siaAPIPassword is the environment variable that sets a custom API
	// password if the default is not used
	siaAPIPassword = "SIA_API_PASSWORD"

	// siaDataDir is the environment variable that tells siad where to put the
	// sia data
	siaDataDir = "SIA_DATA_DIR"

	// siaWalletPassword is the environment variable that can be set to enable
	// auto unlocking the wallet
	siaWalletPassword = "SIA_WALLET_PASSWORD"

	// skynetDataDir is the environment variable that tells siad where to put
	// the miscellaneous skynet data
	skynetDataDir = "SKYNET_DATA_DIR"
)

// APIPassword returns the Sia API Password either from the environment variable
// or from the password file. If no environment variable is set and no file
// exists, a password file is created and that password is returned
func APIPassword() (string, error) {
	// Check the environment variable.
	pw := os.Getenv(siaAPIPassword)
	if pw != "" {
		fmt.Println("Using SIA_API_PASSWORD environment variable")
		return pw, nil
	}

	// Try to read the password from disk.
	path := apiPasswordFile()
	pwFile, err := ioutil.ReadFile(path)
	if err == nil {
		// This is the "normal" case, so don't print anything.
		return strings.TrimSpace(string(pwFile)), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	// No password file; generate a secure one.
	// Generate a password file.
	err = createAPIPasswordFile()
	if err != nil {
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

// SetAPIPassword sets the SiaAPIPassword environment variable
func SetAPIPassword(pw string) error {
	if pw == "" {
		return errors.New("Cannot set blank password")
	}
	return os.Setenv(siaAPIPassword, pw)
}

// SiaDir returns the Sia data directory either from the environment variable or
// the default.
func SiaDir() string {
	siaDir := os.Getenv(siaDataDir)
	if siaDir == "" {
		siaDir = defaultSiaDir()
	}
	return siaDir
}

// SkynetDir returns the Skynet data directory.
func SkynetDir() string {
	skynetDir := os.Getenv(skynetDataDir)
	if skynetDir == "" {
		skynetDir = defaultSkynetDir()
	}
	return skynetDir
}

// WalletPassword returns the SiaWalletPassword environment variable.
func WalletPassword() string {
	return os.Getenv(siaWalletPassword)
}

// apiPasswordFile returns the path to the API's password file. The password
// file is stored in the Sia data directory.
func apiPasswordFile() string {
	return filepath.Join(SiaDir(), "apipassword")
}

// createAPIPasswordFile creates an api password file in the Sia data directory
func createAPIPasswordFile() error {
	return os.Mkdir(SiaDir(), 0700)
}

// defaultSiaDir returns the default data directory of siad. The values for
// supported operating systems are:
//
// Linux:   $HOME/.sia
// MacOS:   $HOME/Library/Application Support/Sia
// Windows: %LOCALAPPDATA%\Sia
func defaultSiaDir() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Sia")
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Sia")
	default:
		return filepath.Join(os.Getenv("HOME"), ".sia")
	}
}

// defaultSkynetDir returns default data directory for miscellaneous Skynet data,
// e.g. skykeys. The values for supported operating systems are:
//
// Linux:   $HOME/.skynet
// MacOS:   $HOME/Library/Application Support/Skynet
// Windows: %LOCALAPPDATA%\Skynet
func defaultSkynetDir() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Skynet")
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Skynet")
	default:
		return filepath.Join(os.Getenv("HOME"), ".skynet")
	}
}
