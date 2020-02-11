package build

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultSiaDir returns the default data directory of siad. The values for
// supported operating systems are:
//
// Linux:   $HOME/.sia
// MacOS:   $HOME/Library/Application Support/Sia
// Windows: %LOCALAPPDATA%\Sia
func DefaultSiaDir() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Sia")
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Sia")
	default:
		return filepath.Join(os.Getenv("HOME"), ".sia")
	}
}

// DefaultSkynetDir returns default data directory for miscellenous Skynet data,
// e.g. skykeys. The values for supported operating systems are:
//
// Linux:   $HOME/.skynet
// MacOS:   $HOME/Library/Application Support/Skynet
// Windows: %LOCALAPPDATA%\Skynet
func DefaultSkynetDir() string {
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "Skynet")
	case "darwin":
		return filepath.Join(os.Getenv("HOME"), "Library", "Application Support", "Skynet")
	default:
		return filepath.Join(os.Getenv("HOME"), ".skynet")
	}
}

// APIPasswordFile returns the path to the API's password file given a Sia
// directory.
func APIPasswordFile(siaDir string) string {
	return filepath.Join(siaDir, "apipassword")
}
