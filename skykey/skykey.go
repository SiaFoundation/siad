package skykey

import (
	"bytes"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	SkykeyVersion       = "1.4.3"
	maxVersionStringLen = 32 // maximum length of the version string

	// MaxKeyNameLen is the maximum length of a skykey's name.
	MaxKeyNameLen = 128

	// headerLen is the length of the skykey file header.
	headerLen = types.SpecifierLen + maxVersionStringLen + 8
)

var (
	// SkykeySpecifier is used as a prefix when hashing Skykeys to compute their
	// Id.
	SkykeySpecifier = types.NewSpecifier("Skykey")

	// SkykeyFileMagic is the first piece of data found in a Skykey file.
	SkykeyFileMagic = "SkykeyFile"

	errUnsupportedSkykeyCipherType = errors.New("Unsupported Skykey ciphertype")
	errNoSkykeysWithThatName       = errors.New("No key with that name")
	errNoSkykeysWithThatId         = errors.New("No key is assocated with that Id")
	errSkykeyNameAlreadyExists     = errors.New("Skykey name already exists.")
	errSkykeyNameToolong           = errors.New("Skykey name exceeds max length")

	// SkykeyPersistFilename is the name of the skykey persistence file.
	SkykeyPersistFilename = "skykeys.dat"
)

// Skykey is a key used to encrypt/decrypt skyfiles.
type Skykey struct {
	Name       string
	CipherType crypto.CipherType
	Entropy    []byte
}

// SkykeyManager manages the creation and handling of new skykeys which can be
// referenced by their unique name or identifier.
type SkykeyManager struct {
	idsByName map[string]string
	keysById  map[string]Skykey

	version string
	fileLen uint64 // Invariant: fileLen is at least headerLen
	keys    []Skykey

	persistFile string
	mu          sync.Mutex
}

// countingWriter is a wrapper of an io.Writer that keep track of the total
// amount of bytes written.
type countingWriter struct {
	writer io.Writer
	count  *int
}

// Write implements the io.Writer interface.
func (cw countingWriter) Write(p []byte) (n int, err error) {
	n, err = cw.writer.Write(p)
	*cw.count += n
	return
}

// unmarshalSia decodes the Skykey into the reader.
func (sk *Skykey) unmarshalSia(r io.Reader) error {
	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	d.Decode(&sk.Name)
	d.Decode(&sk.CipherType)
	d.Decode(&sk.Entropy)
	return d.Err()
}

// marshalSia encodess the Skykey into the writer.
func (sk Skykey) marshalSia(w io.Writer) error {
	e := encoding.NewEncoder(w)
	e.Encode(sk.Name)
	e.Encode(sk.CipherType)
	e.Encode(sk.Entropy)
	return e.Err()
}

// Id returns the Id for the Skykey.
func (sk Skykey) Id() string {
	h := crypto.HashAll(SkykeySpecifier, sk.CipherType, sk.Entropy)
	return base64.URLEncoding.EncodeToString(h[:])
}

// equals returns true if and only if the two Skykeys are equal.
func (sk *Skykey) equals(otherKey Skykey) bool {
	return sk.Name == otherKey.Name && sk.Id() == otherKey.Id() && sk.CipherType.String() == otherKey.CipherType.String()
}

// SupportsCipherType returns true if and only if the SkykeyManager supports
// keys with the given cipher type.
func (sm *SkykeyManager) SupportsCipherType(ct crypto.CipherType) bool {
	return ct == crypto.TypeThreefish // TODO: Change to XChaCha20
}

