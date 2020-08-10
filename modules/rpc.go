package modules

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
)

// RPCPriceTable contains the cost of executing a RPC on a host. Each host can
// set its own prices for the individual MDM instructions and RPC costs.
type RPCPriceTable struct {
	// UID is a specifier that uniquely identifies this price table
	UID UniqueID `json:"uid"`

	// Validity is a duration that specifies how long the host guarantees these
	// prices for and are thus considered valid.
	Validity time.Duration `json:"validity"`

	// HostBlockHeight is the block height of the host. This allows the renter
	// to create valid withdrawal messages in case it is not synced yet.
	HostBlockHeight types.BlockHeight `json:"hostblockheight"`

	// UpdatePriceTableCost refers to the cost of fetching a new price table
	// from the host.
	UpdatePriceTableCost types.Currency `json:"updatepricetablecost"`

	// AccountBalanceCost refers to the cost of fetching the balance of an
	// ephemeral account.
	AccountBalanceCost types.Currency `json:"accountbalancecost"`

	// FundAccountCost refers to the cost of funding an ephemeral account on the
	// host.
	FundAccountCost types.Currency `json:"fundaccountcost"`

	// LatestRevisionCost refers to the cost of asking the host for the latest
	// revision of a contract.
	// TODO: should this be free?
	LatestRevisionCost types.Currency `json:"latestrevisioncost"`

	// MDM related costs
	//
	// InitBaseCost is the amount of cost that is incurred when an MDM program
	// starts to run. This doesn't include the memory used by the program data.
	// The total cost to initialize a program is calculated as
	// InitCost = InitBaseCost + MemoryTimeCost * Time
	InitBaseCost types.Currency `json:"initbasecost"`

	// MemoryTimeCost is the amount of cost per byte per time that is incurred
	// by the memory consumption of the program.
	MemoryTimeCost types.Currency `json:"memorytimecost"`

	// CollateralCost is the amount of money per byte the host is promising to
	// lock away as collateral when adding new data to a contract.
	CollateralCost types.Currency `json:"collateralcost"`

	// Cost values specific to the bandwidth consumption.
	DownloadBandwidthCost types.Currency `json:"downloadbandwidthcost"`
	UploadBandwidthCost   types.Currency `json:"uploadbandwidthcost"`

	// Cost values specific to the DropSectors instruction.
	DropSectorsBaseCost types.Currency `json:"dropsectorsbasecost"`
	DropSectorsUnitCost types.Currency `json:"dropsectorsunitcost"`

	// Cost values specific to the HasSector command.
	HasSectorBaseCost types.Currency `json:"hassectorbasecost"`

	// Cost values specific to the Read instruction.
	ReadBaseCost   types.Currency `json:"readbasecost"`
	ReadLengthCost types.Currency `json:"readlengthcost"`

	// Cost values specific to the RenewContract instruction.
	// TODO: update price tables with value
	RenewContractCost types.Currency `json:"renewcontractcost"`

	// Cost values specific to the Revision command.
	RevisionBaseCost types.Currency `json:"revisionbasecost"`

	// SwapSectorCost is the cost of swapping 2 full sectors by root.
	SwapSectorCost types.Currency `json:"swapsectorcost"`

	// Cost values specific to the Write instruction.
	WriteBaseCost   types.Currency `json:"writebasecost"`   // per write
	WriteLengthCost types.Currency `json:"writelengthcost"` // per byte written
	WriteStoreCost  types.Currency `json:"writestorecost"`  // per byte / block of additional storage

	// TxnFee estimations.
	TxnFeeMinRecommended types.Currency `json:"txnfeeminrecommended"`
	TxnFeeMaxRecommended types.Currency `json:"txnfeemaxrecommended"`
}

var (
	// RPCAccountBalance specifier
	RPCAccountBalance = types.NewSpecifier("AccountBalance")

	// RPCUpdatePriceTable specifier
	RPCUpdatePriceTable = types.NewSpecifier("UpdatePriceTable")

	// RPCExecuteProgram specifier
	RPCExecuteProgram = types.NewSpecifier("ExecuteProgram")

	// RPCFundAccount specifier
	RPCFundAccount = types.NewSpecifier("FundAccount")

	// RPCLatestRevision specifier
	RPCLatestRevision = types.NewSpecifier("LatestRevision")

	// RPCRenewContract specifier
	RPCRenewContract = types.NewSpecifier("RenewContract")
)

