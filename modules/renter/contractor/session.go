package contractor

import (
	"sync"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/proto"
	"gitlab.com/NebulousLabs/Sia/types"
)

var errInvalidSession = errors.New("session has been invalidated because its contract is being renewed")

// A Session modifies a Contract by communicating with a host. It uses the
// renter-host protocol to send modification requests to the host. Among other
// things, Sessions are the means by which the renter transfers file data to
// and from hosts.
type Session interface {
	// Address returns the address of the host.
	Address() modules.NetAddress

	// Close terminates the connection to the host.
	Close() error

	// ContractID returns the FileContractID of the contract.
	ContractID() types.FileContractID

	// Download requests the specified sector data.
	Download(root crypto.Hash, offset, length uint32) ([]byte, error)

	// DownloadIndex requests data from the sector with the specified index
	// within the contract.
	DownloadIndex(index uint64, offset, length uint32) ([]byte, error)

	// EndHeight returns the height at which the contract ends.
	EndHeight() types.BlockHeight

	// Replace replaces the sector at the specified index with data. The old
	// sector is swapped to the end of the contract data, and is deleted if the
	// trim flag is set.
	Replace(data []byte, sectorIndex uint64, trim bool) (crypto.Hash, error)

	// HostSettings will return the currently active host settings for a
	// session, which allows the workers to check for price gouging and
	// determine whether or not an operation should continue.
	HostSettings() modules.HostExternalSettings

	// Upload revises the underlying contract to store the new data. It
	// returns the Merkle root of the data.
	Upload(data []byte) (crypto.Hash, error)
}

// A hostSession modifies a Contract via the renter-host RPC loop. It
// implements the Session interface. hostSessions are safe for use by multiple
// goroutines.
type hostSession struct {
	clients    int // safe to Close when 0
	contractor *Contractor
	session    *proto.Session
	endHeight  types.BlockHeight
	id         types.FileContractID
	invalid    bool // true if invalidate has been called
	netAddress modules.NetAddress

	mu sync.Mutex
}

// invalidate sets the invalid flag and closes the underlying proto.Session.
// Once invalidate returns, the hostSession is guaranteed to not further revise
// its contract. This is used during contract renewal to prevent an Session
// from revising a contract mid-renewal.
func (hs *hostSession) invalidate() {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.invalid {
		return
	}
	hs.session.Close()
	hs.contractor.mu.Lock()
	delete(hs.contractor.sessions, hs.id)
	hs.contractor.mu.Unlock()
	hs.invalid = true
}

// Address returns the NetAddress of the host.
func (hs *hostSession) Address() modules.NetAddress { return hs.netAddress }

// Close cleanly terminates the revision loop with the host and closes the
// connection.
func (hs *hostSession) Close() error {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.clients--
	// Close is a no-op if invalidate has been called, or if there are other
	// clients still using the hostSession.
	if hs.invalid || hs.clients > 0 {
		return nil
	}
	hs.invalid = true
	hs.contractor.mu.Lock()
	delete(hs.contractor.sessions, hs.id)
	hs.contractor.mu.Unlock()

	return hs.session.Close()
}

// ContractID returns the ID of the contract being revised.
func (hs *hostSession) ContractID() types.FileContractID { return hs.id }

// Download retrieves the sector with the specified Merkle root, and revises
// the underlying contract to pay the host proportionally to the data
// retrieved.
func (hs *hostSession) Download(root crypto.Hash, offset, length uint32) ([]byte, error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.invalid {
		return nil, errInvalidSession
	}

	// Download the data.
	_, data, err := hs.session.ReadSection(root, offset, length)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// DownloadIndex retrieves the sector with the specified index.
func (hs *hostSession) DownloadIndex(index uint64, offset, length uint32) ([]byte, error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.invalid {
		return nil, errInvalidSession
	}

	// Retrieve the Merkle root for the index.
	_, roots, err := hs.session.SectorRoots(modules.LoopSectorRootsRequest{
		RootOffset: index,
		NumRoots:   1,
	})
	if err != nil {
		return nil, err
	}

	// Download the data.
	_, data, err := hs.session.ReadSection(roots[0], offset, length)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// EndHeight returns the height at which the host is no longer obligated to
// store the file.
func (hs *hostSession) EndHeight() types.BlockHeight { return hs.endHeight }

// Upload negotiates a revision that adds a sector to a file contract.
func (hs *hostSession) Upload(data []byte) (crypto.Hash, error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.invalid {
		return crypto.Hash{}, errInvalidSession
	}

	// Perform the upload.
	_, sectorRoot, err := hs.session.Append(data)
	if err != nil {
		return crypto.Hash{}, err
	}
	return sectorRoot, nil
}

// Replace replaces the sector at the specified index with data.
func (hs *hostSession) Replace(data []byte, sectorIndex uint64, trim bool) (crypto.Hash, error) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	if hs.invalid {
		return crypto.Hash{}, errInvalidSession
	}

	_, sectorRoot, err := hs.session.Replace(data, sectorIndex, trim)
	if err != nil {
		return crypto.Hash{}, err
	}
	return sectorRoot, nil
}

// HostSettings returns the currently active host settings for the session.
func (hs *hostSession) HostSettings() modules.HostExternalSettings {
	return hs.session.HostSettings()
}

// Session returns a Session object that can be used to upload, modify, and
// delete sectors on a host.
func (c *Contractor) Session(pk types.SiaPublicKey, cancel <-chan struct{}) (_ Session, err error) {
	c.mu.RLock()
	id, gotID := c.pubKeysToContractID[pk.String()]
	cachedSession, haveSession := c.sessions[id]
	height := c.blockHeight
	renewing := c.renewing[id]
	c.mu.RUnlock()
	if !gotID {
		return nil, errors.New("failed to get filecontract id from key")
	}
	if renewing {
		// Cannot use the session if the contract is being renewed.
		return nil, errors.New("currently renewing that contract")
	} else if haveSession {
		// This session already exists. Mark that there are now two routines
		// using the session, and then return the session that already exists.
		cachedSession.mu.Lock()
		cachedSession.clients++
		cachedSession.mu.Unlock()
		return cachedSession, nil
	}

	// Check that the contract and host are both available, and run some brief
	// sanity checks to see that the host is not swindling us.
	contract, haveContract := c.staticContracts.View(id)
	if !haveContract {
		return nil, errors.New("contract not found in the renter contract set")
	}
	host, haveHost, err := c.hdb.Host(contract.HostPublicKey)
	if err != nil {
		return nil, errors.AddContext(err, "error getting host from hostdb:")
	} else if height > contract.EndHeight {
		return nil, errors.New("contract has already ended")
	} else if !haveHost {
		return nil, errors.New("no record of that host")
	} else if host.Filtered {
		return nil, errors.New("host is blacklisted")
	} else if host.StoragePrice.Cmp(maxStoragePrice) > 0 {
		return nil, errTooExpensive
	} else if host.UploadBandwidthPrice.Cmp(maxUploadPrice) > 0 {
		return nil, errTooExpensive
	}

	// Create the session.
	s, err := c.staticContracts.NewSession(host, id, height, c.hdb, cancel)
	if modules.IsContractNotRecognizedErr(err) {
		err = errors.Compose(err, c.MarkContractBad(id))
	}
	if err != nil {
		return nil, err
	}

	// cache session
	hs := &hostSession{
		clients:    1,
		contractor: c,
		session:    s,
		endHeight:  contract.EndHeight,
		id:         id,
		netAddress: host.NetAddress,
	}
	c.mu.Lock()
	c.sessions[contract.ID] = hs
	c.mu.Unlock()

	return hs, nil
}
