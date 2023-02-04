package modules

import (
	"bytes"
	"crypto/cipher"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
	"golang.org/x/crypto/chacha20poly1305"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/types"
)

const (
	// RHPVersion is the version of the Sia renter-host protocol currently
	// implemented by the host module.
	RHPVersion = "1.5.9"

	// MinimumSupportedRenterHostProtocolVersion is the minimum version of Sia
	// that supports the currently used version of the renter-host protocol.
	MinimumSupportedRenterHostProtocolVersion = "1.4.1"

	// V1420HostOutOfStorageErrString is the string used by hosts since before
	// version 1.4.2 to indicate that they have run out of storage.
	//
	// Any update to this string needs to be done by making a new variable. This
	// variable should not be changed. IsOOSErr() needs to be updated to include
	// the new string while also still checking the old string as well to
	// preserve compatibility.
	V1420HostOutOfStorageErrString = "not enough storage remaining to accept sector"

	// V1420ContractNotRecognizedErrString is the string used by hosts since
	// before version 1.4.2 to indicate that they do not recognize the
	// contract that the renter is trying to update.
	//
	// Any update to this string needs to be done by making a new variable. This
	// variable should not be changed. IsContractNotRecognizedErr() needs to be
	// updated to include the new string while also still checking the old
	// string as well to preserve compatibility.
	V1420ContractNotRecognizedErrString = "no record of that contract"
)

const (
	// AcceptResponse is the response given to an RPC call to indicate
	// acceptance, i.e. that the sender wishes to continue communication.
	AcceptResponse = "accept"

	// StopResponse is the response given to an RPC call to indicate graceful
	// termination, i.e. that the sender wishes to cease communication, but
	// not due to an error.
	StopResponse = "stop"
)

const (
	// NegotiateDownloadTime defines the amount of time that the renter and
	// host have to negotiate a download request batch. The time is set high
	// enough that two nodes behind Tor have a reasonable chance of completing
	// the negotiation.
	NegotiateDownloadTime = 600 * time.Second

	// NegotiateFileContractRevisionTime defines the minimum amount of time
	// that the renter and host have to negotiate a file contract revision. The
	// time is set high enough that a full 4MB can be piped through a
	// connection that is running over Tor.
	NegotiateFileContractRevisionTime = 600 * time.Second

	// NegotiateFileContractTime defines the amount of time that the renter and
	// host have to negotiate a file contract. The time is set high enough that
	// a node behind Tor has a reasonable chance at making the multiple
	// required round trips to complete the negotiation.
	NegotiateFileContractTime = 360 * time.Second

	// NegotiateMaxDownloadActionRequestSize defines the maximum size that a
	// download request can be. Note, this is not a max size for the data that
	// can be requested, but instead is a max size for the definition of the
	// data being requested.
	NegotiateMaxDownloadActionRequestSize = 50e3

	// NegotiateMaxErrorSize indicates the maximum number of bytes that can be
	// used to encode an error being sent during negotiation.
	NegotiateMaxErrorSize = 256

	// NegotiateMaxFileContractRevisionSize specifies the maximum size that a
	// file contract revision is allowed to have when being sent over the wire
	// during negotiation.
	NegotiateMaxFileContractRevisionSize = 3e3

	// NegotiateMaxFileContractSetLen determines the maximum allowed size of a
	// transaction set that can be sent when trying to negotiate a file
	// contract. The transaction set will contain all of the unconfirmed
	// dependencies of the file contract, meaning that it can be quite large.
	// The transaction pool's size limit for transaction sets has been chosen
	// as a reasonable guideline for determining what is too large.
	NegotiateMaxFileContractSetLen = TransactionSetSizeLimit - 1e3

	// NegotiateMaxHostExternalSettingsLen is the maximum allowed size of an
	// encoded HostExternalSettings.
	NegotiateMaxHostExternalSettingsLen = 16000

	// NegotiateMaxSiaPubkeySize defines the maximum size that a SiaPubkey is
	// allowed to be when being sent over the wire during negotiation.
	NegotiateMaxSiaPubkeySize = 1e3

	// NegotiateMaxTransactionSignatureSize defines the maximum size that a
	// transaction signature is allowed to be when being sent over the wire
	// during negotiation.
	NegotiateMaxTransactionSignatureSize = 2e3

	// NegotiateMaxTransactionSignaturesSize defines the maximum size that a
	// transaction signature slice is allowed to be when being sent over the
	// wire during negotiation.
	NegotiateMaxTransactionSignaturesSize = 5e3

	// NegotiateRecentRevisionTime establishes the minimum amount of time that
	// the connection deadline is expected to be set to when a recent file
	// contract revision is being requested from the host. The deadline is long
	// enough that the connection should be successful even if both parties are
	// running Tor.
	NegotiateRecentRevisionTime = 120 * time.Second

	// NegotiateRenewContractTime defines the minimum amount of time that the
	// renter and host have to negotiate a final contract renewal. The time is
	// high enough that the negotiation can occur over a Tor connection, and
	// that both the host and the renter can have time to process large Merkle
	// tree calculations that may be involved with renewing a file contract.
	NegotiateRenewContractTime = 600 * time.Second
)

var (
	// NegotiateSettingsTime establishes the minimum amount of time that the
	// connection deadline is expected to be set to when settings are being
	// requested from the host. The deadline is long enough that the connection
	// should be successful even if both parties are on Tor.
	NegotiateSettingsTime = build.Select(build.Var{
		Dev:      120 * time.Second,
		Standard: 120 * time.Second,
		Testnet:  120 * time.Second,
		Testing:  3 * time.Second,
	}).(time.Duration)
)

