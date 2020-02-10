package modules

import (
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/siamux/mux"
)

const (
	// logfile is the filename of the siamux log file
	logfile = "siamux.log"

	// settingsfile is the filename of the host's persistence file
	settingsFile = "host.json"
)

type (
	// hostKeys represents the host's key pair, it is used to extract only the
	// keys from a host's persistence object
	hostKeys struct {
		PublicKey types.SiaPublicKey `json:"publickey"`
		SecretKey crypto.SecretKey   `json:"secretkey"`
	}

	// siaMuxKeys represents a SiaMux key pair
	siaMuxKeys struct {
		pubKey  mux.ED25519PublicKey
		privKey mux.ED25519SecretKey
	}
)

var (
	// v120PersistMetadata is the header of the v120 host persist file
	v120PersistMetadata = persist.Metadata{
		Header:  "Sia Host",
		Version: "1.2.0",
	}
)

// NewSiaMux returns a new SiaMux object
func NewSiaMux(persistDir, address string) (*siamux.SiaMux, error) {
	logger, err := newLogger(persistDir)
	if err != nil {
		return nil, err
	}

	useCompat, keys := useCompatV1421(persistDir)
	if useCompat {
		return siamux.CompatV1421NewWithKeyPair(address, logger, persistDir, keys.privKey, keys.pubKey)
	}
	return siamux.New(address, logger, persistDir)
}

// newLogger creates a new logger
func newLogger(persistDir string) (*persist.Logger, error) {
	// create the directory if it doesn't exist.
	err := os.MkdirAll(persistDir, 0700)
	if err != nil {
		return nil, err
	}

	// create the logger
	logfilePath := filepath.Join(persistDir, logfile)
	logger, err := persist.NewFileLogger(logfilePath)
	if err != nil {
		return nil, err
	}
	return logger, nil
}

// useCompatV1421 returns true if we need to initialize the SiaMux using it's
// compatibility constructor. This will be the case when the host's persistence
// version is 1.2.0. If so we want to recycle the host's key pair to use in the
// SiaMux
func useCompatV1421(persistDir string) (bool, *siaMuxKeys) {
	persistPath := filepath.Join(persistDir, HostDir, settingsFile)

	// check if we can load the host's persistence object with metadata header
	// v120, if so we are upgrading from 1.2.0 -> 1.3.0 which means we want to
	// recycle the host's key pair to use in the SiaMux
	var hk hostKeys
	err := persist.LoadJSON(v120PersistMetadata, &hk, persistPath)
	if err == nil {
		return true, hk.toSiaMuxKeys()
	}

	return false, nil
}

// toSiaMuxKeys converts a set of host keys to siamux keys
func (hk *hostKeys) toSiaMuxKeys() *siaMuxKeys {
	pubKey := mux.ED25519PublicKey{}
	copy(pubKey[:], hk.PublicKey.Key[:])
	privKey := mux.ED25519SecretKey{}
	copy(privKey[:], hk.SecretKey[:])
	return &siaMuxKeys{pubKey, privKey}
}
