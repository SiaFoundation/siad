package renter

import (
	"fmt"
	"net"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/consensus"
	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/types"
)

type (
	// A TransactionPool broadcasts transaction sets to miners for inclusion in
	// an upcoming block.
	TransactionPool interface {
		AddTransactionSet(txns []types.Transaction) error
		FeeEstimate() (min, max types.Currency, err error)
	}

	// A Wallet provides addresses and funds and signs transactions.
	Wallet interface {
		Address() types.Address
		SpendPolicy(types.Address) (types.SpendPolicy, bool)
		FundTransaction(txn *types.Transaction, amount types.Currency, pool []types.Transaction) ([]types.ElementID, func(), error)
		SignTransaction(vc consensus.ValidationContext, txn *types.Transaction, toSign []types.ElementID) error
	}
)

// A Session is an implementation of the renter side of the renter-host protocol.
type Session struct {
	hostKey types.PublicKey

	wallet  Wallet
	tpool   TransactionPool
	cm      *chain.Manager
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

// NewSession initiates a new RHP session with the host.
func NewSession(netaddress string, hostKey types.PublicKey, w Wallet, tp TransactionPool, cm *chain.Manager) (*Session, error) {
	conn, err := net.Dial("tcp", netaddress)
	if err != nil {
		return nil, fmt.Errorf("failed to open connection: %w", err)
	}

	s := &Session{
		hostKey: hostKey,

		cm:     cm,
		wallet: w,
		tpool:  tp,
	}

	s.session, err = rhp.DialSession(conn, hostKey)
	if err != nil {
		return nil, fmt.Errorf("failed to start session: %w", err)
	}

	return s, nil
}
