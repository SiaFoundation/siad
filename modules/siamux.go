package modules

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/siamux/mux"
)

const (
	// keyFile is the filename of the siamux keys file
	keyFile = "siamuxkeys.json"
	// logFile is the filename of the siamux log file
	logFile = "siamux.log"
	// flags specify the flags used when opening the siamux files
	flags = os.O_RDWR | os.O_TRUNC | os.O_CREATE
)

// SiaMuxKeys contains the siamux's public and secret key
type SiaMuxKeys struct {
	SecretKey mux.ED25519SecretKey `json:"secretkey"`
	PublicKey mux.ED25519PublicKey `json:"publickey"`
}

// NewSiaMux returns a new SiaMux object
func NewSiaMux(dir, address string) (*siamux.SiaMux, error) {
	// create the logger
	logger, err := newLogger(dir)
	if err != nil {
		return &siamux.SiaMux{}, err
	}

	// load the keys
	sk, pk := loadKeys(dir)
	if err := persistKeys(dir, sk, pk); err != nil {
		logger.Println(err)
	}

	// create the siamux
	mux, _, err := siamux.New(address, pk, sk, logger)
	return mux, err
}

// LoadSiaMuxKeys returns the siamux keys
func LoadSiaMuxKeys(dir string) *SiaMuxKeys {
	sk, pk, err := loadSiaMuxKeys(dir)
	if err != nil {
		build.Critical("SiaMux keys not found")
		sk, pk = mux.GenerateED25519KeyPair()
	}
	return &SiaMuxKeys{sk, pk}
}

// newLogger creates a new logger
func newLogger(dir string) (*persist.Logger, error) {
	// create the directory if it doesn't exist.
	err := os.MkdirAll(dir, 0700)
	if err != nil {
		return nil, err
	}

	// create the logfile
	logFilePath := filepath.Join(dir, logFile)
	_, err = os.OpenFile(logFilePath, flags, 0600)
	if err != nil {
		return nil, err
	}

	// create the logger
	logger, err := persist.NewFileLogger(filepath.Join(dir, logFile))
	if err != nil {
		return nil, err
	}
	return logger, nil
}

// loadKeys will load the siamux keys. It will first try to load the keys from
// the siamux key file. If that does not exist, it will try to recycle the
// host's keys, if that does not work it generates a new key pair.
func loadKeys(dir string) (sk mux.ED25519SecretKey, pk mux.ED25519PublicKey) {
	sk, pk, err := loadSiaMuxKeys(dir)
	if err == nil {
		return
	}

	sk, pk, err = loadHostKeys(dir)
	if err == nil {
		return
	}

	sk, pk = mux.GenerateED25519KeyPair()
	return
}

// persistKeys will persist the given keys at the keyfile location.
func persistKeys(dir string, sk mux.ED25519SecretKey, pk mux.ED25519PublicKey) (err error) {
	file, err := os.OpenFile(filepath.Join(dir, keyFile), os.O_RDWR|os.O_TRUNC|os.O_CREATE, 0600)
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

// loadSiaMuxKeys loads a siamux key pair. If it can not reuse the host's pubkey
// pair it will generate a new pair and persist them in a location that's used
// by fresh hosts when they establish their default config.
func loadSiaMuxKeys(dir string) (sk mux.ED25519SecretKey, pk mux.ED25519PublicKey, err error) {
	// read the host persistence file
	var bytes []byte
	bytes, err = ioutil.ReadFile(filepath.Join(dir, keyFile))
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
	keys := struct {
		PublicKey types.SiaPublicKey `json:"publickey"`
		SecretKey crypto.SecretKey   `json:"secretkey"`
	}{}
	err = json.Unmarshal(bytes, &keys)
	if err != nil {
		return
	}

	copy(sk[:], keys.SecretKey[:])
	copy(pk[:], keys.PublicKey.Key[:])
	return
}
