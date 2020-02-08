package renter

import (
	"encoding/hex"
	"sync"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

/*

siac skynet createkey [name]

siac skynet addkey [key] [name]

*siac skynet upload --key=[key]

siac skynet download (should infer key)


*/

var (
	SkyKeySpecifier = types.NewSpecifier("SkyKeyPrefix")

	errUnsupportedSkyKeyCipherType = errors.New("Unsupported SkyKey ciphertype")
	errNoSkyKeysWithThatName       = errors.New("No key with that name")
	errNoSkyKeysWithThatId         = errors.New("No key is assocated with that Id")
	errSkyKeyGroupAlreadyExists    = errors.New("Group name already exists. Add more keys using AddKey or choose a different name")
)

type SkyKey struct { // TODO add a version
	CipherType crypto.CipherType
	Entropy    []byte
}

type skyKeyManager struct {
	idsByName map[string][]string
	keysById  map[string]SkyKey

	mu sync.Mutex
}

func newSkyKeyManager() skyKeyManager {
	return skyKeyManager{
		idsByName: make(map[string][]string),
		keysById:  make(map[string]SkyKey),
	}
}

func (sm *skyKeyManager) persistData() {
	// TODO persist.
}

// Id returns the Id for the SkyKey.
func (sk SkyKey) Id() string {
	h := crypto.HashAll(SkyKeySpecifier, sk.CipherType, sk.Entropy)
	return string(h[:])
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
	_, ok := sm.idsByName[name]
	if ok {
		return SkyKey{}, errSkyKeyGroupAlreadyExists
	}

	// Generate the new key.
	cipherKey := crypto.GenerateSiaKey(cipherType)
	skyKey := SkyKey{cipherType, cipherKey.Key()}
	keyId := skyKey.Id()

	// Store the new key.
	sm.idsByName[name] = []string{keyId}
	sm.keysById[keyId] = skyKey

	sm.persistData()
	return skyKey, nil
}

// AddKey creates a key with the given entropy and ciphertype under the given
// name. If no group exists under that name a new group created.
func (sm *skyKeyManager) AddKey(name string, entropyString string, cipherTypeString string) (SkyKey, error) {
	// Decode string inputs.
	entropy, err := hex.DecodeString(entropyString)
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
	ids, ok := sm.idsByName[name]
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
	sm.idsByName[name] = ids
	sm.keysById[keyId] = skyKey

	sm.persistData()
	return skyKey, nil
}

// GetIdsByName returns all the Ids associated with the given group name.
func (sm *skyKeyManager) GetIdsByName(name string) ([]string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	ids, ok := sm.idsByName[name]
	if !ok {
		return nil, errNoSkyKeysWithThatName
	}
	return ids, nil
}

// GetKeysByName returns the SkyKeys associated with that group name.
func (sm *skyKeyManager) GetKeysByName(name string) ([]SkyKey, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	ids, ok := sm.idsByName[name]
	if !ok {
		return nil, errNoSkyKeysWithThatName
	}

	keys := make([]SkyKey, 0)
	for _, id := range ids {
		key, ok := sm.keysById[id]
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

	key, ok := sm.keysById[id]
	if !ok {
		return SkyKey{}, errNoSkyKeysWithThatId
	}
	return key, nil
}
