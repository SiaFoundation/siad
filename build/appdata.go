package build

import (
	"encoding/hex"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gitlab.com/NebulousLabs/fastrand"
)

// APIPassword returns the Sia API Password either from the environment variable
// or from the password file. If no environment variable is set and no file
// exists, a password file is created and that password is returned
func APIPassword() (string, error) {
	// Check the environment variable.
	pw := os.Getenv(siaAPIPassword)
	if pw != "" {
		return pw, nil
	}

	// Try to read the password from disk.
	path := apiPasswordFilePath()
	pwFile, err := ioutil.ReadFile(path)
	if err == nil {
		// This is the "normal" case, so don't print anything.
		return strings.TrimSpace(string(pwFile)), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	// No password file; generate a secure one.
	// Generate a password file.
	pw, err = createAPIPasswordFile()
	if err != nil {
		return "", err
	}
	return pw, nil
}

// ProfileDir returns the directory where any profiles for the running siad
// instance will be stored
func ProfileDir() string {
	return filepath.Join(SiadDataDir(), "profile")
}

// SiadDataDir returns the siad consensus data directory from the
// environment variable. If there is no environment variable it returns an empty
// string, instructing siad to store the consensus in the current directory.
func SiadDataDir() string {
	return os.Getenv(siadDataDir)
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

// WalletPassword returns the SiaWalletPassword environment variable.
func WalletPassword() string {
	return os.Getenv(siaWalletPassword)
}

// ExchangeRate returns the siaExchangeRate environment variable.
func ExchangeRate() string {
	return os.Getenv(siaExchangeRate)
}

// apiPasswordFilePath returns the path to the API's password file. The password
// file is stored in the Sia data directory.
func apiPasswordFilePath() string {
	return filepath.Join(SiaDir(), "apipassword")
}

// createAPIPasswordFile creates an api password file in the Sia data directory
// and returns the newly created password
func createAPIPasswordFile() (string, error) {
	err := os.MkdirAll(SiaDir(), 0700)
	if err != nil {
		return "", err
	}
	// Ensure SiaDir has the correct mode as MkdirAll won't change the mode of
	// an existent directory. We specifically use 0700 in order to prevent
	// potential attackers from accessing the sensitive information inside, both
	// by reading the contents of the directory and/or by creating files with
	// specific names which siad would later on read from and/or write to.
	err = os.Chmod(SiaDir(), 0700)
	if err != nil {
		return "", err
	}
	pw := hex.EncodeToString(fastrand.Bytes(16))
	err = ioutil.WriteFile(apiPasswordFilePath(), []byte(pw+"\n"), 0600)
	if err != nil {
		return "", err
	}
	return pw, nil
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
