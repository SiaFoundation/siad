package skynetblacklist

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// lengthSize is the number of bytes set aside for the length on disk
	lengthSize int64 = 8

	// metadataPageSize is the number of bytes set aside for the metadata page
	// on disk
	metadataPageSize int64 = 4096

	// persistFile is the name of the persist file
	persistFile string = "skynetblacklist"

	// persistMerkleRootSize is the size of a persisted merkleroot in the
	// blacklist
	persistMerkleRootSize int64 = 33
)

var (
	// Metadata validation errors
	errWrongHeader  = errors.New("wrong header")
	errWrongVersion = errors.New("wrong version")

	// metadataHeader is the header of the metadata for the persist file
	metadataHeader = types.NewSpecifier("SkynetBlacklist\n")

	// metadataVersion is the version of the persistence file
	metadataVersion = types.NewSpecifier("v1.4.3\n")
)

// marshalMetadata marshals the Skynet Blacklist's metadata and returns the byte
// slice
func (sb *SkynetBlacklist) marshalMetadata() ([]byte, error) {
	headerBytes, headerErr := metadataHeader.MarshalText()
	versionBytes, versionErr := metadataVersion.MarshalText()
	lengthBytes := encoding.Marshal(sb.persistLength)
	metadataBytes := append(headerBytes, append(versionBytes, lengthBytes...)...)
	return metadataBytes, errors.Compose(headerErr, versionErr)
}

// unmarshalMetadata ummarshals the Skynet Blacklist's metadata from the
// provided byte slice
func (sb *SkynetBlacklist) unmarshalMetadata(raw []byte) error {
	// Define offsets for reading from provided byte slice
	versionOffset := types.SpecifierLen
	lengthOffset := 2 * types.SpecifierLen

	// Unmarshal and check Header and Version for correctness
	var header, version types.Specifier
	err := header.UnmarshalText(raw[:versionOffset])
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal header")
	}
	if header != metadataHeader {
		return errWrongHeader
	}
	err = version.UnmarshalText(raw[versionOffset:lengthOffset])
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal version")
	}
	if version != metadataVersion {
		return errWrongVersion
	}

	// Unmarshal the length
	return encoding.Unmarshal(raw[lengthOffset:], &sb.persistLength)
}

// marshalSia implements the encoding.SiaMarshaler interface.
func marshalSia(w io.Writer, merkleRoot crypto.Hash, blacklisted bool) error {
	e := encoding.NewEncoder(w)
	e.Encode(merkleRoot)
	e.WriteBool(blacklisted)
	return e.Err()
}

// unmarshalBlacklist unmarshals the sia encoded blacklist
func unmarshalBlacklist(r io.Reader, numMerkleRoots int64) (map[crypto.Hash]struct{}, error) {
	// Unmarshal numLinks blacklisted links one by one
	blacklist := make(map[crypto.Hash]struct{})
	for i := int64(0); i < numMerkleRoots; i++ {
		merkleRoot, blacklisted, err := unmarshalSia(r)
		if err != nil {
			return nil, err
		}
		if !blacklisted {
			delete(blacklist, merkleRoot)
			continue
		}
		blacklist[merkleRoot] = struct{}{}
	}
	return blacklist, nil
}

// unmarshalSia implements the encoding.SiaUnmarshaler interface.
func unmarshalSia(r io.Reader) (merkleRoot crypto.Hash, blacklisted bool, err error) {
	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	d.Decode(&merkleRoot)
	blacklisted = d.NextBool()
	err = d.Err()
	return
}

// UpdateSkynetBlacklist updates the list of skylinks that are blacklisted
func (sb *SkynetBlacklist) UpdateSkynetBlacklist(additions, removals []modules.Skylink) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	return sb.update(additions, removals)
}

