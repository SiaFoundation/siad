package modules

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/NebulousLabs/Sia/skykey"
)

const (
	// SkyfileDefaultPathParamName specifies the name of the form parameter that
	// holds the default path.
	SkyfileDefaultPathParamName = "defaultpath"
)

// SkyfileMetadata is all of the metadata that gets placed into the first 4096
// bytes of the skyfile, and is used to set the metadata of the file when
// writing back to disk. The data is json-encoded when it is placed into the
// leading bytes of the skyfile, meaning that this struct can be extended
// without breaking compatibility.
//
// NOTE: This type has a custom JSON unmarshaller which handles the special
// cases for `defaultpath`.
type SkyfileMetadata struct {
	Mode     os.FileMode     `json:"mode,omitempty"`
	Filename string          `json:"filename,omitempty"`
	Subfiles SkyfileSubfiles `json:"subfiles,omitempty"`

	// DefaultPath indicates what content to serve if the user has not specified
	// a path, and the user is not trying to download the Skylink as an archive.
	// It defaults to 'index.html' on upload if not specified and if a file with
	// that name is present in the upload.
	//
	// NOTE: do not `omitempty`, if we were to omit empty default paths, this
	// metadata would be treated as a legacy metadata JSON. Setting the default
	// path to an empty string means an explicit do-not-default.
	DefaultPath string `json:"defaultpath"`
}

// skyfileMetadataJSON is a helper type
type skyfileMetadataJSON SkyfileMetadata

// SkyfileSubfiles contains the subfiles of a skyfile, indexed by their
// filename.
type SkyfileSubfiles map[string]SkyfileSubfileMetadata

// ForPath returns a subset of the SkyfileMetadata that contains all of the
// subfiles for the given path. The path can lead to both a directory or a file.
// Note that this method will return the subfiles with offsets relative to the
// given path, so if a directory is requested, the subfiles in that directory
// will start at offset 0, relative to the path.
func (sm SkyfileMetadata) ForPath(path string) (SkyfileMetadata, bool, uint64, uint64) {
	// All paths must be absolute.
	path = EnsurePrefix(path, "/")
	metadata := SkyfileMetadata{
		Filename:    path,
		Subfiles:    make(SkyfileSubfiles),
		DefaultPath: sm.DefaultPath,
	}

	// Try to find an exact match
	var isFile bool
	for _, sf := range sm.Subfiles {
		if EnsurePrefix(sf.Filename, "/") == path {
			isFile = true
			metadata.Subfiles[sf.Filename] = sf
			break
		}
	}

	// If there is no exact match look for directories.
	if len(metadata.Subfiles) == 0 {
		for _, sf := range sm.Subfiles {
			if strings.HasPrefix(EnsurePrefix(sf.Filename, "/"), path) {
				metadata.Subfiles[sf.Filename] = sf
			}
		}
	}
	offset := metadata.offset()
	if offset > 0 {
		for _, sf := range metadata.Subfiles {
			sf.Offset -= offset
			metadata.Subfiles[sf.Filename] = sf
		}
	}
	return metadata, isFile, offset, metadata.size()
}

// ContentType returns the Content Type of the data. We only return a
// content-type if it has exactly one subfile. As that is the only case where we
// can be sure of it.
func (sm SkyfileMetadata) ContentType() string {
	if len(sm.Subfiles) == 1 {
		for _, sf := range sm.Subfiles {
			return sf.ContentType
		}
	}
	return ""
}

// IsDirectory returns true if the SkyfileMetadata represents a directory.
func (sm SkyfileMetadata) IsDirectory() bool {
	if len(sm.Subfiles) > 1 {
		return true
	}
	if len(sm.Subfiles) == 1 {
		var name string
		for _, sf := range sm.Subfiles {
			name = sf.Filename
			break
		}
		if sm.Filename != name {
			return true
		}
	}
	return false
}

