package skykey

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

const (
	// SkyKeyIDLen is the length of a SkykeyID
	SkykeyIDLen = 16

	// MaxKeyNameLen is the maximum length of a skykey's name.
	MaxKeyNameLen = 128

	// headerLen is the length of the skykey file header.
	headerLen = types.SpecifierLen + types.SpecifierLen + 8
)

var (
	skykeyVersionString = "1.4.3"
	skykeyVersion       = types.NewSpecifier(skykeyVersionString)

	// SkykeySpecifier is used as a prefix when hashing Skykeys to compute their
	// ID.
	SkykeySpecifier = types.NewSpecifier("Skykey")

	// SkykeyFileMagic is the first piece of data found in a Skykey file.
	SkykeyFileMagic = types.NewSpecifier("SkykeyFile")

	errUnsupportedSkykeyCipherType = errors.New("Unsupported Skykey ciphertype")
	errNoSkykeysWithThatName       = errors.New("No Skykey with that name")
	errNoSkykeysWithThatID         = errors.New("No Skykey is assocated with that ID")
	errSkykeyNameAlreadyExists     = errors.New("Skykey name already exists.")
	errSkykeyNameToolong           = errors.New("Skykey name exceeds max length")

	// SkykeyPersistFilename is the name of the skykey persistence file.
	SkykeyPersistFilename = "skykeys.dat"
)

type SkykeyID [SkykeyIDLen]byte

// Skykey is a key used to encrypt/decrypt skyfiles.
type Skykey struct {
	Name       string
	CipherType crypto.CipherType
	Entropy    []byte
}

// SkykeyManager manages the creation and handling of new skykeys which can be
// referenced by their unique name or identifier.
type SkykeyManager struct {
	idsByName map[string]SkykeyID
	keysByID  map[SkykeyID]Skykey

	version types.Specifier
	fileLen uint64 // Invariant: fileLen is at least headerLen

	persistFile string
	mu          sync.Mutex
}

// countingWriter is a wrapper of an io.Writer that keep track of the total
// amount of bytes written.
type countingWriter struct {
	writer io.Writer
	count  int
}

func newCountingWriter(w io.Writer) *countingWriter {
	return &countingWriter{w, 0}
}

// Write implements the io.Writer interface.
func (cw *countingWriter) Write(p []byte) (n int, err error) {
	n, err = cw.writer.Write(p)
	cw.count += n
	return
}

