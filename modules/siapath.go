package modules

import (
	"encoding/base32"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// siapath.go contains the types and methods for creating and manipulating
// siapaths. Any methods such as filepath.Join should be implemented here for
// the SiaPath type to ensure consistent handling across OS.

var (
	// ErrEmptyPath is an error when a path is empty
	ErrEmptyPath = errors.New("path must be a nonempty string")
	// ErrInvalidSiaPath is the error for an invalid SiaPath
	ErrInvalidSiaPath = errors.New("invalid SiaPath")
	// ErrInvalidPathString is the error for an invalid path
	ErrInvalidPathString = errors.New("invalid path string")

	// SiaDirExtension is the extension for siadir metadata files on disk
	SiaDirExtension = ".siadir"

	// SiaFileExtension is the extension for siafiles on disk
	SiaFileExtension = ".sia"

	// PartialsSiaFileExtension is the extension for siafiles which contain
	// combined chunks.
	PartialsSiaFileExtension = ".csia"

	// CombinedChunkExtension is the extension for a combined chunk on disk.
	CombinedChunkExtension = ".cc"
	// UnfinishedChunkExtension is the extension for an unfinished combined chunk
	// and is appended to the file in addition to CombinedChunkExtension.
	UnfinishedChunkExtension = ".unfinished"
	// ChunkMetadataExtension is the extension of a metadata file for a combined
	// chunk.
	ChunkMetadataExtension = ".ccmd"
)

var (
	// BackupFolder is the Sia folder where all of the renter's snapshot
	// siafiles are stored by default.
	BackupFolder = NewGlobalSiaPath("/snapshots")

	// HomeFolder is the Sia folder that is used to store all of the user
	// accessible data.
	HomeFolder = NewGlobalSiaPath("/home")

	// UserFolder is the Sia folder that is used to store the renter's siafiles.
	UserFolder = NewGlobalSiaPath("/home/user")
)

type (
	// SiaPath is the struct used to uniquely identify siafiles and siadirs across
	// Sia
	SiaPath struct {
		Path string `json:"path"`
	}
)

// NewSiaPath returns a new SiaPath with the path set
func NewSiaPath(s string) (SiaPath, error) {
	return newSiaPath(s)
}

// NewGlobalSiaPath can be used to create a global var which is a SiaPath. If
// there is an error creating the SiaPath, the function will panic, making this
// function unsuitable for typical use.
func NewGlobalSiaPath(s string) SiaPath {
	sp, err := NewSiaPath(s)
	if err != nil {
		panic("error creating global siapath: " + err.Error())
	}
	return sp
}

// RandomSiaPath returns a random SiaPath created from 20 bytes of base32
// encoded entropy.
func RandomSiaPath() (sp SiaPath) {
	sp.Path = base32.StdEncoding.EncodeToString(fastrand.Bytes(20))
	sp.Path = sp.Path[:20]
	return
}

// RootSiaPath returns a SiaPath for the root siadir which has a blank path
func RootSiaPath() SiaPath {
	return SiaPath{}
}

// CombinedSiaFilePath returns the SiaPath to a hidden siafile which is used to
// store chunks that contain pieces of multiple siafiles.
func CombinedSiaFilePath(ec ErasureCoder) SiaPath {
	return SiaPath{Path: fmt.Sprintf(".%v", ec.Identifier())}
}

// clean cleans up the string by converting an OS separators to forward slashes
// and trims leading and trailing slashes
func clean(s string) string {
	s = filepath.ToSlash(s)
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, "/")
	return s
}

// newSiaPath returns a new SiaPath with the path set
func newSiaPath(s string) (SiaPath, error) {
	sp := SiaPath{
		Path: clean(s),
	}
	return sp, sp.Validate(false)
}

// AddSuffix adds a numeric suffix to the end of the SiaPath.
func (sp SiaPath) AddSuffix(suffix uint) SiaPath {
	return SiaPath{
		Path: sp.Path + fmt.Sprintf("_%v", suffix),
	}
}

// Dir returns the directory of the SiaPath
func (sp SiaPath) Dir() (SiaPath, error) {
	pathElements := strings.Split(sp.Path, "/")
	// If there is only one path element, then the Siapath was just a filename
	// and did not have a directory, return the root Siapath
	if len(pathElements) <= 1 {
		return RootSiaPath(), nil
	}
	dir := strings.Join(pathElements[:len(pathElements)-1], "/")
	// If dir is empty or a dot, return the root Siapath
	if dir == "" || dir == "." {
		return RootSiaPath(), nil
	}
	return newSiaPath(dir)
}

// Equals compares two SiaPath types for equality
func (sp SiaPath) Equals(siaPath SiaPath) bool {
	return sp.Path == siaPath.Path
}

// IsEmpty returns true if the siapath is equal to the nil value
func (sp SiaPath) IsEmpty() bool {
	return sp.Equals(SiaPath{})
}

// IsRoot indicates whether or not the SiaPath path is a root directory siapath
func (sp SiaPath) IsRoot() bool {
	return sp.Path == ""
}

// Join joins the string to the end of the SiaPath with a "/" and returns the
// new SiaPath.
func (sp SiaPath) Join(s string) (SiaPath, error) {
	cleanStr := clean(s)
	if s == "" || cleanStr == "" {
		return SiaPath{}, errors.New("cannot join an empty string to a siapath")
	}
	return newSiaPath(sp.Path + "/" + cleanStr)
}

// LoadString sets the path of the SiaPath to the provided string
func (sp *SiaPath) LoadString(s string) error {
	sp.Path = clean(s)
	return sp.Validate(false)
}

