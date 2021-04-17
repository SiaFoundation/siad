package siatest

import (
	"go.sia.tech/siad/modules"
)

type (
	// RemoteDir is a helper struct that represents a directory on the Sia
	// network.
	RemoteDir struct {
		siapath modules.SiaPath
	}
)

// SiaPath returns the siapath of a remote directory.
func (rd *RemoteDir) SiaPath() modules.SiaPath {
	return rd.siapath
}
