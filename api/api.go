package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"go.sia.tech/core/types"
	"go.sia.tech/coreutils/chain"
	"go.sia.tech/coreutils/syncer"
	"go.sia.tech/exchange-bridge/internal/siad"
	"go.sia.tech/jape"
	"go.sia.tech/vaultd/vault"
	"go.sia.tech/walletd/v2/wallet"
	"go.uber.org/zap"
)

const (
	primaryWalletName = "primary"
	watchWalletName   = "watch"
)

type api struct {
	chain  *chain.Manager
	syncer *syncer.Syncer
	vault  *vault.Vault
	wallet *wallet.Manager
	log    *zap.Logger
}

func (a *api) handleGETConsensus(jc jape.Context) {
	cs := a.chain.TipState()

	synced := time.Since(cs.PrevTimestamps[0]) < 6*time.Hour

	jc.Encode(ConsensusGET{
		Synced:       synced,
		Height:       cs.Index.Height,
		CurrentBlock: cs.Index.ID,
		Target:       cs.ChildTarget,
		Difficulty:   cs.Difficulty,

		FoundationPrimaryUnlockHash:  cs.FoundationSubsidyAddress,
		FoundationFailsafeUnlockHash: cs.FoundationManagementAddress,

		BlockFrequency: uint64(cs.Network.BlockInterval.Seconds()),

		MaturityDelay: cs.Network.MaturityDelay,
		SiafundCount:  types.NewCurrency(10000, 0),

		InitialCoinbase: cs.Network.InitialCoinbase,
		MinimumCoinbase: cs.Network.MinimumCoinbase,

		SiacoinPrecision: types.Siacoins(1),
	})
}

func (a *api) handleGETConsensusBlocks(jc jape.Context) {
	var id types.BlockID
	var heightStr string

	if jc.DecodeForm("id", &id) != nil {
		return
	} else if jc.DecodeForm("height", &heightStr) != nil {
		return
	}

	if id == (types.BlockID{}) && heightStr == "" {
		jc.Error(errors.New("must provide either id or height"), http.StatusBadRequest)
		return
	}

	var block types.Block
	var ok bool
	switch {
	case id != (types.BlockID{}):
		block, ok = a.chain.Block(id)
	case heightStr != "":
		height, err := strconv.ParseUint(heightStr, 10, 64)
		if err != nil {
			jc.Error(err, http.StatusBadRequest)
			return
		}

		index, ok := a.chain.BestIndex(height)
		if !ok {
			jc.Error(errors.New("block doesn't exist"), http.StatusNotFound)
			return
		}

		block, ok = a.chain.Block(index.ID)
	}

	if !ok {
		jc.Error(errors.New("block doesn't exist"), http.StatusNotFound)
		return
	}

	state, ok := a.chain.State(block.ID())
	if !ok {
		jc.Error(errors.New("couldn't get block state"), http.StatusInternalServerError)
		return
	}

	jc.Encode(NewConsensusBlocksGet(block, state))
}

func (a *api) handleGETWallet(jc jape.Context) {
	primaryWalletID, ok := a.getPrimaryWalletID(jc)
	if !ok {
		return
	}

	seeds, err := a.vault.Seeds(1, 0)
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	}

	scannedTip, err := a.wallet.Tip()
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	}
	cs := a.chain.TipState()

	balance, err := a.wallet.WalletBalance(primaryWalletID)
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	}

	events, err := a.wallet.WalletUnconfirmedEvents(primaryWalletID)
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	}
	var unconfirmedIncoming, unconfirmedOutgoing types.Currency
	for _, event := range events {
		unconfirmedIncoming = unconfirmedIncoming.Add(event.SiacoinInflow())
		unconfirmedOutgoing = unconfirmedOutgoing.Add(event.SiacoinOutflow())
	}

	var siafundClaimBalance types.Currency
	sfes, _, err := a.wallet.UnspentSiafundOutputs(primaryWalletID, 0, 10000)
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	}

	for _, sfe := range sfes {
		taxPortion, underflow := cs.SiafundTaxRevenue.SubWithUnderflow(sfe.ClaimStart)
		if underflow {
			continue
		}
		siafundClaimBalance = siafundClaimBalance.Add(taxPortion.Mul64(sfe.SiafundOutput.Value).Div64(cs.SiafundCount()))
	}

	jc.Encode(WalletGET{
		Encrypted: len(seeds) != 0,
		Unlocked:  a.vault.Unlocked(),

		Rescanning: cs.Index != scannedTip,
		Height:     scannedTip.Height,

		ConfirmedSiacoinBalance: balance.Siacoins,
		SiafundBalance:          types.NewCurrency(balance.Siafunds, 0),
		SiacoinClaimBalance:     siafundClaimBalance,

		UnconfirmedOutgoingSiacoins: unconfirmedOutgoing,
		UnconfirmedIncomingSiacoins: unconfirmedIncoming,
	})
}

