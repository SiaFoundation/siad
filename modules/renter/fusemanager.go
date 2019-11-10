package renter

// TODO: fusemanager is its own subsystem, needs to go in the readme as such.
// Need to document in the readme that Mounted directories hold a threadgroup
// until they are unmounted. Calling Stop() on the renter tg will cause all of

// TODO: Currently shutdown will fail silently if someone still has resources
// that are looking at the fuse directory. Ideally, siad would refuse to
// shutdown until all fuse resources are cleared up, that or we have some way to
// force an unmount.

import (
	"sync"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// A fuseManager manages mounted fuse filesystems.
type fuseManager struct {
	mountPoints map[string]*fuseFS

	mu     sync.Mutex
	renter *Renter
}

// newFuseManager returns a new fuseManager.
func newFuseManager(r *Renter) *fuseManager {
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
		umountErr := fm.Unmount(name)
		if umountErr != nil {
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

	// Create the fuse filesystem object.
	filesystem := &fuseFS{
		// Start the counter at 2 because 0 and 1 are reserved values.

		// TODO: This can one day be deleted.
		inoCounter: 2,
		inoMap:     make(map[string]uint64),

		readOnly: opts.ReadOnly,

		renter: fm.renter,
	}
	// Create the root filesystem object.
	root := &fuseDirnode{
		filesystem: filesystem,
		siapath:    sp,

		// TODO: This will one day need to be fetched using the root inode for
		// the filesystem, instead of having our own counter.
		ino: filesystem.inoCounter,
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
		infos = append(infos, modules.MountInfo{
			MountPoint: mountPoint,
			SiaPath:    filesystem.root.siapath,
		})
	}
	return infos
}

// Unmount unmounts the fuse filesystem currently mounted at mountPoint.
func (fm *fuseManager) Unmount(mountPoint string) error {
	fm.mu.Lock()
	defer fm.mu.Unlock()

	// Grab the filesystem.
	filesystem, exists := fm.mountPoints[mountPoint]
	if !exists {
		return errors.New("nothing mounted at that path")
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
