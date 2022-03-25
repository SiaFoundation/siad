package host

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	rpcSession struct {
		uid     UniqueID
		stream  *mux.Stream
		privkey types.PrivateKey

		session   *Session
		cm        *chain.Manager
		wallet    host.Wallet
		tpool     host.TransactionPool
		settings  *settingsManager
		contracts host.ContractManager
		registry  *host.RegistryManager
		accounts  host.EphemeralAccountStore
		sectors   host.SectorStore
		recorder  MetricRecorder
		log       Logger

		budget        host.Budget
		refundAccount types.PublicKey
	}
)

// validateUsageRevision validates that the correct amount of Siacoin was
// transferred to the host and collateral burnt for RPCWrite and RPCRead.
func validateUsageRevision(existing, revision types.FileContract, storage, usage, collateral types.Currency) error {
	if err := rhp.ValidateStdRevision(existing, revision); err != nil {
		return err
	}
	expectedValidValue := existing.HostOutput.Value.Add(storage).Add(usage)
	expectedMissedValue, underflow := existing.MissedHostValue.Add(usage).SubWithUnderflow(collateral)
	if underflow {
		return fmt.Errorf("not enough remaining collateral: required collateral %v, %v remaining", collateral, existing.MissedHostValue)
	} else if revision.MissedHostValue != expectedMissedValue {
		return fmt.Errorf("expected missed host value %v, but revison has %v", expectedMissedValue, revision.MissedHostValue)
	} else if revision.HostOutput.Value != expectedValidValue {
		return fmt.Errorf("expected valid value %v, but revision has %v", expectedValidValue, revision.HostOutput.Value)
	}
	return nil
}

// refund refunds the remainder of the renter's budget at the end of the RPC.
func (r *rpcSession) refund() {
	v := r.budget.Refund()
	if v.IsZero() {
		return
	}
	r.accounts.Refund(r.refundAccount, v)
}

// readRequest reads an RPC request from the stream.
func (r *rpcSession) readRequest(req rpc.Object) error {
	return rpc.ReadRequest(r.stream, req)
}

// readResponse reads an RPC response from the stream.
func (r *rpcSession) readResponse(resp rpc.Object) error {
	return rpc.ReadResponse(r.stream, resp)
}

// writeErr writes an error response to the stream.
func (r *rpcSession) writeErr(err error) {
	rpc.WriteResponseErr(r.stream, err)
}

// writeResponse writes an RPC response to the stream.
func (r *rpcSession) writeResponse(resp rpc.Object) error {
	return rpc.WriteResponse(r.stream, resp)
}

// note: should we remove connection-level contract locking and stick to more
// flexible stream-level locking?
func (r *rpcSession) rpcLock() error {
	log := r.log.Scope("RPCLock")
	r.stream.SetDeadline(time.Now().Add(time.Second * 30))

	var req rhp.RPCLockRequest
	if err := r.readRequest(&req); err != nil {
		err = fmt.Errorf("failed to read lock request: %w", err)
		log.Warnln(err)
		return err
	}

	contract, newChallenge, err := r.session.setContract(req.ContractID, req.Signature, time.Duration(req.Timeout)*time.Millisecond)
	if err != nil {
		err = fmt.Errorf("failed to set session contract: %w", err)
		log.Warnln(err)
		r.writeErr(errors.New("failed to acquire contract lock"))
		return err
	}

	resp := rhp.RPCLockResponse{
		Acquired:     true,
		NewChallenge: newChallenge,
		Revision:     contract.Revision,
	}
	if err := r.writeResponse(&resp); err != nil {
		err = fmt.Errorf("failed to write lock response: %w", err)
		log.Warnln(err)
		return err
	}
	return nil
}