var (
	// ActionDelete is the specifier for a RevisionAction that deletes a
	// sector.
	ActionDelete = types.NewSpecifier("Delete")

	// ActionInsert is the specifier for a RevisionAction that inserts a
	// sector.
	ActionInsert = types.NewSpecifier("Insert")

	// ActionModify is the specifier for a RevisionAction that modifies sector
	// data.
	ActionModify = types.NewSpecifier("Modify")

	// ErrAnnNotAnnouncement indicates that the provided host announcement does
	// not use a recognized specifier, indicating that it's either not a host
	// announcement or it's not a recognized version of a host announcement.
	ErrAnnNotAnnouncement = errors.New("provided data does not form a recognized host announcement")

	// ErrAnnUnrecognizedSignature is returned when the signature in a host
	// announcement is not a type of signature that is recognized.
	ErrAnnUnrecognizedSignature = errors.New("the signature provided in the host announcement is not recognized")

	// ErrMaxVirtualSectors is returned when a sector cannot be added because
	// the maximum number of virtual sectors for that sector id already exist.
	ErrMaxVirtualSectors = errors.New("sector collides with a physical sector that already has the maximum allowed number of virtual sectors")

	// ErrRevisionCoveredFields is returned if there is a covered fields object
	// in a transaction signature which has the 'WholeTransaction' field set to
	// true, meaning that miner fees cannot be added to the transaction without
	// invalidating the signature.
	ErrRevisionCoveredFields = errors.New("file contract revision transaction signature does not allow miner fees to be added")

	// ErrRevisionSigCount is returned when a file contract revision has the
	// wrong number of transaction signatures.
	ErrRevisionSigCount = errors.New("file contract revision has the wrong number of transaction signatures")

	// ErrStopResponse is the error returned by ReadNegotiationAcceptance when
	// it reads the StopResponse string.
	ErrStopResponse = errors.New("sender wishes to stop communicating")

	// PrefixHostAnnouncement is used to indicate that a transaction's
	// Arbitrary Data field contains a host announcement. The encoded
	// announcement will follow this prefix.
	PrefixHostAnnouncement = types.NewSpecifier("HostAnnouncement")

	// PrefixFileContractIdentifier is used to indicate that a transaction's
	// Arbitrary Data field contains a file contract identifier. The identifier
	// and its signature will follow this prefix.
	PrefixFileContractIdentifier = types.NewSpecifier("FCIdentifier")

	// RPCDownload is the specifier for downloading a file from a host.
	RPCDownload = types.NewSpecifier("Download" + types.RuneToString(2))

	// RPCFormContract is the specifier for forming a contract with a host.
	RPCFormContract = types.NewSpecifier("FormContract" + types.RuneToString(2))

	// RPCReviseContract is the specifier for revising an existing file
	// contract.
	RPCReviseContract = types.NewSpecifier("ReviseContract" + types.RuneToString(2))

	// RPCSettings is the specifier for requesting settings from the host.
	RPCSettings = types.NewSpecifier("Settings" + types.RuneToString(2))

	// SectorSize defines how large a sector should be in bytes. The sector
	// size needs to be a power of two to be compatible with package
	// merkletree. 4MB has been chosen for the live network because large
	// sectors significantly reduce the tracking overhead experienced by the
	// renter and the host.
	SectorSize = build.Select(build.Var{
		Dev:      SectorSizeDev,
		Standard: SectorSizeStandard,
		Testnet:  SectorSizeStandard,
		Testing:  SectorSizeTesting,
	}).(uint64)

	// SectorSizeDev defines how large a sector should be in Dev builds.
	SectorSizeDev = uint64(1 << SectorSizeScalingDev)
	// SectorSizeStandard defines how large a sector should be in Standard
	// builds.
	SectorSizeStandard = uint64(1 << SectorSizeScalingStandard)
	// SectorSizeTesting defines how large a sector should be in Testing builds.
	SectorSizeTesting = uint64(1 << SectorSizeScalingTesting)

	// SectorSizeScalingDev defines the power of 2 to which we scale sector
	// sizes in Dev builds.
	SectorSizeScalingDev = 18 // 256 KiB
	// SectorSizeScalingStandard defines the power of 2 to which we scale sector
	// sizes in Standard builds.
	SectorSizeScalingStandard = 22 // 4 MiB
	// SectorSizeScalingTesting defines the power of 2 to which we scale sector
	// sizes in Testing builds.
	SectorSizeScalingTesting = 12 // 4 KiB
)

