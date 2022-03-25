package host

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"go.sia.tech/core/chain"
	"go.sia.tech/core/host"
	"go.sia.tech/core/net/mux"
	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
	"lukechampine.com/frand"
)

type (

	// A Session manages the host-side mux session with a renter.
	Session struct {
		*rhp.Session

		uid     UniqueID
		privkey types.PrivateKey

		cm       *chain.Manager
		wallet   host.Wallet
		tpool    host.TransactionPool
		settings *settingsManager

		accounts  host.EphemeralAccountStore
		contracts host.ContractManager
		registry  *host.RegistryManager
		sectors   host.SectorStore
		recorder  MetricRecorder
		log       Logger

		// contract field must be protected by a separate mutex to prevent races
		// between streams.
		mu         sync.Mutex
		contractID types.ElementID
	}
)

// setContract verifies a contract challenge is valid, sets the contract
// for the RHP2 session, and updates the challenge.
func (s *Session) setContract(id types.ElementID, signature types.Signature, timeout time.Duration) (rhp.Contract, [16]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	contract, err := s.contracts.Lock(id, timeout)
	if err != nil {
		return rhp.Contract{}, [16]byte{}, fmt.Errorf("failed to lock contract: %w", err)
	}
	// contract must be unlocked as all other RPC rely on the leaner locking of
	// RHP3.
	defer s.contracts.Unlock(contract.ID)

	// validate the contract is still revisable and the signature is valid
	switch {
	case contract.Revision.RevisionNumber == types.MaxRevisionNumber:
		return rhp.Contract{}, [16]byte{}, errors.New("contract is locked for revisions")
	case contract.Revision.WindowStart <= s.cm.Tip().Height:
		return rhp.Contract{}, [16]byte{}, errors.New("contract proof window has started")
	case !s.VerifyChallenge(signature, contract.Revision.RenterPublicKey):
		return rhp.Contract{}, [16]byte{}, errors.New("failed to verify challenge signature")
	}

	challenge := frand.Entropy128()
	s.SetChallenge(challenge)
	s.contractID = id
	return contract, [16]byte{}, nil
}

func (s *Session) lockContract(timeout time.Duration) (rhp.Contract, error) {
	s.mu.Lock()
	contract, err := s.contracts.Lock(s.contractID, timeout)
	if err != nil {
		s.mu.Unlock()
		return rhp.Contract{}, fmt.Errorf("failed to lock contract: %w", err)
	}

	// validate the contract is still revisable
	switch {
	case contract.Revision.RevisionNumber == types.MaxRevisionNumber:
		s.mu.Unlock()
		return rhp.Contract{}, errors.New("contract is locked for revisions")
	case contract.Revision.WindowStart <= s.cm.Tip().Height:
		s.mu.Unlock()
		return rhp.Contract{}, errors.New("contract proof window has started")
	}
	return contract, nil
}

func (s *Session) unlockContract() {
	s.mu.Unlock()
	s.contracts.Unlock(s.contractID)
}

func (s *Session) handleRPC(stream *mux.Stream, specifier rpc.Specifier) {
	r := &rpcSession{
		uid:       generateUniqueID(),
		session:   s,
		privkey:   s.privkey,
		stream:    stream,
		cm:        s.cm,
		wallet:    s.wallet,
		tpool:     s.tpool,
		settings:  s.settings,
		accounts:  s.accounts,
		contracts: s.contracts,
		registry:  s.registry,
		sectors:   s.sectors,
		recorder:  s.recorder,
		log:       s.log,
	}

	rpcFn := map[rpc.Specifier]func() error{
		rhp.RPCAccountBalanceID: r.rpcAccountBalance,
		rhp.RPCExecuteProgramID: r.rpcExecuteProgram,
		rhp.RPCFormContractID:   r.rpcFormContract,
		rhp.RPCFundAccountID:    r.rpcFundAccount,
		rhp.RPCLatestRevisionID: r.rpcLatestRevision,
		rhp.RPCLockID:           r.rpcLock,
		rhp.RPCReadID:           r.rpcRead,
		rhp.RPCRenewContractID:  r.rpcRenewContract,
		rhp.RPCSettingsID:       r.rpcSettings,
		rhp.RPCWriteID:          r.rpcWrite,
	}[specifier]
	if rpcFn == nil {
		r.log.Warnln("unrecognized RPC:", specifier)
		rpc.WriteResponseErr(stream, fmt.Errorf("unknown rpc: %v", specifier))
		return
	}

	recordEnd := r.record(specifier)
	err := rpcFn()
	r.refund()
	recordEnd(err)
}
