// +build !windows

package renter

import (
	"fmt"
	"sync"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

var errNothingMounted = errors.New("nothing mounted at that path")

// A fuseManager manages mounted fuse filesystems.
type fuseManager struct {
	mountPoints map[string]*fuseFS

	mu     sync.Mutex
	renter *Renter
}

// newFuseManager returns a new fuseManager.
func newFuseManager(r *Renter) renterFuseManager {
	// Create the fuseManager
	fm := &fuseManager{
		mountPoints: make(map[string]*fuseFS),
		renter:      r,
	}

	// Close the fuse manager on shutdown.
	r.tg.OnStop(func() error {
		return fm.managedCloseFuseManager()
	})

	return fm
}

// managedCloseFuseManager unmounts all currently-mounted filesystems.
func (fm *fuseManager) managedCloseFuseManager() error {
	// The concurreny here is a little bit annoying because the callto Unmount
	// will modify the fuse map. Lock needs to be released, and any iterator
	// through the map will break, meaning we have to iterate through the map
	// again after each call to unmount.
	var err error
	for {
		// Check the size of the mountpoints.
		fm.mu.Lock()
		if len(fm.mountPoints) == 0 {
			fm.mu.Unlock()
			break
		}
		// Grab one of the mounted points.
		var name string
		for k := range fm.mountPoints {
			name = k
			break
		}
		fm.mu.Unlock()

		// Attempt to unmount the the mountpoint.
		umountErr := fm.managedUnmount(name)
		if umountErr != nil && !errors.Contains(err, errNothingMounted) {
			// If managedUnmount returns an error, then there was no call to
			// r.tg.Done(). Need to call r.tg.Done() for this mount point so
			// that shutdown can complete, also need to delete it from the map
			// so this doesn't lock into an infinite loop.
			fm.mu.Lock()
			delete(fm.mountPoints, name)
			fm.mu.Unlock()
			fm.renter.tg.Done()
			fm.renter.log.Printf("Unable to unmount %s: %v", name, umountErr)
			fmt.Printf("Unable to unmount %s: %v - use `umount [mountpoint]` or `fusermount -u [mountpoint]` to unmount manually\n", name, umountErr)
		}
		if errors.Contains(err, errNothingMounted) {
			fm.renter.log.Critical("Nothing mounted at the provided mountpoint, even though something was mounted at the beginning of shutdown.", name)
		}
		err = errors.Compose(err, errors.AddContext(umountErr, "unable to unmount "+name))
	}
	return errors.AddContext(err, "error closing the fuse manager")
}

// Mount mounts the files under the specified siapath under the 'mountPoint' folder on
// the local filesystem.
func (fm *fuseManager) Mount(mountPoint string, sp modules.SiaPath, opts modules.MountOptions) (err error) {
	// Only release the Add() call if there was no error. Otherwise the Add()
	// call will be released when Unmount is called.
	err = fm.renter.tg.Add()
	if err != nil {
		return err
	}
	// Requires use of named variables as function output.
	defer func() {
		if err != nil {
			fm.renter.tg.Done()
		}
	}()

	// Enforce read-only requirements for now.
	if !opts.ReadOnly {
		return errors.New("fuse must be mounted as read-only")
	}

	// Get the mountpoint's root from the filesystem.
	rootDirNode, err := fm.renter.staticFileSystem.OpenSiaDir(sp)
	if err != nil {
		return errors.AddContext(err, "unable to open the mounted siapath in the filesystem")
	}
	// Create the fuse filesystem object.
	filesystem := &fuseFS{
		readOnly: opts.ReadOnly,

		renter: fm.renter,
	}
	// Create the root filesystem object.
	root := &fuseDirnode{
		staticDirNode:    rootDirNode,
		staticFilesystem: filesystem,
	}
	filesystem.root = root

	// Lock the fuse manager and add the new mountpoint.
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Check if the mountpoint is available.
	_, ok := fm.mountPoints[mountPoint]
	if ok {
		return errors.New("there is already a sia fuse system mounted at " + mountPoint)
	}

	// Mount the filesystem.
	server, err := fs.Mount(mountPoint, filesystem.root, &fs.Options{
		MountOptions: fuse.MountOptions{
			AllowOther: opts.AllowOther,
			// Debug: true,
		},
	})
	if err != nil {
		return errors.AddContext(err, "error calling mount")
	}
	filesystem.server = server
	fm.mountPoints[mountPoint] = filesystem

	return nil
}

// MountInfo returns the list of currently mounted fuse filesystems.
func (fm *fuseManager) MountInfo() []modules.MountInfo {
	if err := fm.renter.tg.Add(); err != nil {
		return nil
	}
	defer fm.renter.tg.Done()
	fm.mu.Lock()
	defer fm.mu.Unlock()

	infos := make([]modules.MountInfo, 0, len(fm.mountPoints))
	for mountPoint, filesystem := range fm.mountPoints {
		siaPath := filesystem.renter.staticFileSystem.DirSiaPath(filesystem.root.staticDirNode)
		infos = append(infos, modules.MountInfo{
			MountPoint: mountPoint,
			SiaPath:    siaPath,
		})
	}
	return infos
}

// managedUnmount will attempt to unmount the provided mount point. Each mounted
// directory has a 'tg.Add()' open. managedUnmount will only call 'tg.Done' if
// the unmount is successful.
func (fm *fuseManager) managedUnmount(mountPoint string) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Grab the filesystem.
	filesystem, exists := fm.mountPoints[mountPoint]
	if !exists {
		return errNothingMounted
	}

	// Unmount the filesystem.
	err := filesystem.server.Unmount()
	if err != nil {
		return errors.AddContext(err, "failed to unmount filesystem")
	}
	delete(fm.mountPoints, mountPoint)

	// Release the threadgroup hold for this mountpoint.
	fm.renter.tg.Done()

	return nil
}

// Unmount unmounts the fuse filesystem currently mounted at mountPoint.
func (fm *fuseManager) Unmount(mountPoint string) error {
	// Use a threadgroup Add to prevent this from being called after shutdown -
	// during shutdown the renter will be self-unmounting everything and
	// multiple threads attempting unmounts can cause complications.
	err := fm.renter.tg.Add()
	if err != nil {
		return errors.AddContext(err, "unable to unmount")
	}
	defer fm.renter.tg.Done()
	return fm.managedUnmount(mountPoint)
}
