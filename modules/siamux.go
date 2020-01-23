package modules

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/siamux/mux"
)

// TODO: add test to verify host keys are recycled as siamux keys properly
// TODO: add persistence to SiaMux so we can get rid of the SiaMux wrap
// TODO: get rid of SafeClose - added due to failing TestWatchdogSweep test

const (
	// keyfile is the filename of the siamux keys file
	keyfile = "siamuxkeys.json"
	// logfile is the filename of the siamux log file
	logfile = "siamux.log"
)

// siamuxKeysMetadata contains the header and version strings that identify
// the siamux keys
var siamuxKeysMetadata = persist.Metadata{
	Header:  "SiaMux Keys",
	Version: "1.4.2.2",
}

type (
	// SiaMux wraps the siamux to allow decorating it with the siamux keys
	SiaMux struct {
		*siamux.SiaMux
		Keys SiaMuxKeys

		closed bool
		mu     sync.Mutex
	}

	// SiaMuxKeys contains the siamux's public and secret key
	SiaMuxKeys struct {
		SecretKey mux.ED25519SecretKey `json:"secretkey"`
		PublicKey mux.ED25519PublicKey `json:"publickey"`
	}
)

// NewSiaMux returns a new SiaMux object
func NewSiaMux(dir, address string) (*SiaMux, error) {
	// create the logger
	logger, err := newLogger(dir)
	if err != nil {
		return &SiaMux{}, err
	}

	// create the siamux
	keys := loadKeys(dir)
	smux, _, err := siamux.New(address, keys.PublicKey, keys.SecretKey, logger)
	if err != nil {
		return &SiaMux{}, err
	}

	// wrap it
	mux := &SiaMux{Keys: keys}
	mux.SiaMux = smux
	return mux, nil
}

// SafeClose ensures Close is never called twice on the SiaMux
func (mux *SiaMux) SafeClose() error {
	mux.mu.Lock()
	defer mux.mu.Unlock()
	if mux.closed {
		return nil
	}
	mux.closed = true
	return mux.Close()
}

// newLogger creates a new logger
func newLogger(dir string) (*persist.Logger, error) {
	// create the directory if it doesn't exist.
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return nil, err
	}

	// create the logger
	logfilePath := filepath.Join(dir, logfile)
	logger, err := persist.NewFileLogger(logfilePath)
	if err != nil {
		return nil, err
	}
	return logger, nil
}

// loadKeys loads the siamux keys, it has several fallbacks. Most importantly it
// will reuse the host's keys as the siamux keys.
func loadKeys(dir string) (keys SiaMuxKeys) {
	keyfilePath := filepath.Join(dir, keyfile)

	// load the siamux keys from the keyfile
	err := persist.LoadJSON(siamuxKeysMetadata, keys, keyfilePath)
	if err == nil {
		return
	}

	// if that failed, recycle the host's keys and use those as siamux keys
	if keys, err = loadHostKeys(dir); err != nil {
		sk, pk := mux.GenerateED25519KeyPair()
		keys = SiaMuxKeys{sk, pk}
	}

	// save the siamux keys to the keyfile
	err = persist.SaveJSON(siamuxKeysMetadata, keys, keyfilePath)
	if err != nil {
		println("Could not persist siamux keys", err)
	}
	return
}

// loadHostKeys looks for the host's key pair in the persistence object
func loadHostKeys(dir string) (keys SiaMuxKeys, err error) {
	settingsPath := filepath.Join(dir, HostDir, HostDir, ".json")

	// read the host persistence file
	var bytes []byte
	bytes, err = ioutil.ReadFile(settingsPath)
	if err != nil {
		return
	}

	// parse the key pair out of the host's persist file
	hostkeys := struct {
		PublicKey types.SiaPublicKey `json:"publickey"`
		SecretKey crypto.SecretKey   `json:"secretkey"`
	}{}
	err = json.Unmarshal(bytes, &hostkeys)
	if err != nil {
		return
	}

	copy(keys.SecretKey[:], hostkeys.SecretKey[:])
	copy(keys.PublicKey[:], hostkeys.PublicKey.Key[:])
	return
}