// LoadSysPath loads a SiaPath from a given system path by trimming the dir at
// the front of the path, the extension at the back and returning the remaining
// path as a SiaPath.
func (sp *SiaPath) LoadSysPath(dir, path string) error {
	if !strings.HasPrefix(path, dir) {
		return fmt.Errorf("%v is not a prefix of %v", dir, path)
	}
	path = strings.TrimSuffix(strings.TrimPrefix(path, dir), SiaFileExtension)
	return sp.LoadString(path)
}

// MarshalJSON marshals a SiaPath as a string.
func (sp SiaPath) MarshalJSON() ([]byte, error) {
	return json.Marshal(sp.String())
}

// Name returns the name of the file.
func (sp SiaPath) Name() string {
	pathElements := strings.Split(sp.Path, "/")
	name := pathElements[len(pathElements)-1]
	// If name is a dot, return the root Siapath name
	if name == "." {
		name = ""
	}
	return name
}

// Rebase changes the base of a siapath from oldBase to newBase and returns a new SiaPath.
// e.g. rebasing 'a/b/myfile' from oldBase 'a/b/' to 'a/' would result in 'a/myfile'
func (sp SiaPath) Rebase(oldBase, newBase SiaPath) (SiaPath, error) {
	if !strings.HasPrefix(sp.Path, oldBase.Path) {
		return SiaPath{}, fmt.Errorf("'%v' isn't the base of '%v'", oldBase.Path, sp.Path)
	}
	relPath := strings.TrimPrefix(sp.Path, oldBase.Path)
	if relPath == "" {
		return newBase, nil
	}
	return newBase.Join(relPath)
}

// UnmarshalJSON unmarshals a siapath into a SiaPath object.
func (sp *SiaPath) UnmarshalJSON(b []byte) error {
	if err := json.Unmarshal(b, &sp.Path); err != nil {
		return err
	}
	sp.Path = clean(sp.Path)
	return sp.Validate(true)
}

// SiaDirSysPath returns the system path needed to read a directory on disk, the
// input dir is the root siadir directory on disk
func (sp SiaPath) SiaDirSysPath(dir string) string {
	return filepath.Join(dir, filepath.FromSlash(sp.Path), "")
}

// SiaDirMetadataSysPath returns the system path needed to read the SiaDir
// metadata file from disk, the input dir is the root siadir directory on disk
func (sp SiaPath) SiaDirMetadataSysPath(dir string) string {
	return filepath.Join(dir, filepath.FromSlash(sp.Path), SiaDirExtension)
}

// SiaFileSysPath returns the system path needed to read the SiaFile from disk,
// the input dir is the root siafile directory on disk
func (sp SiaPath) SiaFileSysPath(dir string) string {
	return filepath.Join(dir, filepath.FromSlash(sp.Path)+SiaFileExtension)
}

// SiaPartialsFileSysPath returns the system path needed to read the
// PartialsSiaFile from disk, the input dir is the root siafile directory on
// disk
func (sp SiaPath) SiaPartialsFileSysPath(dir string) string {
	return filepath.Join(dir, filepath.FromSlash(sp.Path)+PartialsSiaFileExtension)
}

// String returns the SiaPath's path
func (sp SiaPath) String() string {
	return sp.Path
}

// FromSysPath creates a SiaPath from a siaFilePath and corresponding root files
// dir.
func (sp *SiaPath) FromSysPath(siaFilePath, dir string) (err error) {
	if !strings.HasPrefix(siaFilePath, dir) {
		return fmt.Errorf("SiaFilePath %v is not within dir %v", siaFilePath, dir)
	}
	relPath := strings.TrimPrefix(siaFilePath, dir)
	relPath = strings.TrimSuffix(relPath, SiaFileExtension)
	relPath = strings.TrimSuffix(relPath, PartialsSiaFileExtension)
	*sp, err = newSiaPath(relPath)
	return
}

// Validate checks that a Siapath is a legal filename.
func (sp SiaPath) Validate(isRoot bool) error {
	if err := validatePath(sp.Path, isRoot); err != nil {
		return errors.Extend(err, ErrInvalidSiaPath)
	}
	return nil
}

// ValidatePathString validates a path given a string.
func ValidatePathString(path string, isRoot bool) error {
	if err := validatePath(path, isRoot); err != nil {
		return errors.Extend(err, ErrInvalidPathString)
	}
	return nil
}

// validatePath validates a path. ../ and ./ are disallowed to prevent directory
// traversal, and paths must not begin with / or be empty.
func validatePath(path string, isRoot bool) error {
	if path == "" && !isRoot {
		return ErrEmptyPath
	}
	if path == ".." {
		return errors.New("path cannot be '..'")
	}
	if path == "." {
		return errors.New("path cannot be '.'")
	}
	// check prefix
	if strings.HasPrefix(path, "/") {
		return errors.New("path cannot begin with /")
	}
	if strings.HasPrefix(path, "../") {
		return errors.New("path cannot begin with ../")
	}
	if strings.HasPrefix(path, "./") {
		return errors.New("path connot begin with ./")
	}
	var prevElem string
	for _, pathElem := range strings.Split(path, "/") {
		if pathElem == "." || pathElem == ".." {
			return errors.New("path cannot contain . or .. elements")
		}
		if prevElem != "" && pathElem == "" {
			return ErrEmptyPath
		}
		if prevElem == "/" || pathElem == "/" {
			return errors.New("path cannot contain //")
		}
		prevElem = pathElem
	}

	// Final check for a valid utf8
	if !utf8.ValidString(path) {
		return errors.New("path is not a valid utf8 path")
	}

	return nil
}
