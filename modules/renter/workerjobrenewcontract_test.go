package renter

import (
	"context"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestRenewContract is a unit test for the worker's RenewContract method.
func TestRenewContract(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a worker.
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Close the worker.
	defer func() {
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Upload a snapshot to the snapshot table. This makes sure that we got some
	// data in the contract.
	err = wt.UploadSnapshot(context.Background(), modules.UploadedBackup{UID: [16]byte{1, 2, 3}}, fastrand.Bytes(100))
	if err != nil {
		t.Fatal(err)
	}

	// Get a transaction builder and add the funding.
	funding := types.SiacoinPrecision

	// Get the host from the hostdb.
	host, _, err := wt.renter.hostDB.Host(wt.staticHostPubKey)
	if err != nil {
		t.Fatal(err)
	}

	// Get the wallet seed
	seed, _, err := wt.rt.wallet.PrimarySeed()
	if err != nil {
		t.Fatal(err)
	}

	// Get some more vars.
	allowance := wt.rt.renter.hostContractor.Allowance()
	bh := wt.staticCache().staticBlockHeight
	rs := modules.DeriveRenterSeed(seed)

	// Define some params for the contract.
	params := modules.ContractParams{
		Allowance:     allowance,
		Host:          host,
		Funding:       funding,
		StartHeight:   bh,
		EndHeight:     bh + allowance.Period,
		RefundAddress: types.UnlockHash{},
		RenterSeed:    rs.EphemeralRenterSeed(bh + allowance.Period),
	}

	// Get a txnbuilder.
	txnBuilder, err := wt.rt.wallet.StartTransaction()
	if err != nil {
		t.Fatal(err)
	}
	err = txnBuilder.FundSiacoins(funding)
	if err != nil {
		t.Fatal(err)
	}

	// Check contract before renewal.
	oldContractPreRenew, ok := wt.renter.hostContractor.ContractByPublicKey(params.Host.PublicKey)
	if !ok {
		t.Fatal("contract doesn't exist")
	}
	if oldContractPreRenew.Size() == 0 {
		t.Fatal("contract shouldnt be empty pre renewal")
	}
	if len(oldContractPreRenew.Transaction.FileContractRevisions) == 0 {
		t.Fatal("no Revisions")
	}
	oldRevisionPreRenew := oldContractPreRenew.Transaction.FileContractRevisions[0]
	oldMerkleRoot := oldRevisionPreRenew.NewFileMerkleRoot
	if oldMerkleRoot == (crypto.Hash{}) {
		t.Fatal("empty root")
	}

	// Renew the contract.
	_, _, err = wt.RenewContract(context.Background(), oldContractPreRenew.ID, params, txnBuilder)
	if err != nil {
		t.Fatal(err)
	}

	// Mine a block to mine the contract and trigger maintenance.
	b, err := wt.rt.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Check if the right txn was mined. It should contain both a revision and
	// contract.
	found := false
	var fcr types.FileContractRevision
	var newContract types.FileContract
	var fcTxn types.Transaction
	for _, txn := range b.Transactions {
		if len(txn.FileContractRevisions) == 1 && len(txn.FileContracts) == 1 {
			fcr = txn.FileContractRevisions[0]
			newContract = txn.FileContracts[0]
			fcTxn = txn
			found = true
			break
		}
	}
	if !found {
		t.Fatal("txn containing both the final revision and contract wasn't mined")
	}

	// Get the old contract and revision.
	var oldContract modules.RenterContract
	oldContracts := wt.renter.OldContracts()
	for _, c := range oldContracts {
		if c.HostPublicKey.String() == params.Host.PublicKey.String() {
			oldContract = c
		}
	}
	if len(oldContract.Transaction.FileContractRevisions) == 0 {
		t.Fatal("no Revisions")
	}
	oldRevision := oldContract.Transaction.FileContractRevisions[0]

	// Old contract should be empty after renewal.
	size := oldRevision.NewFileSize
	if size != 0 {
		t.Fatal("size should be zero")
	}
	merkleRoot := oldRevision.NewFileMerkleRoot
	if merkleRoot != (crypto.Hash{}) {
		t.Fatal("root should be empty")
	}

	// Check final revision.
	//
	// ParentID should match the old contract's.
	if fcr.ParentID != oldContract.ID {
		t.Fatalf("expected fcr to have parent %v but was %v", oldContract.ID, fcr.ParentID)
	}
	// Valid and missed outputs should match.
	if !reflect.DeepEqual(fcr.NewMissedProofOutputs, fcr.NewValidProofOutputs) {
		t.Fatal("expected valid outputs to match missed ones")
	}
	// Filesize should be 0 and root should be empty.
	if fcr.NewFileSize != 0 {
		t.Fatal("size should be 0", fcr.NewFileSize)
	}
	if fcr.NewFileMerkleRoot != (crypto.Hash{}) {
		t.Fatal("size should be 0", fcr.NewFileSize)
	}
	// Valid and missed outputs should match for renter and host between final
	// revision and the one before that.
	if !fcr.ValidRenterPayout().Equals(oldRevision.ValidRenterPayout()) {
		t.Fatal("renter payouts don't match between old revision and final revision")
	}
	if !fcr.ValidHostPayout().Equals(oldRevision.ValidHostPayout()) {
		t.Fatal("host payouts don't match between old revision and final revision")
	}

	// Compute the expected payouts of the new contract.
	pt := wt.staticPriceTable().staticPriceTable
	basePrice, baseCollateral := modules.RenewBaseCosts(oldRevisionPreRenew, &pt, params.EndHeight)
	allowance, startHeight, endHeight, host, funding := params.Allowance, params.StartHeight, params.EndHeight, params.Host, params.Funding
	period := endHeight - startHeight
	txnFee := pt.TxnFeeMaxRecommended.Mul64(2 * modules.EstimatedFileContractTransactionSetSize)
	renterPayout, hostPayout, hostCollateral, err := modules.RenterPayoutsPreTax(host, funding, txnFee, basePrice, baseCollateral, period, allowance.ExpectedStorage/allowance.Hosts)
	if err != nil {
		t.Fatal(err)
	}
	totalPayout := renterPayout.Add(hostPayout)

	// Check the new contract.
	if newContract.FileMerkleRoot != oldMerkleRoot {
		t.Fatal("new contract got wrong root")
	}
	if newContract.FileSize != oldContractPreRenew.Size() {
		t.Fatal("new contract got wrong size")
	}
	if !newContract.Payout.Equals(totalPayout) {
		t.Fatal("wrong payout")
	}
	if newContract.WindowStart != params.EndHeight {
		t.Fatal("wrong window start")
	}
	if newContract.WindowEnd != params.EndHeight+params.Host.WindowSize {
		t.Fatal("wrong window end")
	}
	_, ourPK := modules.GenerateContractKeyPair(params.RenterSeed, fcTxn)
	uh := types.UnlockConditions{
		PublicKeys: []types.SiaPublicKey{
			types.Ed25519PublicKey(ourPK),
			wt.staticHostPubKey,
		},
		SignaturesRequired: 2,
	}.UnlockHash()
	if newContract.UnlockHash != uh {
		t.Fatal("unlock hash doesn't match")
	}
	if newContract.RevisionNumber != 0 {
		t.Fatal("revision number isn't 0")
	}
	expectedValidRenterOutput := types.SiacoinOutput{
		Value:      types.PostTax(params.StartHeight, totalPayout).Sub(hostPayout),
		UnlockHash: params.RefundAddress,
	}
	expectedMissedRenterOutput := expectedValidRenterOutput
	expectedValidHostOutput := types.SiacoinOutput{
		Value:      hostPayout,
		UnlockHash: params.Host.UnlockHash,
	}
	expectedMissedHostOutput := types.SiacoinOutput{
		Value:      hostCollateral.Sub(baseCollateral).Add(params.Host.ContractPrice),
		UnlockHash: params.Host.UnlockHash,
	}
	expectedVoidOutput := types.SiacoinOutput{
		Value:      basePrice.Add(baseCollateral),
		UnlockHash: types.UnlockHash{},
	}
	if !reflect.DeepEqual(newContract.ValidRenterOutput(), expectedValidRenterOutput) {
		t.Fatal("wrong output")
	}
	if !reflect.DeepEqual(newContract.ValidHostOutput(), expectedValidHostOutput) {
		t.Fatal("wrong output")
	}
	if !reflect.DeepEqual(newContract.MissedRenterOutput(), expectedMissedRenterOutput) {
		t.Fatal("wrong output")
	}
	if !reflect.DeepEqual(newContract.MissedHostOutput(), expectedMissedHostOutput) {
		t.Fatal("wrong output")
	}
	mvo, err := newContract.MissedVoidOutput()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mvo, expectedVoidOutput) {
		t.Fatal("wrong output")
	}

	// Get the new contract's revision from the contractor.
	c, found := wt.renter.hostContractor.ContractByPublicKey(wt.staticHostPubKey)
	if !found {
		t.Fatal("contract not found in contractor")
	}
	if len(c.Transaction.FileContractRevisions) == 0 {
		t.Fatal("no Revisions")
	}
	rev := c.Transaction.FileContractRevisions[0]

	// Check the revision.
	if rev.NewFileMerkleRoot != oldMerkleRoot {
		t.Fatalf("new contract revision got wrong root %v", rev.NewFileMerkleRoot)
	}
	if rev.NewFileSize != oldContractPreRenew.Size() {
		t.Fatal("new contract revision got wrong size")
	}
	if rev.NewWindowStart != params.EndHeight {
		t.Fatal("wrong window start")
	}
	if rev.NewWindowEnd != params.EndHeight+params.Host.WindowSize {
		t.Fatal("wrong window end")
	}
	if rev.NewUnlockHash != uh {
		t.Fatal("unlock hash doesn't match")
	}
	if rev.NewRevisionNumber != 1 {
		t.Fatal("revision number isn't 1")
	}
	if !reflect.DeepEqual(rev.ValidRenterOutput(), expectedValidRenterOutput) {
		t.Fatal("wrong output")
	}
	if !reflect.DeepEqual(rev.ValidHostOutput(), expectedValidHostOutput) {
		t.Fatal("wrong output")
	}
	if !reflect.DeepEqual(rev.MissedRenterOutput(), expectedMissedRenterOutput) {
		t.Fatal("wrong output")
	}
	if !reflect.DeepEqual(rev.MissedHostOutput(), expectedMissedHostOutput) {
		t.Fatal("wrong output")
	}

	// Try using the contract now. Should work.
	err = wt.UploadSnapshot(context.Background(), modules.UploadedBackup{UID: [16]byte{3, 2, 1}}, fastrand.Bytes(100))
	if err != nil {
		t.Fatal(err)
	}
	_, err = wt.ReadOffset(context.Background(), categorySnapshotDownload, 0, modules.SectorSize)
	if err != nil {
		t.Fatal(err)
	}
}

// TestRenewContractEmptyPriceTableUID is a unit test for the worker's
// RenewContract method with a price table that has an empty UID.
func TestRenewContractEmptyPriceTableUID(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a worker.
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Close the worker.
	defer func() {
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Get a transaction builder and add the funding.
	funding := types.SiacoinPrecision

	// Get the host from the hostdb.
	host, _, err := wt.renter.hostDB.Host(wt.staticHostPubKey)
	if err != nil {
		t.Fatal(err)
	}

	// Get the wallet seed
	seed, _, err := wt.rt.wallet.PrimarySeed()
	if err != nil {
		t.Fatal(err)
	}

	// Get some more vars.
	allowance := wt.rt.renter.hostContractor.Allowance()
	bh := wt.staticCache().staticBlockHeight
	rs := modules.DeriveRenterSeed(seed)

	// Define some params for the contract.
	params := modules.ContractParams{
		Allowance:     allowance,
		Host:          host,
		Funding:       funding,
		StartHeight:   bh,
		EndHeight:     bh + allowance.Period,
		RefundAddress: types.UnlockHash{},
		RenterSeed:    rs.EphemeralRenterSeed(bh + allowance.Period),
	}

	// Get a txnbuilder.
	txnBuilder, err := wt.rt.wallet.StartTransaction()
	if err != nil {
		t.Fatal(err)
	}
	err = txnBuilder.FundSiacoins(funding)
	if err != nil {
		t.Fatal(err)
	}

	// Check contract before renewal.
	oldContractPreRenew, ok := wt.renter.hostContractor.ContractByPublicKey(params.Host.PublicKey)
	if !ok {
		t.Fatal("contract doesn't exist")
	}

	// Overwrite the UID of the price table.
	wpt := wt.staticPriceTable()
	wpt.staticPriceTable.UID = modules.UniqueID{}
	wt.staticSetPriceTable(wpt)

	// Renew the contract. This should work without error.
	_, _, err = wt.RenewContract(context.Background(), oldContractPreRenew.ID, params, txnBuilder)
	if err != nil {
		t.Fatal(err)
	}
}