func (a *api) handlePOSTWalletInitSeed(jc jape.Context) {
	var phrase string
	if jc.DecodeForm("seed", &phrase) != nil {
		return
	}

	var password string
	if jc.DecodeForm("encryptionpassword", &password) != nil {
		return
	}

	if password == "" || phrase == "" {
		jc.Error(errors.New("seed phrase and password are required"), http.StatusBadRequest)
		return
	}

	if seeds, err := a.vault.Seeds(1, 0); err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	} else if len(seeds) > 0 {
		jc.Error(errors.New("wallet already initialized"), http.StatusBadRequest)
		return
	}

	if err := a.vault.Unlock(password); err != nil {
		jc.Error(err, http.StatusUnauthorized)
		return
	}

	var seed [32]byte
	defer clear(seed[:])
	if err := siad.SeedFromPhrase(&seed, phrase); err != nil {
		jc.Error(err, http.StatusBadRequest)
		return
	} else if _, err := a.vault.AddSeed(&seed); err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	}
}

func (a *api) handlePOSTWalletUnlock(jc jape.Context) {
	var password string
	if jc.DecodeForm("encryptionpassword", &password) != nil {
		return
	}

	if password == "" {
		jc.Error(errors.New("password is required"), http.StatusBadRequest)
		return
	}

	if seeds, err := a.vault.Seeds(1, 0); err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	} else if len(seeds) == 0 {
		jc.Error(errors.New("wallet not initialized"), http.StatusBadRequest)
		return
	} else if err := a.vault.Unlock(password); err != nil {
		jc.Error(err, http.StatusUnauthorized)
		return
	}
}

func (a *api) handlePOSTWalletLock(jc jape.Context) {
	a.vault.Lock()
	jc.Encode(nil)
}

// why is this GET?
func (a *api) handleGETWalletAddress(jc jape.Context) {
	primarySeedID, ok := a.getPrimarySeedID(jc)
	if !ok {
		return
	}

	primaryWalletID, ok := a.getPrimaryWalletID(jc)
	if !ok {
		return
	}

	pk, err := a.vault.NextKey(primarySeedID)
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	}
	sp := types.SpendPolicy{
		Type: types.PolicyTypeUnlockConditions(types.StandardUnlockConditions(pk)),
	}
	addr := sp.Address()

	err = a.wallet.AddAddress(primaryWalletID, wallet.Address{
		Address:     addr,
		SpendPolicy: &sp,
	})
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	}
	jc.Encode(WalletAddressResponse{
		Address: addr,
	})
}

func (a *api) handleGETWalletAddresses(jc jape.Context) {
	primaryWalletID, ok := a.getPrimaryWalletID(jc)
	if !ok {
		return
	}

	watchWalletID, ok := a.getWatchWalletID(jc)
	if !ok {
		return
	}

	primaryAddresses, err := a.wallet.Addresses(primaryWalletID)
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	}

	watchAddresses, err := a.wallet.Addresses(watchWalletID)
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	}

	addresses := make([]types.Address, 0, len(primaryAddresses)+len(watchAddresses))
	for _, addr := range primaryAddresses {
		addresses = append(addresses, addr.Address)
	}
	for _, addr := range watchAddresses {
		addresses = append(addresses, addr.Address)
	}

	jc.Encode(WalletAddressesResponse{
		Addresses: addresses,
	})
}