func (r *rpcSession) rpcRead() error {
	log := r.log.Scope("RPCRead")
	r.stream.SetDeadline(time.Now().Add(time.Minute))

	var req rhp.RPCReadRequest
	if err := rpc.ReadRequest(r.stream, &req); err != nil {
		err = fmt.Errorf("failed to read request: %w", err)
		log.Warnln(err)
		r.writeErr(errors.New("failed to read request"))
		return err
	}

	// lock the session's contract
	contract, err := r.session.lockContract(time.Second * 10)
	if err != nil {
		err = fmt.Errorf("failed to lock contract: %w", err)
		log.Warnln(err)
		r.writeErr(errors.New("failed to lock contract"))
		return err
	}
	defer r.session.unlockContract()

	// validate the requested sections
	for i, sec := range req.Sections {
		switch {
		case uint64(sec.Offset)+uint64(sec.Length) > rhp.SectorSize:
			err = fmt.Errorf("section %v is out of bounds", i)
		case sec.Length == 0:
			err = fmt.Errorf("section %v length cannot be zero", i)
		case req.MerkleProof && (sec.Offset%rhp.LeafSize != 0 || sec.Length%rhp.LeafSize != 0):
			err = errors.New("offset and length must be multiples of SegmentSize when requesting a Merkle proof")
		}
		if err != nil {
			log.Warnln(err)
			r.writeErr(err)
			return err
		}
	}

	// create a new contract revision
	revision := contract.Revision
	revision.RevisionNumber = req.NewRevisionNumber
	revision.RenterSignature = req.Signature
	req.NewOutputs.Apply(&revision)

	vc := r.cm.TipContext()
	sigHash := vc.ContractSigHash(revision)
	if !revision.RenterPublicKey.VerifyHash(sigHash, revision.RenterSignature) {
		err = errors.New("revision signature is invalid")
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	// validate the renter transferred enough funds to cover the cost of the
	// reads.
	cost := rhp.RPCReadRenterCost(r.settings.Settings(), req.Sections)
	if err := validateUsageRevision(contract.Revision, revision, types.ZeroCurrency, cost, types.ZeroCurrency); err != nil {
		err = fmt.Errorf("failed to validate revision: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	// send the host signature to the renter
	revision.HostSignature = r.privkey.SignHash(sigHash)
	resp := rhp.RPCReadResponse{
		Signature: revision.HostSignature,
	}
	if err := r.writeResponse(&resp); err != nil {
		err = fmt.Errorf("failed to write read response: %w", err)
		log.Warnln(err)
		return err
	}

	buf := bytes.NewBuffer(make([]byte, 0, rhp.SectorSize))
	for _, sec := range req.Sections {
		// reset the deadline and buffer for each requested section
		r.stream.SetDeadline(time.Now().Add(time.Minute))
		buf.Reset()

		// read the segment
		_, err = r.sectors.Read(sec.MerkleRoot, buf, 0, rhp.SectorSize)
		if err != nil {
			err = fmt.Errorf("failed to read sector %v data: %w", sec.MerkleRoot, err)
			log.Warnln(err)
			r.writeErr(err)
			return err
		}

		// build the Merkle proof
		// note: the read RPC always validates the proof regardless. Should
		// RPCReadRequest.MerkleProof be rmeoved?
		proofStart := int(sec.Offset) / rhp.LeafSize
		proofEnd := int(sec.Offset+sec.Length) / rhp.LeafSize
		var sector [rhp.SectorSize]byte
		copy(sector[:], buf.Bytes())
		proof := rhp.BuildProof(&sector, proofStart, proofEnd, nil)
		// write the segment to the stream
		if _, err := r.stream.Write(sector[sec.Offset : sec.Offset+sec.Length]); err != nil {
			err = fmt.Errorf("failed to write sector %v data: %w", sec.MerkleRoot, err)
			log.Warnln(err)
			return err
		}
		// write the proof to the stream
		for _, p := range proof {
			if _, err := r.stream.Write(p[:]); err != nil {
				err = fmt.Errorf("failed to write sector %v proof: %w", sec.MerkleRoot, err)
				log.Warnln(err)
				return err
			}
		}
	}
	return nil
}

func (r *rpcSession) rpcWrite() error {
	log := r.log.Scope("RPCWrite")
	r.stream.SetDeadline(time.Now().Add(time.Minute))

	var req rhp.RPCWriteRequest
	if err := rpc.ReadObject(r.stream, &req); err != nil {
		err = fmt.Errorf("failed to read request: %w", err)
		log.Warnln(err)
		r.writeErr(errors.New("failed to read request"))
		return err
	}

	// lock the session's contract
	contract, err := r.session.lockContract(time.Second * 10)
	if err != nil {
		err = fmt.Errorf("failed to lock contract: %w", err)
		log.Warnln(err)
		r.writeErr(errors.New("failed to lock contract"))
		return err
	}
	defer r.session.unlockContract()

	// validate the requested sections.
	// note: the renter proto currently only supports a single append action, so
	// the host will also only support Append.
	if len(req.Actions) != 1 {
		err = errors.New("only a single append action is supported")
		r.writeErr(err)
		return err
	}

	for i, action := range req.Actions {
		switch action.Type {
		case rhp.RPCWriteActionAppend:
			if len(action.Data) != rhp.SectorSize {
				err = fmt.Errorf("action %v invalid: append data must be %v bytes", i, rhp.SectorSize)
			}
		// TODO: support other actions
		default:
			err = fmt.Errorf("action %v invalid: unknown action type %v", i, action.Type)
		}
		if err != nil {
			log.Warnln(err)
			r.writeErr(err)
			return err
		}
	}

	// create a new contract revision
	revision := contract.Revision
	revision.RevisionNumber = req.NewRevisionNumber
	req.NewOutputs.Apply(&revision)

	vc := r.cm.TipContext()
	settings := r.settings.Settings()
	// validate the renter transferred enough funds to cover the cost of the
	// writes and enough collateral was burnt.
	storage, usage := rhp.RPCWriteRenterCost(settings, contract.Revision, req.Actions)
	collateral := rhp.RPCWriteHostCollateral(settings, contract.Revision, req.Actions)
	if err := validateUsageRevision(contract.Revision, revision, storage, usage, collateral); err != nil {
		err = fmt.Errorf("failed to validate revision: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	// get the contract's sector roots
	sectorRoots, err := r.contracts.Roots(contract.ID)
	if err != nil {
		err = fmt.Errorf("failed to get contract %v roots: %w", contract.ID, err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	// keep state in memory to commit or revert the actions
	newRoots := append([]types.Hash256(nil), sectorRoots...)
	gainedSectors := make(map[types.Hash256]uint64)
	removedSectors := make(map[types.Hash256]uint64)
	revert := func() {
		for root, n := range gainedSectors {
			if n <= 0 {
				continue
			} else if err := r.sectors.Delete(root, n); err != nil {
				log.Warnf("unable to delete sector %v: %v", root, err)
			}
		}
	}
	commit := func() {
		for root, n := range removedSectors {
			if n <= 0 {
				continue
			} else if err := r.sectors.Delete(root, n); err != nil {
				log.Warnf("unable to delete sector %v: %v", root, err)
			}
			delete(removedSectors, root)
		}
		// reset the gained sectors so revert is a no-op
		gainedSectors = make(map[types.Hash256]uint64)
	}
	defer revert()

	for i, action := range req.Actions {
		switch action.Type {
		case rhp.RPCWriteActionAppend:
			var sector [rhp.SectorSize]byte
			copy(sector[:], action.Data)
			root := rhp.SectorRoot(&sector)
			if err := r.sectors.Add(root, &sector); err != nil {
				err = fmt.Errorf("action %v failed: failed to append sector %v: %w", i, root, err)
				log.Warnln(err)
				r.writeErr(err)
				return err
			}
			gainedSectors[root]++
			newRoots = append(newRoots, root)
		// TODO: support other actions
		default:
			panic("unknown write action type: " + action.Type.String())
		}
	}

	revision.FileMerkleRoot = rhp.MetaRoot(newRoots)
	revision.Filesize = uint64(len(newRoots)) * rhp.SectorSize
	resp := rhp.RPCWriteMerkleProof{
		OldSubtreeHashes: sectorRoots,
		NewMerkleRoot:    revision.FileMerkleRoot,
	}
	resp.OldSubtreeHashes = rhp.BuildAppendProof(sectorRoots)
	if err := r.writeResponse(&resp); err != nil {
		err = fmt.Errorf("failed to write merkle proof response: %w", err)
		log.Warnln(err)
		return err
	}

	// validate and exchange revision signatures
	sigHash := vc.ContractSigHash(revision)
	var renterSigResp rhp.RPCWriteResponse
	if err := r.readResponse(&renterSigResp); err != nil {
		err = fmt.Errorf("failed to read renter signature response: %w", err)
		log.Warnln(err)
		return err
	} else if !contract.Revision.RenterPublicKey.VerifyHash(sigHash, renterSigResp.Signature) {
		err = fmt.Errorf("renter signature is invalid")
		log.Warnln(err)
		r.writeErr(err)
		return err
	}
	revision.RenterSignature = renterSigResp.Signature
	revision.HostSignature = r.privkey.SignHash(sigHash)
	contract.Revision = revision

	// update the stored contract's roots and revision
	if err := r.contracts.SetRoots(contract.ID, newRoots); err != nil {
		err = fmt.Errorf("failed to set contract %v roots: %w", contract.ID, err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err := r.contracts.Revise(contract); err != nil {
		err = fmt.Errorf("failed to revise contract %v: %w", contract.ID, err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}
	// now that the roots are set and the contract is revised, commit the sector
	// changes.
	commit()

	// send the host signature to the renter
	hostSigs := rhp.RPCWriteResponse{
		Signature: revision.HostSignature,
	}
	if err := r.writeResponse(&hostSigs); err != nil {
		err = fmt.Errorf("failed to write host signature response: %w", err)
		log.Warnln(err)
		return err
	}
	return nil
}

func (r *rpcSession) rpcAccountBalance() error {
	log := r.log.Scope("RPCAccountBalance")
	r.stream.SetDeadline(time.Now().Add(time.Second * 30))

	var settingsID rhp.SettingsID
	if err := rpc.ReadObject(r.stream, &settingsID); err != nil {
		return fmt.Errorf("failed to read settings UID: %w", err)
	}

	settings, err := r.settings.valid(settingsID)
	if err != nil {
		err = fmt.Errorf("failed to get settings %v: %w", settingsID, err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err = r.processPayment(); err != nil {
		err = fmt.Errorf("failed to process payment: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err := r.budget.Spend(settings.RPCAccountBalanceCost); err != nil {
		err = fmt.Errorf("failed to pay for RPC: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	var req rhp.RPCAccountBalanceRequest
	if err = r.readResponse(&req); err != nil {
		err = fmt.Errorf("failed to read account balance request: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	balance, err := r.accounts.Balance(req.AccountID)
	if err != nil {
		err = fmt.Errorf("failed to get account balance: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	resp := &rhp.RPCAccountBalanceResponse{
		Balance: balance,
	}
	if err = r.writeResponse(resp); err != nil {
		err = fmt.Errorf("failed to write account balance response: %w", err)
		log.Warnln(err)
		return err
	}
	return nil
}

func (r *rpcSession) rpcFundAccount() error {
	log := r.log.Scope("RPCFundAccount")
	r.stream.SetDeadline(time.Now().Add(time.Second * 30))

	var settingsID rhp.SettingsID
	if err := rpc.ReadObject(r.stream, &settingsID); err != nil {
		err = fmt.Errorf("failed to read settings UID: %w", err)
		log.Warnln(err)
		return err
	}

	settings, err := r.settings.valid(settingsID)
	if err != nil {
		err = fmt.Errorf("failed to get settings %v: %w", settingsID, err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err = r.processPayment(); err != nil {
		err = fmt.Errorf("failed to process payment: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err := r.budget.Spend(settings.RPCFundAccountCost); err != nil {
		err = fmt.Errorf("failed to pay for RPC: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	var req rhp.RPCFundAccountRequest
	if err = r.readResponse(&req); err != nil {
		err = fmt.Errorf("failed to read fund account request: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	// fund the account with the remaining funds
	fundAmount := r.budget.Remaining()
	balance, err := r.accounts.Credit(req.AccountID, fundAmount)
	if err != nil {
		err = fmt.Errorf("failed to credit account: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}
	// clear the budget now that the account is funded.
	r.budget.Refund()

	receipt := rhp.Receipt{
		Host:      r.privkey.PublicKey(),
		Account:   req.AccountID,
		Amount:    fundAmount,
		Timestamp: time.Now(),
	}
	resp := &rhp.RPCFundAccountResponse{
		Balance:   balance,
		Receipt:   receipt,
		Signature: r.privkey.SignHash(receipt.SigHash()),
	}

	// write the receipt and current balance.
	err = r.writeResponse(resp)
	if err != nil {
		err = fmt.Errorf("failed to write fund account response: %w", err)
		log.Warnln(err)
		return err
	}
	return nil
}

func (r *rpcSession) rpcLatestRevision() error {
	log := r.log.Scope("RPCLatestRevision")
	r.stream.SetDeadline(time.Now().Add(time.Second * 30))

	var settingsID rhp.SettingsID
	if err := rpc.ReadObject(r.stream, &settingsID); err != nil {
		err = fmt.Errorf("failed to read settings UID: %w", err)
		log.Warnln(err)
		return err
	}

	settings, err := r.settings.valid(settingsID)
	if err != nil {
		err = fmt.Errorf("failed to get settings %v: %w", settingsID, err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err = r.processPayment(); err != nil {
		err = fmt.Errorf("failed to process payment: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err := r.budget.Spend(settings.RPCLatestRevisionCost); err != nil {
		err = fmt.Errorf("failed to pay for RPC: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	var req rhp.RPCLatestRevisionRequest
	if err = r.readResponse(&req); err != nil {
		err = fmt.Errorf("failed to read latest revision request: %w", err)
		log.Warnln("failed to read latest revision request:", err)
		r.writeErr(err)
		return err
	}

	contract, err := r.contracts.Lock(req.ContractID, time.Second*10)
	if err != nil {
		err = fmt.Errorf("failed to lock contract %v: %w", req.ContractID, err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}
	r.contracts.Unlock(req.ContractID)

	resp := &rhp.RPCLatestRevisionResponse{
		Revision: contract,
	}
	if err := r.writeResponse(resp); err != nil {
		err = fmt.Errorf("failed to write latest revision response: %w", err)
		log.Warnln("failed to write latest revision response:", err)
		return err
	}
	return nil
}

func (r *rpcSession) rpcSettings() error {
	log := r.log.Scope("RPCSettings")
	r.stream.SetDeadline(time.Now().Add(time.Second * 30))

	settings := r.settings.Settings()
	buf, err := json.Marshal(settings)
	if err != nil {
		err = fmt.Errorf("failed to marshal settings: %w", err)
		log.Errorln(err)
		return err
	}

	// write the settings to the stream. The settings are sent before payment so
	// the renter can determine if they want to continue interacting with the
	// host.
	err = r.writeResponse(&rhp.RPCSettingsResponse{
		Settings: buf,
	})
	if err != nil {
		err = fmt.Errorf("failed to write settings response: %w", err)
		log.Warnln(err)
		return err
	}

	// process the payment, catch connection closed and EOF errors since the renter
	// likely did not intend to pay.
	err = r.processPayment()
	if errors.Is(err, mux.ErrClosedConn) || errors.Is(err, mux.ErrClosedStream) || errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		err = fmt.Errorf("failed to process payment: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err := r.budget.Spend(settings.RPCHostSettingsCost); err != nil {
		err = fmt.Errorf("failed to pay for settings RPC: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	// track the settings so the renter can reference it in later RPC.
	resp := rhp.RPCSettingsRegisteredResponse{
		ID: frand.Entropy128(),
	}
	r.settings.register(resp.ID, settings)

	// write the registered settings ID to the stream.
	if err := r.writeResponse(&resp); err != nil {
		err = fmt.Errorf("failed to write settings registered response: %w", err)
		log.Warnln(err)
		return err
	}
	return nil
}

func (r *rpcSession) rpcFormContract() error {
	log := r.log.Scope("RPCFormContract")
	r.stream.SetDeadline(time.Now().Add(time.Minute))

	settings := r.settings.Settings()
	if !settings.AcceptingContracts {
		err := errors.New("failed to form contract: host is not accepting contracts")
		r.writeErr(err)
		return err
	}

	var formContractReq rhp.RPCFormContractRequest
	if err := rpc.ReadObject(r.stream, &formContractReq); err != nil {
		err = fmt.Errorf("failed to read form contract request: %w", err)
		log.Warnln(err)
		return err
	}

	initial := formContractReq.Contract

	// verify the renter's signature and sign the new contract
	vc := r.cm.TipContext()
	sigHash := vc.ContractSigHash(initial)
	if !initial.RenterPublicKey.VerifyHash(sigHash, initial.RenterSignature) {
		err := errors.New("failed to verify renter signature")
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err := rhp.ValidateContractFormation(initial, vc.Index.Height, settings); err != nil {
		err = fmt.Errorf("failed to validate contract: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}
	initial.HostSignature = r.privkey.SignHash(sigHash)
	formationTxn := types.Transaction{
		SiacoinInputs:  formContractReq.Inputs,
		SiacoinOutputs: formContractReq.Outputs,
		MinerFee:       formContractReq.MinerFee,
		FileContracts:  []types.FileContract{initial},
	}

	// Fund the formation transaction with the host's collateral.
	renterInputs, renterOutputs := len(formationTxn.SiacoinInputs), len(formationTxn.SiacoinOutputs)
	toSign, cleanup, err := r.wallet.FundTransaction(&formationTxn, initial.TotalCollateral, r.tpool.Transactions())
	if err != nil {
		err = fmt.Errorf("failed to fund formation transaction: %w", err)
		log.Warnln(err)
		// should not write the failure reason to the stream
		r.writeErr(errors.New("failed to form contract"))
		return err
	}
	defer cleanup()

	// send the host's inputs and contract signature
	hostAdditions := &rhp.RPCFormContractHostAdditions{
		Inputs:            formationTxn.SiacoinInputs[renterInputs:],
		Outputs:           formationTxn.SiacoinOutputs[renterOutputs:],
		ContractSignature: initial.HostSignature,
	}
	if err := r.writeResponse(hostAdditions); err != nil {
		err = fmt.Errorf("failed to write host additions: %w", err)
		log.Warnln(err)
		return err
	}

	// read the renter signatures from the stream.
	var renterSigs rhp.RPCContractSignatures
	if err := r.readResponse(&renterSigs); err != nil {
		err = fmt.Errorf("failed to read renter signatures: %w", err)
		log.Warnln(err)
		return err
	}
	// add the renter's signatures to the transaction
	for i := range renterSigs.SiacoinInputSignatures {
		formationTxn.SiacoinInputs[i].Signatures = renterSigs.SiacoinInputSignatures[i]
	}

	// sign the transaction
	if err := r.wallet.SignTransaction(vc, &formationTxn, toSign); err != nil {
		err = fmt.Errorf("failed to sign transaction: %w", err)
		log.Errorln(err)
		// should not write failure reason to stream
		r.writeErr(errors.New("failed to form contract"))
		return err
	}

	hostSigs := &rhp.RPCContractSignatures{
		SiacoinInputSignatures: make([][]types.Signature, len(formationTxn.SiacoinInputs)-renterInputs),
	}
	for i, si := range formationTxn.SiacoinInputs[renterInputs:] {
		hostSigs.SiacoinInputSignatures[i] = si.Signatures
	}

	contract := rhp.Contract{
		ID:       formationTxn.FileContractID(0),
		Revision: initial,
	}

	if err := vc.ValidateTransaction(formationTxn); err != nil {
		err = fmt.Errorf("failed to validate transaction: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err := r.contracts.Add(contract, formationTxn); err != nil {
		err = fmt.Errorf("failed to add contract: %w", err)
		log.Errorln(err)
		// should not write failure reason to stream
		r.writeErr(errors.New("failed to form contract"))
		return err
	} else if err := r.tpool.AddTransaction(formationTxn); err != nil {
		err = fmt.Errorf("failed to broadcast transaction: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err := r.writeResponse(hostSigs); err != nil {
		err = fmt.Errorf("failed to write host signatures: %w", err)
		log.Warnln(err)
		return err
	}

	r.recorder.RecordEvent(EventContractFormed{
		RPCUID:     r.uid,
		SessionUID: r.session.uid,
		Contract:   contract,
	})
	return nil
}

func (r *rpcSession) rpcRenewContract() error {
	log := r.log.Scope("RPCRenewContract")
	r.stream.SetDeadline(time.Now().Add(time.Minute))

	var settingsID rhp.SettingsID
	if err := rpc.ReadObject(r.stream, &settingsID); err != nil {
		err = fmt.Errorf("failed to read settings id: %w", err)
		log.Warnln(err)
		return err
	}

	settings, err := r.settings.valid(settingsID)
	if err != nil {
		err = fmt.Errorf("failed to get settings %v: %w", settingsID, err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	var renewContractReq rhp.RPCRenewContractRequest
	if err := rpc.ReadRequest(r.stream, &renewContractReq); err != nil {
		err = fmt.Errorf("failed to read renew contract request: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	// check that the renter sent a resolution with a renewal
	if !renewContractReq.Resolution.HasRenewal() {
		err = fmt.Errorf("contract resolution must have renewal")
		log.Warnln("renewal transaction does not contain a renewal resolution")
		r.writeErr(err)
		return err
	}

	renewalTxn := types.Transaction{
		SiacoinInputs:           renewContractReq.Inputs,
		SiacoinOutputs:          renewContractReq.Outputs,
		MinerFee:                renewContractReq.MinerFee,
		FileContractResolutions: []types.FileContractResolution{renewContractReq.Resolution},
	}

	existingID := renewContractReq.Resolution.Parent.ID
	existing, err := r.contracts.Lock(existingID, time.Second*10)
	if err != nil {
		err = fmt.Errorf("failed to lock contract %v: %w", existingID, err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}
	defer r.contracts.Unlock(existingID)

	vc := r.cm.TipContext()

	// validate the renter's signatures, renewal, and finalization
	finalRevisionSigHash := vc.ContractSigHash(renewalTxn.FileContractResolutions[0].Renewal.FinalRevision)
	initialSigHash := vc.ContractSigHash(renewalTxn.FileContractResolutions[0].Renewal.InitialRevision)
	if !existing.Revision.RenterPublicKey.VerifyHash(finalRevisionSigHash, renewalTxn.FileContractResolutions[0].Renewal.FinalRevision.RenterSignature) {
		err = fmt.Errorf("renewal %v failed: invalid finalization signature", existingID)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if !existing.Revision.RenterPublicKey.VerifyHash(initialSigHash, renewalTxn.FileContractResolutions[0].Renewal.InitialRevision.RenterSignature) {
		err = fmt.Errorf("renewal %v failed: invalid contract signature", existingID)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err := rhp.ValidateContractRenewal(existing.Revision, renewalTxn.FileContractResolutions[0].Renewal.InitialRevision, vc.Index.Height, settings); err != nil {
		err = fmt.Errorf("renewal %v invalid: %w", existingID, err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if err := rhp.ValidateContractFinalization(existing.Revision, renewalTxn.FileContractResolutions[0].Renewal.FinalRevision); err != nil {
		err = fmt.Errorf("finalization %v invalid: %w", existingID, err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	// calculate the "base" storage cost to the renter and risked collateral for
	// the host for the data already in the contract. If the contract height did
	// not increase, base costs are zero since the storage is already paid for.
	var baseStorageCost, baseCollateral types.Currency
	initial := renewalTxn.FileContractResolutions[0].Renewal.InitialRevision
	if initial.WindowEnd > existing.Revision.WindowEnd {
		extension := initial.WindowEnd - existing.Revision.WindowEnd
		baseStorageCost = settings.StoragePrice.Mul64(initial.Filesize).Mul64(extension)
		baseCollateral = settings.Collateral.Mul64(initial.Filesize).Mul64(extension)
	}

	additionalCollateral := initial.TotalCollateral.Sub(baseCollateral)
	validHostValue := settings.ContractFee.Add(baseStorageCost).Add(initial.TotalCollateral)
	missedHostValue := settings.ContractFee.Add(baseStorageCost).Add(additionalCollateral)

	// calculate the amount the host needs to fund as the difference between the
	// valid proof output, base storage revenue, and contract fee. The renter is
	// responsible for funding the remainder.
	if initial.HostOutput.Value.Cmp(validHostValue) != 0 {
		err := fmt.Errorf("renewal %v rejected: expected %v host value, received %v", existingID, validHostValue, initial.HostOutput.Value)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if initial.MissedHostValue.Cmp(missedHostValue) != 0 {
		err := fmt.Errorf("renewal %v rejected: expected %v host missed value, received %v", existingID, missedHostValue, initial.MissedHostValue)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if initial.TotalCollateral.Cmp(settings.MaxCollateral) > 0 {
		err := fmt.Errorf("renewal %v rejected: host max collateral is %v, expected to post %v", existingID, settings.MaxCollateral, initial.TotalCollateral)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	// sign the renewal and finalization.
	renewalTxn.FileContractResolutions[0].Renewal.InitialRevision.HostSignature = r.privkey.SignHash(initialSigHash)
	renewalTxn.FileContractResolutions[0].Renewal.FinalRevision.HostSignature = r.privkey.SignHash(finalRevisionSigHash)
	renewalSigHash := vc.RenewalSigHash(renewalTxn.FileContractResolutions[0].Renewal)
	renewalTxn.FileContractResolutions[0].Renewal.HostSignature = r.privkey.SignHash(renewalSigHash)

	renterInputs, renterOutputs := len(renewalTxn.SiacoinInputs), len(renewalTxn.SiacoinOutputs)
	toSign, cleanup, err := r.wallet.FundTransaction(&renewalTxn, initial.TotalCollateral, r.tpool.Transactions())
	if err != nil {
		err = fmt.Errorf("failed to fund renewal transaction: %w", err)
		log.Warnln(err)
		// should not send the actual error to the renter, just a generic message.
		r.writeErr(errors.New("failed to renew contract"))
		return err
	}
	defer cleanup()

	// send the renter the host's unsigned inputs, additional outputs, and
	// contract signatures.
	hostAdditions := &rhp.RPCRenewContractHostAdditions{
		Inputs:  renewalTxn.SiacoinInputs[renterInputs:],
		Outputs: renewalTxn.SiacoinOutputs[renterOutputs:],
		// TODO: should hosts use rollover? Currently assumes that the host does
		// not want to rollover anything into the contract and should use new funds.
		HostRollover:          types.ZeroCurrency,
		FinalizationSignature: renewalTxn.FileContractResolutions[0].Renewal.FinalRevision.HostSignature,
		InitialSignature:      renewalTxn.FileContractResolutions[0].Renewal.InitialRevision.HostSignature,
		RenewalSignature:      renewalTxn.FileContractResolutions[0].Renewal.HostSignature,
	}

	// write the host transaction additions.
	if err := r.writeResponse(hostAdditions); err != nil {
		err = fmt.Errorf("failed to write host additions: %w", err)
		log.Warnln(err)
		return err
	}

	// read the renter signatures from the stream.
	var renterSigs rhp.RPCRenewContractRenterSignatures
	if err := r.readResponse(&renterSigs); err != nil {
		err = fmt.Errorf("failed to read renter signatures: %w", err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	} else if !existing.Revision.RenterPublicKey.VerifyHash(renewalSigHash, renterSigs.RenewalSignature) {
		err = fmt.Errorf("renewal %v failed: renter signature invalid", existingID)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	// add the renter's signatures to the transaction
	for i := range renterSigs.SiacoinInputSignatures {
		renewalTxn.SiacoinInputs[i].Signatures = renterSigs.SiacoinInputSignatures[i]
	}
	renewalTxn.FileContractResolutions[0].Renewal.RenterSignature = renterSigs.RenewalSignature
	if err := r.wallet.SignTransaction(vc, &renewalTxn, toSign); err != nil {
		err = fmt.Errorf("failed to sign renewal transaction: %w", err)
		log.Errorln(err)
		// should not send the actual error to the renter, just a generic message.
		r.writeErr(errors.New("failed to renew contract"))
		return err
	}
	hostSigs := &rhp.RPCContractSignatures{
		SiacoinInputSignatures: make([][]types.Signature, len(renewalTxn.SiacoinInputs)-renterInputs),
	}
	for i, si := range renewalTxn.SiacoinInputs[renterInputs:] {
		hostSigs.SiacoinInputSignatures[i] = si.Signatures
	}

	// validate the renewal transactions
	if err := vc.ValidateTransaction(renewalTxn); err != nil {
		err = fmt.Errorf("renewal %v failed: transaction invalid: %w", existingID, err)
		log.Warnln(err)
		r.writeErr(err)
		return err
	}

	existing.Revision = renewalTxn.FileContractResolutions[0].Renewal.FinalRevision
	renewed := rhp.Contract{
		ID: types.ElementID{
			Source: types.Hash256(renewalTxn.ID()),
			// the renewal request is restricted to only siacoin inputs and
			// outputs so the index can be reduced to len(siacoin outputs).
			Index: uint64(len(renewalTxn.SiacoinOutputs)),
		},
		Revision: renewalTxn.FileContractResolutions[0].Renewal.InitialRevision,
	}

	if err := r.contracts.Revise(existing); err != nil {
		err = fmt.Errorf("failed to revise existing contract %v: %w", existingID, err)
		log.Errorln(err)
		r.writeErr(errors.New("failed to renew contract"))
		return err
	} else if err := r.contracts.Add(renewed, renewalTxn); err != nil {
		err = fmt.Errorf("failed to add renewed contract %v: %w", renewed.ID, err)
		log.Errorln(err)
		r.writeErr(errors.New("failed to renew contract"))
		return err
	} else if err := r.tpool.AddTransaction(renewalTxn); err != nil {
		err = fmt.Errorf("failed to broadcast renewed transaction: %w", err)
		log.Errorln(err)
		r.writeErr(err)
		return err
	} else if err := r.writeResponse(hostSigs); err != nil {
		err = fmt.Errorf("failed to write host signatures: %w", err)
		log.Warnln(err)
		return err
	}
	r.recorder.RecordEvent(EventContractRenewed{
		RPCUID:            r.uid,
		SessionUID:        r.session.uid,
		RenewedContract:   renewed,
		FinalizedContract: existing,
	})
	return nil
}
