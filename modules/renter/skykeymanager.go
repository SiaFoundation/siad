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
	SkyKeySpecifier = types.NewSpecifier("SkyKeyPrefix")

	errUnsupportedSkyKeyCipherType = errors.New("Unsupported SkyKey ciphertype")
	errNoSkyKeysWithThatName       = errors.New("No key with that name")
	errNoSkyKeysWithThatId         = errors.New("No key is assocated with that Id")
	errSkyKeyGroupAlreadyExists    = errors.New("Group name already exists. Add more keys using AddKey or choose a different name")

	skyKeyManagerPersistMeta = persist.Metadata{
		Header:  "SkyKey Manager Persist",
		Version: "1.4.3",
	}
	skyKeyManagerPersistFilename = "skykeys.json"
)

// SkyKeys are keys used to encrypt/decrypt skyfiles.
type SkyKey struct {
	CipherType crypto.CipherType `json:"ciphertype"`
	Entropy    []byte            `json:"entropy"`
}

// skyKeyManager manages the creation and handling of new skykeys. It supports
// creating groups with human readable names and the addition of multiple keys
// to a single group. Keys can also be referenced by  unique identifiers which
// are stored in skyfiles.
type skyKeyManager struct {
	IdsByName  map[string][]string `json:"idsbyname"`
	KeysById   map[string]SkyKey   `json:"keysbyid"`
	persistDir string

	mu sync.Mutex
}

// newSkyKeyManager returns a new skyKeyManager, with data loaded from the given
// persistDir if it exists.
func newSkyKeyManager(persistDir string) (*skyKeyManager, error) {
	var sm skyKeyManager
	err := persist.LoadJSON(skyKeyManagerPersistMeta, &sm, filepath.Join(sm.persistDir, skyKeyManagerPersistFilename))
	if err != nil && !os.IsNotExist(err) {
		return &skyKeyManager{}, err
	}

	if os.IsNotExist(err) {
		return &skyKeyManager{
			IdsByName:  make(map[string][]string),
			KeysById:   make(map[string]SkyKey),
			persistDir: persistDir,
		}, nil
	}
	return &sm, nil
}

// persist saves the state of the skyKeyManager to disk.
func (sm *skyKeyManager) persist() error {
	err := os.MkdirAll(sm.persistDir, 0750)
	if err != nil {
		return errors.AddContext(err, "Error creating Skynet data dir")
	}

	return persist.SaveJSON(skyKeyManagerPersistMeta, &sm, filepath.Join(sm.persistDir, skyKeyManagerPersistFilename))
}

// Id returns the Id for the SkyKey.
func (sk SkyKey) Id() string {
	h := crypto.HashAll(SkyKeySpecifier, sk.CipherType, sk.Entropy)
	return base64.URLEncoding.EncodeToString(h[:])
}

// SupportsCipherType returns true if and only if the SkyKeyManager supports
// keys with the given cipher type.
func (sm *skyKeyManager) SupportsCipherType(ct crypto.CipherType) bool {
	return ct == crypto.TypeThreefish // TODO: Change to XChaCha20
}

// CreateKeyGroup creates a new key group and generates a new SkyKey for it
// which is returned.
func (sm *skyKeyManager) CreateKeyGroup(name string, cipherTypeString string) (SkyKey, error) {
	var cipherType crypto.CipherType
	err := cipherType.FromString(cipherTypeString)
	if err != nil {
		return SkyKey{}, errors.AddContext(err, "AddKey error decoding cipherType")
	}
	if !sm.SupportsCipherType(cipherType) {
		return SkyKey{}, errUnsupportedSkyKeyCipherType
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()
	_, ok := sm.IdsByName[name]
	if ok {
		return SkyKey{}, errSkyKeyGroupAlreadyExists
	}

	// Generate the new key.
	cipherKey := crypto.GenerateSiaKey(cipherType)
	skyKey := SkyKey{cipherType, cipherKey.Key()}
	keyId := skyKey.Id()

	// Store the new key.
	sm.IdsByName[name] = []string{keyId}
	sm.KeysById[keyId] = skyKey

	err = sm.persist()
	if err != nil {
		return SkyKey{}, err
	}
	return skyKey, nil
}

// AddKey creates a key with the given entropy and ciphertype under the given
// name. If no group exists under that name a new group created.
func (sm *skyKeyManager) AddKey(name string, entropyString string, cipherTypeString string) (SkyKey, error) {
	// Decode string inputs.
	entropy, err := base64.URLEncoding.DecodeString(entropyString)
	if err != nil {
		return SkyKey{}, errors.AddContext(err, "AddKey error decoding key entropy")
	}
	var cipherType crypto.CipherType
	err = cipherType.FromString(cipherTypeString)
	if err != nil {
		return SkyKey{}, errors.AddContext(err, "AddKey error decoding cipherType")
	}
	if !sm.SupportsCipherType(cipherType) {
		return SkyKey{}, errUnsupportedSkyKeyCipherType
	}

	// Create a SkyKey with the input data.
	cipherKey, err := crypto.NewSiaKey(cipherType, entropy)
	if err != nil {
		return SkyKey{}, errors.AddContext(err, "AddKey error creating key")
	}
	skyKey := SkyKey{cipherType, cipherKey.Key()}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Check if this SkyKey is already stored under the same name.
	keyId := skyKey.Id()
	ids, ok := sm.IdsByName[name]
	for _, id := range ids {
		if id == keyId {
			return SkyKey{}, errors.New("Already have key with same identifier and name")
		}
	}

	// Store the new key.
	if !ok {
		ids = make([]string, 0)
	}
	ids = append(ids, keyId)
	sm.IdsByName[name] = ids
	sm.KeysById[keyId] = skyKey

	err = sm.persist()
	if err != nil {
		return SkyKey{}, err
	}
	return skyKey, nil
}

// GetIdsByName returns all the Ids associated with the given group name.
func (sm *skyKeyManager) GetIdsByName(name string) ([]string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	ids, ok := sm.IdsByName[name]
	if !ok {
		return nil, errNoSkyKeysWithThatName
	}
	return ids, nil
}

// GetKeysByName returns the SkyKeys associated with that group name.
func (sm *skyKeyManager) GetKeysByName(name string) ([]SkyKey, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	ids, ok := sm.IdsByName[name]
	if !ok {
		return nil, errNoSkyKeysWithThatName
	}

	keys := make([]SkyKey, 0)
	for _, id := range ids {
		key, ok := sm.KeysById[id]
		if !ok {
			return nil, errNoSkyKeysWithThatId
		}
		keys = append(keys, key)
	}

	return keys, nil
}

// GetKeyById returns the SkyKey associated with that Id.
func (sm *skyKeyManager) GetKeyById(id string) (SkyKey, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key, ok := sm.KeysById[id]
	if !ok {
		return SkyKey{}, errNoSkyKeysWithThatId
	}
	return key, nil
}
