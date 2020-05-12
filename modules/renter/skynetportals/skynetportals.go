package skynetportals

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// persistFile is the name of the persist file
	persistFile string = "skynetportals"

	// persistSize is the size of a persisted portal in the portals list. It is
	// the length of `NetAddress` plus the `public` and `listed` flags.
	persistSize uint64 = modules.MaxEncodedNetAddressLength + 2
)

var (
	// ErrSkynetPortalsValidation is the error returned when validation of
	// changes to the Skynet portals list fails.
	ErrSkynetPortalsValidation = errors.New("could not validate additions and removals")

	// metadataHeader is the header of the metadata for the persist file
	metadataHeader = types.NewSpecifier("SkynetPortals\n")

	// metadataVersion is the version of the persistence file
	metadataVersion = types.NewSpecifier("v1.4.8\n")
)

type (
	// PersistList manages a list of known Skynet portals by persisting the
	// list to disk.
	PersistList struct {
		staticAop *persist.AppendOnlyPersist

		// portals is a map of portal addresses to public status.
		portals map[modules.NetAddress]bool

		mu sync.Mutex
	}

	// persistEntry contains a Skynet portal and whether it should be listed as
	// being in the persistence file.
	persistEntry struct {
		address modules.NetAddress
		public  bool
		listed  bool
	}
)

// New returns an initialized PersistList.
func New(persistDir string) (*PersistList, error) {
	// Initialize the persistence of the portal list.
	aop, bytes, err := persist.NewAppendOnlyPersist(persistDir, persistFile, persistSize, metadataHeader, metadataVersion)
	if err != nil {
		return nil, errors.AddContext(err, fmt.Sprintf("unable to initialize the skynet portal list persistence at '%v'", aop.FilePath()))
	}

	pl := &PersistList{
		staticAop: aop,
	}
	portals, err := unmarshalObjects(bytes)
	if err != nil {
		return nil, errors.AddContext(err, "unable to unmarshal persist objects")
	}
	pl.portals = portals

	return pl, nil
}

// Portals returns the list of known Skynet portals.
func (pl *PersistList) Portals() []modules.SkynetPortal {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	var portals []modules.SkynetPortal
	for addr, public := range pl.portals {
		portal := modules.SkynetPortal{
			Address: addr,
			Public:  public,
		}
		portals = append(portals, portal)
	}
	return portals
}

// UpdatePortals updates the list of known Skynet portals.
func (pl *PersistList) UpdatePortals(additions []modules.SkynetPortal, removals []modules.NetAddress) error {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	// Convert portal addresses to lowercase for case-insensitivity.
	addPortals := make([]modules.SkynetPortal, len(additions))
	for i, portalInfo := range additions {
		address := modules.NetAddress(strings.ToLower(string(portalInfo.Address)))
		portalInfo.Address = address
		addPortals[i] = portalInfo
	}
	removePortals := make([]modules.NetAddress, len(removals))
	for i, address := range removals {
		address = modules.NetAddress(strings.ToLower(string(address)))
		removePortals[i] = address
	}

	// Validate now before we start making changes.
	err := pl.validatePortalChanges(additions, removals)
	if err != nil {
		return errors.AddContext(err, ErrSkynetPortalsValidation.Error())
	}

	buf, err := pl.marshalObjects(additions, removals)
	if err != nil {
		return errors.AddContext(err, fmt.Sprintf("unable to update skynet portal list persistence at '%v'", pl.staticAop.FilePath()))
	}
	_, err = pl.staticAop.Write(buf.Bytes())
	return errors.AddContext(err, fmt.Sprintf("unable to update skynet portal list persistence at '%v'", pl.staticAop.FilePath()))
}

