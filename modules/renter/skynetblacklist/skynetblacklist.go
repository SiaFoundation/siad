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

// SkynetBlacklist manages a set of blacklisted skylinks by tracking the
// merkleroots and persists the list to disk
type SkynetBlacklist struct {
	aop *persist.AppendOnlyPersist

	merkleroots map[crypto.Hash]struct{}

	mu sync.Mutex
}

// New creates a new SkynetBlacklist.
func New(persistDir string) (*SkynetBlacklist, error) {
	// Initialize the persistence of the blacklist.
	aop, r, err := persist.NewAppendOnlyPersist(persistDir, persistFile, persistSize, metadataHeader, metadataVersion)
	if err != nil {
		return nil, errors.AddContext(err, fmt.Sprintf("unable to initialize the skynet blacklist persistence at '%v'", aop.FilePath()))
	}
	defer r.Close()

	sb := &SkynetBlacklist{
		aop:         aop,
		merkleroots: make(map[crypto.Hash]struct{}),
	}
	blacklist, err := unmarshalObjects(r)
	if err != nil {
		return nil, errors.AddContext(err, "unable to unmarshal persist objects")
	}
	sb.merkleroots = blacklist

	return sb, nil
}

// Blacklist returns the merkleroots that are blacklisted
func (sb *SkynetBlacklist) Blacklist() []crypto.Hash {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	var blacklist []crypto.Hash
	for mr := range sb.merkleroots {
		blacklist = append(blacklist, mr)
	}
	return blacklist
}

// IsBlacklisted indicates if a skylink is currently blacklisted
func (sb *SkynetBlacklist) IsBlacklisted(skylink modules.Skylink) bool {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	_, ok := sb.merkleroots[skylink.MerkleRoot()]
	return ok
}

// UpdateSkynetBlacklist updates the list of skylinks that are blacklisted
func (sb *SkynetBlacklist) UpdateSkynetBlacklist(additions, removals []modules.Skylink) error {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	buf, err := sb.marshalObjects(additions, removals)
	if err != nil {
		return errors.AddContext(err, fmt.Sprintf("unable to update skynet blacklist persistence at '%v'", sb.aop.FilePath()))
	}
	_, err = sb.aop.Write(buf.Bytes())
	return errors.AddContext(err, fmt.Sprintf("unable to update skynet blacklist persistence at '%v'", sb.aop.FilePath()))
}

// marshalObjects marshals the given objects into a byte buffer.
//
// NOTE: this method does not check for duplicate additions or removals
func (sb *SkynetBlacklist) marshalObjects(additions, removals []modules.Skylink) (bytes.Buffer, error) {
	// Create buffer for encoder
	var buf bytes.Buffer
	// Create and encode the persist links
	for _, skylink := range additions {
		// Add skylink merkleroot to map
		mr := skylink.MerkleRoot()
		sb.merkleroots[mr] = struct{}{}

		// Marshal the update
		err := marshalSia(&buf, mr, true)
		if err != nil {
			return bytes.Buffer{}, errors.AddContext(err, "unable to encode persisted blacklist link")
		}
	}
	for _, skylink := range removals {
		// Remove skylink merkleroot from map
		mr := skylink.MerkleRoot()
		delete(sb.merkleroots, mr)

		// Marshal the update
		err := marshalSia(&buf, mr, false)
		if err != nil {
			return bytes.Buffer{}, errors.AddContext(err, "unable to encode persisted blacklist removal link")
		}
	}

	return buf, nil
}

// unmarshalObjects unmarshals the sia encoded objects.
func unmarshalObjects(r io.Reader) (map[crypto.Hash]struct{}, error) {
	blacklist := make(map[crypto.Hash]struct{})
	// Unmarshal blacklisted links one by one until EOF.
	for {
		merkleRoot, blacklisted, err := unmarshalSia(r)
		if errors.Contains(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if !blacklisted {
			delete(blacklist, merkleRoot)
			continue
		}
		blacklist[merkleRoot] = struct{}{}
	}
	return blacklist, nil
}

// marshalSia implements the encoding.SiaMarshaler interface.
func marshalSia(w io.Writer, merkleRoot crypto.Hash, listed bool) error {
	e := encoding.NewEncoder(w)
	e.Encode(merkleRoot)
	e.WriteBool(listed)
	return e.Err()
}

// unmarshalSia implements the encoding.SiaUnmarshaler interface.
func unmarshalSia(r io.Reader) (merkleRoot crypto.Hash, listed bool, err error) {
	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	d.Decode(&merkleRoot)
	listed = d.NextBool()
	err = d.Err()
	return
}
