package registry

import (
	"encoding/binary"
	"os"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// List of supported signature algorithms.
const (
	signatureEd25519 = 1
)

type (
	// pesistedEntry is an entry
	// Size on disk: 1 + 32 + 32 + 8 + 1 + 109 + 8 + 64 + 1 = 256
	persistedEntry struct {
		// key data
		Key   compressedPublicKey
		Tweak [TweakSize]byte

		// value data
		Expiry    types.BlockHeight
		DataLen   uint8
		Data      [RegistryDataSize]byte
		Revision  uint64
		Signature crypto.Signature

		// data related to persistence
		IsUsed bool
	}

	// compressedPublicKey is a version of the types.SiaPublicKey which is
	// optimized for being stored on disk.
	compressedPublicKey struct {
		Algorithm byte
		Key       [crypto.PublicKeySize]byte
	}
)

// newCompressedPublicKey creates a compressed public key from a sia public key.
func newCompressedPublicKey(spk types.SiaPublicKey) (cpk compressedPublicKey, _ error) {
	if len(spk.Key) != len(cpk.Key) {
		return cpk, errors.New("newCompressedPublicKey: unsupported key length")
	}
	if spk.Algorithm != types.SignatureEd25519 {
		return cpk, errors.New("newCompressedPublicKey: unsupported signature algo")
	}
	copy(cpk.Key[:], spk.Key)
	cpk.Algorithm = signatureEd25519
	return
}

// newSiaPublicKey creates a sia public key from a compressed public key.
func newSiaPublicKey(cpk compressedPublicKey) (spk types.SiaPublicKey, _ error) {
	if cpk.Algorithm != signatureEd25519 {
		return spk, errors.New("newSiaPublicKey: unknown signature algorithm")
	}
	spk.Algorithm = types.SignatureEd25519
	spk.Key = append([]byte{}, cpk.Key[:]...)
	return
}

// initRegistry initializes a registry at the specified path using the provided
// wal.
func initRegistry(path string, wal *writeaheadlog.WAL) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, modules.DefaultFilePerm)
	if err != nil {
		return nil, errors.AddContext(err, "failed to create new file for key/value store")
	}

	// The first entry is reserved for metadata. Right now only the version
	// number.
	initData := make([]byte, persistedEntrySize)
	binary.LittleEndian.PutUint64(initData, registryVersion)

	// Write data to disk in an ACID way.
	initUpdate := writeaheadlog.WriteAtUpdate(path, 0, initData)
	err = wal.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, initUpdate)
	if err != nil {
		err = errors.Compose(err, f.Close()) // close the file on error
		return nil, errors.AddContext(err, "failed to apply init update")
	}
	return f, nil
}

// newPersistedEntry turns a key-value pair into a persistedEntry.
func newPersistedEntry(value value, isUsed bool) (persistedEntry, error) {
	if len(value.data) > RegistryDataSize {
		build.Critical("newPersistedEntry: called with too much data")
		return persistedEntry{}, errors.New("value's data is too large")
	}
	cpk, err := newCompressedPublicKey(value.key)
	if err != nil {
		return persistedEntry{}, errors.AddContext(err, "newPersistedEntry: failed to compress key")
	}
	pe := persistedEntry{
		Key:   cpk,
		Tweak: value.tweak,

		DataLen:  uint8(len(value.data)),
		Expiry:   value.expiry,
		Revision: value.revision,

		IsUsed: isUsed,
	}
	copy(pe.Data[:], value.data)
	return pe, nil
}

// KeyValue converts a persistedEntry into a key-value pair.
func (entry persistedEntry) Value(index int64) (value, error) {
	if entry.DataLen > RegistryDataSize {
		err := errors.New("KeyValue: entry has a too big data len")
		build.Critical(err)
		return value{}, err
	}
	spk, err := newSiaPublicKey(entry.Key)
	if err != nil {
		return value{}, errors.AddContext(err, "KeyVale: failed to convert compressed key to SiaPublicKey")
	}
	return value{
		key:         spk,
		tweak:       entry.Tweak,
		expiry:      entry.Expiry,
		data:        entry.Data[:entry.DataLen],
		revision:    entry.Revision,
		staticIndex: index,
	}, nil
}

// Marshal marshals a persistedEntry.
func (entry persistedEntry) Marshal() ([]byte, error) {
	if entry.DataLen > RegistryDataSize {
		build.Critical(errTooMuchData)
		return nil, errTooMuchData
	}
	b := make([]byte, persistedEntrySize)
	b[0] = entry.Key.Algorithm
	copy(b[1:], entry.Key.Key[:])
	copy(b[33:], entry.Tweak[:])
	binary.LittleEndian.PutUint64(b[65:], uint64(entry.Expiry))
	binary.LittleEndian.PutUint64(b[73:], uint64(entry.Revision))
	copy(b[81:], entry.Signature[:])
	if entry.IsUsed {
		b[145] = 1
	}
	b[146] = byte(entry.DataLen)
	copy(b[147:], entry.Data[:])
	return b, nil
}

// Unmarshal unmarshals a persistedEntry.
func (entry *persistedEntry) Unmarshal(b []byte) error {
	if len(b) != persistedEntrySize {
		build.Critical(errEntryWrongSize)
		return errEntryWrongSize
	}
	entry.Key.Algorithm = b[0]
	copy(entry.Key.Key[:], b[1:])
	copy(entry.Tweak[:], b[33:])
	entry.Expiry = types.BlockHeight(binary.LittleEndian.Uint64(b[65:]))
	entry.Revision = binary.LittleEndian.Uint64(b[73:])
	copy(entry.Signature[:], b[81:])
	entry.IsUsed = b[145] == 1
	entry.DataLen = uint8(b[146])
	if int(entry.DataLen) > len(entry.Data) {
		return errors.New("read DataLen exceeds length of available data")
	}
	copy(entry.Data[:], b[147:])
	return nil
}

// saveEntry stores a key-value pair on disk in an ACID fashion.
func (r *Registry) saveEntry(v value, isUsed bool) error {
	entry, err := newPersistedEntry(v, isUsed)
	if err != nil {
		return errors.AddContext(err, "Save: failed to get persistedEntry from key-value pair")
	}
	b, err := entry.Marshal()
	if err != nil {
		return errors.AddContext(err, "Save: failed to marshal persistedEntry")
	}
	update := writeaheadlog.WriteAtUpdate(r.staticPath, v.staticIndex*persistedEntrySize, b)
	return r.staticWAL.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, update)
}
