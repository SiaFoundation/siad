package proto

import (
	"net"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/errors"
)

// FormContract forms a contract with a host and submits the contract
// transaction to tpool. The contract is added to the ContractSet and its
// metadata is returned.
func (cs *ContractSet) FormContract(params ContractParams, txnBuilder transactionBuilder, tpool transactionPool, hdb hostDB, cancel <-chan struct{}) (rc modules.RenterContract, err error) {
	// use the new renter-host protocol for hosts v1.4.0 or above
	if build.VersionCmp(params.Host.Version, "1.4.0") >= 0 {
		return cs.newFormContract(params, txnBuilder, tpool, hdb, cancel)
	}
	return cs.oldFormContract(params, txnBuilder, tpool, hdb, cancel)
}

func (cs *ContractSet) oldFormContract(params ContractParams, txnBuilder transactionBuilder, tpool transactionPool, hdb hostDB, cancel <-chan struct{}) (rc modules.RenterContract, err error) {
	// Extract vars from params, for convenience.
	allowance, host, funding, startHeight, endHeight, refundAddress := params.Allowance, params.Host, params.Funding, params.StartHeight, params.EndHeight, params.RefundAddress

	// Create our key.
	ourSK, ourPK := crypto.GenerateKeyPair()
	// Create unlock conditions.
	uc := types.UnlockConditions{
		PublicKeys: []types.SiaPublicKey{
			types.Ed25519PublicKey(ourPK),
			host.PublicKey,
		},
		SignaturesRequired: 2,
	}

	// Calculate the anticipated transaction fee.
	_, maxFee := tpool.FeeEstimation()
	txnFee := maxFee.Mul64(modules.EstimatedFileContractTransactionSetSize)

	// Calculate the payouts for the renter, host, and whole contract.
	period := endHeight - startHeight
	expectedStorage := allowance.ExpectedStorage / allowance.Hosts
	renterPayout, hostPayout, _, err := modules.RenterPayoutsPreTax(host, funding, txnFee, types.ZeroCurrency, types.ZeroCurrency, period, expectedStorage)
	if err != nil {
		return modules.RenterContract{}, err
	}
	totalPayout := renterPayout.Add(hostPayout)

	// Check for negative currency.
	if types.PostTax(startHeight, totalPayout).Cmp(hostPayout) < 0 {
		return modules.RenterContract{}, errors.New("not enough money to pay both siafund fee and also host payout")
	}
	// Create file contract.
	fc := types.FileContract{
		FileSize:       0,
		FileMerkleRoot: crypto.Hash{}, // no proof possible without data
		WindowStart:    endHeight,
		WindowEnd:      endHeight + host.WindowSize,
		Payout:         totalPayout,
		UnlockHash:     uc.UnlockHash(),
		RevisionNumber: 0,
		ValidProofOutputs: []types.SiacoinOutput{
			// Outputs need to account for tax.
			{Value: types.PostTax(startHeight, totalPayout).Sub(hostPayout), UnlockHash: refundAddress}, // This is the renter payout, but with tax applied.
			// Collateral is returned to host.
			{Value: hostPayout, UnlockHash: host.UnlockHash},
		},
		MissedProofOutputs: []types.SiacoinOutput{
			// Same as above.
			{Value: types.PostTax(startHeight, totalPayout).Sub(hostPayout), UnlockHash: refundAddress},
			// Same as above.
			{Value: hostPayout, UnlockHash: host.UnlockHash},
			// Once we start doing revisions, we'll move some coins to the host and some to the void.
			{Value: types.ZeroCurrency, UnlockHash: types.UnlockHash{}},
		},
	}

	// Build transaction containing fc, e.g. the File Contract.
	err = txnBuilder.FundSiacoins(funding)
	if err != nil {
		return modules.RenterContract{}, err
	}
	txnBuilder.AddFileContract(fc)
	// Add miner fee.
	txnBuilder.AddMinerFee(txnFee)
	// Add FileContract identifier.
	fcTxn, _ := txnBuilder.View()
	si, err := PrefixedSignedIdentifier(params.RenterSeed, fcTxn.SiacoinInputs[0])
	if err != nil {
		return modules.RenterContract{}, errors.AddContext(err, "failed to create signed identifier")
	}
	_ = txnBuilder.AddArbitraryData(si[:])

	// Create initial transaction set.
	txn, parentTxns := txnBuilder.View()
	unconfirmedParents, err := txnBuilder.UnconfirmedParents()
	if err != nil {
		return modules.RenterContract{}, err
	}
	txnSet := append(unconfirmedParents, append(parentTxns, txn)...)

	// Increase Successful/Failed interactions accordingly
	defer func() {
		if err != nil {
			hdb.IncrementFailedInteractions(host.PublicKey)
			err = errors.Extend(err, modules.ErrHostFault)
		} else {
			hdb.IncrementSuccessfulInteractions(host.PublicKey)
		}
	}()

	// Initiate connection.
	dialer := &net.Dialer{
		Cancel:  cancel,
		Timeout: connTimeout,
	}
	conn, err := dialer.Dial("tcp", string(host.NetAddress))
	if err != nil {
		return modules.RenterContract{}, err
	}
	defer func() { _ = conn.Close() }()

	// Allot time for sending RPC ID + verifySettings.
	extendDeadline(conn, modules.NegotiateSettingsTime)
	if err = encoding.WriteObject(conn, modules.RPCFormContract); err != nil {
		return modules.RenterContract{}, err
	}

	// Verify the host's settings and confirm its identity.
	host, err = verifySettings(conn, host)
	if err != nil {
		return modules.RenterContract{}, err
	}
	if !host.AcceptingContracts {
		return modules.RenterContract{}, errors.New("host is not accepting contracts")
	}

	// Allot time for negotiation.
	extendDeadline(conn, modules.NegotiateFileContractTime)

	// Send acceptance, txn signed by us, and pubkey.
	if err = modules.WriteNegotiationAcceptance(conn); err != nil {
		return modules.RenterContract{}, errors.New("couldn't send initial acceptance: " + err.Error())
	}
	if err = encoding.WriteObject(conn, txnSet); err != nil {
		return modules.RenterContract{}, errors.New("couldn't send the contract signed by us: " + err.Error())
	}
	if err = encoding.WriteObject(conn, ourSK.PublicKey()); err != nil {
		return modules.RenterContract{}, errors.New("couldn't send our public key: " + err.Error())
	}

	// Read acceptance and txn signed by host.
	if err = modules.ReadNegotiationAcceptance(conn); err != nil {
		return modules.RenterContract{}, errors.New("host did not accept our proposed contract: " + err.Error())
	}
	// Host now sends any new parent transactions, inputs and outputs that
	// were added to the transaction.
	var newParents []types.Transaction
	var newInputs []types.SiacoinInput
	var newOutputs []types.SiacoinOutput
	if err = encoding.ReadObject(conn, &newParents, types.BlockSizeLimit); err != nil {
		return modules.RenterContract{}, errors.New("couldn't read the host's added parents: " + err.Error())
	}
	if err = encoding.ReadObject(conn, &newInputs, types.BlockSizeLimit); err != nil {
		return modules.RenterContract{}, errors.New("couldn't read the host's added inputs: " + err.Error())
	}
	if err = encoding.ReadObject(conn, &newOutputs, types.BlockSizeLimit); err != nil {
		return modules.RenterContract{}, errors.New("couldn't read the host's added outputs: " + err.Error())
	}

	// Merge txnAdditions with txnSet.
	txnBuilder.AddParents(newParents)
	for _, input := range newInputs {
		txnBuilder.AddSiacoinInput(input)
	}
	for _, output := range newOutputs {
		txnBuilder.AddSiacoinOutput(output)
	}

	// Sign the txn.
	signedTxnSet, err := txnBuilder.Sign(true)
	if err != nil {
		return modules.RenterContract{}, modules.WriteNegotiationRejection(conn, errors.New("failed to sign transaction: "+err.Error()))
	}

	// Calculate signatures added by the transaction builder.
	var addedSignatures []types.TransactionSignature
	_, _, _, addedSignatureIndices := txnBuilder.ViewAdded()
	for _, i := range addedSignatureIndices {
		addedSignatures = append(addedSignatures, signedTxnSet[len(signedTxnSet)-1].TransactionSignatures[i])
	}

	// create initial (no-op) revision, transaction, and signature
	initRevision := types.FileContractRevision{
		ParentID:          signedTxnSet[len(signedTxnSet)-1].FileContractID(0),
		UnlockConditions:  uc,
		NewRevisionNumber: 1,

		NewFileSize:           fc.FileSize,
		NewFileMerkleRoot:     fc.FileMerkleRoot,
		NewWindowStart:        fc.WindowStart,
		NewWindowEnd:          fc.WindowEnd,
		NewValidProofOutputs:  fc.ValidProofOutputs,
		NewMissedProofOutputs: fc.MissedProofOutputs,
		NewUnlockHash:         fc.UnlockHash,
	}
	renterRevisionSig := types.TransactionSignature{
		ParentID:       crypto.Hash(initRevision.ParentID),
		PublicKeyIndex: 0,
		CoveredFields: types.CoveredFields{
			FileContractRevisions: []uint64{0},
		},
	}
	revisionTxn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{initRevision},
		TransactionSignatures: []types.TransactionSignature{renterRevisionSig},
	}
	encodedSig := crypto.SignHash(revisionTxn.SigHash(0, startHeight), ourSK)
	revisionTxn.TransactionSignatures[0].Signature = encodedSig[:]

	// Send acceptance and signatures.
	if err = modules.WriteNegotiationAcceptance(conn); err != nil {
		return modules.RenterContract{}, errors.New("couldn't send transaction acceptance: " + err.Error())
	}
	if err = encoding.WriteObject(conn, addedSignatures); err != nil {
		return modules.RenterContract{}, errors.New("couldn't send added signatures: " + err.Error())
	}
	if err = encoding.WriteObject(conn, revisionTxn.TransactionSignatures[0]); err != nil {
		return modules.RenterContract{}, errors.New("couldn't send revision signature: " + err.Error())
	}

	// Read the host acceptance and signatures.
	err = modules.ReadNegotiationAcceptance(conn)
	if err != nil {
		return modules.RenterContract{}, errors.New("host did not accept our signatures: " + err.Error())
	}
	var hostSigs []types.TransactionSignature
	if err = encoding.ReadObject(conn, &hostSigs, 2e3); err != nil {
		return modules.RenterContract{}, errors.New("couldn't read the host's signatures: " + err.Error())
	}
	for _, sig := range hostSigs {
		txnBuilder.AddTransactionSignature(sig)
	}
	var hostRevisionSig types.TransactionSignature
	if err = encoding.ReadObject(conn, &hostRevisionSig, 2e3); err != nil {
		return modules.RenterContract{}, errors.New("couldn't read the host's revision signature: " + err.Error())
	}
	revisionTxn.TransactionSignatures = append(revisionTxn.TransactionSignatures, hostRevisionSig)

	// Construct the final transaction.
	txn, parentTxns = txnBuilder.View()
	txnSet = append(parentTxns, txn)

	// Submit to blockchain.
	err = tpool.AcceptTransactionSet(txnSet)
	if err == modules.ErrDuplicateTransactionSet {
		// As long as it made it into the transaction pool, we're good.
		err = nil
	}
	if err != nil {
		return modules.RenterContract{}, err
	}

	// Construct contract header.
	header := contractHeader{
		Transaction: revisionTxn,
		SecretKey:   ourSK,
		StartHeight: startHeight,
		TotalCost:   funding,
		ContractFee: host.ContractPrice,
		TxnFee:      txnFee,
		SiafundFee:  types.Tax(startHeight, fc.Payout),
		Utility: modules.ContractUtility{
			GoodForUpload: true,
			GoodForRenew:  true,
		},
	}

	// Add contract to set.
	meta, err := cs.managedInsertContract(header, nil) // no Merkle roots yet
	if err != nil {
		return modules.RenterContract{}, err
	}
	return meta, nil
}

