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
		SpendPolicy(types.Address) (types.SpendPolicy, bool)
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
	hostKey types.PublicKey

	wallet  Wallet
	tpool   TransactionPool
	cm      ChainManager
	session *rhp.Session

	settings   rhp.HostSettings
	settingsID rhp.SettingsID
}

func (s *Session) currentSettings() (rhp.SettingsID, rhp.HostSettings, error) {
	if s.settings.ValidUntil.Before(time.Now()) {
		return rhp.SettingsID{}, rhp.HostSettings{}, fmt.Errorf("settings expired")
	}
	return s.settingsID, s.settings, nil
}

// Close ends the RHP session and closes the underlying connection.
func (s *Session) Close() error {
	return s.session.Close()
}

// NewSession initiates an RHP session with the specified host.
func NewSession(hostIP string, hostKey types.PublicKey, w Wallet, tp TransactionPool, cm ChainManager) (*Session, error) {
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

		wallet:  w,
		tpool:   tp,
		cm:      cm,
		session: sess,
	}, nil
}
