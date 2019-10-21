package renter

// TODO: Note in the README that the mountpoints hold a tg.Add throughout their
// life, and get unmounted upon renter shutdown.

// TODO: Need to test opening multiple mount points at once, need to test trying
// to open mount points after shutdown.

// TODO: Documentation for both nodefs and pathfs say 'Deprecated', use
// go-fuse/v2/fs instead. Heh.

import (
	"sync"

	"github.com/hanwen/go-fuse/fuse"
	"github.com/hanwen/go-fuse/fuse/nodefs"
	"github.com/hanwen/go-fuse/fuse/pathfs"
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
	// Threadgroup should prevent more interactions with the fuseManager after
	// shutdown, and this function should only be called after shutdown.
	// Grabbing the list of mountpoints and unlocking should be safe. No lock is
	// used here so that the race detector will get upset if this assumption is
	// incorrect.
	//
	// The map needs to be turned into an array because the Unmount call
	// modifies the map.
	mounts := make([]string, 0, len(fm.mountPoints))
	for name := range fm.mountPoints {
		mounts = append(mounts, name)
	}

	// Unmount any mounted fuse filesystems.
	var err error
	for _, name := range mounts {
		err = errors.Compose(err, fm.Unmount(name))
	}
	return errors.AddContext(err, "error closing the fuse manager")
}

// Mount mounts the files under the specified siapath under the 'mountPoint' folder on
// the local filesystem.
func (fm *fuseManager) Mount(mountPoint string, sp modules.SiaPath, opts modules.MountOptions) (err error) {
	// Only release the Add() call if there was no error. Otherwise the Add()
	// call will be released when Unmount is called.
	if err := fm.renter.tg.Add(); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			fm.renter.tg.Done()
		}
	}()

	fm.mu.Lock()
	_, ok := fm.mountPoints[mountPoint]
	fm.mu.Unlock()
	if ok {
		return errors.New("already mounted")
	}
	if !opts.ReadOnly {
		return errors.New("writable fuse is not supported")
	}

	fs := &fuseFS{
		FileSystem: pathfs.NewDefaultFileSystem(),
		root:       sp,
		renter:     fm.renter,
	}
	nfs := pathfs.NewPathNodeFs(fs, nil)
	// we need to call `Mount` rather than `MountRoot` because we want to define
	// the fuse mount flag `AllowOther`, which enables non-permissioned users to
	// access the fuse mount. This makes life easier in Docker.
	mountOpts := &fuse.MountOptions{
		AllowOther: true,
		// TODO: What is the MaxReadAhead value?
		MaxReadAhead: 1,
	}
	server, _, err := nodefs.Mount(mountPoint, nfs.Root(), mountOpts, nil)
	if err != nil {
		return errors.AddContext(err, "unable to mount using nodefs")
	}
	go server.Serve()
	fs.srv = server

	fm.mu.Lock()
	fm.mountPoints[mountPoint] = fs
	fm.mu.Unlock()
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

	var infos []modules.MountInfo
	for mountPoint, fs := range fm.mountPoints {
		infos = append(infos, modules.MountInfo{
			MountPoint: mountPoint,
			SiaPath:    fs.root,
		})
	}
	return infos
}

// Unmount unmounts the fuse filesystem currently mounted at mountPoint.
func (fm *fuseManager) Unmount(mountPoint string) error {
	fm.mu.Lock()
	fs, ok := fm.mountPoints[mountPoint]
	delete(fm.mountPoints, mountPoint)
	fm.mu.Unlock()
	if !ok {
		return errors.New("nothing mounted at that path")
	}

	// Release the threadgroup hold for this mountpoint.
	fm.renter.tg.Done()

	return errors.AddContext(fs.srv.Unmount(), "failed to unmount filesystem")
}