func (a *api) handleGETWalletSeedAddrs(jc jape.Context) {
	primarySeedID, ok := a.getPrimarySeedID(jc)
	if !ok {
		return
	}

	primaryWalletID, ok := a.getPrimaryWalletID(jc)
	if !ok {
		return
	}

	var count uint64
	if jc.DecodeForm("count", &count) != nil {
		return
	}

	meta, err := a.vault.SeedMeta(primarySeedID)
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	}

	start := meta.LastIndex + 1
	end := start + count

	for i := start; i < end; i++ {
		pk, err := a.vault.NextKey(primarySeedID)
		if err != nil {
			jc.Error(err, http.StatusInternalServerError)
			return
		}
		sp := types.SpendPolicy{
			Type: types.PolicyTypeUnlockConditions(types.StandardUnlockConditions(pk)),
		}
		err = a.wallet.AddAddress(primaryWalletID, wallet.Address{
			Address:     sp.Address(),
			SpendPolicy: &sp,
		})
		if err != nil {
			jc.Error(err, http.StatusInternalServerError)
			return
		}
	}
}

func (a *api) handleGETWalletWatchAddrs(jc jape.Context) {
	watchWalletID, ok := a.getWatchWalletID(jc)
	if !ok {
		return
	}

	watchAddresses, err := a.wallet.Addresses(watchWalletID)
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return
	}

	addresses := make([]types.Address, 0, len(watchAddresses))
	for _, addr := range watchAddresses {
		addresses = append(addresses, addr.Address)
	}

	jc.Encode(WalletAddressesResponse{
		Addresses: addresses,
	})
}

func (a *api) handlePOSTWalletWatchAddrs(jc jape.Context) {
	var req WalletWatchPOST
	if jc.Decode(&req) != nil {
		return
	}

	watchWalletID, ok := a.getWatchWalletID(jc)
	if !ok {
		return
	}

	for _, addr := range req.Addresses {
		if req.Remove {
			if err := a.wallet.RemoveAddress(watchWalletID, addr); err != nil {
				jc.Error(err, http.StatusInternalServerError)
				return
			}
		} else {
			err := a.wallet.AddAddress(watchWalletID, wallet.Address{
				Address: addr,
			})
			if err != nil {
				jc.Error(err, http.StatusInternalServerError)
				return
			}
		}
	}
	jc.Encode(nil)
}

func (a *api) handleGETTPoolFee(jc jape.Context) {
	jc.Encode(TpoolFeeGET{
		Minimum: a.chain.RecommendedFee(),
		Maximum: a.chain.RecommendedFee(),
	})
}

func (a *api) getPrimaryWalletID(jc jape.Context) (wallet.ID, bool) {
	wallets, err := a.wallet.Wallets()
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return 0, false
	}
	for _, w := range wallets {
		if w.Name == primaryWalletName {
			return w.ID, true
		}
	}
	w, err := a.wallet.AddWallet(wallet.Wallet{
		Name: primaryWalletName,
	})
	return w.ID, true
}

func (a *api) getWatchWalletID(jc jape.Context) (wallet.ID, bool) {
	wallets, err := a.wallet.Wallets()
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return 0, false
	}
	for _, w := range wallets {
		if w.Name == watchWalletName {
			return w.ID, true
		}
	}
	w, err := a.wallet.AddWallet(wallet.Wallet{
		Name: watchWalletName,
	})
	return w.ID, true
}

func (a *api) getPrimarySeedID(jc jape.Context) (vault.SeedID, bool) {
	seeds, err := a.vault.Seeds(1, 0)
	if err != nil {
		jc.Error(err, http.StatusInternalServerError)
		return 0, false
	} else if len(seeds) == 0 {
		jc.Error(errors.New("wallet not initialized"), http.StatusBadRequest)
		return 0, false
	}
	return seeds[0].ID, true
}

func (a *api) handleGETTPoolTransactions(jc jape.Context) {
	jc.Encode(TpoolTxnsGET{
		Transactions:   a.chain.PoolTransactions(),
		V2Transactions: a.chain.V2PoolTransactions(),
	})
}

const txnSize = 2000 // bytes

