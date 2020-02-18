package skynetblacklist

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// headerSize is the number of bytes set aside for the header on disk
	headerSize int64 = 16

	// lengthSize is the number of bytes set aside for the length on disk
	lengthSize int64 = 8

	// metadataHeader is the header of the metadata for the persist file
	metadataHeader string = "Skynet Blacklist"

	// metadataPageSize is the number of bytes set aside for the metadata page
	// on disk
	metadataPageSize int64 = 4096

	// metadataVersion is the version of the persistence file
	metadataVersion string = "v1.4.3"

	// persistFile is the name of the persist file
	persistFile string = "skynetblacklist"

	// persistLinkSize is the size of a persistLink in the blacklist
	persistLinkSize int64 = 33

	// versionSize is the number of bytes set aside for the version on disk
	versionSize int64 = 16
)

// persistLink is the information about the link that is persisted on disk
type persistLink struct {
	MerkleRoot  crypto.Hash `json:"merkleroot"`
	Blacklisted bool        `json:"blacklisted"`
}

// unmarshalLength reads the first lengthSize bytes of the file and tries to
// unmarshal them into the length
func unmarshalLength(f *os.File) (int64, error) {
	_, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return 0, err
	}
	lengthbytes := make([]byte, lengthSize)
	_, err = f.Read(lengthbytes)
	if err != nil {
		return 0, err
	}
	var length int64
	err = encoding.Unmarshal(lengthbytes, &length)
	return length, err
}

// unmarshalPersistLinks unmarshals a sia encoded persistLinks
func unmarshalPersistLinks(raw []byte) (persistLinks []persistLink, err error) {
	// Create the buffer.
	r := bytes.NewBuffer(raw)
	// Unmarshal the keys one by one until EOF or a different error occur.
	for {
		var pl persistLink
		if err = pl.unmarshalSia(r); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		persistLinks = append(persistLinks, pl)
	}
	return persistLinks, nil
}

// marshalSia implements the encoding.SiaMarshaler interface.
func (pl persistLink) marshalSia(w io.Writer) error {
	e := encoding.NewEncoder(w)
	e.Encode(pl.MerkleRoot)
	e.WriteBool(pl.Blacklisted)
	return e.Err()
}

// unmarshalSia implements the encoding.SiaUnmarshaler interface.
func (pl *persistLink) unmarshalSia(r io.Reader) error {
	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	d.Decode(&pl.MerkleRoot)
	pl.Blacklisted = d.NextBool()
	return d.Err()
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

	// Marshal the metadata. By marshalling the metadataPageSize as the first
	// element we can then quickly encode and decode the length without having
	// to worry about the header or the version
	metadataBytes := encoding.MarshalAll(metadataPageSize, metadataHeader, metadataVersion)
	_, err = f.Write(metadataBytes)
	if err != nil {
		return errors.AddContext(err, "unable to write metadata to file on initialization")
	}
	err = f.Sync()
	if err != nil {
		return errors.AddContext(err, "unable to fsync file")
	}
	return nil
}

// load loads the persisted blacklisted skylinks from disk
func (sb *SkynetBlacklist) load() error {
	// Open File
	f, err := os.Open(filepath.Join(sb.staticPersistDir, persistFile))
	if err != nil {
		// Intentionally don't add context to allow for IsNotExist error check
		return err
	}
	defer f.Close()

	// Unmarshall the length of the file
	length, err := unmarshalLength(f)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal length")
	}

	// Check if there are any persisted links
	goodBytes := length - metadataPageSize
	if goodBytes <= 0 {
		return nil
	}

	// Read raw bytes
	linkBytes := make([]byte, goodBytes)
	_, err = f.Seek(metadataPageSize, io.SeekStart)
	if err != nil {
		return err
	}
	_, err = f.Read(linkBytes)
	if err != nil {
		return err
	}
	// Decode persist links
	persistLinks, err := unmarshalPersistLinks(linkBytes)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal persistLinks")
	}

	// Add to Skynet Blacklist
	for _, link := range persistLinks {
		if !link.Blacklisted {
			delete(sb.merkleroots, link.MerkleRoot)
			continue
		}
		sb.merkleroots[link.MerkleRoot] = struct{}{}
	}

	return nil
}

// update updates the persistence on disk with the new additions and removals
// from the blacklist
func (sb *SkynetBlacklist) update(additions, removals []modules.Skylink) error {
	// Create buffer and encoder
	var buf bytes.Buffer
	// Create and encode the persist links
	for _, skylink := range additions {
		mr := skylink.MerkleRoot()
		// Add skylink merkleroot to map
		sb.merkleroots[mr] = struct{}{}

		// Create persistLink
		link := persistLink{
			MerkleRoot:  mr,
			Blacklisted: true,
		}

		// Encode link
		err := link.marshalSia(&buf)
		if err != nil {
			return errors.AddContext(err, "unable to encode persistLink")
		}
	}
	for _, skylink := range removals {
		mr := skylink.MerkleRoot()
		// Remove skylink merkleroot from map
		delete(sb.merkleroots, mr)

		// Create persistLink
		link := persistLink{
			MerkleRoot:  mr,
			Blacklisted: false,
		}

		// Encode link
		err := link.marshalSia(&buf)
		if err != nil {
			return errors.AddContext(err, "unable to encode persistLink")
		}
	}

	// Open file
	f, err := os.OpenFile(filepath.Join(sb.staticPersistDir, persistFile), os.O_RDWR|os.O_CREATE, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "unable to open persistence file")
	}
	defer f.Close()

	// Unmarshall the length of the file
	length, err := unmarshalLength(f)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal length")
	}

	// Append data and sync
	_, err = f.WriteAt(buf.Bytes(), length)
	if err != nil {
		return errors.AddContext(err, "unable to write bytes at offset")
	}
	err = f.Sync()
	if err != nil {
		return errors.AddContext(err, "unable to fsync file")
	}

	// Update length and sync
	length += int64(buf.Len())
	lengthBytes := encoding.Marshal(length)

	// Write to file
	_, err = f.WriteAt(lengthBytes, 0)
	if err != nil {
		return errors.AddContext(err, "unable to write length")
	}
	err = f.Sync()
	if err != nil {
		return errors.AddContext(err, "unable to fsync file")
	}
	return nil
}
