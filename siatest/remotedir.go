package siatest

type (
	// RemoteDir is a helper struct that represents a directory on the Sia
	// network.
	RemoteDir struct {
		siapath string
	}
)

// SiaPath returns the siapath of a remote directory.
func (rd *RemoteDir) SiaPath() string {
	return rd.siapath
}