// CreateKey creates a new Skykey under the given name and cipherType.
func (sm *SkykeyManager) CreateKey(name string, cipherTypeString string) (Skykey, error) {
	if len(name) > MaxKeyNameLen {
		return Skykey{}, errSkykeyNameToolong
	}

	var cipherType crypto.CipherType
	err := cipherType.FromString(cipherTypeString)
	if err != nil {
		return Skykey{}, errors.AddContext(err, "CreateKey error decoding cipherType")
	}
	if !sm.SupportsCipherType(cipherType) {
		return Skykey{}, errUnsupportedSkykeyCipherType
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()
	_, ok := sm.idsByName[name]
	if ok {
		return Skykey{}, errSkykeyNameAlreadyExists
	}

	// Generate the new key.
	cipherKey := crypto.GenerateSiaKey(cipherType)
	skykey := Skykey{name, cipherType, cipherKey.Key()}
	keyId := skykey.Id()

	// Store the new key.
	sm.idsByName[name] = string(keyId)
	sm.keysById[keyId] = skykey
	sm.keys = append(sm.keys, skykey)

	err = sm.save()
	if err != nil {
		return Skykey{}, err
	}
	return skykey, nil
}

// GetIdByName returns the Id associated with the given key name.
func (sm *SkykeyManager) GetIdByName(name string) (string, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	id, ok := sm.idsByName[name]
	if !ok {
		return "", errNoSkykeysWithThatName
	}
	return id, nil
}

// GetKeyByName returns the Skykey associated with that key name.
func (sm *SkykeyManager) GetKeyByName(name string) (Skykey, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	id, ok := sm.idsByName[name]
	if !ok {
		return Skykey{}, errNoSkykeysWithThatName
	}

	key, ok := sm.keysById[id]
	if !ok {
		return Skykey{}, errNoSkykeysWithThatId
	}

	return key, nil
}

// GetKeyById returns the Skykey associated with that Id.
func (sm *SkykeyManager) GetKeyById(id string) (Skykey, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key, ok := sm.keysById[id]
	if !ok {
		return Skykey{}, errNoSkykeysWithThatId
	}
	return key, nil
}

// NewSkykeyManager creates a SkykeyManager for managing skykeys.
func NewSkykeyManager(persistDir string) (*SkykeyManager, error) {
	sm := &SkykeyManager{
		idsByName:   make(map[string]string),
		keysById:    make(map[string]Skykey),
		fileLen:     0,
		persistFile: filepath.Join(persistDir, SkykeyPersistFilename),
	}

	// create the persist dir if it doesn't already exist.
	err := os.MkdirAll(persistDir, 0750)
	if err != nil {
		return nil, err
	}

	// Load the persist. If it's empty, it will be initialized.
	err = sm.load()
	if err != nil {
		return nil, err
	}
	return sm, nil
}

// loadHeader loads the header from the skykey file.
func (sm *SkykeyManager) loadHeader(file *os.File) error {
	headerBytes := make([]byte, headerLen)
	_, err := file.Read(headerBytes)
	if err != nil {
		return errors.AddContext(err, "Error reading Skykey file metadata")
	}

	dec := encoding.NewDecoder(bytes.NewReader(headerBytes), encoding.DefaultAllocLimit)
	var magic string
	dec.Decode(&magic)
	if magic != SkykeyFileMagic {
		return errors.New("Expected skykey file magic")
	}

	dec.Decode(&sm.version)
	if dec.Err() != nil {
		return errors.AddContext(dec.Err(), "Error decoding skykey file version")
	}

	if !build.IsVersion(sm.version) {
		return errors.New("skykey file header missing version")
	}

	// Read the length of the file into the key manager.
	dec.Decode(&sm.fileLen)
	return dec.Err()
}

// saveHeader saves the header data of the skykey file to disk and syncs the
// file.
func (sm *SkykeyManager) saveHeader(file *os.File) error {
	if len(sm.version) > maxVersionStringLen {
		return errors.New("Version string too long")
	}

	_, err := file.Seek(0, 0)
	if err != nil {
		return errors.AddContext(err, "Unable to save skykey header")
	}

	e := encoding.NewEncoder(file)
	e.Encode(SkykeyFileMagic)
	e.Encode(sm.version)
	e.Encode(sm.fileLen)
	if e.Err() != nil {
		return errors.AddContext(e.Err(), "Error encoding skykey file header")
	}
	return file.Sync()
}

// load initializes the SkykeyManager with the data stored in the skykey file if
// it exists. If it does not exist, it initializes that file with the default
// header values.
func (sm *SkykeyManager) load() error {
	file, err := os.OpenFile(sm.persistFile, os.O_RDWR|os.O_CREATE, 0750)
	if err != nil {
		return errors.AddContext(err, "Unable to open SkykeyManager persist file")
	}

	// Check if the file has a header.
	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}

	// If there is no header, set the default values and create it.
	if fileInfo.Size() < int64(headerLen) {
		sm.version = SkykeyVersion
		sm.fileLen = uint64(headerLen)
		err = sm.saveHeader(file)
	} else { // Otherwise load the existing header.
		err = sm.loadHeader(file)
		if err != nil {
			return errors.AddContext(err, "Error loading header")
		}
	}

	n := headerLen
	_, err = file.Seek(int64(headerLen), io.SeekStart)
	if err != nil {
		return err
	}

	// Read all the skykeys up to the length set in the header.
	for n < int(sm.fileLen) {
		var sk Skykey
		err = sk.unmarshalSia(file)
		if err != nil {
			return errors.AddContext(err, "Error unmarshaling Skykey")
		}

		// Store the skykey.
		sm.keys = append(sm.keys, sk)
		sm.idsByName[sk.Name] = sk.Id()
		sm.keysById[sk.Id()] = sk

		// Set n to current offset in file.
		currOffset, err := file.Seek(0, io.SeekCurrent)
		n = int(currOffset)
		if err != nil {
			return errors.AddContext(err, "Error getting skykey file offset")
		}
	}

	if n != int(sm.fileLen) {
		return errors.New("Expected to read entire specified skykey file length")
	}
	return nil
}

// Save appends the last key to the skykey file and updates/syncs the header.
func (sm *SkykeyManager) save() error {
	file, err := os.OpenFile(sm.persistFile, os.O_RDWR|os.O_CREATE, 0750)
	if err != nil {
		return errors.AddContext(err, "Unable to open SkykeyManager persist file")
	}

	// Seek to the end of the known-to-be-valid part of the file.
	_, err = file.Seek(int64(sm.fileLen), io.SeekStart)
	if err != nil {
		return err
	}

	var writerCount int
	writer := countingWriter{file, &writerCount}
	lastKey := sm.keys[len(sm.keys)-1]
	err = lastKey.marshalSia(writer)
	if err != nil {
		return errors.AddContext(err, "Error writing skykey to file")
	}

	err = file.Sync()
	if err != nil {
		return err
	}

	// Update the header
	sm.fileLen += uint64(writerCount)
	return sm.saveHeader(file)
}
