package proto

// TEMPORARY FILE!
//
// I found it hard to jump around a longish file between a dozen methods, so I
//temporarily split them into this new file. I'll merge them back into the
// original after it's all done. Or I can leave them here, if that makes sense.

import (
	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// ApplyUpdates is a wrapper for applyUpdates that uses the production
// dependencies.
func ApplyUpdates(updates ...writeaheadlog.Update) error {
	return applyUpdates(updates...)
}

// applyUpdates applies a number of writeaheadlog updates to the corresponding
// RefCounter file. This method can apply updates from different RefCounter
// files and should only be run before the RefCounter files are loaded from disk
// right after the startup of siad. Otherwise we might run into concurrency issues.
func applyUpdates(updates ...writeaheadlog.Update) error {
	for _, u := range updates {
		err := func() error {
			switch u.Name {
			case walValueName:
				return readAndApplyResizeUpdate(u)
			case walResizeName:
				return readAndApplyResizeUpdate(u)
			default:
				return errUnknownRefCounterUpdate
			}
		}()
		if err != nil {
			return errors.AddContext(err, "failed to apply update")
		}
	}
	return nil
}

// createResizeUpdate is a helper method which creates a writeaheadlog update
// for resizing the file on disk from the specified old number of sectors to
// the new one.
func createResizeUpdate(path string, oldSecNum, newSecNum uint64) writeaheadlog.Update {
	if oldSecNum < 0 || newSecNum < 0 {
		oldSecNum, newSecNum = 0, 0
		build.Critical("size passed to createResizeUpdate should never be negative")
	}
	// Create update
	return writeaheadlog.Update{
		Name:         walResizeName,
		Instructions: encoding.MarshalAll(path, oldSecNum, newSecNum),
	}
}

// TODO: Implement
// readAndApplyDeleteUpdate reads the delete update and applies it. This helper
// assumes that the file is not open
func readAndApplyDeleteUpdate(update writeaheadlog.Update) error {
	update = update // Just make this compile
	return nil
}

// readAndApplyResizeUpdate reads the resize update and applies it. This helper
// assumes that the file is not open
func readAndApplyResizeUpdate(update writeaheadlog.Update) error {
	path, oldSecNum, newSecNum, err := readResizeUpdate(update)
	if err != nil {
		return err
	}
	// TODO: if we don't want to load it we can just inspect the file directly
	rc, err := LoadRefCounter(path)
	if err != nil {
		return err
	}
	// check if old size matches
	if rc.numSectors != oldSecNum {
		return errors.New("Resize failed - the old size doesn't match the current size.")
	}
	return rc.managedDropSectors(oldSecNum - newSecNum)
}

// TODO: Implement
// readAndApplyValueUpdate reads the delete update and applies it. This helper
// assumes that the file is not open
func readAndApplyValueUpdate(update writeaheadlog.Update) error {
	update = update // Just make this compile
	return nil
}

// readResizeUpdate unmarshals the update's instructions and returns them
// TODO: I'm happy to wrap these return values in a convenience type
func readResizeUpdate(update writeaheadlog.Update) (path string, oldSecNum uint64, newSecNum uint64, err error) {
	err = encoding.UnmarshalAll(update.Instructions, path, oldSecNum, newSecNum)
	return
}

// applyUpdates applies updates to the RefCounter file. Only updates that belong
// to the RefCounter file on which applyUpdates is called can be applied.
// Everything else will be considered a developer error and cause the update to
// not be applied to avoid corruption.
func (rc *RefCounter) applyUpdates(updates ...writeaheadlog.Update) error {
	// TODO: Add this `deleted` flag + handling
	//// Sanity check that file hasn't been deleted.
	//if sf.deleted {
	//	return errors.New("can't call applyUpdates on deleted file")
	//}

	// If the set of updates contains a delete, all updates prior to that delete
	// are irrelevant, so perform the last delete and then process the remaining
	// updates. This also prevents a bug on Windows where we attempt to delete
	// the file while holding a open file handle.
	for i := len(updates) - 1; i >= 0; i-- {
		u := updates[i]
		switch u.Name {
		case walDeleteName:
			if err := readAndApplyDeleteUpdate(u); err != nil {
				return err
			}
			updates = updates[i+1:]
			break
		default:
			continue
		}
	}
	if len(updates) == 0 {
		return nil
	}

	// Apply updates.
	for _, u := range updates {
		err := func() error {
			switch u.Name {
			case walDeleteName:
				return rc.readAndApplyDeleteUpdate(u)
			case walResizeName:
				return rc.readAndApplyResizeUpdate(u)
			case walValueName:
				return rc.readAndApplyValueUpdate(u)
			default:
				return errUnknownRefCounterUpdate
			}
		}()
		if err != nil {
			return errors.AddContext(err, "failed to apply update")
		}
	}
	return nil
}

// createResizeUpdate is a helper method which creates a writeaheadlog update for
// resizing the file on disk from the specified old number of sectors to the new one.
func (rc *RefCounter) createResizeUpdate(oldSecNum, newSecNum uint64) writeaheadlog.Update {
	return createResizeUpdate(rc.filepath, oldSecNum, newSecNum)
}

// createValueUpdate is a helper method which creates a writeaheadlog update for
// writing the specified data to the provided index, overwriting the data
// existing in the updated region.
func (rc *RefCounter) createValueUpdate(secNum int64, value []byte) writeaheadlog.Update {
	if secNum < 0 {
		secNum = 0
		value = []byte{}
		build.Critical("secNum passed to createValueUpdate should never be negative")
	}
	// Create update
	return writeaheadlog.Update{
		Name:         walValueName,
		Instructions: encoding.MarshalAll(secNum, value),
	}
}

func (rc *RefCounter) readAndApplyDeleteUpdate(update writeaheadlog.Update) error {
	return readAndApplyDeleteUpdate(update)
}

func (rc *RefCounter) readAndApplyResizeUpdate(update writeaheadlog.Update) error {
	return readAndApplyResizeUpdate(update)
}

func (rc *RefCounter) readAndApplyValueUpdate(update writeaheadlog.Update) error {
	return readAndApplyValueUpdate(update)
}