func (cw countingWriter) BytesWritten() uint64 {
	return uint64(cw.count)
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

// ID returns the ID for the Skykey.
func (sk Skykey) ID() (keyID SkykeyID) {
	h := crypto.HashAll(SkykeySpecifier, sk.CipherType, sk.Entropy)
	copy(keyID[:], h[:SkykeyIDLen])
	return keyID
}

// equals returns true if and only if the two Skykeys are equal.
func (sk *Skykey) equals(otherKey Skykey) bool {
	return sk.Name == otherKey.Name && sk.ID() == otherKey.ID() && sk.CipherType.String() == otherKey.CipherType.String()
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

	err = sm.saveKey(skykey)
	if err != nil {
		return Skykey{}, err
	}
	return skykey, nil
}

// AddKey creates a key with the given name, cipherType, and entropy and adds it
// to the key file.
func (sm *SkykeyManager) AddKey(name string, cipherTypeString string, entropy []byte) (Skykey, error) {
	if len(name) > MaxKeyNameLen {
		return Skykey{}, errSkykeyNameToolong
	}

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
	_, ok := sm.idsByName[name]
	if ok {
		return Skykey{}, errSkykeyNameAlreadyExists
	}

	// Generate the new key.
	cipherKey, err := crypto.NewSiaKey(cipherType, entropy)
	if err != nil {
		return Skykey{}, errors.AddContext(err, "Error creating new cipher key")
	}
	skykey := Skykey{name, cipherType, cipherKey.Key()}

	err = sm.saveKey(skykey)
	if err != nil {
		return Skykey{}, err
	}
	return skykey, nil
}

// GetIDByName returns the ID associated with the given key name.
func (sm *SkykeyManager) GetIDByName(name string) (SkykeyID, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	id, ok := sm.idsByName[name]
	if !ok {
		return SkykeyID{}, errNoSkykeysWithThatName
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

	key, ok := sm.keysByID[id]
	if !ok {
		return Skykey{}, errNoSkykeysWithThatID
	}

	return key, nil
}

// GetKeyByID returns the Skykey associated with that ID.
func (sm *SkykeyManager) GetKeyByID(id SkykeyID) (Skykey, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	key, ok := sm.keysByID[id]
	if !ok {
		return Skykey{}, errNoSkykeysWithThatID
	}
	return key, nil
}

// NewSkykeyManager creates a SkykeyManager for managing skykeys.
func NewSkykeyManager(persistDir string) (*SkykeyManager, error) {
	sm := &SkykeyManager{
		idsByName:   make(map[string]SkykeyID),
		keysByID:    make(map[SkykeyID]Skykey),
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
	var magic types.Specifier
	dec.Decode(&magic)
	if magic != SkykeyFileMagic {
		return errors.New("Expected skykey file magic")
	}

	dec.Decode(&sm.version)
	if dec.Err() != nil {
		return errors.AddContext(dec.Err(), "Error decoding skykey file version")
	}

	versionBytes, err := sm.version.MarshalText()
	if err != nil {
		return err
	}
	version := strings.ReplaceAll(string(versionBytes), string(0x0), "")

	if !build.IsVersion(version) {
		return errors.New("skykey file header missing version")
	}
	if build.VersionCmp(skykeyVersionString, version) < 0 {
		return errors.New("Unknown skykey version")
	}

	// Read the length of the file into the key manager.
	dec.Decode(&sm.fileLen)
	return dec.Err()
}

// saveHeader saves the header data of the skykey file to disk and syncs the
// file.
func (sm *SkykeyManager) saveHeader(file *os.File) error {
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
	file, err := os.OpenFile(sm.persistFile, os.O_RDWR|os.O_CREATE, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "Unable to open SkykeyManager persist file")
	}
	defer file.Close()

	// Check if the file has a header.  If there is not, then set the default
	// values and save it.
	fileInfo, err := file.Stat()
	if err != nil {
		return err
	}
	if fileInfo.Size() < int64(headerLen) {
		sm.version = skykeyVersion
		sm.fileLen = uint64(headerLen)
		return sm.saveHeader(file)
	}

	// Otherwise load the existing header and all the skykeys in the file.
	err = sm.loadHeader(file)
	if err != nil {
		return errors.AddContext(err, "Error loading header")
	}

	_, err = file.Seek(int64(headerLen), io.SeekStart)
	if err != nil {
		return err
	}

	// Read all the skykeys up to the length set in the header.
	n := headerLen
	for n < int(sm.fileLen) {
		var sk Skykey
		err = sk.unmarshalSia(file)
		if err != nil {
			return errors.AddContext(err, "Error unmarshaling Skykey")
		}

		// Store the skykey.
		sm.idsByName[sk.Name] = sk.ID()
		sm.keysByID[sk.ID()] = sk

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

// saveKey saves the key and  appends it to the skykey file and updates/syncs
// the header.
func (sm *SkykeyManager) saveKey(skykey Skykey) error {
	keyID := skykey.ID()

	// Store the new key.
	sm.idsByName[skykey.Name] = keyID
	sm.keysByID[keyID] = skykey

	file, err := os.OpenFile(sm.persistFile, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "Unable to open SkykeyManager persist file")
	}
	defer file.Close()

	// Seek to the end of the known-to-be-valid part of the file.
	_, err = file.Seek(int64(sm.fileLen), io.SeekStart)
	if err != nil {
		return err
	}

	writer := newCountingWriter(file)
	err = skykey.marshalSia(writer)
	if err != nil {
		return errors.AddContext(err, "Error writing skykey to file")
	}

	err = file.Sync()
	if err != nil {
		return err
	}

	// Update the header
	sm.fileLen += writer.BytesWritten()
	return sm.saveHeader(file)
}
