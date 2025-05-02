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

	// TpoolTxnsGET contains the information about the tpool's transactions
	TpoolTxnsGET struct {
		Transactions   []types.Transaction   `json:"transactions"`
		V2Transactions []types.V2Transaction `json:"v2transactions"`
	}

	// WalletSiacoinsPOST contains the transaction sent in the POST call to /wallet/siacoins.
	WalletSiacoinsPOST struct {
		Transactions   []types.Transaction   `json:"transactions"`
		TransactionIDs []types.TransactionID `json:"transactionids"`

		V2Transactions   []types.V2Transaction `json:"v2transactions"`
		V2TransactionIDs []types.TransactionID `json:"v2transactionids"`
	}
)

type (
	// ConsensusBlocksGet contains all fields of a types.Block and additional
	// fields for ID and Height.
	ConsensusBlocksGet struct {
		ID           types.Hash256           `json:"id"`
		Height       uint64                  `json:"height"`
		ParentID     types.Hash256           `json:"parentid"`
		Nonce        [8]byte                 `json:"nonce"`
		Difficulty   types.Currency          `json:"difficulty"`
		Timestamp    legacy.Timestamp        `json:"timestamp"`
		MinerPayouts []legacy.SiacoinOutput  `json:"minerpayouts"`
		Transactions []ConsensusBlocksGetTxn `json:"transactions"`
	}

	// ConsensusBlocksGetTxn contains all fields of a legacy.Transaction and an
	// additional ID field.
	ConsensusBlocksGetTxn struct {
		ID                    types.TransactionID               `json:"id"`
		SiacoinInputs         []legacy.SiacoinInput             `json:"siacoininputs"`
		SiacoinOutputs        []ConsensusBlocksGetSiacoinOutput `json:"siacoinoutputs"`
		FileContracts         []ConsensusBlocksGetFileContract  `json:"filecontracts"`
		FileContractRevisions []legacy.FileContractRevision     `json:"filecontractrevisions"`
		StorageProofs         []legacy.StorageProof             `json:"storageproofs"`
		SiafundInputs         []legacy.SiafundInput             `json:"siafundinputs"`
		SiafundOutputs        []ConsensusBlocksGetSiafundOutput `json:"siafundoutputs"`
		MinerFees             []types.Currency                  `json:"minerfees"`
		ArbitraryData         [][]byte                          `json:"arbitrarydata"`
		TransactionSignatures []legacy.TransactionSignature     `json:"transactionsignatures"`
	}

	// ConsensusBlocksGetFileContract contains all fields of a legacy.FileContract
	// and an additional ID field.
	ConsensusBlocksGetFileContract struct {
		ID                 types.FileContractID              `json:"id"`
		FileSize           uint64                            `json:"filesize"`
		FileMerkleRoot     types.Hash256                     `json:"filemerkleroot"`
		WindowStart        uint64                            `json:"windowstart"`
		WindowEnd          uint64                            `json:"windowend"`
		Payout             types.Currency                    `json:"payout"`
		ValidProofOutputs  []ConsensusBlocksGetSiacoinOutput `json:"validproofoutputs"`
		MissedProofOutputs []ConsensusBlocksGetSiacoinOutput `json:"missedproofoutputs"`
		UnlockHash         types.Address                     `json:"unlockhash"`
		RevisionNumber     uint64                            `json:"revisionnumber"`
	}

	// ConsensusBlocksGetSiacoinOutput contains all fields of a legacy.SiacoinOutput
	// and an additional ID field.
	ConsensusBlocksGetSiacoinOutput struct {
		ID         types.SiacoinOutputID `json:"id"`
		Value      types.Currency        `json:"value"`
		UnlockHash types.Address         `json:"unlockhash"`
	}

	// ConsensusBlocksGetSiafundOutput contains all fields of a legacy.SiafundOutput
	// and an additional ID field.
	ConsensusBlocksGetSiafundOutput struct {
		ID         types.SiafundOutputID `json:"id"`
		Value      types.Currency        `json:"value"`
		UnlockHash types.Address         `json:"unlockhash"`
	}
)

func NewConsensusBlocksGet(b types.Block, state consensus.State) ConsensusBlocksGet {
	txns := make([]ConsensusBlocksGetTxn, 0, len(b.Transactions))
	for _, t := range b.Transactions {
		// Get the transaction's SiacoinOutputs.
		scos := make([]ConsensusBlocksGetSiacoinOutput, 0, len(t.SiacoinOutputs))
		for i, sco := range t.SiacoinOutputs {
			scos = append(scos, ConsensusBlocksGetSiacoinOutput{
				ID:         t.SiacoinOutputID(i),
				Value:      sco.Value,
				UnlockHash: sco.Address,
			})
		}
		// Get the transaction's SiafundOutputs.
		sfos := make([]ConsensusBlocksGetSiafundOutput, 0, len(t.SiafundOutputs))
		for i, sfo := range t.SiafundOutputs {
			sfos = append(sfos, ConsensusBlocksGetSiafundOutput{
				ID:         t.SiafundOutputID(i),
				Value:      types.NewCurrency64(sfo.Value),
				UnlockHash: sfo.Address,
			})
		}
		// Get the transaction's FileContracts.
		fcos := make([]ConsensusBlocksGetFileContract, 0, len(t.FileContracts))
		for i, fc := range t.FileContracts {
			// Get the FileContract's valid proof outputs.
			fcid := t.FileContractID(i)
			vpos := make([]ConsensusBlocksGetSiacoinOutput, 0, len(fc.ValidProofOutputs))
			for j, vpo := range fc.ValidProofOutputs {
				vpos = append(vpos, ConsensusBlocksGetSiacoinOutput{
					ID:         fcid.ValidOutputID(j),
					Value:      vpo.Value,
					UnlockHash: vpo.Address,
				})
			}
			// Get the FileContract's missed proof outputs.
			mpos := make([]ConsensusBlocksGetSiacoinOutput, 0, len(fc.MissedProofOutputs))
			for j, mpo := range fc.MissedProofOutputs {
				mpos = append(mpos, ConsensusBlocksGetSiacoinOutput{
					ID:         fcid.MissedOutputID(j),
					Value:      mpo.Value,
					UnlockHash: mpo.Address,
				})
			}
			fcos = append(fcos, ConsensusBlocksGetFileContract{
				ID:                 fcid,
				FileSize:           fc.Filesize,
				FileMerkleRoot:     fc.FileMerkleRoot,
				WindowStart:        fc.WindowStart,
				WindowEnd:          fc.WindowEnd,
				Payout:             fc.Payout,
				ValidProofOutputs:  vpos,
				MissedProofOutputs: mpos,
				UnlockHash:         fc.UnlockHash,
				RevisionNumber:     fc.RevisionNumber,
			})
		}
		txns = append(txns, ConsensusBlocksGetTxn{
			ID:                    t.ID(),
			SiacoinInputs:         legacy.ConvertSiacoinInputs(t.SiacoinInputs),
			SiacoinOutputs:        scos,
			FileContracts:         fcos,
			FileContractRevisions: legacy.ConvertFileContractRevisions(t.FileContractRevisions),
			StorageProofs:         legacy.ConvertStorageProofs(t.StorageProofs),
			SiafundInputs:         legacy.ConvertSiafundInputs(t.SiafundInputs),
			SiafundOutputs:        sfos,
			MinerFees:             t.MinerFees,
			ArbitraryData:         t.ArbitraryData,
			TransactionSignatures: legacy.ConvertTransactionSignatures(t.Signatures),
		})
	}

	return ConsensusBlocksGet{
		// TODO: populate
	}
}
