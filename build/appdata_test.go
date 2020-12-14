package build

import (
	"os"
	"testing"
)

// TestAPIPassword tests getting and setting the API Password
func TestAPIPassword(t *testing.T) {
	// Unset any defaults, this only affects in memory state. Any Env Vars will
	// remain intact on disk
	err := os.Unsetenv(siaAPIPassword)
	if err != nil {
		t.Error(err)
	}

	// Calling APIPassword should return a non-blank password if the env
	// variable isn't set
	pw, err := APIPassword()
	if err != nil {
		t.Error(err)
	}
	if pw == "" {
		t.Error("Password should not be blank")
	}

	// Test setting the env variable
	newPW := "abc123"
	err = os.Setenv(siaAPIPassword, newPW)
	if err != nil {
		t.Error(err)
	}
	pw, err = APIPassword()
	if err != nil {
		t.Error(err)
	}
	if pw != newPW {
		t.Errorf("Expected password to be %v but was %v", newPW, pw)
	}
}

// TestSiadDataDir tests getting and setting the Sia consensus directory
func TestSiadDataDir(t *testing.T) {
	// Unset any defaults, this only affects in memory state. Any Env Vars will
	// remain intact on disk
	err := os.Unsetenv(siadDataDir)
	if err != nil {
		t.Error(err)
	}

	// Test Default SiadDataDir
	siadDir := SiadDataDir()
	if siadDir != "" {
		t.Errorf("Expected siadDir to be empty but was %v", siadDir)
	}

	// Test Env Variable
	newSiaDir := "foo/bar"
	err = os.Setenv(siadDataDir, newSiaDir)
	if err != nil {
		t.Error(err)
	}
	siadDir = SiadDataDir()
	if siadDir != newSiaDir {
		t.Errorf("Expected siadDir to be %v but was %v", newSiaDir, siadDir)
	}
}

// TestSiaDir tests getting and setting the Sia data directory
func TestSiaDir(t *testing.T) {
	// Unset any defaults, this only affects in memory state. Any Env Vars will
	// remain intact on disk
	err := os.Unsetenv(siaDataDir)
	if err != nil {
		t.Error(err)
	}

	// Test Default SiaDir
	siaDir := SiaDir()
	if siaDir != defaultSiaDir() {
		t.Errorf("Expected siaDir to be %v but was %v", defaultSiaDir(), siaDir)
	}

	// Test Env Variable
	newSiaDir := "foo/bar"
	err = os.Setenv(siaDataDir, newSiaDir)
	if err != nil {
		t.Error(err)
	}
	siaDir = SiaDir()
	if siaDir != newSiaDir {
		t.Errorf("Expected siaDir to be %v but was %v", newSiaDir, siaDir)
	}
}

// TestSiaWalletPassword tests getting and setting the Sia Wallet Password
func TestSiaWalletPassword(t *testing.T) {
	// Unset any defaults, this only affects in memory state. Any Env Vars will
	// remain intact on disk
	err := os.Unsetenv(siaWalletPassword)
	if err != nil {
		t.Error(err)
	}

	// Test Default Wallet Password
	pw := WalletPassword()
	if pw != "" {
		t.Errorf("Expected wallet password to be blank but was %v", pw)
	}

	// Test Env Variable
	newPW := "abc123"
	err = os.Setenv(siaWalletPassword, newPW)
	if err != nil {
		t.Error(err)
	}
	pw = WalletPassword()
	if pw != newPW {
		t.Errorf("Expected wallet password to be %v but was %v", newPW, pw)
	}
}

// TestSiaExchangeRate tests getting and setting the Sia Exchange Rate
func TestSiaExchangeRate(t *testing.T) {
	// Unset any defaults, this only affects in memory state. Any Env Vars will
	// remain intact on disk
	err := os.Unsetenv(siaExchangeRate)
	if err != nil {
		t.Error(err)
	}

	// Test Default
	rate := ExchangeRate()
	if rate != "" {
		t.Errorf("Expected exchange rate to be blank but was %v", rate)
	}

	// Test Env Variable
	newRate := "abc123"
	err = os.Setenv(siaExchangeRate, newRate)
	if err != nil {
		t.Error(err)
	}
	rate = ExchangeRate()
	if rate != newRate {
		t.Errorf("Expected exchange rate to be %v but was %v", newRate, rate)
	}
}
