package siatest

import (
	"strings"
)

type (
	// RemoteDir is a helper struct that represents a directory on the Sia
	// network.
	RemoteDir struct {
		path string
	}
)

// Path returns the path of a remote directory.
func (rd *RemoteDir) Path() string {
	return rd.path
}

// RemoteDirSiaPath returns the siaPath of a remote directory.
func (tn *TestNode) RemoteDirSiaPath(rd *RemoteDir) string {
	return strings.TrimPrefix(rd.path, tn.RenterDir())
}
