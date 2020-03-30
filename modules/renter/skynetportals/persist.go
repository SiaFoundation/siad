package skynetportals

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/build"
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
	persistFile string = "skynetportals"

	// persistPortalSize is the size of a persisted portal in the portals list
	persistPortalSize int64 = modules.MaxEncodedNetAddressLength + 2
)

var (
	// Metadata validation errors
	errWrongHeader  = errors.New("wrong header")
	errWrongVersion = errors.New("wrong version")

	// metadataHeader is the header of the metadata for the persist file
	metadataHeader = types.NewSpecifier("SkynetPortals\n")

	// metadataVersion is the version of the persistence file
	metadataVersion = types.NewSpecifier("v1.4.6\n")
)

// marshalMetadata marshals the Skynet Portal List's metadata and returns the byte
// slice
func (sp *SkynetPortals) marshalMetadata() ([]byte, error) {
	headerBytes, headerErr := metadataHeader.MarshalText()
	versionBytes, versionErr := metadataVersion.MarshalText()
	lengthBytes := encoding.Marshal(sp.persistLength)
	metadataBytes := append(headerBytes, append(versionBytes, lengthBytes...)...)
	return metadataBytes, errors.Compose(headerErr, versionErr)
}

// marshalSia implements the encoding.SiaMarshaler interface.
func marshalSia(w io.Writer, address modules.NetAddress, public bool, listed bool) error {
	e := encoding.NewEncoder(w)
	// Create a padded buffer so that we always write the same amount of bytes.
	buf := make([]byte, modules.MaxEncodedNetAddressLength)
	copy(buf, address)
	_, err := e.Write(buf)
	if err != nil {
		return err
	}
	err = e.WriteBool(public)
	if err != nil {
		return err
	}
	err = e.WriteBool(listed)
	if err != nil {
		return err
	}
	return e.Err()
}

// unmarshalPortals unmarshals the sia encoded portals list
func unmarshalPortals(r io.Reader, numPortals int64) (map[modules.NetAddress]bool, error) {
	// Unmarshal portals one by one
	portals := make(map[modules.NetAddress]bool)
	for i := int64(0); i < numPortals; i++ {
		address, public, listed, err := unmarshalSia(r)
		if err != nil {
			return nil, err
		}
		if !listed {
			delete(portals, address)
			continue
		}
		portals[address] = public
	}
	return portals, nil
}

// unmarshalSia implements the encoding.SiaUnmarshaler interface.
func unmarshalSia(r io.Reader) (address modules.NetAddress, public, listed bool, err error) {
	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	// Read into a padded buffer and extract the address string.
	buf := make([]byte, modules.MaxEncodedNetAddressLength)
	n, err := d.Read(buf)
	if err != nil {
		err = errors.AddContext(err, "unable to read address")
		return
	}
	if n != len(buf) {
		err = errors.New("did not read address correctly")
		return
	}
	end := bytes.IndexByte(buf, 0)
	if end == -1 {
		end = len(buf)
	}
	address = modules.NetAddress(string(buf[:end]))
	public = d.NextBool()
	listed = d.NextBool()
	err = d.Err()
	return
}

// initPersist initializes the persistence of the SkynetPortals
func (sp *SkynetPortals) callInitPersist() error {
	// Initialize the persistence directory
	err := os.MkdirAll(sp.staticPersistDir, modules.DefaultDirPerm)
	if err != nil {
		return errors.AddContext(err, "unable to make persistence directory")
	}

	// Try and Load persistence
	err = sp.load()
	if err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return errors.AddContext(err, "unable to load persistence")
	}

	// Persist File doesn't exist, create it
	f, err := os.OpenFile(filepath.Join(sp.staticPersistDir, persistFile), os.O_RDWR|os.O_CREATE, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "unable to open persistence file")
	}
	defer f.Close()

	// Marshal the metadata.
	sp.persistLength = metadataPageSize
	metadataBytes, err := sp.marshalMetadata()
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

// callUpdateAndAppend updates the portals list with the additions and removals
// and appends the changes to the persist file on disk.
//
// NOTE: this method does not check for duplicate additions or removals
func (sp *SkynetPortals) callUpdateAndAppend(additions []modules.SkynetPortalInfo, removals []modules.NetAddress) error {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	// Create buffer for encoder
	var buf bytes.Buffer
	// Create and encode the persist portals
	for _, portal := range additions {
		// Add portal to map
		address := portal.Address
		public := portal.Public
		sp.portals[address] = public

		// Marshal the update
		err := marshalSia(&buf, address, public, true)
		if err != nil {
			return errors.AddContext(err, "unable to encode persist portal")
		}
	}
	for _, address := range removals {
		// Remove portal from map
		delete(sp.portals, address)

		// Marshal the update
		err := marshalSia(&buf, address, true, false)
		if err != nil {
			return errors.AddContext(err, "unable to encode persist portal")
		}
	}

	// Open file
	f, err := os.OpenFile(filepath.Join(sp.staticPersistDir, persistFile), os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "unable to open persistence file")
	}
	defer f.Close()

	// Append data and sync
	_, err = f.WriteAt(buf.Bytes(), sp.persistLength)
	if err != nil {
		return errors.AddContext(err, "unable to append new data to portals persist file")
	}
	err = f.Sync()
	if err != nil {
		return errors.AddContext(err, "unable to fsync file")
	}

	// Update length and sync
	sp.persistLength += int64(buf.Len())
	lengthBytes := encoding.Marshal(sp.persistLength)

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

// load loads the persisted portals list from disk.
func (sp *SkynetPortals) load() error {
	// Open File
	f, err := os.Open(filepath.Join(sp.staticPersistDir, persistFile))
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
		return errors.AddContext(err, "unable to read metadata bytes from file")
	}
	err = sp.unmarshalMetadata(metadataBytes)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal metadata bytes")
	}

	// Check if there is a persisted portals list after the metatdata
	goodBytes := sp.persistLength - metadataPageSize
	if goodBytes <= 0 {
		return nil
	}

	// Seek to the start of the persisted portals list
	_, err = f.Seek(metadataPageSize, 0)
	if err != nil {
		return errors.AddContext(err, "unable to seek to start of persisted portals list")
	}
	// Decode persist portals
	portals, err := unmarshalPortals(f, goodBytes/persistPortalSize)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal persist portals")
	}

	// Add to Skynet Portals List
	sp.portals = portals

	return nil
}

// unmarshalMetadata ummarshals the Skynet Portals List's metadata from the
// provided byte slice.
func (sp *SkynetPortals) unmarshalMetadata(raw []byte) error {
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
	return encoding.Unmarshal(raw[lengthOffset:], &sp.persistLength)
}
