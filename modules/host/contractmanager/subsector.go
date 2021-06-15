package contractmanager

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

const (
	subSectorLocationPageSize = 64
)

var (
	subSectorsVersion                   = types.NewSpecifier("v1.5.5")
	subSectorLocationPersistenceVersion = uint8(1)
)

type (
	subSectorLocation struct {
		offset uint32
		length uint32
		parent sectorID

		diskOffset int64
	}

	subSectorLocationPersistence struct {
		checksum [16]byte
		version  uint8
		offset   uint32
		length   uint32
		parent   sectorID
		id       sectorID
	}

	subSectors struct {
		subSectorLocation map[sectorID][]subSectorLocation
		staticFile        modules.File
		nextOffset        int64
		mu                sync.Mutex
	}
)

func (ssl subSectorLocation) Marshal(sid sectorID) [subSectorLocationPageSize]byte {
	var page [subSectorLocationPageSize]byte
	page[16] = subSectorLocationPersistenceVersion
	binary.LittleEndian.PutUint32(page[17:21], ssl.offset)
	binary.LittleEndian.PutUint32(page[21:25], ssl.length)
	copy(page[25:37], ssl.parent[:])
	copy(page[37:49], sid[:])
	checksum := crypto.HashBytes(page[16:])
	copy(page[:16], checksum[:])
	return page
}

var (
	errInvalidChecksum = errors.New("invalid checksum")
	errInvalidVersion  = errors.New("invalid version")
)

func (ssl *subSectorLocation) Unmarshal(page [subSectorLocationPageSize]byte) (sid sectorID, _ error) {
	checksum := crypto.HashBytes(page[:16])
	if !bytes.Equal(checksum[:16], page[:16]) {
		return sectorID{}, errInvalidChecksum
	}
	version := uint8(page[16])
	if version != subSectorLocationPersistenceVersion {
		return sectorID{}, errInvalidVersion
	}
	ssl.offset = binary.LittleEndian.Uint32(page[17:21])
	ssl.length = binary.LittleEndian.Uint32(page[21:25])
	copy(ssl.parent[:], page[25:37])
	copy(sid[:], page[37:49])
	return sid, nil
}

func newSubSectors(path string) (_ *subSectors, err error) {
	// Create/Open file.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, modules.DefaultFilePerm)
	if err != nil {
		return nil, err
	}
	// Close the file if this function fails.
	defer func() {
		if err != nil {
			err = errors.Compose(err, f.Close())
		}
	}()
	// Create the struct.
	ss := &subSectors{
		subSectorLocation: make(map[sectorID][]subSectorLocation),
		staticFile:        f,
	}
	// Check if file is empty.
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	// Init or load the file.
	if fi.Size() == 0 {
		err = ss.init()
	} else {
		err = ss.load(fi.Size())
	}
	return ss, err
}

func (ss *subSectors) init() error {
	// Set the offset.
	ss.nextOffset = subSectorLocationPageSize
	// Write version to first page.
	_, err := ss.staticFile.WriteAt(subSectorsVersion[:], 0)
	if err != nil {
		return errors.AddContext(err, "failed to write version")
	}
	// Sync changes.
	return ss.staticFile.Sync()
}

func (ss *subSectors) load(size int64) error {
	// Sanity check size and set offset.
	if size%subSectorLocationPageSize != 0 {
		return fmt.Errorf("subSectors file has wrong length - needs to be a multiple of %v but was %v", subSectorLocationPageSize, size)
	}
	ss.nextOffset = size

	// Load the pages one after another.
	r := bufio.NewReader(ss.staticFile)
	var page [subSectorLocationPageSize]byte
	var emptyPage [subSectorLocationPageSize]byte
	for offset := subSectorLocationPageSize; ; offset += subSectorLocationPageSize {
		_, err := io.ReadFull(r, page[:])
		if err == io.EOF {
			return nil // done
		}
		if err != nil {
			return errors.AddContext(err, "failed to read page")
		}

		// Check for deleted sub sector.
		if page == emptyPage {
			// TODO: handle deleted sub sectors.
		}

		var ssl subSectorLocation
		sid, err := ssl.Unmarshal(page)
		if err != nil {
			return errors.AddContext(err, "failed to unmarshal page")
		}

		// Add location to map.
		ss.subSectorLocation[sid] = append(ss.subSectorLocation[sid], ssl)
	}
}

func (ss *subSectors) Close() error {
	errSync := ss.staticFile.Sync()
	errClose := ss.staticFile.Close()
	return errors.Compose(errSync, errClose)
}

func (ss *subSectors) AddLocation(sid sectorID, ssl subSectorLocation) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	// Save it to disk.
	newPage := ssl.Marshal(sid)
	_, err := ss.staticFile.WriteAt(newPage[:], ss.nextOffset)
	if err != nil {
		return err
	}
	ssl.diskOffset = ss.nextOffset
	ss.nextOffset += subSectorLocationPageSize

	// Add to in-memory map.
	ss.subSectorLocation[sid] = append(ss.subSectorLocation[sid], ssl)
	return nil
}

func (ss *subSectors) deleteSubSector(ssl subSectorLocation) error {
	var emptyPage [subSectorLocationPageSize]byte
	_, err := ss.staticFile.WriteAt(emptyPage[:], ssl.diskOffset)
	return err
}

func (ss *subSectors) RemoveParent(subSectorID, parentID sectorID) error {
	ss.mu.Lock()
	defer ss.mu.Unlock()

	ssls, exists := ss.subSectorLocation[subSectorID]
	if !exists {
		return nil // nothing to do
	}
	var newSSLs []subSectorLocation
	for _, ssl := range ssls {
		if ssl.parent == parentID {
			// delete from disk.
			if err := ss.deleteSubSector(ssl); err != nil {
				return err
			}
			continue
		}
		newSSLs = append(newSSLs, ssl)
	}
	if len(newSSLs) > 0 {
		ss.subSectorLocation[subSectorID] = newSSLs
	} else {
		delete(ss.subSectorLocation, subSectorID)
	}
	return nil
}

func (ss *subSectors) First(sid sectorID) (subSectorLocation, bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ssls, exists := ss.subSectorLocation[sid]
	if !exists {
		return subSectorLocation{}, false
	}
	if len(ssls) == 0 {
		build.Critical("found sub sector id but not sub sectors which shouldn't happen")
		delete(ss.subSectorLocation, sid)
		return subSectorLocation{}, false
	}
	return ssls[0], true
}

func (ss *subSectors) All(sid sectorID) ([]subSectorLocation, bool) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ssls, exists := ss.subSectorLocation[sid]
	if !exists {
		return nil, false
	}
	return ssls, true
}

func (ss *subSectors) Len() int {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	return len(ss.subSectorLocation)
}
