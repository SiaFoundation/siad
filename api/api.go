package api

import (
	"errors"
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
	jc.Encode(block) // TODO: this is technically not correct, need to process it
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
		"GET /consensus/blocks":                  func(jape.Context) { panic("todo") },
		"GET /consensus/validate/transactionset": func(jape.Context) { panic("todo") },

		"GET /tpool/fee":          func(ctx jape.Context) { panic("todo") },
		"GET /tpool/transactions": func(ctx jape.Context) { panic("todo") },
		"POST /tpool/raw":         func(ctx jape.Context) { panic("todo") },

		"GET /wallet": func(jape.Context) { panic("todo") },

		"POST /wallet/lock":   api.handlePOSTWalletLock,
		"POST /wallet/unlock": api.handlePOSTWalletUnlock,

		"POST /wallet/init/seed": api.handlePOSTWalletInitSeed,

		"GET /wallet/address":   func(jape.Context) { panic("todo") },
		"GET /wallet/addresses": func(jape.Context) { panic("todo") },
		"GET /wallet/seedaddrs": func(jape.Context) { panic("todo") },

		"POST /wallet/seed":              func(jape.Context) { panic("todo") },
		"POST /wallet/siacoins":          func(jape.Context) { panic("todo") },
		"POST /wallet/siafunds":          func(jape.Context) { panic("todo") },
		"GET /wallet/transaction/:id":    func(jape.Context) { panic("todo") },
		"GET /wallet/transactions":       func(jape.Context) { panic("todo") },
		"GET /wallet/transactions/:addr": func(jape.Context) { panic("todo") },

		"GET /wallet/unlockconditions/:addr": func(jape.Context) { panic("todo") },
		"GET /wallet/unspent":                func(jape.Context) { panic("todo") },
		"POST /wallet/sign":                  func(jape.Context) { panic("todo") },
		"GET /wallet/watch":                  func(jape.Context) { panic("todo") },
		"POST /wallet/watch":                 func(jape.Context) { panic("todo") },
	})
}