type (
	// A DownloadAction is a description of a download that the renter would
	// like to make. The MerkleRoot indicates the root of the sector, the
	// offset indicates what portion of the sector is being downloaded, and the
	// length indicates how many bytes should be grabbed starting from the
	// offset.
	DownloadAction struct {
		MerkleRoot crypto.Hash
		Offset     uint64
		Length     uint64
	}

	// HostAnnouncement is an announcement by the host that appears in the
	// blockchain. 'Specifier' is always 'PrefixHostAnnouncement'. The
	// announcement is always followed by a signature from the public key of
	// the whole announcement.
	HostAnnouncement struct {
		Specifier  types.Specifier
		NetAddress NetAddress
		PublicKey  types.SiaPublicKey
	}

	// HostExternalSettings are the parameters advertised by the host. These
	// are the values that the renter will request from the host in order to
	// build its database.
	//
	// NOTE: Anytime the pricing is extended for the HostExternalSettings, the
	// Allowance also needs to be extended to support manually setting a maximum
	// reasonable price.
	HostExternalSettings struct {
		// MaxBatchSize indicates the maximum size in bytes that a batch is
		// allowed to be. A batch is an array of revision actions; each
		// revision action can have a different number of bytes, depending on
		// the action, so the number of revision actions allowed depends on the
		// sizes of each.
		AcceptingContracts   bool              `json:"acceptingcontracts"`
		MaxDownloadBatchSize uint64            `json:"maxdownloadbatchsize"`
		MaxDuration          types.BlockHeight `json:"maxduration"`
		MaxReviseBatchSize   uint64            `json:"maxrevisebatchsize"`
		NetAddress           NetAddress        `json:"netaddress"`
		RemainingStorage     uint64            `json:"remainingstorage"`
		SectorSize           uint64            `json:"sectorsize"`
		TotalStorage         uint64            `json:"totalstorage"`
		UnlockHash           types.UnlockHash  `json:"unlockhash"`
		WindowSize           types.BlockHeight `json:"windowsize"`

		// Collateral is the amount of collateral that the host will put up for
		// storage in 'bytes per block', as an assurance to the renter that the
		// host really is committed to keeping the file. But, because the file
		// contract is created with no data available, this does leave the host
		// exposed to an attack by a wealthy renter whereby the renter causes
		// the host to lockup in-advance a bunch of funds that the renter then
		// never uses, meaning the host will not have collateral for other
		// clients.
		//
		// MaxCollateral indicates the maximum number of coins that a host is
		// willing to put into a file contract.
		Collateral    types.Currency `json:"collateral"`
		MaxCollateral types.Currency `json:"maxcollateral"`

		// ContractPrice is the number of coins that the renter needs to pay to
		// the host just to open a file contract with them. Generally, the price
		// is only to cover the siacoin fees that the host will suffer when
		// submitting the file contract revision and storage proof to the
		// blockchain.
		//
		// BaseRPC price is a flat per-RPC fee charged by the host for any
		// non-free RPC.
		//
		// 'Download' bandwidth price is the cost per byte of downloading data
		// from the host. This includes metadata such as Merkle proofs.
		//
		// SectorAccessPrice is the cost per sector of data accessed when
		// downloading data.
		//
		// StoragePrice is the cost per-byte-per-block in hastings of storing
		// data on the host.
		//
		// 'Upload' bandwidth price is the cost per byte of uploading data to
		// the host.
		BaseRPCPrice           types.Currency `json:"baserpcprice"`
		ContractPrice          types.Currency `json:"contractprice"`
		DownloadBandwidthPrice types.Currency `json:"downloadbandwidthprice"`
		SectorAccessPrice      types.Currency `json:"sectoraccessprice"`
		StoragePrice           types.Currency `json:"storageprice"`
		UploadBandwidthPrice   types.Currency `json:"uploadbandwidthprice"`

		// EphemeralAccountExpiry is the amount of time an account can be
		// inactive before the host considers it expired.
		//
		// MaxEphemeralAccountBalance is the maximum amount of money the host
		// allows to be deposited into a single ephemeral account.
		EphemeralAccountExpiry     time.Duration  `json:"ephemeralaccountexpiry"`
		MaxEphemeralAccountBalance types.Currency `json:"maxephemeralaccountbalance"`

		// Because the host has a public key, and settings are signed, and
		// because settings may be MITM'd, settings need a revision number so
		// that a renter can compare multiple sets of settings and determine
		// which is the most recent.
		RevisionNumber uint64 `json:"revisionnumber"`
		Version        string `json:"version"`

		SiaMuxPort string `json:"siamuxport"`
	}

	// HostOldExternalSettings are the pre-v1.4.0 host settings.
	HostOldExternalSettings struct {
		AcceptingContracts     bool              `json:"acceptingcontracts"`
		MaxDownloadBatchSize   uint64            `json:"maxdownloadbatchsize"`
		MaxDuration            types.BlockHeight `json:"maxduration"`
		MaxReviseBatchSize     uint64            `json:"maxrevisebatchsize"`
		NetAddress             NetAddress        `json:"netaddress"`
		RemainingStorage       uint64            `json:"remainingstorage"`
		SectorSize             uint64            `json:"sectorsize"`
		TotalStorage           uint64            `json:"totalstorage"`
		UnlockHash             types.UnlockHash  `json:"unlockhash"`
		WindowSize             types.BlockHeight `json:"windowsize"`
		Collateral             types.Currency    `json:"collateral"`
		MaxCollateral          types.Currency    `json:"maxcollateral"`
		ContractPrice          types.Currency    `json:"contractprice"`
		DownloadBandwidthPrice types.Currency    `json:"downloadbandwidthprice"`
		StoragePrice           types.Currency    `json:"storageprice"`
		UploadBandwidthPrice   types.Currency    `json:"uploadbandwidthprice"`
		RevisionNumber         uint64            `json:"revisionnumber"`
		Version                string            `json:"version"`
	}

	// A RevisionAction is a description of an edit to be performed on a file
	// contract. Three types are allowed, 'ActionDelete', 'ActionInsert', and
	// 'ActionModify'. ActionDelete just takes a sector index, indicating which
	// sector is going to be deleted. ActionInsert takes a sector index, and a
	// full sector of data, indicating that a sector at the index should be
	// inserted with the provided data. 'Modify' revises the sector at the
	// given index, rewriting it with the provided data starting from the
	// 'offset' within the sector.
	//
	// Modify could be simulated with an insert and a delete, however an insert
	// requires a full sector to be uploaded, and a modify can be just a few
	// kb, which can be significantly faster.
	RevisionAction struct {
		Type        types.Specifier
		SectorIndex uint64
		Offset      uint64
		Data        []byte
	}
)

// MaxBaseRPCPrice returns the maximum value for the BaseRPCPrice based on the
// DownloadBandwidthPrice
func (hes HostExternalSettings) MaxBaseRPCPrice() types.Currency {
	return hes.DownloadBandwidthPrice.Mul64(MaxBaseRPCPriceVsBandwidth)
}

// MaxSectorAccessPrice returns the maximum value for the SectorAccessPrice
// based on the DownloadBandwidthPrice
func (hes HostExternalSettings) MaxSectorAccessPrice() types.Currency {
	return hes.DownloadBandwidthPrice.Mul64(MaxSectorAccessPriceVsBandwidth)
}

// SiaMuxAddress returns the address of the host's siamux.
func (hes HostExternalSettings) SiaMuxAddress() string {
	return fmt.Sprintf("%s:%s", hes.NetAddress.Host(), hes.SiaMuxPort)
}

// New RPC IDs
var (
	RPCLoopEnter              = types.NewSpecifier("LoopEnter")
	RPCLoopExit               = types.NewSpecifier("LoopExit")
	RPCLoopFormContract       = types.NewSpecifier("LoopFormContract")
	RPCLoopLock               = types.NewSpecifier("LoopLock")
	RPCLoopRead               = types.NewSpecifier("LoopRead")
	RPCLoopRenewClearContract = types.NewSpecifier("LoopRenewClear")
	RPCLoopSectorRoots        = types.NewSpecifier("LoopSectorRoots")
	RPCLoopSettings           = types.NewSpecifier("LoopSettings")
	RPCLoopUnlock             = types.NewSpecifier("LoopUnlock")
	RPCLoopWrite              = types.NewSpecifier("LoopWrite")
)

// RPC ciphers
var (
	CipherChaCha20Poly1305 = types.NewSpecifier("ChaCha20Poly1305")
	CipherNoOverlap        = types.NewSpecifier("NoOverlap")
)

