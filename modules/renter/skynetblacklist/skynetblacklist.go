package skynetblacklist

import (
	"bytes"
	"fmt"
	"io"
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
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
	metadataVersion = persist.MetadataVersionv150
)

type (
	// SkynetBlacklist manages a set of blacklisted skylinks by tracking the
	// merkleroots and persists the list to disk.
	SkynetBlacklist struct {
		staticAop *persist.AppendOnlyPersist

		// hashes is a set of hashed blacklisted merkleroots.
		hashes map[crypto.Hash]struct{}

		mu sync.Mutex
	}

	// persistEntry contains a hash and whether it should be listed as being in
	// the current blacklist.
	persistEntry struct {
		Hash   crypto.Hash
		Listed bool
	}
)

// New returns an initialized SkynetBlacklist.
func New(persistDir string) (*SkynetBlacklist, error) {
	// Load the persistence of the blacklist.
	aop, reader, err := loadPersist(persistDir)
	if err != nil {
		return nil, errors.AddContext(err, "unable to load the skynet blacklist persistence")
	}

	sb := &SkynetBlacklist{
		staticAop: aop,
	}
	hashes, err := unmarshalObjects(reader)
	if err != nil {
		err = errors.Compose(err, aop.Close())
		return nil, errors.AddContext(err, "unable to unmarshal persist objects")
	}
	sb.hashes = hashes

	return sb, nil
}

// Blacklist returns the hashes of the merkleroots that are blacklisted
func (sb *SkynetBlacklist) Blacklist() []crypto.Hash {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	var blacklist []crypto.Hash
	for hash := range sb.hashes {
		blacklist = append(blacklist, hash)
	}
	return blacklist
}

// Close closes and frees associated resources.
func (sb *SkynetBlacklist) Close() error {
	return sb.staticAop.Close()
}

// IsBlacklisted indicates if a skylink is currently blacklisted
func (sb *SkynetBlacklist) IsBlacklisted(skylink modules.Skylink) bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()
	hash := crypto.HashObject(skylink.MerkleRoot())
	_, ok := sb.hashes[hash]
	return ok
}

// UpdateBlacklist updates the list of skylinks that are blacklisted.
func (sb *SkynetBlacklist) UpdateBlacklist(additions, removals []crypto.Hash) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	buf, err := sb.marshalObjects(additions, removals)
	if err != nil {
		return errors.AddContext(err, fmt.Sprintf("unable to update skynet blacklist persistence at '%v'", sb.staticAop.FilePath()))
	}
	_, err = sb.staticAop.Write(buf.Bytes())
	return errors.AddContext(err, fmt.Sprintf("unable to update skynet blacklist persistence at '%v'", sb.staticAop.FilePath()))
}

// marshalObjects marshals the given objects into a byte buffer.
//
// NOTE: this method does not check for duplicate additions or removals
func (sb *SkynetBlacklist) marshalObjects(additions, removals []crypto.Hash) (bytes.Buffer, error) {
	// Create buffer for encoder
	var buf bytes.Buffer
	// Create and encode the persist links
	listed := true
	for _, hash := range additions {
		// Add hash to map
		sb.hashes[hash] = struct{}{}

		// Marshal the update
		pe := persistEntry{hash, listed}
		data := encoding.Marshal(pe)
		_, err := buf.Write(data)
		if err != nil {
			return bytes.Buffer{}, errors.AddContext(err, "unable to write addition to the buffer")
		}
	}
	listed = false
	for _, hash := range removals {
		// Remove hash from map
		delete(sb.hashes, hash)

		// Marshal the update
		pe := persistEntry{hash, listed}
		data := encoding.Marshal(pe)
		_, err := buf.Write(data)
		if err != nil {
			return bytes.Buffer{}, errors.AddContext(err, "unable to write removal to the buffer")
		}
	}

	return buf, nil
}

// unmarshalObjects unmarshals the sia encoded objects.
func unmarshalObjects(reader io.Reader) (map[crypto.Hash]struct{}, error) {
	blacklist := make(map[crypto.Hash]struct{})
	// Unmarshal blacklisted links one by one until EOF.
	var offset uint64
	for {
		buf := make([]byte, persistSize)
		_, err := io.ReadFull(reader, buf)
		if errors.Contains(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		var pe persistEntry
		err = encoding.Unmarshal(buf, &pe)
		if err != nil {
			return nil, err
		}
		offset += persistSize

		if !pe.Listed {
			delete(blacklist, pe.Hash)
			continue
		}
		blacklist[pe.Hash] = struct{}{}
	}
	return blacklist, nil
}