// marshalObjects marshals the given objects into a byte buffer.
//
// NOTE: this method does not check for duplicate additions or removals
func (pl *PersistList) marshalObjects(additions []modules.SkynetPortal, removals []modules.NetAddress) (bytes.Buffer, error) {
	// Create buffer for encoder
	var buf bytes.Buffer
	// Create and encode the persist portals
	listed := true
	for _, portal := range additions {
		// Add portal to map
		pl.portals[portal.Address] = portal.Public

		// Marshal the update
		pe := persistEntry{portal.Address, portal.Public, listed}
		err := pe.MarshalSia(&buf)
		if err != nil {
			return bytes.Buffer{}, errors.AddContext(err, "unable to encode persisted portal")
		}
	}
	listed = false
	for _, address := range removals {
		// Remove portal from map
		public, exists := pl.portals[address]
		if !exists {
			return bytes.Buffer{}, fmt.Errorf("address %v does not exist", address)
		}
		delete(pl.portals, address)

		// Marshal the update
		pe := persistEntry{address, public, listed}
		err := pe.MarshalSia(&buf)
		if err != nil {
			return bytes.Buffer{}, errors.AddContext(err, "unable to encode persisted portal")
		}
	}

	return buf, nil
}

// unmarshalObjects unmarshals the sia encoded objects.
func unmarshalObjects(readBytes []byte) (map[modules.NetAddress]bool, error) {
	portals := make(map[modules.NetAddress]bool)
	r := bytes.NewReader(readBytes)
	// Unmarshal portals one by one until EOF.
	for {
		var pe persistEntry
		err := pe.UnmarshalSia(r)
		if errors.Contains(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if !pe.listed {
			delete(portals, pe.address)
			continue
		}
		portals[pe.address] = pe.public
	}
	return portals, nil
}

// MarshalSia implements the encoding.SiaMarshaler interface.
//
// TODO: get rid of these, the length is already handled by marshal methods.
func (pe persistEntry) MarshalSia(w io.Writer) error {
	if len(pe.address) > modules.MaxEncodedNetAddressLength {
		return fmt.Errorf("given address %v does not fit in %v bytes", pe.address, modules.MaxEncodedNetAddressLength)
	}
	e := encoding.NewEncoder(w)
	// Create a padded buffer so that we always write the same amount of bytes.
	buf := make([]byte, modules.MaxEncodedNetAddressLength)
	copy(buf, pe.address)
	e.Write(buf)
	e.WriteBool(pe.public)
	e.WriteBool(pe.listed)
	return e.Err()
}

// UnmarshalSia implements the encoding.SiaUnmarshaler interface.
func (pe *persistEntry) UnmarshalSia(r io.Reader) error {
	*pe = persistEntry{}
	d := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	// Read into a padded buffer and extract the address string.
	buf := make([]byte, modules.MaxEncodedNetAddressLength)
	n, err := d.Read(buf)
	if err != nil {
		return errors.AddContext(err, "unable to read address")
	}
	if n != len(buf) {
		return errors.New("did not read address correctly")
	}
	end := bytes.IndexByte(buf, 0)
	if end == -1 {
		end = len(buf)
	}
	pe.address = modules.NetAddress(string(buf[:end]))
	pe.public = d.NextBool()
	pe.listed = d.NextBool()
	err = d.Err()
	return err
}

// validatePortalChanges validates the changes to be made to the Skynet portals
// list.
func (pl *PersistList) validatePortalChanges(additions []modules.SkynetPortal, removals []modules.NetAddress) error {
	// Check for nil input
	if len(additions)+len(removals) == 0 {
		return errors.New("no portals being added or removed")
	}

	additionsMap := make(map[modules.NetAddress]struct{})
	for _, addition := range additions {
		address := addition.Address
		if err := address.IsStdValid(); err != nil {
			return errors.New("invalid network address: " + err.Error())
		}
		additionsMap[address] = struct{}{}
	}
	// Check that each removal is valid.
	for _, removalAddress := range removals {
		if err := removalAddress.IsStdValid(); err != nil {
			return errors.New("invalid network address: " + err.Error())
		}
		if _, exists := pl.portals[removalAddress]; !exists {
			if _, added := additionsMap[removalAddress]; !added {
				return errors.New("address " + string(removalAddress) + " not already present in list of portals or being added")
			}
		}
	}
	return nil
}
