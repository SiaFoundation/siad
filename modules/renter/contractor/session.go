package contractor

import (
	"errors"
	"sync"

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

	// EndHeight returns the height at which the contract ends.
	EndHeight() types.BlockHeight

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
	_, data, err := hs.session.Download(root, offset, length)
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
	_, sectorRoot, err := hs.session.Upload(data)
	if err != nil {
		return crypto.Hash{}, err
	}
	return sectorRoot, nil
}

// Session returns a Session object that can be used to upload, modify, and
// delete sectors on a host.
func (c *Contractor) Session(pk types.SiaPublicKey, cancel <-chan struct{}) (_ Session, err error) {
	c.mu.RLock()
	id, gotID := c.pubKeysToContractID[string(pk.Key)]
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
		return nil, errors.New("no record of that contract")
	}
	host, haveHost := c.hdb.Host(contract.HostPublicKey)
	if height > contract.EndHeight {
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
	if err != nil {
		return nil, err
	}

	// Call RecentRevision to synchronize our revision with the host's.
	//
	// TODO: ideally we could do this lazily, i.e. only sync if an RPC fails.
	if _, _, err := s.RecentRevision(); err != nil {
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
