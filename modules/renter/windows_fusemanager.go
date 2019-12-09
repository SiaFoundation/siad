// +build windows

package renter

import (
	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/modules"
)

var errNoFuseOnWindows = errors.New("Fuse library is incompatible with Windows")

// dummyFuseManager implements the renterFuseManager interface.
type dummyFuseManager struct {
}

// Mount always returns an error since mounting a FUSE filesystem is not
// possible.
func (dm dummyFuseManager) Mount(mountPoint string, sp modules.SiaPath, opts modules.MountOptions) (err error) {
	return errNoFuseOnWindows
}

// MountInfo returns the list of currently mounted fuse filesystems which is
// always empty on Windows systems.
func (dm dummyFuseManager) MountInfo() []modules.MountInfo { return nil }

// Unmount always returns an error since mounting is not possible.
func (dm dummyFuseManager) Unmount(mountPoint string) error { return errNoFuseOnWindows }

// newFuseManager return a dummyFuseManager.
func newFuseManager(r *Renter) renterFuseManager {
	return dummyFuseManager{}
}
