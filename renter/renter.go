package renter

import (
	"fmt"
	"net"
	"time"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/types"
)

type (
	// A TransactionPool broadcasts transaction sets to miners for inclusion in
	// an upcoming block.
	TransactionPool interface {
		AddTransactionSet(txns []types.Transaction) error
		RecommendedFee() types.Currency
	}

	// A Wallet provides addresses and funds and signs transactions.
	Wallet interface {
		Address() types.Address
		FundTransaction(txn *types.Transaction, amount types.Currency, pool []types.Transaction) ([]types.ElementID, func(), error)
		SignTransaction(vc consensus.ValidationContext, txn *types.Transaction, toSign []types.ElementID) error
	}

	// A ChainManager manages blockchain state.
	ChainManager interface {
		TipContext() consensus.ValidationContext
	}
)

// A Session implements of the renter side of the renter-host protocol.
type Session struct {
	hostKey   types.PublicKey
	renterKey types.PrivateKey

	cm      ChainManager
	session *rhp.Session

	settings   rhp.HostSettings
	settingsID rhp.SettingsID
	contract   rhp.Contract
}

func (s *Session) contractLocked() bool {
	return s.contract.ID != types.ElementID{}
}

func (s *Session) canRevise() bool {
	return s.contract.Revision.RevisionNumber != types.MaxRevisionNumber
}

func (s *Session) sufficientFunds(cost types.Currency) bool {
	return s.contract.Revision.RenterOutput.Value.Cmp(cost) >= 0
}

func (s *Session) currentSettings() (rhp.SettingsID, rhp.HostSettings, error) {
	if s.settings.ValidUntil.Before(time.Now()) {
		return rhp.SettingsID{}, rhp.HostSettings{}, fmt.Errorf("settings expired")
	}
	return s.settingsID, s.settings, nil
}

// HostKey returns the public key of the host.
func (s *Session) HostKey() types.PublicKey { return s.hostKey }

// LockedContract returns the currently locked contract.
func (s *Session) LockedContract() rhp.Contract { return s.contract }

// Close ends the RHP session and closes the underlying connection.
func (s *Session) Close() error {
	return s.session.Close()
}

// NewSession initiates an RHP session with the specified host.
func NewSession(hostIP string, hostKey types.PublicKey, cm ChainManager) (*Session, error) {
	conn, err := net.Dial("tcp", hostIP)
	if err != nil {
		return nil, fmt.Errorf("failed to open connection: %w", err)
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	sess, err := rhp.DialSession(conn, hostKey)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to start session: %w", err)
	}
	return &Session{
		hostKey: hostKey,
		cm:      cm,
		session: sess,
	}, nil
}
