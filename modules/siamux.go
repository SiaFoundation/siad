package modules

import (
	"errors"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/siamux"
	"gitlab.com/NebulousLabs/siamux/mux"
)

const (
	// logfile is the filename of the siamux log file
	logfile = "siamux.log"
)

// NewSiaMux returns a new SiaMux object
func NewSiaMux(persistDir, address string) (*siamux.SiaMux, error) {
<<<<<<< HEAD
	// can't use relative path
	if !filepath.IsAbs(persistDir) {
		err := errors.New("siamux path needs to be absolute")
		build.Critical(err)
=======
	// ensure persistDir is an absolute path, in particular this will transform
	// an empty path, which os.MkdirAll can not handle, in "/"
	persistDir, err := filepath.Abs(persistDir)
	if err != nil {
>>>>>>> ac84ce5225eb77d31988cd0450288410188a3774
		return nil, err
	}

	// ensure the persist directory exists
	err = os.MkdirAll(persistDir, 0700)
	if err != nil {
		return nil, err
	}

	// create a logger
	file, err := os.OpenFile(filepath.Join(persistDir, logfile), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	logger := persist.NewLogger(file)

	// create a siamux, if the host's persistence file is at v120 we want to
	// recycle the host's key pair to use in the siamux
	pubKey, privKey, compat := compatLoadKeysFromHost(persistDir)
	if compat {
		return siamux.CompatV1421NewWithKeyPair(address, logger, persistDir, privKey, pubKey)
	}

	return siamux.New(address, logger, persistDir)
}

// compatLoadKeysFromHost will try and load the host's keypair from its
// persistence file. It tries all host metadata versions before v143. From that
// point on, the siamux was introduced and will already have a correct set of
// keys persisted in its persistence file. Only for hosts upgrading to v143 we
// want to recycle the host keys in the siamux.
func compatLoadKeysFromHost(persistDir string) (pubKey mux.ED25519PublicKey, privKey mux.ED25519SecretKey, compat bool) {
	persistPath := filepath.Join(persistDir, HostDir, HostSettingsFile)

	historicMetadata := []persist.Metadata{
		Hostv120PersistMetadata,
		Hostv112PersistMetadata,
	}

	// Try to load the host's key pair from its persistence file, we try all
	// metadata version up until v143
	hk := struct {
		PublicKey types.SiaPublicKey `json:"publickey"`
		SecretKey crypto.SecretKey   `json:"secretkey"`
	}{}
	for _, metadata := range historicMetadata {
		err := persist.LoadJSON(metadata, &hk, persistPath)
		if err == nil {
			copy(pubKey[:], hk.PublicKey.Key[:])
			copy(privKey[:], hk.SecretKey[:])
			compat = true
			return
		}
	}

	compat = false
	return
}