// UnmarshalJSON unmarshals a byte slice into a SkyfileMetadata while handling
// some special cases, e.g. DefaultPath field being empty vs not set.
func (sm *SkyfileMetadata) UnmarshalJSON(b []byte) error {
	var m skyfileMetadataJSON
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	sm.Mode = m.Mode
	sm.Filename = m.Filename
	sm.Subfiles = m.Subfiles
	sm.DefaultPath = m.DefaultPath

	// Handle the special case of `defaultpath` not being set.
	if !strings.Contains(string(b), "\"defaultpath\":\"") {
		// If this is a single file we want to set the default path to the only
		// file in order to support the legacy case in which this field is
		// missing. We only need to do that for multipart file, i.e. files that
		// have subfiles.
		if len(sm.Subfiles) == 1 {
			for filename := range sm.Subfiles {
				sm.DefaultPath = EnsurePrefix(filename, "/")
				break
			}
		}
	}
	return nil
}

// size returns the total size, which is the sum of the length of all subfiles.
func (sm SkyfileMetadata) size() uint64 {
	var total uint64
	for _, sf := range sm.Subfiles {
		total += sf.Len
	}
	return total
}

// offset returns the offset of the subfile with the smallest offset.
func (sm SkyfileMetadata) offset() uint64 {
	if len(sm.Subfiles) == 0 {
		return 0
	}
	var min uint64 = math.MaxUint64
	for _, sf := range sm.Subfiles {
		if sf.Offset < min {
			min = sf.Offset
		}
	}
	return min
}

// SkyfileSubfileMetadata is all of the metadata that belongs to a subfile in a
// skyfile. Most importantly it contains the offset at which the subfile is
// written and its length. Its filename can potentially include a '/' character
// as nested files and directories are allowed within a single Skyfile, but it
// is not allowed to contain ./, ../, be empty, or start with a forward slash.
type SkyfileSubfileMetadata struct {
	FileMode    os.FileMode `json:"mode,omitempty,siamismatch"` // different json name for compat reasons
	Filename    string      `json:"filename,omitempty"`
	ContentType string      `json:"contenttype,omitempty"`
	Offset      uint64      `json:"offset,omitempty"`
	Len         uint64      `json:"len,omitempty"`
}

// IsDir implements the os.FileInfo interface for SkyfileSubfileMetadata.
func (sm SkyfileSubfileMetadata) IsDir() bool {
	return false
}

// Mode implements the os.FileInfo interface for SkyfileSubfileMetadata.
func (sm SkyfileSubfileMetadata) Mode() os.FileMode {
	return sm.FileMode
}

// ModTime implements the os.FileInfo interface for SkyfileSubfileMetadata.
func (sm SkyfileSubfileMetadata) ModTime() time.Time {
	return time.Time{} // no modtime available
}

// Name implements the os.FileInfo interface for SkyfileSubfileMetadata.
func (sm SkyfileSubfileMetadata) Name() string {
	return filepath.Base(sm.Filename)
}

// Size implements the os.FileInfo interface for SkyfileSubfileMetadata.
func (sm SkyfileSubfileMetadata) Size() int64 {
	return int64(sm.Len)
}

// Sys implements the os.FileInfo interface for SkyfileSubfileMetadata.
func (sm SkyfileSubfileMetadata) Sys() interface{} {
	return nil
}

// SkyfileFormat is the file format the API uses to return a Skyfile as.
type SkyfileFormat string

var (
	// SkyfileFormatNotSpecified is the default format for the endpoint when the
	// format isn't specified explicitly.
	SkyfileFormatNotSpecified = SkyfileFormat("")
	// SkyfileFormatConcat returns the skyfiles in a concatenated manner.
	SkyfileFormatConcat = SkyfileFormat("concat")
	// SkyfileFormatTar returns the skyfiles as a .tar.
	SkyfileFormatTar = SkyfileFormat("tar")
	// SkyfileFormatTarGz returns the skyfiles as a .tar.gz.
	SkyfileFormatTarGz = SkyfileFormat("targz")
	// SkyfileFormatZip returns the skyfiles as a .zip.
	SkyfileFormatZip = SkyfileFormat("zip")
)