// Write actions
var (
	WriteActionAppend = types.NewSpecifier("Append")
	WriteActionTrim   = types.NewSpecifier("Trim")
	WriteActionSwap   = types.NewSpecifier("Swap")
	WriteActionUpdate = types.NewSpecifier("Update")
)

// Read interrupt
var (
	RPCLoopReadStop = types.NewSpecifier("ReadStop")
)

var (
	// RPCChallengePrefix is the prefix prepended to the challenge data
	// supplied by the host when proving ownership of a contract's secret key.
	RPCChallengePrefix = types.NewSpecifier("challenge")
)

// New RPC request and response types
type (
	// An RPCError may be sent instead of a Response to any RPC.
	RPCError struct {
		Type        types.Specifier
		Data        []byte // structure depends on Type
		Description string // human-readable error string
	}

	// LoopKeyExchangeRequest is the first object sent when initializing the
	// renter-host protocol.
	LoopKeyExchangeRequest struct {
		// The renter's ephemeral X25519 public key.
		PublicKey crypto.X25519PublicKey

		// Encryption ciphers that the renter supports.
		Ciphers []types.Specifier
	}

	// LoopKeyExchangeResponse contains the host's response to the
	// KeyExchangeRequest.
	LoopKeyExchangeResponse struct {
		// The host's ephemeral X25519 public key.
		PublicKey crypto.X25519PublicKey

		// Signature of (Host's Public Key | Renter's Public Key). Note that this
		// also serves to authenticate the host.
		Signature []byte

		// Cipher selected by the host. Must be one of the ciphers offered in
		// the key exchange request.
		Cipher types.Specifier
	}

	// LoopChallengeRequest contains a challenge for the renter to prove their
	// identity. It is the host's first encrypted message, and immediately
	// follows KeyExchangeResponse.
	LoopChallengeRequest struct {
		// Entropy signed by the renter to prove that it controls the secret key
		// used to sign contract revisions. The actual data signed should be:
		//
		//    blake2b(RPCChallengePrefix | Challenge)
		Challenge [16]byte
	}

	// LoopLockRequest contains the request parameters for RPCLoopLock.
	LoopLockRequest struct {
		// The contract to lock; implicitly referenced by subsequent RPCs.
		ContractID types.FileContractID

		// The host's challenge, signed by the renter's contract key.
		Signature []byte

		// Lock timeout, in milliseconds.
		Timeout uint64
	}

	// LoopLockResponse contains the response data for RPCLoopLock.
	LoopLockResponse struct {
		Acquired     bool
		NewChallenge [16]byte
		Revision     types.FileContractRevision
		Signatures   []types.TransactionSignature
	}

	// LoopReadRequestSection is a section requested in LoopReadRequest.
	LoopReadRequestSection struct {
		MerkleRoot [32]byte
		Offset     uint32
		Length     uint32
	}

	// LoopReadRequest contains the request parameters for RPCLoopRead.
	LoopReadRequest struct {
		Sections    []LoopReadRequestSection
		MerkleProof bool

		NewRevisionNumber    uint64
		NewValidProofValues  []types.Currency
		NewMissedProofValues []types.Currency
		Signature            []byte
	}

	// LoopReadResponse contains the response data for RPCLoopRead.
	LoopReadResponse struct {
		Signature   []byte
		Data        []byte
		MerkleProof []crypto.Hash
	}

	// LoopSectorRootsRequest contains the request parameters for RPCLoopSectorRoots.
	LoopSectorRootsRequest struct {
		RootOffset uint64
		NumRoots   uint64

		NewRevisionNumber    uint64
		NewValidProofValues  []types.Currency
		NewMissedProofValues []types.Currency
		Signature            []byte
	}

	// LoopSectorRootsResponse contains the response data for RPCLoopSectorRoots.
	LoopSectorRootsResponse struct {
		Signature   []byte
		SectorRoots []crypto.Hash
		MerkleProof []crypto.Hash
	}

	// LoopFormContractRequest contains the request parameters for RPCLoopFormContract.
	LoopFormContractRequest struct {
		Transactions []types.Transaction
		RenterKey    types.SiaPublicKey
	}

	// LoopContractAdditions contains the parent transaction, inputs, and
	// outputs added by the host when negotiating a file contract.
	LoopContractAdditions struct {
		Parents []types.Transaction
		Inputs  []types.SiacoinInput
		Outputs []types.SiacoinOutput
	}

	// LoopContractSignatures contains the signatures for a contract
	// transaction and initial revision. These signatures are sent by both the
	// renter and host during contract formation and renewal.
	LoopContractSignatures struct {
		ContractSignatures []types.TransactionSignature
		RevisionSignature  types.TransactionSignature
	}

	// LoopRenewContractRequest contains the request parameters for RPCLoopRenewContract.
	LoopRenewContractRequest struct {
		Transactions []types.Transaction
		RenterKey    types.SiaPublicKey
	}

	// LoopRenewAndClearContractSignatures contains the signatures for a contract
	// transaction, initial revision and final revision of the old contract. These
	// signatures are sent by the renter during contract renewal.
	LoopRenewAndClearContractSignatures struct {
		ContractSignatures []types.TransactionSignature
		RevisionSignature  types.TransactionSignature

		FinalRevisionSignature []byte
	}

	// LoopRenewAndClearContractRequest contains the request parameters for
	// RPCLoopRenewClearContract.
	LoopRenewAndClearContractRequest struct {
		Transactions []types.Transaction
		RenterKey    types.SiaPublicKey

		FinalValidProofValues  []types.Currency
		FinalMissedProofValues []types.Currency
	}

	// LoopSettingsResponse contains the response data for RPCLoopSettingsResponse.
	LoopSettingsResponse struct {
		Settings []byte // actually a JSON-encoded HostExternalSettings
	}

	// LoopWriteRequest contains the request parameters for RPCLoopWrite.
	LoopWriteRequest struct {
		Actions     []LoopWriteAction
		MerkleProof bool

		NewRevisionNumber    uint64
		NewValidProofValues  []types.Currency
		NewMissedProofValues []types.Currency
	}

	// LoopWriteAction is a generic Write action. The meaning of each field
	// depends on the Type of the action.
	LoopWriteAction struct {
		Type types.Specifier
		A, B uint64
		Data []byte
	}

	// LoopWriteMerkleProof contains the optional Merkle proof for response data
	// for RPCLoopWrite.
	LoopWriteMerkleProof struct {
		OldSubtreeHashes []crypto.Hash
		OldLeafHashes    []crypto.Hash
		NewMerkleRoot    crypto.Hash
	}

	// LoopWriteResponse contains the response data for RPCLoopWrite.
	LoopWriteResponse struct {
		Signature []byte
	}
)

