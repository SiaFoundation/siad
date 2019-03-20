package types

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

// EqualSiaPaths compares two SiaPath types for equality
//
// NOTE: each field should be checked explicitly
func EqualSiaPaths(siaPath1, siaPath2 SiaPath) bool {
	// Compare paths
	if siaPath1.Path != siaPath2.Path {
		return false
	}

	// SiaPaths are equal
	return true
}

// NewSiaPath returns a new SiaPath with the path set
func NewSiaPath(s string) (SiaPath, error) {
	return newSiaPath(s)
}

// RootDirSiaPath returns a SiaPath for the root siadir which has a blank path
func RootDirSiaPath() SiaPath {
	return SiaPath{
		Path: "",
	}
}

// newSiaPath returns a new SiaPath with the path set
func newSiaPath(s string) (SiaPath, error) {
	// Trim any leading or trailing /
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimSuffix(s, "/")
	sp := SiaPath{
		Path: s,
	}
	return sp, sp.validate()
}

// Dir returns the directory of the SiaPath
func (sp SiaPath) Dir() SiaPath {
	return SiaPath{
		Path: filepath.Dir(sp.Path),
	}
}

// DirSysPath returns the system path needed to read a directory on disk
func (sp SiaPath) DirSysPath(dir string) string {
	return filepath.Join(dir, sp.Path+"/")
}

// IsBlank indicates whether or not the SiaPath path is a blank string
func (sp SiaPath) IsBlank() bool {
	return sp.Path == ""
}

// Join joins the string to the end of the SiaPath with a "/" and returns
// the new SiaPath
func (sp SiaPath) Join(s string) (SiaPath, error) {
	return newSiaPath(sp.Path + "/" + s)
}

// LoadString sets the path of the SiaPath to the provided string
func (sp *SiaPath) LoadString(s string) {
	sp.Path = s
}

// SiaDirMetadataSysPath returns the system path needed to read the SiaDir
// metadata file from disk
func (sp SiaPath) SiaDirMetadataSysPath(dir string) string {
	return filepath.Join(dir, sp.Path+"/"+SiaDirExtension)
}

// SiaFileSysPath returns the system path needed to read the SiaFile from disk
func (sp SiaPath) SiaFileSysPath(dir string) string {
	return filepath.Join(dir, sp.Path+SiaFileExtension)
}

// ToString returns the SiaPath's path
func (sp SiaPath) ToString() string {
	return sp.Path
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
		prevElem = pathElem
	}
	return nil
}
