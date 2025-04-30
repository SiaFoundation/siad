package api

import (
	"math/big"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/types"
	"go.sia.tech/exchange-bridge/legacy"
)

type (
	// ConsensusGET contains general information about the consensus set, with tags
	// to support idiomatic json encodings.
	ConsensusGET struct {
		// Consensus status values.
		Synced       bool           `json:"synced"`
		Height       uint64         `json:"height"`
		CurrentBlock types.BlockID  `json:"currentblock"`
		Target       types.BlockID  `json:"target"`
		Difficulty   consensus.Work `json:"difficulty"`

		// Foundation unlock hashes.
		FoundationPrimaryUnlockHash  types.Address `json:"foundationprimaryunlockhash"`
		FoundationFailsafeUnlockHash types.Address `json:"foundationfailsafeunlockhash"`

		// Consensus code constants.
		BlockFrequency         uint64           `json:"blockfrequency"`
		BlockSizeLimit         uint64           `json:"blocksizelimit"`
		ExtremeFutureThreshold legacy.Timestamp `json:"extremefuturethreshold"`
		FutureThreshold        legacy.Timestamp `json:"futurethreshold"`
		GenesisTimestamp       legacy.Timestamp `json:"genesistimestamp"`
		MaturityDelay          uint64           `json:"maturitydelay"`
		MedianTimestampWindow  uint64           `json:"mediantimestampwindow"`
		SiafundCount           types.Currency   `json:"siafundcount"`
		SiafundPortion         *big.Rat         `json:"siafundportion"`

		InitialCoinbase types.Currency `json:"initialcoinbase"`
		MinimumCoinbase types.Currency `json:"minimumcoinbase"`

		RootTarget types.Hash256 `json:"roottarget"`
		RootDepth  types.Hash256 `json:"rootdepth"`

		SiacoinPrecision types.Currency `json:"siacoinprecision"`
	}

	// WalletResponse is a response containing wallet balance
	// and information
	WalletResponse struct {
		ConfirmedSiacoinBalance     types.Currency `json:"confirmedsiacoinbalance"`
		UnconfirmedOutgoingSiacoins types.Currency `json:"unconfirmedoutgoingsiacoins"`
		UnconfirmedIncomingSiacoins types.Currency `json:"unconfirmedincomingsiacoins"`
	}

	// WalletAddressResponse is a response containing a single wallet address.
	WalletAddressResponse struct {
		Address types.Address `json:"address"`
	}

	// WalletAddressesResponse is a response containing a list of wallet addresses.
	WalletAddressesResponse struct {
		Addresses []types.Address `json:"addresses"`
	}

	// WalletGET contains general information about the wallet.
	WalletGET struct {
		Unlocked   bool   `json:"unlocked"`
		Encrypted  bool   `json:"encrypted"`
		Height     uint64 `json:"height"`
		Rescanning bool   `json:"rescanning"`

		ConfirmedSiacoinBalance     types.Currency `json:"confirmedsiacoinbalance"`
		UnconfirmedOutgoingSiacoins types.Currency `json:"unconfirmedoutgoingsiacoins"`
		UnconfirmedIncomingSiacoins types.Currency `json:"unconfirmedincomingsiacoins"`

		SiacoinClaimBalance types.Currency `json:"siacoinclaimbalance"`
		SiafundBalance      types.Currency `json:"siafundbalance"`

		DustThreshold types.Currency `json:"dustthreshold"`
	}

	// WalletWatchPOST contains the set of addresses to add or remove from the watch set.
	WalletWatchPOST struct {
		Addresses []types.Address `json:"addresses"`
		Remove    bool            `json:"remove"`
	}

	// TpoolFeeGET contains the current estimated fee
	TpoolFeeGET struct {
		Minimum types.Currency `json:"minimum"`
		Maximum types.Currency `json:"maximum"`
	}
)
