package modules

import (
	"errors"
	"path/filepath"
	"strings"
)

// siapath.go contains the types and methods for creating and manipulating
// siapaths. Any methods such as filepath.Join should be implemented here for
// the SiaPath type to ensure consistent handling across OS.

var (
	// ErrEmptySiaPath is an error when SiaPath is empty
	ErrEmptySiaPath = errors.New("SiaPath must be a nonempty string")

	// SiaDirExtension is the extension for siadir metadata files on disk
	SiaDirExtension = ".siadir"

	// SiaFileExtension is the extension for siafiles on disk
	SiaFileExtension = ".sia"
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

// RootSiaPath returns a SiaPath for the root siadir which has a blank path
func RootSiaPath() SiaPath {
	return SiaPath{}
}

// newSiaPath returns a new SiaPath with the path set
func newSiaPath(s string) (SiaPath, error) {
	// Remove any OS specific path delimiters and make them slashes
	s = filepath.ToSlash(s)
	// Trim any leading or trailing /
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, "/")
	sp := SiaPath{
		Path: s,
	}
	return sp, sp.validate()
}

// Dir returns the directory of the SiaPath
func (sp SiaPath) Dir() (SiaPath, error) {
	str := filepath.Dir(sp.Path)
	if str == "." {
		return RootSiaPath(), nil
	}
	return newSiaPath(str)
}

// Equals compares two SiaPath types for equality
func (sp SiaPath) Equals(siaPath SiaPath) bool {
	return sp.Path == siaPath.Path
}

// IsRoot indicates whether or not the SiaPath path is a root directory siapath
func (sp SiaPath) IsRoot() bool {
	return sp.Path == ""
}

// Join joins the string to the end of the SiaPath with a "/" and returns
// the new SiaPath
func (sp SiaPath) Join(s string) (SiaPath, error) {
	return newSiaPath(sp.Path + "/" + filepath.ToSlash(s))
}

// LoadString sets the path of the SiaPath to the provided string
func (sp *SiaPath) LoadString(s string) error {
	sp.Path = s
	return sp.validate()
}

// SiaDirSysPath returns the system path needed to read a directory on disk, the
// input dir is the root siadir directory on disk
func (sp SiaPath) SiaDirSysPath(dir string) string {
	return filepath.Join(dir, sp.sysPath(), "")
}

// SiaDirMetadataSysPath returns the system path needed to read the SiaDir
// metadata file from disk, the input dir is the root siadir directory on disk
func (sp SiaPath) SiaDirMetadataSysPath(dir string) string {
	return filepath.Join(dir, sp.sysPath(), SiaDirExtension)
}

// SiaFileSysPath returns the system path needed to read the SiaFile from disk,
// the input dir is the root siafile directory on disk
func (sp SiaPath) SiaFileSysPath(dir string) string {
	return filepath.Join(dir, sp.sysPath()+SiaFileExtension)
}

// String returns the SiaPath's path
func (sp SiaPath) String() string {
	return sp.Path
}

// sysPath returns to the SiaPath's path joined with OS delimiters
func (sp SiaPath) sysPath() string {
	return filepath.Join(strings.Split(sp.Path, "/")...)
}

// validate checks that a Siapath is a legal filename. ../ is disallowed to
// prevent directory traversal, and paths must not begin with / or be empty.
func (sp SiaPath) validate() error {
	if sp.Path == "" {
		return ErrEmptySiaPath
	}
	if sp.Path == ".." {
		return errors.New("siapath cannot be '..'")
	}
	if sp.Path == "." {
		return errors.New("siapath cannot be '.'")
	}
	// check prefix
	if strings.HasPrefix(sp.Path, "/") {
		return errors.New("siapath cannot begin with /")
	}
	if strings.HasPrefix(sp.Path, "../") {
		return errors.New("siapath cannot begin with ../")
	}
	if strings.HasPrefix(sp.Path, "./") {
		return errors.New("siapath connot begin with ./")
	}
	var prevElem string
	for _, pathElem := range strings.Split(sp.Path, "/") {
		if pathElem == "." || pathElem == ".." {
			return errors.New("siapath cannot contain . or .. elements")
		}
		if prevElem != "" && pathElem == "" {
			return ErrEmptySiaPath
		}
		if prevElem == "/" || pathElem == "/" {
			return errors.New("siapath cannot contain //")
		}
		prevElem = pathElem
	}
	return nil
}
