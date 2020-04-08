package build

import (
	"os"
	"testing"
)

// resetEnvVar is a helper function to reset an environment variable to its
// original state after testing
func resetEnvVar(env, value string) error {
	if value == "" {
		return os.Unsetenv(env)
	}
	return os.Setenv(env, value)
}

// TestAPIPassword tests getting and setting the API Password
func TestAPIPassword(t *testing.T) {
	// Not a short test because it loads env variables from disk. Don't run in
	// parallel since it chances env variables
	if testing.Short() {
		t.SkipNow()
	}

	// Record any defaults
	originalPW := os.Getenv(siaAPIPassword)
	defer func() {
		err := resetEnvVar(siaAPIPassword, originalPW)
		if err != nil {
			t.Fatal("Error resetting env variable to:", originalPW)
		}
	}()
	err := os.Unsetenv(siaAPIPassword)
	if err != nil {
		t.Error(err)
	}

	// Calling APIPassword should return a none blank password if the env
	// variable isn't set
	pw, err := APIPassword()
	if err != nil {
		t.Error(err)
	}
	if pw == "" {
		t.Error("Password should not be blank")
	}

	// Test setting the env variable
	err = SetAPIPassword("")
	if err == nil {
		t.Error("Shouldn't be able to set blank API Password")
	}
	newPW := "abc123"
	err = SetAPIPassword(newPW)
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

// TestSiaDir tests getting and setting the Sia data directory
func TestSiaDir(t *testing.T) {
	// Not a short test because it loads env variables from disk. Don't run in
	// parallel since it chances env variables
	if testing.Short() {
		t.SkipNow()
	}

	// Record any defaults
	originalSiaDir := os.Getenv(siaDataDir)
	defer func() {
		err := resetEnvVar(siaDataDir, originalSiaDir)
		if err != nil {
			t.Fatal("Error resetting env variable to:", originalSiaDir)
		}
	}()
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
	// Not a short test because it loads env variables from disk. Don't run in
	// parallel since it chances env variables
	if testing.Short() {
		t.SkipNow()
	}

	// Record any defaults
	originalSiaWalletPassword := os.Getenv(siaWalletPassword)
	defer func() {
		err := resetEnvVar(siaWalletPassword, originalSiaWalletPassword)
		if err != nil {
			t.Fatal("Error resetting env variable to:", originalSiaWalletPassword)
		}
	}()
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

// TestSkynetDir tests getting and setting the Skynet data directory
func TestSkynetDir(t *testing.T) {
	// Not a short test because it loads env variables from disk. Don't run in
	// parallel since it chances env variables
	if testing.Short() {
		t.SkipNow()
	}

	// Record any defaults
	originalSkeynetDir := os.Getenv(skynetDataDir)
	defer func() {
		err := resetEnvVar(skynetDataDir, originalSkeynetDir)
		if err != nil {
			t.Fatal("Error resetting env variable to:", originalSkeynetDir)
		}
	}()
	err := os.Unsetenv(skynetDataDir)
	if err != nil {
		t.Error(err)
	}

	// Test Default SkynetDir
	skyDir := SkynetDir()
	if skyDir != defaultSkynetDir() {
		t.Errorf("Expected skyDir to be %v but was %v", defaultSkynetDir(), skyDir)
	}

	// Test Env Variable
	newSkyDir := "foo/bar"
	err = os.Setenv(skynetDataDir, newSkyDir)
	if err != nil {
		t.Error(err)
	}
	skyDir = SkynetDir()
	if skyDir != newSkyDir {
		t.Errorf("Expected skyDir to be %v but was %v", newSkyDir, skyDir)
	}
}
