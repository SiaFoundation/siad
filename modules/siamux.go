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

	// SiaMuxDir is the name of the siamux dir
	SiaMuxDir = "siamux"
)

// NewSiaMux returns a new SiaMux object
func NewSiaMux(siaMuxDir, siaDir, address string) (*siamux.SiaMux, error) {
	// can't use relative path
	if !filepath.IsAbs(siaMuxDir) || !filepath.IsAbs(siaDir) {
		err := errors.New("paths need to be absolute")
		build.Critical(err)
		return nil, err
	}

	// ensure the persist directory exists
	err := os.MkdirAll(siaMuxDir, 0700)
	if err != nil {
		return nil, err
	}

	// CompatV143 migrate existing mux in siaDir root to siaMuxDir.
	if err := compatV143MigrateSiaMux(siaMuxDir, siaDir); err != nil {
		return nil, err
	}

	// create a logger
	file, err := os.OpenFile(filepath.Join(siaMuxDir, logfile), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	logger := persist.NewLogger(file)

	// create a siamux, if the host's persistence file is at v120 we want to
	// recycle the host's key pair to use in the siamux
	pubKey, privKey, compat := compatLoadKeysFromHost(siaDir)
	if compat {
		return siamux.CompatV1421NewWithKeyPair(address, logger, siaMuxDir, privKey, pubKey)
	}
	return siamux.New(address, logger, siaMuxDir)
}

// SiaPKToMuxPK turns a SiaPublicKey into a mux.ED25519PublicKey
func SiaPKToMuxPK(spk types.SiaPublicKey) (mk mux.ED25519PublicKey) {
	// Sanity check key length
	if len(spk.Key) != len(mk) {
		build.Critical("Expected the given SiaPublicKey to have a length equal to the mux.ED25519PublicKey length")
	}
	copy(mk[:], spk.Key)
	return
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

// compatV143MigrateSiaMux migrates the SiaMux from the root dir of the sia data
// dir to the siamux subdir.
func compatV143MigrateSiaMux(siaMuxDir, siaDir string) error {
	oldPath := filepath.Join(siaDir, "siamux.json")
	newPath := filepath.Join(siaMuxDir, "siamux.json")
	oldPathTmp := filepath.Join(siaDir, "siamux.json_temp")
	newPathTmp := filepath.Join(siaMuxDir, "siamux.json_temp")
	oldPathLog := filepath.Join(siaDir, logfile)
	newPathLog := filepath.Join(siaMuxDir, logfile)
	_, errOld := os.Stat(oldPath)
	_, errNew := os.Stat(newPath)

	// Migrate if old file exists but no file at new location exists yet.
	migrated := false
	if errOld == nil && os.IsNotExist(errNew) {
		if err := os.Rename(oldPath, newPath); err != nil {
			return err
		}
		migrated = true
	}
	// If no migration is necessary we are done.
	if !migrated {
		return nil
	}
	// If we migrated the main files, also migrate the tmp files if available.
	if err := os.Rename(oldPathTmp, newPathTmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	// Also migrate the log file.
	if err := os.Rename(oldPathLog, newPathLog); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