func (a *api) constructV1Txn(walletID wallet.ID, recipients []types.SiacoinOutput, fee types.Currency) (types.Transaction, error) {
	txn := types.Transaction{
		MinerFees:      []types.Currency{fee},
		SiacoinOutputs: recipients,
	}

	outgoing := fee
	for _, output := range recipients {
		outgoing = outgoing.Add(output.Value)
	}

	utxos, _, change, err := a.wallet.SelectSiacoinElements(walletID, outgoing, true)
	if err != nil {
		return types.Transaction{}, fmt.Errorf("failed to select siacoin elements: %w", err)
	}

	if !change.IsZero() {
		walletAddresses, err := a.wallet.Addresses(walletID)
		if err != nil {
			return types.Transaction{}, fmt.Errorf("failed to get wallet addresses: %w", err)
		} else if len(walletAddresses) == 0 {
			return types.Transaction{}, fmt.Errorf("no wallet addresses found")
		}
		changeAddr := walletAddresses[0].Address
		txn.SiacoinOutputs = append(txn.SiacoinOutputs, types.SiacoinOutput{
			Address: changeAddr,
			Value:   change,
		})
	}

	unlockConditions := make(map[types.Address]types.UnlockConditions)
	addressUnlockConditions := func(addr types.Address) (types.UnlockConditions, error) {
		if uc, ok := unlockConditions[addr]; ok {
			return uc, nil
		}
		meta, err := a.wallet.WalletAddress(walletID, addr)
		if err != nil {
			return types.UnlockConditions{}, fmt.Errorf("failed to get unlock conditions: %w", err)
		} else if meta.SpendPolicy == nil {
			return types.UnlockConditions{}, fmt.Errorf("no spend policy for address %s", addr)
		}
		uc, ok := meta.SpendPolicy.Type.(types.PolicyTypeUnlockConditions)
		if !ok {
			return types.UnlockConditions{}, fmt.Errorf("invalid spend policy type for address %s", addr)
		} else if len(uc.PublicKeys) != 1 {
			return types.UnlockConditions{}, fmt.Errorf("invalid number of public keys for address %s", addr)
		}
		unlockConditions[addr] = types.UnlockConditions(uc)
		return unlockConditions[addr], nil
	}
	for _, sce := range utxos {
		uc, err := addressUnlockConditions(sce.SiacoinOutput.Address)
		if err != nil {
			return types.Transaction{}, fmt.Errorf("failed to get unlock conditions for address %q: %w", sce.SiacoinOutput.Address, err)
		}
		txn.SiacoinInputs = append(txn.SiacoinInputs, types.SiacoinInput{
			ParentID:         sce.ID,
			UnlockConditions: uc,
		})
		txn.Signatures = append(txn.Signatures, types.TransactionSignature{
			ParentID: types.Hash256(sce.ID),
			CoveredFields: types.CoveredFields{
				WholeTransaction: true,
			},
		})
	}

	cs := a.chain.TipState()

	for i := range txn.SiacoinInputs {
		sigHash := cs.WholeSigHash(txn, txn.Signatures[i].ParentID, 0, 0, nil)
		sig, err := a.vault.Sign(types.PublicKey(txn.SiacoinInputs[i].UnlockConditions.PublicKeys[0].Key), sigHash)
		if err != nil {
			return types.Transaction{}, fmt.Errorf("failed to sign transaction: %w", err)
		}
		txn.Signatures[i].Signature = sig[:]
	}

	return txn, nil
}