// Error implements the error interface.
func (e *RPCError) Error() string {
	return e.Description
}

// RPCMinLen is the minimum size of an RPC message. If an encoded message
// would be smaller than RPCMinLen, it is padded with random data.
const RPCMinLen = 4096

// WriteRPCMessage writes an encrypted RPC message.
func WriteRPCMessage(w io.Writer, aead cipher.AEAD, obj interface{}) error {
	payload := encoding.Marshal(obj)
	// pad the payload to RPCMinLen bytes to prevent eavesdroppers from
	// identifying RPCs by their size.
	minLen := RPCMinLen - aead.Overhead() - aead.NonceSize()
	if len(payload) < minLen {
		payload = append(payload, fastrand.Bytes(minLen-len(payload))...)
	}
	return encoding.WritePrefixedBytes(w, crypto.EncryptWithNonce(payload, aead))
}

// ReadRPCMessage reads an encrypted RPC message.
func ReadRPCMessage(r io.Reader, aead cipher.AEAD, obj interface{}, maxLen uint64) error {
	ciphertext, err := encoding.ReadPrefixedBytes(r, maxLen)
	if err != nil {
		return err
	}
	plaintext, err := crypto.DecryptWithNonce(ciphertext, aead)
	if err != nil {
		return err
	}
	return encoding.Unmarshal(plaintext, obj)
}

// WriteRPCRequest writes an encrypted RPC request using the new loop
// protocol.
func WriteRPCRequest(w io.Writer, aead cipher.AEAD, rpcID types.Specifier, req interface{}) error {
	if err := WriteRPCMessage(w, aead, rpcID); err != nil {
		return err
	}
	if req != nil {
		return WriteRPCMessage(w, aead, req)
	}
	return nil
}

// WriteRPCResponse writes an RPC response or error using the new loop
// protocol. Either resp or err must be nil. If err is an *RPCError, it is
// sent directly; otherwise, a generic RPCError is created from err's Error
// string.
func WriteRPCResponse(w io.Writer, aead cipher.AEAD, resp interface{}, err error) error {
	re, ok := err.(*RPCError)
	if err != nil && !ok {
		re = &RPCError{Description: err.Error()}
	}
	return WriteRPCMessage(w, aead, &rpcResponse{re, resp})
}

// ReadRPCID reads an RPC request ID using the new loop protocol.
func ReadRPCID(r io.Reader, aead cipher.AEAD) (rpcID types.Specifier, err error) {
	err = ReadRPCMessage(r, aead, &rpcID, RPCMinLen)
	return
}

// ReadRPCRequest reads an RPC request using the new loop protocol.
func ReadRPCRequest(r io.Reader, aead cipher.AEAD, req interface{}, maxLen uint64) error {
	return ReadRPCMessage(r, aead, req, maxLen)
}

// ReadRPCResponse reads an RPC response using the new loop protocol.
func ReadRPCResponse(r io.Reader, aead cipher.AEAD, resp interface{}, maxLen uint64) error {
	if maxLen < RPCMinLen {
		build.Critical("maxLen must be at least RPCMinLen")
		maxLen = RPCMinLen
	}
	response := rpcResponse{nil, resp}
	if err := ReadRPCMessage(r, aead, &response, maxLen); err != nil {
		return err
	}

	// Note: this nil check is important, we can not simply return response.err
	// here because of it being a pointer, doing so leads to 'bad access' error
	if response.err != nil {
		return response.err
	}
	return nil
}

// A RenterHostSession is a session of the new renter-host protocol.
type RenterHostSession struct {
	aead cipher.AEAD
	conn net.Conn
}

// WriteRequest writes an encrypted RPC request using the new loop
// protocol.
func (s *RenterHostSession) WriteRequest(rpcID types.Specifier, req interface{}) error {
	return WriteRPCRequest(s.conn, s.aead, rpcID, req)
}

// WriteResponse writes an RPC response or error using the new loop
// protocol. Either resp or err must be nil. If err is an *RPCError, it is
// sent directly; otherwise, a generic RPCError is created from err's Error
// string.
func (s *RenterHostSession) WriteResponse(resp interface{}, err error) error {
	return WriteRPCResponse(s.conn, s.aead, resp, err)
}

// ReadRPCID reads an RPC request ID using the new loop protocol.
func (s *RenterHostSession) ReadRPCID() (rpcID types.Specifier, err error) {
	return ReadRPCID(s.conn, s.aead)
}

// ReadRequest reads an RPC request using the new loop protocol.
func (s *RenterHostSession) ReadRequest(req interface{}, maxLen uint64) error {
	return ReadRPCRequest(s.conn, s.aead, req, maxLen)
}

// ReadResponse reads an RPC response using the new loop protocol.
func (s *RenterHostSession) ReadResponse(resp interface{}, maxLen uint64) error {
	return ReadRPCResponse(s.conn, s.aead, resp, maxLen)
}