type (
	// AccountBalanceRequest specifies the account for which to retrieve the
	// balance.
	AccountBalanceRequest struct {
		Account AccountID
	}

	// AccountBalanceResponse contains the balance of the previously specified
	// account.
	AccountBalanceResponse struct {
		Balance types.Currency
	}

	// FundAccountRequest specifies the ephemeral account id that gets funded.
	FundAccountRequest struct {
		Account AccountID
	}

	// FundAccountResponse contains the signature. This signature is a
	// signed receipt, and can be used as proof of funding.
	FundAccountResponse struct {
		Balance   types.Currency
		Receipt   Receipt
		Signature crypto.Signature
	}

	// RPCExecuteProgramRequest is the request sent by the renter to execute a
	// program on the host's MDM.
	RPCExecuteProgramRequest struct {
		// FileContractID is the id of the filecontract we would like to modify.
		FileContractID types.FileContractID
		// Instructions to be executed as a program.
		Program Program
		// ProgramDataLength is the length of the programData following this
		// request.
		ProgramDataLength uint64
	}

	// RPCExecuteProgramResponse is the response sent by the host for each
	// executed MDMProgram instruction.
	RPCExecuteProgramResponse struct {
		AdditionalCollateral types.Currency
		OutputLength         uint64
		NewMerkleRoot        crypto.Hash
		NewSize              uint64
		Proof                []crypto.Hash
		Error                error
		TotalCost            types.Currency
		StorageCost          types.Currency
	}

	// RPCExecuteProgramRevisionSigningRequest is the request sent by the renter
	// for updating a contract when executing a write MDM program.
	RPCExecuteProgramRevisionSigningRequest struct {
		Signature            []byte
		NewRevisionNumber    uint64
		NewValidProofValues  []types.Currency
		NewMissedProofValues []types.Currency
	}

	// RPCExecuteProgramRevisionSigningResponse is the response from the host,
	// containing the host signature for the new revision.
	RPCExecuteProgramRevisionSigningResponse struct {
		Signature []byte
	}

	// RPCLatestRevisionRequest contains the id of the contract for which to
	// retrieve the latest revision.
	RPCLatestRevisionRequest struct {
		FileContractID types.FileContractID
	}

	// RPCLatestRevisionResponse contains the latest file contract revision
	// signed by both host and renter.
	// TODO: might need to update this to match MDMInstructionRevisionResponse?
	RPCLatestRevisionResponse struct {
		Revision types.FileContractRevision
	}

	// RPCUpdatePriceTableResponse contains a JSON encoded RPC price table
	RPCUpdatePriceTableResponse struct {
		PriceTableJSON []byte
	}

	// RPCTrackedPriceTableResponse is an empty response sent by the host to
	// signal it has received payment for the price table and has tracked it,
	// thus considering it valid.
	RPCTrackedPriceTableResponse struct{}

	// RPCRenewContractRequest contains the transaction set with both the final
	// revision of a contract to be renewed as well as the new contract and the
	// renter's public key used within the unlock conditions of the new
	// contract.
	RPCRenewContractRequest struct {
		TSet     []types.Transaction
		RenterPK types.SiaPublicKey
	}

	// RPCRenewContractCollateralResponse is the response sent by the host after
	// adding the collateral to the transaction. It contains any new parents,
	// inputs and outputs that were added.
	RPCRenewContractCollateralResponse struct {
		NewParents []types.Transaction
		NewInputs  []types.SiacoinInput
		NewOutputs []types.SiacoinOutput
	}

	RPCRenewContractRenterSignatures struct {
		RenterFinalRevisionSig []byte
		RenterContractSig      []types.TransactionSignature
		RenterNoOpRevisionSig  types.TransactionSignature
	}

	RPCRenewContractHostSignatures struct {
		ContractSignatures     []types.TransactionSignature
		FinalRevisionSignature []byte
		NoOpRevisionSignature  types.TransactionSignature
	}

	// rpcResponse is a helper type for encoding and decoding RPC response
	// messages.
	rpcResponse struct {
		err  *RPCError
		data interface{}
	}
)

// MarshalSia implements the SiaMarshaler interface.
func (epr RPCExecuteProgramResponse) MarshalSia(w io.Writer) error {
	var errStr string
	if epr.Error != nil {
		errStr = epr.Error.Error()
	}
	ec := encoding.NewEncoder(w)
	_ = ec.Encode(epr.AdditionalCollateral)
	_ = ec.Encode(epr.OutputLength)
	_ = ec.Encode(epr.NewMerkleRoot)
	_ = ec.Encode(epr.NewSize)
	_ = ec.Encode(epr.Proof)
	_ = ec.Encode(errStr)
	_ = ec.Encode(epr.TotalCost)
	_ = ec.Encode(epr.StorageCost)
	return ec.Err()
}

