package skynetblacklist

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// initialLength is how big the persistence file is on initialization
	initialLength = int64(43)

	// trueLength is the length of a persisLink when blacklist is set to true
	trueLength = int64(76)

	// trueLength is the length of a persisLink when blacklist is set to false
	falseLength = int64(77)

	// filename is the name of the persist file
	filename = "skynetblacklist.json"

	// metadataHeader is the header of the metadata for the persist file
	metadataHeader = "SkyNet Blacklist Persistence"

	// metadataVersion is the version of the persistence file
	metadataVersion = "v1.3.0"
)

// persistLink is the information about the link that is persisted on disk
type persistLink struct {
	Link        string `json:"link"`
	Blacklisted bool   `json:"blacklisted"`
}

// encodeMetadata encodes the metadata of the persistence file and returns the
// Buffer containing the encoded data
func encodeMetadata(header, version string, length int64) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)
	if err := enc.Encode(header); err != nil {
		return nil, errors.AddContext(err, "unable to encode metadata header")
	}
	if err := enc.Encode(version); err != nil {
		return nil, errors.AddContext(err, "unable to encode metadata version")
	}
	if err := enc.Encode(length); err != nil {
		return nil, errors.AddContext(err, "unable to encode metadata length")
	}
	return buf, nil
}

// decodeMetada decodes the metadata of the persistence file
func decodeMetadata(f *os.File, dec *json.Decoder) (header, version string, length int64, err error) {
	if decodeErr := dec.Decode(&header); err != nil {
		err = errors.AddContext(decodeErr, "unable to read header from persisted json object file")
		return
	}
	if header != metadataHeader {
		err = errors.New("header doesn't match")
		return
	}
	if decodeErr := dec.Decode(&version); err != nil {
		err = errors.AddContext(decodeErr, "unable to read version from persisted json object file")
		return
	}
	if version != metadataVersion {
		err = errors.New("version doesn't match")
		return
	}
	if decodeErr := dec.Decode(&length); err != nil {
		err = errors.AddContext(decodeErr, "unable to read length from persisted json object file")
		return
	}
	return
}

// writeAtAndSync writes at an offset and fsyncs the file
func writeAtAndSync(f *os.File, bytes []byte, offset int64) error {
	n, err := f.WriteAt(bytes, offset)
	if err != nil {
		return errors.AddContext(err, "unable to write bytes at offset")
	}
	if n != len(bytes) {
		return errors.New("number of bytes written doesn't equal length of bytes")
	}
	err = f.Sync()
	if err != nil {
		return errors.AddContext(err, "unable to fsync file")
	}
	return nil
}

// initPersist initializes the persistence of the SkynetBlacklist
func (sb *SkynetBlacklist) initPersist() error {
	// Initialize the persistence directory
	err := os.MkdirAll(sb.staticPersistDir, 0700)
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
	f, err := os.OpenFile(filepath.Join(sb.staticPersistDir, filename), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return errors.AddContext(err, "unable to open persistence file")
	}
	defer f.Close()

	// Encode the initial metadata
	buf, err := encodeMetadata(metadataHeader, metadataVersion, initialLength)
	if err != nil {
		return errors.AddContext(err, "unable to encode metadata")
	}

	// Marshal into bytes
	bytes := buf.Bytes()

	// Write to file
	err = writeAtAndSync(f, bytes, 0)
	if err != nil {
		return errors.AddContext(err, "unable to write and sync metadata")
	}

	return nil
}

// load loads the persisted blacklisted skylinks from disk
func (sb *SkynetBlacklist) load() error {
	// Open File
	f, err := os.Open(filepath.Join(sb.staticPersistDir, filename))
	if err != nil {
		// Intentionally don't add context to allow for IsNotExist error check
		return err
	}
	defer f.Close()

	// Decode the metadata to find the length of the file
	dec := json.NewDecoder(f)
	_, _, length, err := decodeMetadata(f, dec)
	if err != nil {
		return errors.AddContext(err, "unable to decode metadata")
	}

	// Decode persist links until the end of the file is reached or bytesRead
	// has reached the length listed in the metadata
	bytesRead := initialLength
	for bytesRead < length {
		// Decode link
		var link persistLink
		err = dec.Decode(&link)
		if err != nil {
			break
		}

		// Load skylink
		var skylink modules.Skylink
		err := skylink.LoadString(link.Link)
		if err != nil {
			return errors.AddContext(err, "unable to load skylink from string")
		}

		// Remove if not Blacklisted
		if !link.Blacklisted {
			bytesRead += falseLength
			delete(sb.skylinks, skylink)
			continue
		}

		// Add if Blacklisted
		bytesRead += trueLength
		sb.skylinks[skylink] = struct{}{}
	}

	// Ignore end of file errors
	if err.Error() != io.EOF.Error() {
		return errors.AddContext(err, "unable to decode persistLinks")
	}
	return nil
}

// update updates the persistence on disk with the new additions and removals
// from the blacklist
func (sb *SkynetBlacklist) update(additions, removals []modules.Skylink) error {
	// Create buffer and encoder
	buf := new(bytes.Buffer)
	enc := json.NewEncoder(buf)

	// Create and encode the persist links
	for _, skylink := range additions {
		// Add to skylink map
		sb.skylinks[skylink] = struct{}{}

		// Create persistLink
		link := persistLink{
			Link:        skylink.String(),
			Blacklisted: true,
		}

		// Encode link
		err := enc.Encode(link)
		if err != nil {
			return errors.AddContext(err, "unable to encode persistLink")
		}
	}
	for _, skylink := range removals {
		// Remove from skylink map
		delete(sb.skylinks, skylink)

		// Create persistLink
		link := persistLink{
			Link:        skylink.String(),
			Blacklisted: false,
		}

		// Encode link
		err := enc.Encode(link)
		if err != nil {
			return errors.AddContext(err, "unable to encode persistLink")
		}
	}

	// Open file
	f, err := os.OpenFile(filepath.Join(sb.staticPersistDir, filename), os.O_RDWR|os.O_CREATE, 0755)
	if err != nil {
		return errors.AddContext(err, "unable to open persistence file")
	}
	defer f.Close()

	// Decode the length
	dec := json.NewDecoder(f)
	header, version, length, err := decodeMetadata(f, dec)
	if err != nil {
		return errors.AddContext(err, "unable to decode metadata")
	}

	// Append data and sync
	offset := length + 1
	err = writeAtAndSync(f, buf.Bytes(), offset)
	if err != nil {
		return errors.AddContext(err, "unable to write and sync persisted bytes")
	}

	// Update length and sync
	length += int64(buf.Len())
	metadataBuf, err := encodeMetadata(header, version, length)
	if err != nil {
		return errors.AddContext(err, "unable to encode metadata")
	}

	// Marshal into bytes
	bytes := metadataBuf.Bytes()

	// Write to file
	err = writeAtAndSync(f, bytes, 0)
	if err != nil {
		return errors.AddContext(err, "unable to write and sync metadata")
	}

	return nil
}
