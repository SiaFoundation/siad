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
	"gitlab.com/NebulousLabs/errors"
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

type (
	// SiaMux wraps the siamux to allow decorating it with the persistDir
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

// NewSiaMux returns a new SiaMux object
func NewSiaMux(dir, address string) (*SiaMux, error) {
	// create the logger
	logger, err := newLogger(dir)
	if err != nil {
		return &SiaMux{}, err
	}

	// create the siamux
	sk, pk := loadKeys(dir)
	smux, _, err := siamux.New(address, pk, sk, logger)

	mux := &SiaMux{Keys: SiaMuxKeys{sk, pk}}
	mux.SiaMux = smux
	return mux, nil
}

// newLogger creates a new logger
func newLogger(dir string) (*persist.Logger, error) {
	// create the directory if it doesn't exist.
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return nil, err
	}

	// create the logfile
	logfilePath := filepath.Join(dir, logfile)
	_, err = os.OpenFile(logfilePath, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}

	// create the logger
	logger, err := persist.NewFileLogger(filepath.Join(dir, logfile))
	if err != nil {
		return nil, err
	}
	return logger, nil
}

// loadKeys loads the siamux keys, it has several fallbacks. Most importantly it
// will reuse the host's keys as the siamux keys.
func loadKeys(dir string) (sk mux.ED25519SecretKey, pk mux.ED25519PublicKey) {
	sk, pk, err := loadSiaMuxKeys(dir)
	if err == nil {
		return
	}

	// defer a persist if the keys are recycled from the host or if we generate
	// a new key pair
	defer func() {
		err := persistKeys(dir, sk, pk)
		if err != nil {
			println("Could not persist siamux keys", err)
		}
	}()

	sk, pk, err = loadHostKeys(dir)
	if err == nil {
		return
	}

	sk, pk = mux.GenerateED25519KeyPair()
	return
}

// persistKeys will persist the given keys at the keyfile location.
func persistKeys(dir string, sk mux.ED25519SecretKey, pk mux.ED25519PublicKey) (err error) {
	// open keyfile
	keyfilePath := filepath.Join(dir, keyfile)
	file, err := os.OpenFile(keyfilePath, os.O_RDWR|os.O_TRUNC|os.O_CREATE, 0600)
	if err != nil {
		return errors.AddContext(err, "could not open siamux keyfile")
	}
	defer func() {
		err = errors.Compose(err, file.Close())
	}()

	// encode the keys
	keys := SiaMuxKeys{sk, pk}
	bytes, err := json.Marshal(keys)
	if err != nil {
		return errors.AddContext(err, "could not encode siamux keys")
	}

	// persist the keys
	_, err = file.Write(bytes)
	if err != nil {
		return errors.AddContext(err, "could not persist siamux keys")
	}

	err = file.Sync()
	return
}

// loadSiaMuxKeys loads the siamux keys from the keyfile
func loadSiaMuxKeys(dir string) (sk mux.ED25519SecretKey, pk mux.ED25519PublicKey, err error) {
	// read the keyfile
	var bytes []byte
	bytes, err = ioutil.ReadFile(filepath.Join(dir, keyfile))
	if err != nil {
		return
	}

	// unmarshal the keys
	var keys SiaMuxKeys
	err = json.Unmarshal(bytes, &keys)
	if err != nil {
		return
	}

	sk = keys.SecretKey
	pk = keys.PublicKey
	return
}

// loadHostKeys looks for the host's key pair in the persistence object
func loadHostKeys(dir string) (sk mux.ED25519SecretKey, pk mux.ED25519PublicKey, err error) {
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

	copy(sk[:], hostkeys.SecretKey[:])
	copy(pk[:], hostkeys.PublicKey.Key[:])
	return
}
