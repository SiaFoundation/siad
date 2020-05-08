package skynetportals

import (
	"bytes"
	"fmt"
	"io"
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
	//
	// TODO: We can use a variable-sized buffer for a persisted portal instead
	// of a fixed-size buffer. We currently use a large fixed-size buffer so
	// that we always know the amount of stored objects given the file size (and
	// for consistency with the blacklist implementation). This wastes a lot of
	// space.
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
	// SkynetPortals manages a list of known Skynet portals by persisting the
	// list to disk.
	SkynetPortals struct {
		aop *persist.AppendOnlyPersist

		portals map[modules.NetAddress]bool

		mu sync.Mutex
	}

	// listedPortal contains a Skynet portal and whether it is listed.
	listedPortal struct {
		address modules.NetAddress
		public  bool
		listed  bool
	}
)

// New creates a new SkynetPortals.
func New(persistDir string) (*SkynetPortals, error) {
	// Initialize the persistence of the portal list.
	aop, r, err := persist.NewAppendOnlyPersist(persistDir, persistFile, persistSize, metadataHeader, metadataVersion)
	if err != nil {
		return nil, errors.AddContext(err, fmt.Sprintf("unable to initialize the skynet portal list persistence at '%v'", aop.FilePath()))
	}
	defer r.Close()

	sp := &SkynetPortals{
		aop: aop,
	}
	portals, err := unmarshalObjects(r)
	if err != nil {
		return nil, errors.AddContext(err, "unable to unmarshal persist objects")
	}
	sp.portals = portals

	return sp, nil
}

// Portals returns the list of known Skynet portals.
func (sp *SkynetPortals) Portals() []modules.SkynetPortal {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	var portals []modules.SkynetPortal
	for addr, public := range sp.portals {
		portal := modules.SkynetPortal{
			Address: addr,
			Public:  public,
		}
		portals = append(portals, portal)
	}
	return portals
}

// UpdateSkynetPortals updates the list of known Skynet portals.
func (sp *SkynetPortals) UpdateSkynetPortals(additions []modules.SkynetPortal, removals []modules.NetAddress) error {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	buf, err := sp.marshalObjects(additions, removals)
	if err != nil {
		return errors.AddContext(err, fmt.Sprintf("unable to update skynet portal list persistence at '%v'", sp.aop.FilePath()))
	}
	_, err = sp.aop.Write(buf.Bytes())
	return errors.AddContext(err, fmt.Sprintf("unable to update skynet portal list persistence at '%v'", sp.aop.FilePath()))
}

// marshalObjects marshals the given objects into a byte buffer.
//
// NOTE: this method does not check for duplicate additions or removals
func (sp *SkynetPortals) marshalObjects(additions []modules.SkynetPortal, removals []modules.NetAddress) (bytes.Buffer, error) {
	// Create buffer for encoder
	var buf bytes.Buffer
	// Create and encode the persist portals
	for _, portal := range additions {
		// Add portal to map
		address := portal.Address
		public := portal.Public
		listed := true
		sp.portals[address] = public

		// Marshal the update
		lp := listedPortal{address, public, listed}
		err := lp.MarshalSia(&buf)
		if err != nil {
			return bytes.Buffer{}, errors.AddContext(err, "unable to encode persisted portal")
		}
	}
	for _, address := range removals {
		// Remove portal from map
		public := true
		listed := false
		delete(sp.portals, address)

		// Marshal the update
		lp := listedPortal{address, public, listed}
		err := lp.MarshalSia(&buf)
		if err != nil {
			return bytes.Buffer{}, errors.AddContext(err, "unable to encode persisted portal")
		}
	}

	return buf, nil
}

// unmarshalObjects unmarshals the sia encoded objects.
func unmarshalObjects(r io.Reader) (map[modules.NetAddress]bool, error) {
	portals := make(map[modules.NetAddress]bool)
	// Unmarshal portals one by one until EOF.
	for {
		var lp listedPortal
		err := lp.UnmarshalSia(r)
		if errors.Contains(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if !lp.listed {
			delete(portals, lp.address)
			continue
		}
		portals[lp.address] = lp.public
	}
	return portals, nil
}

// MarshalSia implements the encoding.SiaMarshaler interface.
func (lp listedPortal) MarshalSia(w io.Writer) error {
	if len(lp.address) > modules.MaxEncodedNetAddressLength {
		return errors.New("given address " + string(lp.address) + " does not fit in " + string(modules.MaxEncodedNetAddressLength) + " bytes")
	}
	e := encoding.NewEncoder(w)
	// Create a padded buffer so that we always write the same amount of bytes.
	buf := make([]byte, modules.MaxEncodedNetAddressLength)
	copy(buf, lp.address)
	e.Write(buf)
	e.WriteBool(lp.public)
	e.WriteBool(lp.listed)
	return e.Err()
}

// UnmarshalSia implements the encoding.SiaUnmarshaler interface.
func (lp *listedPortal) UnmarshalSia(r io.Reader) error {
	*lp = listedPortal{}
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
	lp.address = modules.NetAddress(string(buf[:end]))
	lp.public = d.NextBool()
	lp.listed = d.NextBool()
	err = d.Err()
	return err
}

// validatePortalChanges validates the changes to be made to the Skynet portals
// list.
func (sp *SkynetPortals) validatePortalChanges(additions []modules.SkynetPortal, removals []modules.NetAddress) error {
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
		if _, exists := sp.portals[removalAddress]; !exists {
			if _, added := additionsMap[removalAddress]; !added {
				return errors.New("address " + string(removalAddress) + " not already present in list of portals or being added")
			}
		}
	}
	return nil
}