// NewRenterSession returns a new renter-side session of the renter-host
// protocol.
func NewRenterSession(conn net.Conn, hostPublicKey types.SiaPublicKey) (*RenterHostSession, LoopChallengeRequest, error) {
	// generate a session key
	xsk, xpk := crypto.GenerateX25519KeyPair()

	// send our half of the key exchange
	req := LoopKeyExchangeRequest{
		PublicKey: xpk,
		Ciphers:   []types.Specifier{CipherChaCha20Poly1305},
	}
	if err := encoding.NewEncoder(conn).EncodeAll(RPCLoopEnter, req); err != nil {
		return nil, LoopChallengeRequest{}, err
	}
	// read host's half of the key exchange
	var resp LoopKeyExchangeResponse
	if err := encoding.NewDecoder(conn, encoding.DefaultAllocLimit).Decode(&resp); err != nil {
		return nil, LoopChallengeRequest{}, err
	}
	// validate the signature before doing anything else; don't want to punish
	// the "host" if we're talking to an imposter
	var hpk crypto.PublicKey
	copy(hpk[:], hostPublicKey.Key)
	var sig crypto.Signature
	copy(sig[:], resp.Signature)
	if err := crypto.VerifyHash(crypto.HashAll(req.PublicKey, resp.PublicKey), hpk, sig); err != nil {
		return nil, LoopChallengeRequest{}, err
	}
	// check for compatible cipher
	if resp.Cipher != CipherChaCha20Poly1305 {
		return nil, LoopChallengeRequest{}, errors.New("host selected unsupported cipher")
	}
	// derive shared secret, which we'll use as an encryption key
	cipherKey := crypto.DeriveSharedSecret(xsk, resp.PublicKey)

	// use cipherKey to initialize an AEAD cipher
	aead, err := chacha20poly1305.New(cipherKey[:])
	if err != nil {
		build.Critical("could not create cipher")
		return nil, LoopChallengeRequest{}, err
	}

	// read host's challenge
	var challengeReq LoopChallengeRequest
	if err := ReadRPCMessage(conn, aead, &challengeReq, RPCMinLen); err != nil {
		return nil, LoopChallengeRequest{}, err
	}
	return &RenterHostSession{
		aead: aead,
		conn: conn,
	}, challengeReq, nil
}

// ReadNegotiationAcceptance reads an accept/reject response from r (usually a
// net.Conn). If the response is not AcceptResponse, ReadNegotiationAcceptance
// returns the response as an error. If the response is StopResponse,
// ErrStopResponse is returned, allowing for direct error comparison.
//
// Note that since errors returned by ReadNegotiationAcceptance are newly
// allocated, they cannot be compared to other errors in the traditional
// fashion.
func ReadNegotiationAcceptance(r io.Reader) error {
	var resp string
	err := encoding.ReadObject(r, &resp, NegotiateMaxErrorSize)
	if err != nil {
		return err
	}
	switch resp {
	case AcceptResponse:
		return nil
	case StopResponse:
		return ErrStopResponse
	default:
		return errors.New(resp)
	}
}

// WriteNegotiationAcceptance writes the 'accept' response to w (usually a
// net.Conn).
func WriteNegotiationAcceptance(w io.Writer) error {
	return encoding.WriteObject(w, AcceptResponse)
}

// WriteNegotiationRejection will write a rejection response to w (usually a
// net.Conn) and return the input error. If the write fails, the write error
// is joined with the input error.
func WriteNegotiationRejection(w io.Writer, err error) error {
	writeErr := encoding.WriteObject(w, err.Error())
	if writeErr != nil {
		return build.JoinErrors([]error{err, writeErr}, "; ")
	}
	return err
}

// WriteNegotiationStop writes the 'stop' response to w (usually a
// net.Conn).
func WriteNegotiationStop(w io.Writer) error {
	return encoding.WriteObject(w, StopResponse)
}

// CreateAnnouncement will take a host announcement and encode it, returning
// the exact []byte that should be added to the arbitrary data of a
// transaction.
func CreateAnnouncement(addr NetAddress, pk types.SiaPublicKey, sk crypto.SecretKey) (signedAnnouncement []byte, err error) {
	if err := addr.IsValid(); err != nil {
		return nil, err
	}

	// Create the HostAnnouncement and marshal it.
	annBytes := encoding.Marshal(HostAnnouncement{
		Specifier:  PrefixHostAnnouncement,
		NetAddress: addr,
		PublicKey:  pk,
	})

	// Create a signature for the announcement.
	annHash := crypto.HashBytes(annBytes)
	sig := crypto.SignHash(annHash, sk)
	// Return the signed announcement.
	return append(annBytes, sig[:]...), nil
}

// DecodeAnnouncement decodes announcement bytes into a host announcement,
// verifying the prefix and the signature.
func DecodeAnnouncement(fullAnnouncement []byte) (na NetAddress, spk types.SiaPublicKey, err error) {
	// Read the first part of the announcement to get the intended host
	// announcement.
	var ha HostAnnouncement
	dec := encoding.NewDecoder(bytes.NewReader(fullAnnouncement), len(fullAnnouncement)*3)
	err = dec.Decode(&ha)
	if err != nil {
		return "", types.SiaPublicKey{}, err
	}

	// Check that the announcement was registered as a host announcement.
	if ha.Specifier != PrefixHostAnnouncement {
		return "", types.SiaPublicKey{}, ErrAnnNotAnnouncement
	}
	// Check that the public key is a recognized type of public key.
	if ha.PublicKey.Algorithm != types.SignatureEd25519 {
		return "", types.SiaPublicKey{}, ErrAnnUnrecognizedSignature
	}

	// Read the signature out of the reader.
	var sig crypto.Signature
	err = dec.Decode(&sig)
	if err != nil {
		return "", types.SiaPublicKey{}, err
	}
	// Verify the signature.
	var pk crypto.PublicKey
	copy(pk[:], ha.PublicKey.Key)
	annHash := crypto.HashObject(ha)
	err = crypto.VerifyHash(annHash, pk, sig)
	if err != nil {
		return "", types.SiaPublicKey{}, err
	}
	return ha.NetAddress, ha.PublicKey, nil
}

// IsOOSErr is a helper function to determine whether an error from a host is
// indicating that they are out of storage.
//
// Note: To preserve compatibility, this function needsd to be extended
// exclusively by adding more checks, the existing checks should not be altered
// or removed.
func IsOOSErr(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), V1420HostOutOfStorageErrString) {
		return true
	}
	return false
}

// IsContractNotRecognizedErr is a helper function to determine whether an error
// from a host is a indicating that they do not recognize a contract that the
// renter is updating.
//
// Note: To preserve compatibility, this function needsd to be extended
// exclusively by adding more checks, the existing checks should not be altered
// or removed.
func IsContractNotRecognizedErr(err error) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), V1420ContractNotRecognizedErrString) {
		return true
	}
	return false
}