// initPersist initializes the persistence of the SkynetBlacklist
func (sb *SkynetBlacklist) callInitPersist() error {
	// Initialize the persistence directory
	err := os.MkdirAll(sb.staticPersistDir, modules.DefaultDirPerm)
	if err != nil {
		return errors.AddContext(err, "unable to make persistence directory")
	}

	// Try and Load persistence
	err = sb.load()
	if err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return errors.AddContext(err, "unable to load persistence")
	}

	// Persist File doesn't exist, create it
	f, err := os.OpenFile(filepath.Join(sb.staticPersistDir, persistFile), os.O_RDWR|os.O_CREATE, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "unable to open persistence file")
	}
	defer f.Close()

	// Marshal the metadata.
	sb.persistLength = metadataPageSize
	metadataBytes, err := sb.marshalMetadata()
	if err != nil {
		return errors.AddContext(err, "unable to marshal metadata")
	}

	// Sanity check that the metadataBytes are less than the metadatPageSize
	if int64(len(metadataBytes)) > metadataPageSize {
		err = fmt.Errorf("metadata is londer than the defined page size %v", len(metadataBytes))
		build.Critical(err)
		return err
	}

	// Write metadata to beginning of file. This is a small amount of data and
	// so operation is ACID as a single write and sync.
	_, err = f.WriteAt(metadataBytes, 0)
	if err != nil {
		return errors.AddContext(err, "unable to write metadata to file on initialization")
	}
	err = f.Sync()
	if err != nil {
		return errors.AddContext(err, "unable to fsync file")
	}
	return nil
}

// load loads the persisted blacklist from disk
func (sb *SkynetBlacklist) load() error {
	// Open File
	f, err := os.Open(filepath.Join(sb.staticPersistDir, persistFile))
	if err != nil {
		// Intentionally don't add context to allow for IsNotExist error check
		return err
	}
	defer f.Close()

	// Check the Header and Version of the file
	metadataSize := int64(2*types.SpecifierLen) + lengthSize
	metadataBytes := make([]byte, metadataSize)
	_, err = f.ReadAt(metadataBytes, 0)
	if err != nil {
		return errors.AddContext(err, "enable to read metadata bytes from file")
	}
	err = sb.unmarshalMetadata(metadataBytes)
	if err != nil {
		return errors.AddContext(err, "enable to unmarshal metadata bytes")
	}

	// Check if there is a persisted blacklist after the metatdata
	goodBytes := sb.persistLength - metadataPageSize
	if goodBytes <= 0 {
		return nil
	}

	// Seek to the start of the persisted blacklist
	_, err = f.Seek(metadataPageSize, 0)
	if err != nil {
		return errors.AddContext(err, "unable to seek to start of persisted blacklist")
	}
	// Decode persist links
	blacklist, err := unmarshalBlacklist(f, goodBytes/persistMerkleRootSize)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal persistLinks")
	}

	// Add to Skynet Blacklist
	sb.merkleroots = blacklist

	return nil
}

// update updates the persistence on disk with the new additions and removals
// from the blacklist
func (sb *SkynetBlacklist) update(additions, removals []modules.Skylink) error {
	// Create buffer for encoder
	var buf bytes.Buffer
	// Create and encode the persist links
	for _, skylink := range additions {
		// Add skylink merkleroot to map
		mr := skylink.MerkleRoot()
		sb.merkleroots[mr] = struct{}{}

		// Marshal the update
		err := marshalSia(&buf, mr, true)
		if err != nil {
			return errors.AddContext(err, "unable to encode persistLink")
		}
	}
	for _, skylink := range removals {
		// Remove skylink merkleroot from map
		mr := skylink.MerkleRoot()
		delete(sb.merkleroots, mr)

		// Marshal the update
		err := marshalSia(&buf, mr, false)
		if err != nil {
			return errors.AddContext(err, "unable to encode persistLink")
		}
	}

	// Open file
	f, err := os.OpenFile(filepath.Join(sb.staticPersistDir, persistFile), os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "unable to open persistence file")
	}
	defer f.Close()

	// Append data and sync
	_, err = f.WriteAt(buf.Bytes(), sb.persistLength)
	if err != nil {
		return errors.AddContext(err, "unable to append new data to blacklist persist file")
	}
	err = f.Sync()
	if err != nil {
		return errors.AddContext(err, "unable to fsync file")
	}

	// Update length and sync
	sb.persistLength += int64(buf.Len())
	lengthBytes := encoding.Marshal(sb.persistLength)

	// Write to file
	lengthOffset := int64(2 * types.SpecifierLen)
	_, err = f.WriteAt(lengthBytes, lengthOffset)
	if err != nil {
		return errors.AddContext(err, "unable to write length")
	}
	err = f.Sync()
	if err != nil {
		return errors.AddContext(err, "unable to fsync file")
	}
	return nil
}
