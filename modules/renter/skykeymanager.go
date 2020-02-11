package renter

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	SkykeySpecifier = types.NewSpecifier("SkykeyPrefix")

	errUnsupportedSkykeyCipherType = errors.New("Unsupported Skykey ciphertype")
	errNoSkykeysWithThatName       = errors.New("No key with that name")
	errNoSkykeysWithThatId         = errors.New("No key is assocated with that Id")
	errSkykeyGroupAlreadyExists    = errors.New("Group name already exists. Add more keys using AddKey or choose a different name")

	skykeyManagerPersistMeta = persist.Metadata{
		Header:  "Skykey Manager Persist",
		Version: "1.4.3",
	}
	skykeyManagerPersistFilename = "skykeys.json"
)

// Skykeys are keys used to encrypt/decrypt skyfiles.
type Skykey struct {
	CipherType crypto.CipherType `json:"ciphertype"`
	Entropy    []byte            `json:"entropy"`
}

// skykeyManager manages the creation and handling of new skykeys. It supports
// creating groups with human readable names and the addition of multiple keys
// to a single group. Keys can also be referenced by  unique identifiers which
// are stored in skyfiles.
type skykeyManager struct {
	IdsByName  map[string][]string `json:"idsbyname"`
	KeysById   map[string]Skykey   `json:"keysbyid"`
	persistDir string

	mu sync.Mutex
}

// newSkykeyManager returns a new skykeyManager, with data loaded from the given
// persistDir if it exists.
func newSkykeyManager(persistDir string) (*skykeyManager, error) {
	var sm skykeyManager
	err := persist.LoadJSON(skykeyManagerPersistMeta, &sm, filepath.Join(sm.persistDir, skykeyManagerPersistFilename))
	if err != nil && !os.IsNotExist(err) {
		return &skykeyManager{}, err
	}

	if os.IsNotExist(err) {
		return &skykeyManager{
			IdsByName:  make(map[string][]string),
			KeysById:   make(map[string]Skykey),
			persistDir: persistDir,
		}, nil
	}
	return &sm, nil
}

// persist saves the state of the skykeyManager to disk.
func (sm *skykeyManager) persist() error {
	err := os.MkdirAll(sm.persistDir, 0750)
	if err != nil {
		return errors.AddContext(err, "Error creating Skynet data dir")
	}

	return persist.SaveJSON(skykeyManagerPersistMeta, &sm, filepath.Join(sm.persistDir, skykeyManagerPersistFilename))
}

// Id returns the Id for the Skykey.
func (sk Skykey) Id() string {
	h := crypto.HashAll(SkykeySpecifier, sk.CipherType, sk.Entropy)
	return base64.URLEncoding.EncodeToString(h[:])
}

// SupportsCipherType returns true if and only if the SkykeyManager supports
// keys with the given cipher type.
func (sm *skykeyManager) SupportsCipherType(ct crypto.CipherType) bool {
	return ct == crypto.TypeThreefish // TODO: Change to XChaCha20
}

// CreateKeyGroup creates a new key group and generates a new Skykey for it
// which is returned.
func (sm *skykeyManager) CreateKeyGroup(name string, cipherTypeString string) (Skykey, error) {
	var cipherType crypto.CipherType
	err := cipherType.FromString(cipherTypeString)
	if err != nil {
		return Skykey{}, errors.AddContext(err, "AddKey error decoding cipherType")
	}
	if !sm.SupportsCipherType(cipherType) {
		return Skykey{}, errUnsupportedSkykeyCipherType
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()
	_, ok := sm.IdsByName[name]
	if ok {
		return Skykey{}, errSkykeyGroupAlreadyExists
	}

	// Generate the new key.
	cipherKey := crypto.GenerateSiaKey(cipherType)
	skykey := Skykey{cipherType, cipherKey.Key()}
	keyId := skykey.Id()

	// Store the new key.
	sm.IdsByName[name] = []string{keyId}
	sm.KeysById[keyId] = skykey

	err = sm.persist()
	if err != nil {
		return Skykey{}, err
	}
	return skykey, nil
}

// AddKey creates a key with the given entropy and ciphertype under the given
// name. If no group exists under that name a new group created.
func (sm *skykeyManager) AddKey(name string, entropyString string, cipherTypeString string) (Skykey, error) {
	// Decode string inputs.
	entropy, err := base64.URLEncoding.DecodeString(entropyString)
	if err != nil {
		return Skykey{}, errors.AddContext(err, "AddKey error decoding key entropy")
	}
	var cipherType crypto.CipherType
	err = cipherType.FromString(cipherTypeString)
	if err != nil {
		return Skykey{}, errors.AddContext(err, "AddKey error decoding cipherType")
	}
	if !sm.SupportsCipherType(cipherType) {
		return Skykey{}, errUnsupportedSkykeyCipherType
	}

	// Create a Skykey with the input data.
	cipherKey, err := crypto.NewSiaKey(cipherType, entropy)
	if err != nil {
		return Skykey{}, errors.AddContext(err, "AddKey error creating key")
	}
	skykey := Skykey{cipherType, cipherKey.Key()}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if this Skykey is already stored under the same name.
	keyId := skykey.Id()
	ids, ok := sm.IdsByName[name]
	for _, id := range ids {
		if id == keyId {
			return Skykey{}, errors.New("Already have key with same identifier and name")
		}
	}

	// Store the new key.
	if !ok {
		ids = make([]string, 0)
	}
	ids = append(ids, keyId)
	sm.IdsByName[name] = ids
	sm.KeysById[keyId] = skykey

	err = sm.persist()
	if err != nil {
		return Skykey{}, err
	}
	return skykey, nil
}

// GetIdsByName returns all the Ids associated with the given group name.
func (sm *skykeyManager) GetIdsByName(name string) ([]string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	ids, ok := sm.IdsByName[name]
	if !ok {
		return nil, errNoSkykeysWithThatName
	}
	return ids, nil
}

// GetKeysByName returns the Skykeys associated with that group name.
func (sm *skykeyManager) GetKeysByName(name string) ([]Skykey, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	ids, ok := sm.IdsByName[name]
	if !ok {
		return nil, errNoSkykeysWithThatName
	}

	keys := make([]Skykey, 0)
	for _, id := range ids {
		key, ok := sm.KeysById[id]
		if !ok {
			return nil, errNoSkykeysWithThatId
		}
		keys = append(keys, key)
	}

	return keys, nil
}

// GetKeyById returns the Skykey associated with that Id.
func (sm *skykeyManager) GetKeyById(id string) (Skykey, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key, ok := sm.KeysById[id]
	if !ok {
		return Skykey{}, errNoSkykeysWithThatId
	}
	return key, nil
}