// VerifyFileContractRevisionTransactionSignatures checks that the signatures
// on a file contract revision are valid and cover the right fields.
func VerifyFileContractRevisionTransactionSignatures(fcr types.FileContractRevision, tsigs []types.TransactionSignature, height types.BlockHeight) error {
	if len(tsigs) != 2 {
		return ErrRevisionSigCount
	}
	for _, tsig := range tsigs {
		// The transaction needs to be malleable so that miner fees can be
		// added. If the whole transaction is covered, it is doomed to have no
		// fees.
		if tsig.CoveredFields.WholeTransaction {
			return ErrRevisionCoveredFields
		}
	}
	txn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{fcr},
		TransactionSignatures: tsigs,
	}
	// Check that the signatures verify. This will also check that the covered
	// fields object is not over-aggressive, because if the object is pointing
	// to elements that haven't been added to the transaction, verification
	// will fail.
	return txn.StandaloneValid(height)
}

// VerifyRenewalTransactionSignatures checks that the signatures on a file
// contract and revision are valid and cover the right fields.
func VerifyRenewalTransactionSignatures(fcr types.FileContractRevision, fc types.FileContract, tsigs []types.TransactionSignature, height types.BlockHeight) error {
	if len(tsigs) != 2 {
		return ErrRevisionSigCount
	}
	for _, tsig := range tsigs {
		// The transaction needs to be malleable so that miner fees can be
		// added. If the whole transaction is covered, it is doomed to have no
		// fees.
		if tsig.CoveredFields.WholeTransaction {
			return ErrRevisionCoveredFields
		}
	}
	txn := types.Transaction{
		FileContracts:         []types.FileContract{fc},
		FileContractRevisions: []types.FileContractRevision{fcr},
		TransactionSignatures: tsigs,
	}
	// Check that the signatures verify. This will also check that the covered
	// fields object is not over-aggressive, because if the object is pointing
	// to elements that haven't been added to the transaction, verification
	// will fail.
	return txn.StandaloneValid(height)
}

// RenterPayoutsPreTax calculates the renterPayout before tax and the hostPayout
// given a host, the available renter funding, the expected txnFee for the
// transaction and an optional basePrice in case this helper is used for a
// renewal. It also returns the hostCollateral.
func RenterPayoutsPreTax(host HostDBEntry, funding, txnFee, basePrice, baseCollateral types.Currency, period types.BlockHeight, expectedStorage uint64) (renterPayout, hostPayout, hostCollateral types.Currency, err error) {
	// Divide by zero check.
	if host.StoragePrice.IsZero() {
		host.StoragePrice = types.NewCurrency64(1)
	}
	// Underflow check.
	if funding.Cmp(host.ContractPrice.Add(txnFee).Add(basePrice)) <= 0 {
		err = fmt.Errorf("contract price (%v) plus transaction fee (%v) plus base price (%v) exceeds funding (%v)",
			host.ContractPrice.HumanString(), txnFee.HumanString(), basePrice.HumanString(), funding.HumanString())
		return
	}
	// Calculate renterPayout.
	renterPayout = funding.Sub(host.ContractPrice).Sub(txnFee).Sub(basePrice)
	// Calculate hostCollateral by calculating the maximum amount of storage
	// the renter can afford with 'funding' and calculating how much collateral
	// the host wouldl have to put into the contract for that. We also add a
	// potential baseCollateral.
	maxStorageSizeTime := renterPayout.Div(host.StoragePrice)
	hostCollateral = maxStorageSizeTime.Mul(host.Collateral).Add(baseCollateral)
	// Don't add more collateral than 10x the collateral for the expected
	// storage to save on fees.
	maxRenterCollateral := host.Collateral.Mul64(uint64(period)).Mul64(expectedStorage).Mul64(5)
	if hostCollateral.Cmp(maxRenterCollateral) > 0 {
		hostCollateral = maxRenterCollateral
	}
	// Don't add more collateral than the host is willing to put into a single
	// contract.
	if hostCollateral.Cmp(host.MaxCollateral) > 0 {
		hostCollateral = host.MaxCollateral
	}
	// Calculate hostPayout.
	hostPayout = hostCollateral.Add(host.ContractPrice).Add(basePrice)
	return
}

// RPCBeginSubscription begins a subscription on a new stream and returns
// it.
func RPCBeginSubscription(stream siamux.Stream, host types.SiaPublicKey, pt *RPCPriceTable, accID AccountID, accSK crypto.SecretKey, initialBudget types.Currency, bh types.BlockHeight, subscriber types.Specifier) error {
	// initiate the RPC
	buf := bytes.NewBuffer(nil)
	err := RPCWrite(buf, RPCRegistrySubscription)
	if err != nil {
		return err
	}

	// Write the pricetable uid.
	err = RPCWrite(buf, pt.UID)
	if err != nil {
		return err
	}

	// Provide the payment.
	err = RPCProvidePayment(buf, accID, accSK, bh, initialBudget)
	if err != nil {
		return err
	}

	// Send subscriber.
	err = RPCWrite(buf, subscriber)
	if err != nil {
		return err
	}

	// Write buffer to stream.
	_, err = buf.WriteTo(stream)
	if err != nil {
		return err
	}

	return nil
}

// RPCProvidePayment is a helper function that provides a payment by writing the
// required payment request objects to the given stream.
func RPCProvidePayment(stream io.Writer, accID AccountID, accSK crypto.SecretKey, blockHeight types.BlockHeight, amount types.Currency) error {
	// Send the payment request.
	err := RPCWrite(stream, PaymentRequest{Type: PayByEphemeralAccount})
	if err != nil {
		return err
	}

	// Send the payment details.
	pbear := NewPayByEphemeralAccountRequest(accID, blockHeight, amount, accSK)
	err = RPCWrite(stream, pbear)
	if err != nil {
		return err
	}
	return nil
}

// RPCStopSubscription gracefully stops a subscription session.
func RPCStopSubscription(stream siamux.Stream) (err error) {
	// Close the stream at the end.
	defer func() {
		err = errors.Compose(err, stream.Close())
	}()
	// Write the stop signal.
	err = RPCWrite(stream, SubscriptionRequestStop)
	if err != nil {
		return errors.AddContext(err, "StopSubscription failed to send specifier")
	}
	// Read a byte to block until the host closed the connection.
	_, err = stream.Read(make([]byte, 1))
	if err == nil || !strings.Contains(err.Error(), io.ErrClosedPipe.Error()) {
		return errors.AddContext(err, "StopSubscription failed to wait for closed stream")
	}
	return nil
}

