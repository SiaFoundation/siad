package modules

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// backupPageSize defines the amount of data that will be written to the
	// backup file at a time
	backupPageSize = 4096

	// defaultDirDepth is the number of directories created when turning a skylink
	// into a filepath.
	defaultDirDepth = 3

	// defaultDirLength is the character length of the directory name when turning
	// a skylink into a filepath.
	defaultDirLength = 2

	// MetadataHeader defines the header for the backup file
	MetadataHeader = "Skyfile Backup\n"

	// MetadataVersion defines the version for the backup file
	MetadataVersion = "v1.5.4\n"
)

var (
	// errWrongHeader is returned if the wrong header is found in a backup file
	errWrongHeader = errors.New("wrong header")

	// errWrongVersion is returned if the wrong version is found in a backup file
	errWrongVersion = errors.New("wrong version")
)

// SkyfileBackupHeader defines the data that goes at the head of the backup file
type SkyfileBackupHeader struct {
	// Metadata contains the persist Metadata identifying the type and version of
	// the file
	persist.Metadata

	// SkyfileLayoutBytes is the encoded layout of the skyfile being backed up so
	// the Skyfile can be properly restored
	SkyfileLayoutBytes []byte

	// SkyfileMetadataBytes is the marshaled metadata of the skyfile being backed
	// up so the Skyfile can be properly restored
	SkyfileMetadataBytes []byte
}

// BackupSkylink backs up a skylink by writing the reader data to disk
//
// NOTE: This action is not intended to be ACID. Additionally since the file on
// disk that is created has a filepath derived from the skylink, running
// BackupSkylink on the same skylink with the same backupDir will overwrite the
// original backup file.
func BackupSkylink(skylink, backupDir string, reader io.Reader, sl SkyfileLayout, sm SkyfileMetadata) (_ string, err error) {
	// Create the path for the backup file
	path := SkylinkToSysPath(skylink)
	fullPath := filepath.Join(backupDir, path)

	// Create the directory on disk
	err = os.MkdirAll(filepath.Dir(fullPath), DefaultDirPerm)
	if err != nil {
		return "", errors.AddContext(err, "unable to create backup directory")
	}

	// Create the backup file on disk
	file, err := os.Create(fullPath)
	if err != nil {
		return "", errors.AddContext(err, "unable to create backup file")
	}
	defer func() {
		err = errors.Compose(err, file.Close())
	}()

	// Write the header
	err = writeBackupHeader(file, sl, sm)
	if err != nil {
		return "", errors.AddContext(err, "unable to write header to disk")
	}

	// Write the body of the skyfile to disk
	err = writeBackupBody(file, reader)
	if err != nil {
		return "", errors.AddContext(err, "unable to write skyfile data to disk")
	}
	return fullPath, nil
}

// RestoreSkylink restores a skylink from a backed up file by returning the
// Skyfile Metadata, original skylink, and a reader to the skyfile data
func RestoreSkylink(backupPath string) (string, io.Reader, SkyfileLayout, SkyfileMetadata, error) {
	// Clean up backup path
	backupPath = filepath.Clean(backupPath)

	// Derive the Skylink and relative path to the backup file
	skylinkStr := SkylinkFromSysPath(backupPath)

	// Open the file
	f, err := os.Open(backupPath)
	if err != nil {
		return "", nil, SkyfileLayout{}, SkyfileMetadata{}, errors.AddContext(err, "unable to open backup file")
	}

	// Read the header
	sl, sm, err := readBackupHeader(f)
	if err != nil {
		return "", nil, SkyfileLayout{}, SkyfileMetadata{}, errors.AddContext(err, "unable to read header")
	}

	// Return information needs to restore the Skyfile by re-uploading
	return skylinkStr, f, sl, sm, nil
}

// SkylinkFromSysPath returns a skylink string from a system path
func SkylinkFromSysPath(path string) string {
	// Sanitize the path
	path = filepath.Clean(path)
	path = filepath.ToSlash(path)
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")

	// Recreate the skylink by joining the split elements in reverse order. This
	// is so we can handle absolute paths and relative paths
	els := strings.Split(path, "/")
	skylinkStr := ""
	for i := len(els); i >= len(els)-defaultDirDepth; i-- {
		skylinkStr = els[i-1] + skylinkStr
	}
	return skylinkStr
}

// SkylinkToSysPath takes the string of a skylink and turns it into a filepath
// that has defaultDirDepth number of directories that have names which have
// defaultDirLength characters
func SkylinkToSysPath(skylinkStr string) string {
	str := skylinkStr[:defaultDirLength]
	for i := 1; i < defaultDirDepth; i++ {
		str = filepath.Join(str, skylinkStr[defaultDirLength*i:defaultDirLength*(i+1)])
	}
	str = filepath.Join(str, skylinkStr[defaultDirLength*defaultDirDepth:])
	return str
}

// readBackupHeader reads the header from the backup file and returns the
// Skyfile Metadata
func readBackupHeader(f *os.File) (sl SkyfileLayout, sm SkyfileMetadata, err error) {
	// Read the header from disk
	headerBytes := make([]byte, backupPageSize)
	_, err = f.Read(headerBytes)
	if err != nil {
		err = errors.AddContext(err, "header read error")
		return
	}

	// Unmarshal the Header
	var sbh SkyfileBackupHeader
	err = encoding.Unmarshal(headerBytes, &sbh)
	if err != nil {
		err = errors.AddContext(err, "unable to unmarshal header")
		return
	}

	// Header and Version Check
	if sbh.Header != MetadataHeader {
		err = errWrongHeader
		return
	}
	if sbh.Version != MetadataVersion {
		err = errWrongVersion
		return
	}

	// Decode the Layout
	sl.Decode(sbh.SkyfileLayoutBytes)

	// Unmarshal the SkyfileMetadata
	err = json.Unmarshal(sbh.SkyfileMetadataBytes, &sm)
	if err != nil {
		err = errors.AddContext(err, "unable to unmarshal Skyfile Metadata")
		return
	}
	return
}

// writeBackupBody writes the contents of the reader to the backup file
func writeBackupBody(f *os.File, reader io.Reader) error {
	// Make a buffer to write to disk in chunks
	buf := make([]byte, backupPageSize)
	nextWriteOffset := backupPageSize
	for {
		// Read a chunk
		n, err := reader.Read(buf)
		if err != nil && err != io.EOF {
			return err
		}
		if n == 0 {
			break
		}

		// Write a chunk
		_, err = f.WriteAt(buf[:n], int64(nextWriteOffset))
		if err != nil {
			return err
		}
		nextWriteOffset += n
	}
	return nil
}

// writeBackupHeader writes the header of the backup file to disk
func writeBackupHeader(f *os.File, sl SkyfileLayout, sm SkyfileMetadata) error {
	// Marshal the SkyfileMetadata
	smBytes, err := json.Marshal(sm)
	if err != nil {
		return errors.AddContext(err, "unable to marshal skyfile metadata")
	}

	// Encoding the header information
	encodedHeader := encoding.Marshal(SkyfileBackupHeader{
		Metadata: persist.Metadata{
			Header:  MetadataHeader,
			Version: MetadataVersion,
		},
		SkyfileLayoutBytes:   sl.Encode(),
		SkyfileMetadataBytes: smBytes,
	})
	if len(encodedHeader) > backupPageSize {
		return errors.New("encoded header is too large")
	}

	// Write the data to disk
	_, err = f.WriteAt(encodedHeader, 0)
	if err != nil {
		return errors.AddContext(err, "header write failed")
	}
	return nil
}