// SkyfileUploadParameters establishes the parameters such as the intra-root
// erasure coding.
type SkyfileUploadParameters struct {
	// SiaPath defines the siapath that the skyfile is going to be uploaded to.
	// Recommended that the skyfile is placed in /var/skynet
	SiaPath SiaPath `json:"siapath"`

	// DryRun allows to retrieve the skylink without actually uploading the file
	// to the Sia network.
	DryRun bool `json:"dryrun"`

	// Force determines whether the upload should overwrite an existing siafile
	// at 'SiaPath'. If set to false, an error will be returned if there is
	// already a file or folder at 'SiaPath'. If set to true, any existing file
	// or folder at 'SiaPath' will be deleted and overwritten.
	Force bool `json:"force"`

	// Root determines whether the upload should treat the filepath as a path
	// from system root, or if the path should be from /var/skynet.
	Root bool `json:"root"`

	// The base chunk is always uploaded with a 1-of-N erasure coding setting,
	// meaning that only the redundancy needs to be configured by the user.
	BaseChunkRedundancy uint8 `json:"basechunkredundancy"`

	// This metadata will be included in the base chunk, meaning that this
	// metadata is visible to the downloader before any of the file data is
	// visible.
	FileMetadata SkyfileMetadata `json:"filemetadata"`

	// Reader supplies the file data for the skyfile.
	Reader io.Reader `json:"reader"`

	// SkykeyName is the name of the Skykey that should be used to encrypt the
	// Skyfile.
	SkykeyName string `json:"skykeyname"`

	// SkykeyID is the ID of Skykey that should be used to encrypt the file.
	SkykeyID skykey.SkykeyID `json:"skykeyid"`

	// If Encrypt is set to true and one of SkykeyName or SkykeyID was set, a
	// Skykey will be derived from the Master Skykey found under that name/ID to
	// be used for this specific upload.
	FileSpecificSkykey skykey.Skykey
}

// SkyfileMultipartUploadParameters defines the parameters specific to multipart
// uploads. See SkyfileUploadParameters for a detailed description of the
// fields.
type SkyfileMultipartUploadParameters struct {
	SiaPath             SiaPath   `json:"siapath"`
	Force               bool      `json:"force"`
	Root                bool      `json:"root"`
	BaseChunkRedundancy uint8     `json:"basechunkredundancy"`
	Reader              io.Reader `json:"reader"`

	// Filename indicates the filename of the skyfile.
	Filename string `json:"filename"`

	// DefaultPath indicates the default file to be opened when opening skyfiles
	// that contain directories. If set to empty string no file will be opened
	// by default.
	DefaultPath *string `json:"defaultpath,omitempty"`

	// ContentType indicates the media type of the data supplied by the reader.
	ContentType string `json:"contenttype"`
}

// SkyfilePinParameters defines the parameters specific to pinning a skylink.
// See SkyfileUploadParameters for a detailed description of the fields.
type SkyfilePinParameters struct {
	SiaPath             SiaPath `json:"siapath"`
	Force               bool    `json:"force"`
	Root                bool    `json:"root"`
	BaseChunkRedundancy uint8   `json:"basechunkredundancy"`
}

// SkynetPortal contains information identifying a Skynet portal.
type SkynetPortal struct {
	Address NetAddress `json:"address"` // the IP or domain name of the portal. Must be a valid network address
	Public  bool       `json:"public"`  // indicates whether the portal can be accessed publicly or not
}

// EnsurePrefix checks if `str` starts with `prefix` and adds it if that's not
// the case.
func EnsurePrefix(str, prefix string) string {
	if strings.HasPrefix(str, prefix) {
		return str
	}
	return fmt.Sprintf("%s%s", prefix, str)
}

// EnsureSuffix checks if `str` ends with `suffix` and adds it if that's not
// the case.
func EnsureSuffix(str, suffix string) string {
	if strings.HasSuffix(str, suffix) {
		return str
	}
	return fmt.Sprintf("%s%s", str, suffix)
}