// RPCSubscribeToRVs subscribes to the given publickey/tweak pairs.
func RPCSubscribeToRVs(stream siamux.Stream, requests []RPCRegistrySubscriptionRequest) ([]RPCRegistrySubscriptionNotificationEntryUpdate, error) {
	// Send the type of the request.
	buf := bytes.NewBuffer(nil)
	err := RPCWrite(buf, SubscriptionRequestSubscribe)
	if err != nil {
		return nil, err
	}
	// Send the request.
	err = RPCWrite(buf, requests)
	if err != nil {
		return nil, err
	}
	// Write buffer to stream.
	_, err = buf.WriteTo(stream)
	if err != nil {
		return nil, err
	}
	// Read response.
	var rvs []SignedRegistryValue
	err = RPCRead(stream, &rvs)
	if err != nil {
		return nil, err
	}
	// Check the length of the response.
	if len(rvs) > len(requests) {
		return nil, fmt.Errorf("host returned more rvs than we subscribed to %v > %v", len(rvs), len(requests))
	}
	// Verify response. The rvs should be returned in the same order as
	// requested so we start by verifying against the first request and work our
	// way through to the last one. If not all rvs are verified successfully
	// after running out of requests, something is wrong.
	left := rvs
	var initialNotifications []RPCRegistrySubscriptionNotificationEntryUpdate
	for _, req := range requests {
		if len(left) == 0 {
			return initialNotifications, nil // all returned notifications were verified
		}
		rv := left[0]
		err = rv.Verify(req.PubKey.ToPublicKey())
		if err != nil {
			continue // try next request
		}
		left = left[1:]
		initialNotifications = append(initialNotifications, RPCRegistrySubscriptionNotificationEntryUpdate{
			Entry:  rv,
			PubKey: req.PubKey,
		})
	}
	if len(left) > 0 {
		return nil, fmt.Errorf("failed to verify %v of %v rvs", len(left), len(rvs))
	}
	return initialNotifications, nil
}

// RPCSubscribeToRVs subscribes to the given publickey/tweak pairs.
func RPCSubscribeToRVsByRID(stream siamux.Stream, requests []RPCRegistrySubscriptionByRIDRequest) ([]RPCRegistrySubscriptionNotificationEntryUpdate, error) {
	// Send the type of the request.
	buf := bytes.NewBuffer(nil)
	err := RPCWrite(buf, SubscriptionRequestSubscribeRID)
	if err != nil {
		return nil, err
	}
	// Send the request.
	err = RPCWrite(buf, requests)
	if err != nil {
		return nil, err
	}
	// Write buffer to stream.
	_, err = buf.WriteTo(stream)
	if err != nil {
		return nil, err
	}
	// Read response.
	var rvs []RPCRegistrySubscriptionNotificationEntryUpdate
	err = RPCRead(stream, &rvs)
	if err != nil {
		return nil, err
	}
	// Check the length of the response.
	if len(rvs) > len(requests) {
		return nil, fmt.Errorf("host returned more rvs than we subscribed to %v > %v", len(rvs), len(requests))
	}
	// Verify response. The rvs should be returned in the same order as
	// requested so we start by verifying against the first request and work our
	// way through to the last one. If not all rvs are verified successfully
	// after running out of requests, something is wrong.
	for _, rv := range rvs {
		err = rv.Entry.Verify(rv.PubKey.ToPublicKey())
		if err != nil {
			return nil, err
		}
	}
	return rvs, nil
}

// RPCUnsubscribeFromRVs unsubscribes from the given publickey/tweak pairs.
func RPCUnsubscribeFromRVs(stream siamux.Stream, requests []RPCRegistrySubscriptionRequest) error {
	// Send the type of the request.
	err := RPCWrite(stream, SubscriptionRequestUnsubscribe)
	if err != nil {
		return err
	}
	// Send the request.
	err = RPCWrite(stream, requests)
	if err != nil {
		return err
	}
	// Read the "OK" response.
	var resp RPCRegistrySubscriptionNotificationType
	err = RPCRead(stream, &resp)
	if err != nil {
		return err
	}
	if resp.Type != SubscriptionResponseUnsubscribeSuccess {
		return fmt.Errorf("wrong type was returned: %v", resp.Type)
	}
	return nil
}

// RPCUnsubscribeFromRVsByRID unsubscribes from the given publickey/tweak pairs.
func RPCUnsubscribeFromRVsByRID(stream siamux.Stream, requests []RPCRegistrySubscriptionByRIDRequest) error {
	// Send the type of the request.
	err := RPCWrite(stream, SubscriptionRequestUnsubscribeRID)
	if err != nil {
		return err
	}
	// Send the request.
	err = RPCWrite(stream, requests)
	if err != nil {
		return err
	}
	// Read the "OK" response.
	var resp RPCRegistrySubscriptionNotificationType
	err = RPCRead(stream, &resp)
	if err != nil {
		return err
	}
	if resp.Type != SubscriptionResponseUnsubscribeSuccess {
		return fmt.Errorf("wrong type was returned: %v", resp.Type)
	}
	return nil
}

// RPCFundSubscription pays the host to increase the subscription budget.
func RPCFundSubscription(stream siamux.Stream, host types.SiaPublicKey, accID AccountID, accSK crypto.SecretKey, bh types.BlockHeight, fundAmt types.Currency) error {
	// Send the type of the request.
	buf := bytes.NewBuffer(nil)
	err := RPCWrite(buf, SubscriptionRequestPrepay)
	if err != nil {
		return err
	}

	// Provide the payment.
	err = RPCProvidePayment(buf, accID, accSK, bh, fundAmt)
	if err != nil {
		return err
	}

	// Write buffer to stream.
	_, err = buf.WriteTo(stream)
	return err
}

// RPCExtendSubscription extends the subscription with the given price table.
func RPCExtendSubscription(stream siamux.Stream, pt *RPCPriceTable) error {
	// Send the type of the request.
	err := RPCWrite(stream, SubscriptionRequestExtend)
	if err != nil {
		return err
	}

	// Write the pricetable uid.
	err = RPCWrite(stream, pt.UID)
	if err != nil {
		return err
	}
	return nil
}
