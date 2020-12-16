package modules

// The Skynet Backup subsystem handles persistence for creating and reading
// skynet backup data. These backups contain all the information needed to
// restore a Skyfile with the original Skylink.

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// backupHeaderSize defines the size of the encoded backup header
	backupHeaderSize = 4196

	// defaultDirDepth is the number of directories created when turning a skylink
	// into a filepath.
	defaultDirDepth = 3

	// defaultDirLength is the character length of the directory name when turning
	// a skylink into a filepath.
	defaultDirLength = 2

	// MetadataHeader defines the header for the backup file
	MetadataHeader = "Skyfile Backup\n"

	// MetadataVersion defines the version for the backup file
	MetadataVersion = "v1.5.5\n"
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

	// BaseSector is the encoded baseSector for the backed up skyfile
	BaseSector []byte

	// Skylink is the skylink for the backed up skyfile
	Skylink string
}

// BackupSkylink backs up a skylink by writing skylink and baseSector to
// a header and then writing the header and the reader data to the writer.
func BackupSkylink(skylink string, baseSector []byte, reader io.Reader, writer io.Writer) error {
	// Write the header
	err := writeBackupHeader(writer, skylink, baseSector)
	if err != nil {
		return errors.AddContext(err, "unable to write header")
	}

	// Write the body of the skyfile
	err = writeBackupBody(writer, reader)
	return errors.AddContext(err, "unable to write skyfile data to the writer")
}

// RestoreSkylink restores a skylink by returning the Skylink and the baseSector
// from the reader.
func RestoreSkylink(r io.Reader) (string, []byte, error) {
	// Read the header
	skylink, baseSector, err := readBackupHeader(r)
	if err != nil {
		return "", nil, errors.AddContext(err, "unable to read header")
	}

	// Return information needs to restore the Skyfile by re-uploading
	return skylink, baseSector, nil
}

// SkylinkFromSysPath returns a skylink string from a system path
func SkylinkFromSysPath(path string) string {
	// Sanitize the path
	path = filepath.Clean(path)
	path = filepath.ToSlash(path)
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")

	// Recreate the skylink by joining the last defaultDirDepth + 1 elements
	els := strings.Split(path, "/")
	start := len(els) - defaultDirDepth - 1
	return strings.Join(els[start:], "")
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
func readBackupHeader(r io.Reader) (string, []byte, error) {
	// Read the header
	headerBytes := make([]byte, backupHeaderSize)
	_, err := io.ReadFull(r, headerBytes)
	if err != nil {
		return "", nil, errors.AddContext(err, "header read error")
	}

	// Unmarshal the Header
	var sbh SkyfileBackupHeader
	err = encoding.Unmarshal(headerBytes, &sbh)
	if err != nil {
		return "", nil, errors.AddContext(err, "unable to unmarshal header")
	}

	// Header and Version Check
	if sbh.Header != MetadataHeader {
		return "", nil, errWrongHeader
	}
	if sbh.Version != MetadataVersion {
		return "", nil, errWrongVersion
	}

	return sbh.Skylink, sbh.BaseSector, nil
}

// writeBackupBody writes the contents of the reader to the backup file
func writeBackupBody(w io.Writer, reader io.Reader) error {
	_, err := io.Copy(w, reader)
	return errors.AddContext(err, "unable to copy data from reader to file")
}

// writeBackupHeader writes the header of the backup file
func writeBackupHeader(w io.Writer, skylink string, baseSector []byte) error {
	// Encoding the header information
	encodedHeader := encoding.Marshal(SkyfileBackupHeader{
		Metadata: persist.Metadata{
			Header:  MetadataHeader,
			Version: MetadataVersion,
		},
		BaseSector: baseSector,
		Skylink:    skylink,
	})
	if len(encodedHeader) > backupHeaderSize {
		return fmt.Errorf("encoded header has length of %v; max length is %v", len(encodedHeader), backupHeaderSize)
	}

	// Create a reader for the encoded Header and copy it to the writer
	headerReader := bytes.NewReader(encodedHeader)
	_, err := io.Copy(w, headerReader)
	return errors.AddContext(err, "unable to copy header data to file")
}