func (a *api) constructV2Txn(walletID wallet.ID, recipients []types.SiacoinOutput, fee types.Currency) (types.V2Transaction, types.ChainIndex, error) {
	txn := types.V2Transaction{
		MinerFee:       fee,
		SiacoinOutputs: recipients,
	}

	var outgoing types.Currency
	for _, output := range recipients {
		outgoing = outgoing.Add(output.Value)
	}

	if outgoing.IsZero() {
		return types.V2Transaction{}, types.ChainIndex{}, errors.New("no outgoing value")
	}
	outgoing = outgoing.Add(fee)

	utxos, basis, change, err := a.wallet.SelectSiacoinElements(walletID, outgoing, true)
	if err != nil {
		return types.V2Transaction{}, types.ChainIndex{}, fmt.Errorf("failed to select siacoin elements: %w", err)
	}

	if !change.IsZero() {
		walletAddresses, err := a.wallet.Addresses(walletID)
		if err != nil {
			return types.V2Transaction{}, types.ChainIndex{}, fmt.Errorf("failed to get wallet addresses: %w", err)
		} else if len(walletAddresses) == 0 {
			return types.V2Transaction{}, types.ChainIndex{}, errors.New("no wallet addresses found")
		}
		changeAddr := walletAddresses[0].Address

		txn.SiacoinOutputs = append(txn.SiacoinOutputs, types.SiacoinOutput{
			Address: changeAddr,
			Value:   change,
		})
	}

	spendPolicies := make(map[types.Address]types.SpendPolicy)
	addressPolicy := func(addr types.Address) (types.SpendPolicy, error) {
		if sp, ok := spendPolicies[addr]; ok {
			return sp, nil
		}
		meta, err := a.wallet.WalletAddress(walletID, addr)
		if err != nil {
			return types.SpendPolicy{}, fmt.Errorf("failed to get unlock conditions: %w", err)
		} else if meta.SpendPolicy == nil {
			return types.SpendPolicy{}, fmt.Errorf("no spend policy for address %s", addr)
		}
		uc, ok := meta.SpendPolicy.Type.(types.PolicyTypeUnlockConditions)
		if !ok {
			return types.SpendPolicy{}, fmt.Errorf("invalid spend policy type for address %s", addr)
		} else if len(uc.PublicKeys) != 1 {
			return types.SpendPolicy{}, fmt.Errorf("invalid number of public keys for address %s", addr)
		}
		spendPolicies[addr] = *meta.SpendPolicy
		return spendPolicies[addr], nil
	}
	for _, sce := range utxos {
		sp, err := addressPolicy(sce.SiacoinOutput.Address)
		if err != nil {
			return types.V2Transaction{}, types.ChainIndex{}, fmt.Errorf("failed to get unlock conditions for address %q: %w", sce.SiacoinOutput.Address, err)
		}
		txn.SiacoinInputs = append(txn.SiacoinInputs, types.V2SiacoinInput{
			Parent: sce,
			SatisfiedPolicy: types.SatisfiedPolicy{
				Policy: sp,
			},
		})
	}

	cs := a.chain.TipState()
	sigHash := cs.InputSigHash(txn)
	for i := range txn.SiacoinInputs {
		uc, ok := txn.SiacoinInputs[i].SatisfiedPolicy.Policy.Type.(types.PolicyTypeUnlockConditions)
		if !ok {
			return types.V2Transaction{}, types.ChainIndex{}, fmt.Errorf("invalid spend policy type for address %s", txn.SiacoinInputs[i].Parent.SiacoinOutput.Address)
		} else if len(uc.PublicKeys) != 1 {
			return types.V2Transaction{}, types.ChainIndex{}, fmt.Errorf("invalid number of public keys for address %s", txn.SiacoinInputs[i].Parent.SiacoinOutput.Address)
		}
		sig, err := a.vault.Sign(types.PublicKey(uc.PublicKeys[0].Key), sigHash)
		if err != nil {
			return types.V2Transaction{}, types.ChainIndex{}, fmt.Errorf("failed to sign transaction: %w", err)
		}
		txn.SiacoinInputs[i].SatisfiedPolicy.Signatures = []types.Signature{sig}
	}

	return txn, basis, nil
}