// UnmarshalSia implements the SiaMarshaler interface.
func (epr *RPCExecuteProgramResponse) UnmarshalSia(r io.Reader) error {
	var errStr string
	dc := encoding.NewDecoder(r, encoding.DefaultAllocLimit)
	_ = dc.Decode(&epr.AdditionalCollateral)
	_ = dc.Decode(&epr.OutputLength)
	_ = dc.Decode(&epr.NewMerkleRoot)
	_ = dc.Decode(&epr.NewSize)
	_ = dc.Decode(&epr.Proof)
	_ = dc.Decode(&errStr)
	_ = dc.Decode(&epr.TotalCost)
	_ = dc.Decode(&epr.StorageCost)
	if errStr != "" {
		epr.Error = errors.New(errStr)
	}
	return dc.Err()
}

// RPCRead tries to read the given object from the stream.
func RPCRead(r io.Reader, obj interface{}) error {
	resp := rpcResponse{nil, obj}
	err := encoding.ReadObject(r, &resp, uint64(RPCMinLen))
	if err != nil {
		return err
	}
	if resp.err != nil {
		// must wrap the error here, for more info see: https://www.pixelstech.net/article/1554553347-Be-careful-about-nil-check-on-interface-in-GoLang
		return errors.New(resp.err.Error())
	}
	return nil
}

// RPCWrite writes the given object to the stream.
func RPCWrite(w io.Writer, obj interface{}) error {
	return encoding.WriteObject(w, &rpcResponse{nil, obj})
}

// RPCWriteAll writes the given objects to the stream.
func RPCWriteAll(w io.Writer, objs ...interface{}) error {
	for _, obj := range objs {
		err := encoding.WriteObject(w, &rpcResponse{nil, obj})
		if err != nil {
			return err
		}
	}
	return nil
}

// RPCWriteError writes the given error to the stream.
func RPCWriteError(w io.Writer, err error) error {
	re, ok := err.(*RPCError)
	if err != nil && !ok {
		re = &RPCError{Description: err.Error()}
	}
	return encoding.WriteObject(w, &rpcResponse{re, nil})
}

// MarshalSia implements the encoding.SiaMarshaler interface.
func (resp *rpcResponse) MarshalSia(w io.Writer) error {
	if resp.data == nil {
		resp.data = struct{}{}
	}
	return encoding.NewEncoder(w).EncodeAll(resp.err, resp.data)
}

// UnmarshalSia implements the encoding.SiaUnmarshaler interface.
func (resp *rpcResponse) UnmarshalSia(r io.Reader) error {
	// NOTE: no allocation limit is required because this method is always
	// called via encoding.Unmarshal, which already imposes an allocation limit.
	d := encoding.NewDecoder(r, 0)
	if err := d.Decode(&resp.err); err != nil {
		return err
	}
	if resp.err != nil {
		// rpc response data is not decoded in the event of an error, we return
		// nil here because unmarshaling was successful and is unrelated from
		// the error in the rpc response
		return nil
	}
	return d.Decode(resp.data)
}

// UniqueID is a unique identifier
type UniqueID types.Specifier

// MarshalJSON marshals an id as a hex string.
func (uid UniqueID) MarshalJSON() ([]byte, error) {
	return json.Marshal(uid.String())
}

// String prints the uid in hex.
func (uid UniqueID) String() string {
	return hex.EncodeToString(uid[:])
}

// LoadString loads the unique id from the given string. It is the inverse of
// the `String` method.
func (uid *UniqueID) LoadString(input string) error {
	// *2 because there are 2 hex characters per byte.
	if len(input) != types.SpecifierLen*2 {
		return errors.New("incorrect length")
	}
	uidBytes, err := hex.DecodeString(input)
	if err != nil {
		return errors.New("could not unmarshal hash: " + err.Error())
	}
	copy(uid[:], uidBytes)
	return nil
}

// UnmarshalJSON decodes the json hex string of the id.
func (uid *UniqueID) UnmarshalJSON(b []byte) error {
	// *2 because there are 2 hex characters per byte.
	// +2 because the encoded JSON string is wrapped in `"`.
	if len(b) != types.SpecifierLen*2+2 {
		return errors.New("incorrect length")
	}

	// b[1 : len(b)-1] cuts off the leading and trailing `"` in the JSON string.
	return uid.LoadString(string(b[1 : len(b)-1]))
}