// newFormContract forms a contract with a host using the new renter-host
// protocol.
func (cs *ContractSet) newFormContract(params ContractParams, txnBuilder transactionBuilder, tpool transactionPool, hdb hostDB, cancel <-chan struct{}) (rc modules.RenterContract, err error) {
	// Extract vars from params, for convenience.
	allowance, host, funding, startHeight, endHeight, refundAddress := params.Allowance, params.Host, params.Funding, params.StartHeight, params.EndHeight, params.RefundAddress

	// Create our key.
	ourSK, ourPK := crypto.GenerateKeyPair()
	// Create unlock conditions.
	uc := types.UnlockConditions{
		PublicKeys: []types.SiaPublicKey{
			types.Ed25519PublicKey(ourPK),
			host.PublicKey,
		},
		SignaturesRequired: 2,
	}

	// Calculate the anticipated transaction fee.
	_, maxFee := tpool.FeeEstimation()
	txnFee := maxFee.Mul64(modules.EstimatedFileContractTransactionSetSize)

	// Calculate the payouts for the renter, host, and whole contract.
	period := endHeight - startHeight
	expectedStorage := allowance.ExpectedStorage / allowance.Hosts
	renterPayout, hostPayout, _, err := modules.RenterPayoutsPreTax(host, funding, txnFee, types.ZeroCurrency, types.ZeroCurrency, period, expectedStorage)
	if err != nil {
		return modules.RenterContract{}, err
	}
	totalPayout := renterPayout.Add(hostPayout)

	// Check for negative currency.
	if types.PostTax(startHeight, totalPayout).Cmp(hostPayout) < 0 {
		return modules.RenterContract{}, errors.New("not enough money to pay both siafund fee and also host payout")
	}
	// Create file contract.
	fc := types.FileContract{
		FileSize:       0,
		FileMerkleRoot: crypto.Hash{}, // no proof possible without data
		WindowStart:    endHeight,
		WindowEnd:      endHeight + host.WindowSize,
		Payout:         totalPayout,
		UnlockHash:     uc.UnlockHash(),
		RevisionNumber: 0,
		ValidProofOutputs: []types.SiacoinOutput{
			// Outputs need to account for tax.
			{Value: types.PostTax(startHeight, totalPayout).Sub(hostPayout), UnlockHash: refundAddress}, // This is the renter payout, but with tax applied.
			// Collateral is returned to host.
			{Value: hostPayout, UnlockHash: host.UnlockHash},
		},
		MissedProofOutputs: []types.SiacoinOutput{
			// Same as above.
			{Value: types.PostTax(startHeight, totalPayout).Sub(hostPayout), UnlockHash: refundAddress},
			// Same as above.
			{Value: hostPayout, UnlockHash: host.UnlockHash},
			// Once we start doing revisions, we'll move some coins to the host and some to the void.
			{Value: types.ZeroCurrency, UnlockHash: types.UnlockHash{}},
		},
	}

	// Build transaction containing fc, e.g. the File Contract.
	err = txnBuilder.FundSiacoins(funding)
	if err != nil {
		return modules.RenterContract{}, err
	}
	txnBuilder.AddFileContract(fc)
	// Add miner fee.
	txnBuilder.AddMinerFee(txnFee)
	// Add FileContract identifier.
	fcTxn, _ := txnBuilder.View()
	si, err := PrefixedSignedIdentifier(params.RenterSeed, fcTxn.SiacoinInputs[0])
	if err != nil {
		return modules.RenterContract{}, errors.AddContext(err, "failed to create signed identifier")
	}
	_ = txnBuilder.AddArbitraryData(si[:])

	// Create initial transaction set.
	txn, parentTxns := txnBuilder.View()
	unconfirmedParents, err := txnBuilder.UnconfirmedParents()
	if err != nil {
		return modules.RenterContract{}, err
	}
	txnSet := append(unconfirmedParents, append(parentTxns, txn)...)

	// Increase Successful/Failed interactions accordingly
	defer func() {
		if err != nil {
			hdb.IncrementFailedInteractions(host.PublicKey)
			err = errors.Extend(err, modules.ErrHostFault)
		} else {
			hdb.IncrementSuccessfulInteractions(host.PublicKey)
		}
	}()

	// Initiate connection.
	dialer := &net.Dialer{
		Cancel:  cancel,
		Timeout: connTimeout,
	}
	conn, err := dialer.Dial("tcp", string(host.NetAddress))
	if err != nil {
		return modules.RenterContract{}, err
	}
	defer conn.Close()
	extendDeadline(conn, modules.NegotiateFileContractTime)

	// Perform initial handshake,
	if err := encoding.WriteObject(conn, modules.RPCLoopEnter); err != nil {
		return modules.RenterContract{}, err
	}
	handshakeReq := modules.LoopHandshakeRequest{
		Version:    1,
		Ciphers:    []types.Specifier{modules.CipherPlaintext},
		KeyData:    nil,
		ContractID: types.FileContractID{},
	}
	if err := encoding.NewEncoder(conn).Encode(handshakeReq); err != nil {
		return modules.RenterContract{}, err
	}
	var handshakeResp modules.LoopHandshakeResponse
	if err := modules.ReadRPCResponse(conn, &handshakeResp); err != nil {
		return modules.RenterContract{}, err
	}
	if handshakeResp.Cipher != modules.CipherPlaintext {
		return modules.RenterContract{}, errors.New("host selected unsupported cipher")
	}

	// Send the challenge response and FormContract request.
	req := modules.LoopFormContractRequest{
		Transactions: txnSet,
		RenterKey:    uc.PublicKeys[0],
	}
	if err := encoding.NewEncoder(conn).EncodeAll(modules.LoopChallengeResponse{}, modules.RPCLoopFormContract, req); err != nil {
		return modules.RenterContract{}, err
	}

	// Read the host's response.
	var resp modules.LoopContractAdditions
	if err := modules.ReadRPCResponse(conn, &resp); err != nil {
		return modules.RenterContract{}, err
	}

	// Incorporate host's modifications.
	txnBuilder.AddParents(resp.Parents)
	for _, input := range resp.Inputs {
		txnBuilder.AddSiacoinInput(input)
	}
	for _, output := range resp.Outputs {
		txnBuilder.AddSiacoinOutput(output)
	}

	// Sign the txn.
	signedTxnSet, err := txnBuilder.Sign(true)
	if err != nil {
		return modules.RenterContract{}, modules.WriteNegotiationRejection(conn, errors.New("failed to sign transaction: "+err.Error()))
	}

	// Calculate signatures added by the transaction builder.
	var addedSignatures []types.TransactionSignature
	_, _, _, addedSignatureIndices := txnBuilder.ViewAdded()
	for _, i := range addedSignatureIndices {
		addedSignatures = append(addedSignatures, signedTxnSet[len(signedTxnSet)-1].TransactionSignatures[i])
	}

	// create initial (no-op) revision, transaction, and signature
	initRevision := types.FileContractRevision{
		ParentID:          signedTxnSet[len(signedTxnSet)-1].FileContractID(0),
		UnlockConditions:  uc,
		NewRevisionNumber: 1,

		NewFileSize:           fc.FileSize,
		NewFileMerkleRoot:     fc.FileMerkleRoot,
		NewWindowStart:        fc.WindowStart,
		NewWindowEnd:          fc.WindowEnd,
		NewValidProofOutputs:  fc.ValidProofOutputs,
		NewMissedProofOutputs: fc.MissedProofOutputs,
		NewUnlockHash:         fc.UnlockHash,
	}
	renterRevisionSig := types.TransactionSignature{
		ParentID:       crypto.Hash(initRevision.ParentID),
		PublicKeyIndex: 0,
		CoveredFields: types.CoveredFields{
			FileContractRevisions: []uint64{0},
		},
	}
	revisionTxn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{initRevision},
		TransactionSignatures: []types.TransactionSignature{renterRevisionSig},
	}
	encodedSig := crypto.SignHash(revisionTxn.SigHash(0, startHeight), ourSK)
	revisionTxn.TransactionSignatures[0].Signature = encodedSig[:]

	// Send acceptance and signatures.
	renterSigs := modules.LoopContractSignatures{
		ContractSignatures: addedSignatures,
		RevisionSignature:  revisionTxn.TransactionSignatures[0],
	}
	if err := encoding.NewEncoder(conn).Encode(renterSigs); err != nil {
		return modules.RenterContract{}, err
	}

	// Read the host acceptance and signatures.
	var hostSigs modules.LoopContractSignatures
	if err := modules.ReadRPCResponse(conn, &hostSigs); err != nil {
		return modules.RenterContract{}, err
	}
	for _, sig := range hostSigs.ContractSignatures {
		txnBuilder.AddTransactionSignature(sig)
	}
	revisionTxn.TransactionSignatures = append(revisionTxn.TransactionSignatures, hostSigs.RevisionSignature)

	// Construct the final transaction.
	txn, parentTxns = txnBuilder.View()
	txnSet = append(parentTxns, txn)

	// Submit to blockchain.
	err = tpool.AcceptTransactionSet(txnSet)
	if err == modules.ErrDuplicateTransactionSet {
		// As long as it made it into the transaction pool, we're good.
		err = nil
	}
	if err != nil {
		return modules.RenterContract{}, err
	}

	// Construct contract header.
	header := contractHeader{
		Transaction: revisionTxn,
		SecretKey:   ourSK,
		StartHeight: startHeight,
		TotalCost:   funding,
		ContractFee: host.ContractPrice,
		TxnFee:      txnFee,
		SiafundFee:  types.Tax(startHeight, fc.Payout),
		Utility: modules.ContractUtility{
			GoodForUpload: true,
			GoodForRenew:  true,
		},
	}

	// Add contract to set.
	meta, err := cs.managedInsertContract(header, nil) // no Merkle roots yet
	if err != nil {
		return modules.RenterContract{}, err
	}
	return meta, nil
}