func (a *api) handlePOSTWalletSiacoins(jc jape.Context) {
	var encodedOutputs string
	if jc.DecodeForm("outputs", &encodedOutputs) != nil {
		return
	}

	primaryWalletID, ok := a.getPrimaryWalletID(jc)
	if !ok {
		return
	}

	fee := a.chain.RecommendedFee().Mul64(txnSize)
	var outputs []types.SiacoinOutput
	if encodedOutputs != "" {
		if err := json.Unmarshal([]byte(encodedOutputs), &outputs); err != nil {
			jc.Error(err, http.StatusBadRequest)
			return
		} else if len(outputs) == 0 {
			jc.Error(errors.New("no outputs provided"), http.StatusBadRequest)
			return
		}
	} else {
		var amount types.Currency
		if jc.DecodeForm("amount", &amount) != nil {
			return
		}
		var recipient types.Address
		if jc.DecodeForm("recipient", &recipient) != nil {
			return
		}

		var includeFee bool
		if jc.DecodeForm("feeIncluded", &includeFee) != nil {
			return
		}

		if includeFee {
			var underflow bool
			// subtract the fee from the amount
			amount, underflow = amount.SubWithUnderflow(fee)
			if underflow {
				jc.Error(errors.New("amount too small to cover fee"), http.StatusBadRequest)
				return
			}
		}

		if amount.IsZero() {
			jc.Error(errors.New("amount must be greater than 0"), http.StatusBadRequest)
			return
		}

		outputs = []types.SiacoinOutput{
			{
				Address: recipient,
				Value:   amount,
			},
		}
	}

	cs := a.chain.TipState()
	if cs.Index.Height >= cs.Network.HardforkV2.AllowHeight {
		txn, basis, err := a.constructV2Txn(primaryWalletID, outputs, fee)
		if err != nil {
			jc.Error(err, http.StatusInternalServerError)
			return
		}
		basis, txnset, err := a.chain.V2TransactionSet(basis, txn)
		if err != nil {
			jc.Error(err, http.StatusInternalServerError)
			return
		} else if _, err := a.chain.AddV2PoolTransactions(basis, txnset); err != nil {
			jc.Error(err, http.StatusInternalServerError)
			return
		}

		ids := make([]types.TransactionID, len(txnset))
		for i, txn := range txnset {
			ids[i] = txn.ID()
		}

		jc.Encode(WalletSiacoinsPOST{
			V2Transactions:   txnset,
			V2TransactionIDs: ids,
		})
	} else {
		txn, err := a.constructV1Txn(primaryWalletID, outputs, fee)
		if err != nil {
			jc.Error(err, http.StatusInternalServerError)
			return
		}
		txnset := append(a.chain.UnconfirmedParents(txn), txn)
		if _, err := a.chain.AddPoolTransactions(txnset); err != nil {
			jc.Error(err, http.StatusInternalServerError)
			return
		} else if err := a.syncer.BroadcastTransactionSet(txnset); err != nil {
			jc.Error(err, http.StatusInternalServerError)
			return
		}
		ids := make([]types.TransactionID, len(txnset))
		for i, txn := range txnset {
			ids[i] = txn.ID()
		}

		jc.Encode(WalletSiacoinsPOST{
			Transactions:   txnset,
			TransactionIDs: ids,
		})
	}
}

// NewHandler creates a new API handler
func NewHandler(cm *chain.Manager, s *syncer.Syncer, v *vault.Vault, w *wallet.Manager, log *zap.Logger) http.Handler {
	api := &api{
		chain:  cm,
		syncer: s,
		vault:  v,
		wallet: w,
		log:    log,
	}
	return jape.Mux(map[string]jape.Handler{
		"GET /consensus":                         api.handleGETConsensus,
		"GET /consensus/blocks":                  api.handleGETConsensusBlocks,
		"GET /consensus/validate/transactionset": func(jape.Context) { panic("todo") },

		"GET /tpool/fee":          api.handleGETTPoolFee,
		"GET /tpool/transactions": api.handleGETTPoolTransactions,
		"POST /tpool/raw":         func(ctx jape.Context) { panic("todo") },

		"GET /wallet": api.handleGETWallet,

		"POST /wallet/lock":   api.handlePOSTWalletLock,
		"POST /wallet/unlock": api.handlePOSTWalletUnlock,

		"POST /wallet/init/seed": api.handlePOSTWalletInitSeed,

		"GET /wallet/address":   api.handleGETWalletAddress,
		"GET /wallet/addresses": api.handleGETWalletAddresses,
		"GET /wallet/seedaddrs": api.handleGETWalletSeedAddrs,

		"POST /wallet/siacoins": api.handlePOSTWalletSiacoins,
		"POST /wallet/siafunds": func(jape.Context) { panic("todo") },

		"GET /wallet/transaction/:id":    func(jape.Context) { panic("todo") },
		"GET /wallet/transactions":       func(jape.Context) { panic("todo") },
		"GET /wallet/transactions/:addr": func(jape.Context) { panic("todo") },

		"GET /wallet/unlockconditions/:addr": func(jape.Context) { panic("todo") },
		"GET /wallet/unspent":                func(jape.Context) { panic("todo") },
		"POST /wallet/sign":                  func(jape.Context) { panic("todo") },

		"GET /wallet/watch":  api.handleGETWalletWatchAddrs,
		"POST /wallet/watch": api.handlePOSTWalletWatchAddrs,
	})
}
