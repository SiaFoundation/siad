package modules

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/siamux/mux"
)

const logFile = "siamux.log"

func NewSiaMux(dir, address string) (*siamux.SiaMux, error) {
	sk, pk := LoadSiaMuxKeyPair(dir)
	logger, err := persist.NewFileLogger(filepath.Join(dir, logFile))
	if err != nil {
		return &siamux.SiaMux{}, err
	}
	sm, _, err := siamux.New(address, pk, sk, logger)
	return sm, err
}

func LoadSiaMuxKeyPair(persistDir string) (sk mux.ED25519SecretKey, pk mux.ED25519PublicKey) {
	generateNew := func() {
		sk, pk = mux.GenerateED25519KeyPair()
		writeKeyPair(sk, pk)
	}

	// check if we can find the host settings
	hostSettingsPath := filepath.Join(persistDir, HostDir, HostDir, ".json")
	_, statErr := os.Stat(hostSettingsPath)

	// if the host settings do not exist, create a new key pair and save
	// them,when the host loads it will look for this key file
	if os.IsNotExist(statErr) {
		generateNew()
		return
	}

	// if it exists, try to read the file and extract the keys
	// fall back to generatign a new key pair
	bytes, err := ioutil.ReadFile(hostSettingsPath)
	if err != nil {
		generateNew()
		return
	}
	keys := struct {
		PublicKey types.SiaPublicKey `json:"publickey"`
		SecretKey crypto.SecretKey   `json:"secretkey"`
	}{}
	err = json.Unmarshal(bytes, &keys)
	if err != nil {
		generateNew()
		return
	}
	copy(sk[:], keys.SecretKey[:])
	copy(pk[:], keys.PublicKey.Key[:])
	return
}

func writeKeyPair(sk mux.ED25519SecretKey, pk mux.ED25519PublicKey) {
	// TODO write to location on disk
}
