package skynetblacklist

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// persistFile is the name of the persist file
	persistFile string = "skynetblacklist"

	// persistSize is the size of a persisted merkleroot in the blacklist. It is
	// the length of `merkleroot` plus the `listed` flag (32 + 1).
	persistSize uint64 = 33
)

var (
	// metadataHeader is the header of the metadata for the persist file
	metadataHeader = types.NewSpecifier("SkynetBlacklist\n")

	// metadataVersion is the version of the persistence file
	metadataVersion = types.NewSpecifier("v1.4.3\n")
)

type (
	// PersistList manages a set of blacklisted skylinks by tracking the
	// merkleroots and persists the list to disk.
	PersistList struct {
		staticAop *persist.AppendOnlyPersist

		// merkleRoots is a set of blacklisted links.
		merkleRoots map[crypto.Hash]struct{}

		mu sync.Mutex
	}

	// persistEntry contains a Skynet blacklist link and whether it should be
	// listed as being in the persistence file.
	persistEntry struct {
		MerkleRoot crypto.Hash
		Listed     bool
	}
)

// New returns an initialized PersistList.
func New(persistDir string) (*PersistList, error) {
	// Initialize the persistence of the blacklist.
	aop, bytes, err := persist.NewAppendOnlyPersist(persistDir, persistFile, metadataHeader, metadataVersion)
	if err != nil {
		return nil, errors.AddContext(err, fmt.Sprintf("unable to initialize the skynet blacklist persistence at '%v'", aop.FilePath()))
	}

	pl := &PersistList{
		staticAop: aop,
	}
	blacklist, err := unmarshalObjects(bytes)
	if err != nil {
		return nil, errors.AddContext(err, "unable to unmarshal persist objects")
	}
	pl.merkleRoots = blacklist

	return pl, nil
}

// Blacklist returns the merkleroots that are blacklisted
func (pl *PersistList) Blacklist() []crypto.Hash {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	var blacklist []crypto.Hash
	for mr := range pl.merkleRoots {
		blacklist = append(blacklist, mr)
	}
	return blacklist
}

// IsBlacklisted indicates if a skylink is currently blacklisted
func (pl *PersistList) IsBlacklisted(skylink modules.Skylink) bool {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	_, ok := pl.merkleRoots[skylink.MerkleRoot()]
	return ok
}

// UpdateBlacklist updates the list of skylinks that are blacklisted.
func (pl *PersistList) UpdateBlacklist(additions, removals []modules.Skylink) error {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	buf, err := pl.marshalObjects(additions, removals)
	if err != nil {
		return errors.AddContext(err, fmt.Sprintf("unable to update skynet blacklist persistence at '%v'", pl.staticAop.FilePath()))
	}
	_, err = pl.staticAop.Write(buf.Bytes())
	return errors.AddContext(err, fmt.Sprintf("unable to update skynet blacklist persistence at '%v'", pl.staticAop.FilePath()))
}

// marshalObjects marshals the given objects into a byte buffer.
//
// NOTE: this method does not check for duplicate additions or removals
func (pl *PersistList) marshalObjects(additions, removals []modules.Skylink) (bytes.Buffer, error) {
	// Create buffer for encoder
	var buf bytes.Buffer
	// Create and encode the persist links
	listed := true
	for _, skylink := range additions {
		// Add skylink merkleroot to map
		mr := skylink.MerkleRoot()
		pl.merkleRoots[mr] = struct{}{}

		// Marshal the update
		pe := persistEntry{mr, listed}
		bytes := encoding.Marshal(pe)
		buf.Write(bytes)
	}
	listed = false
	for _, skylink := range removals {
		// Remove skylink merkleroot from map
		mr := skylink.MerkleRoot()
		delete(pl.merkleRoots, mr)

		// Marshal the update
		pe := persistEntry{mr, listed}
		bytes := encoding.Marshal(pe)
		buf.Write(bytes)
	}

	return buf, nil
}

// unmarshalObjects unmarshals the sia encoded objects.
func unmarshalObjects(bytes []byte) (map[crypto.Hash]struct{}, error) {
	blacklist := make(map[crypto.Hash]struct{})
	// Unmarshal blacklisted links one by one until EOF.
	var offset uint64
	for {
		if offset+persistSize > uint64(len(bytes)) {
			break
		}

		var pe persistEntry
		err := encoding.Unmarshal(bytes[offset:offset+persistSize], &pe)
		if errors.Contains(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		offset += persistSize

		if !pe.Listed {
			delete(blacklist, pe.MerkleRoot)
			continue
		}
		blacklist[pe.MerkleRoot] = struct{}{}
	}
	return blacklist, nil
}
