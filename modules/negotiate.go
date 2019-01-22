package modules

import (
	"bytes"
	"crypto/cipher"
	"errors"
	"io"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
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
		Testing:  3 * time.Second,
	}).(time.Duration)
)

var (
	// ActionDelete is the specifier for a RevisionAction that deletes a
	// sector.
	ActionDelete = types.Specifier{'D', 'e', 'l', 'e', 't', 'e'}

	// ActionInsert is the specifier for a RevisionAction that inserts a
	// sector.
	ActionInsert = types.Specifier{'I', 'n', 's', 'e', 'r', 't'}

	// ActionModify is the specifier for a RevisionAction that modifies sector
	// data.
	ActionModify = types.Specifier{'M', 'o', 'd', 'i', 'f', 'y'}

	// ErrAnnNotAnnouncement indicates that the provided host announcement does
	// not use a recognized specifier, indicating that it's either not a host
	// announcement or it's not a recognized version of a host announcement.
	ErrAnnNotAnnouncement = errors.New("provided data does not form a recognized host announcement")

	// ErrAnnUnrecognizedSignature is returned when the signature in a host
	// announcement is not a type of signature that is recognized.
	ErrAnnUnrecognizedSignature = errors.New("the signature provided in the host announcement is not recognized")

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
	PrefixHostAnnouncement = types.Specifier{'H', 'o', 's', 't', 'A', 'n', 'n', 'o', 'u', 'n', 'c', 'e', 'm', 'e', 'n', 't'}

	// PrefixFileContractIdentifier is used to indicate that a transaction's
	// Arbitrary Data field contains a file contract identifier. The identifier
	// and its signature will follow this prefix.
	PrefixFileContractIdentifier = types.Specifier{'F', 'C', 'I', 'd', 'e', 'n', 't', 'i', 'f', 'i', 'e', 'r'}

	// RPCDownload is the specifier for downloading a file from a host.
	RPCDownload = types.Specifier{'D', 'o', 'w', 'n', 'l', 'o', 'a', 'd', 2}

	// RPCFormContract is the specifier for forming a contract with a host.
	RPCFormContract = types.Specifier{'F', 'o', 'r', 'm', 'C', 'o', 'n', 't', 'r', 'a', 'c', 't', 2}

	// RPCRenewContract is the specifier to renewing an existing contract.
	RPCRenewContract = types.Specifier{'R', 'e', 'n', 'e', 'w', 'C', 'o', 'n', 't', 'r', 'a', 'c', 't', 2}

	// RPCReviseContract is the specifier for revising an existing file
	// contract.
	RPCReviseContract = types.Specifier{'R', 'e', 'v', 'i', 's', 'e', 'C', 'o', 'n', 't', 'r', 'a', 'c', 't', 2}

	// RPCSettings is the specifier for requesting settings from the host.
	RPCSettings = types.Specifier{'S', 'e', 't', 't', 'i', 'n', 'g', 's', 2}

	// SectorSize defines how large a sector should be in bytes. The sector
	// size needs to be a power of two to be compatible with package
	// merkletree. 4MB has been chosen for the live network because large
	// sectors significantly reduce the tracking overhead experienced by the
	// renter and the host.
	SectorSize = build.Select(build.Var{
		Dev:      uint64(1 << 18), // 256 KiB
		Standard: uint64(1 << 22), // 4 MiB
		Testing:  uint64(1 << 12), // 4 KiB
	}).(uint64)
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
		// the host just to open a file contract with them. Generally, the
		// price is only to cover the siacoin fees that the host will suffer
		// when submitting the file contract revision and storage proof to the
		// blockchain.
		//
		// The storage price is the cost per-byte-per-block in hastings of
		// storing data on the host.
		//
		// 'Download' bandwidth price is the cost per byte of downloading data
		// from the host.
		//
		// 'Upload' bandwidth price is the cost per byte of uploading data to
		// the host.
		ContractPrice          types.Currency `json:"contractprice"`
		DownloadBandwidthPrice types.Currency `json:"downloadbandwidthprice"`
		StoragePrice           types.Currency `json:"storageprice"`
		UploadBandwidthPrice   types.Currency `json:"uploadbandwidthprice"`

		// Because the host has a public key, and settings are signed, and
		// because settings may be MITM'd, settings need a revision number so
		// that a renter can compare multiple sets of settings and determine
		// which is the most recent.
		RevisionNumber uint64 `json:"revisionnumber"`
		Version        string `json:"version"`
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

// New RPC IDs
var (
	RPCLoopEnter         = types.Specifier{'L', 'o', 'o', 'p', 'E', 'n', 't', 'e', 'r'}
	RPCLoopExit          = types.Specifier{'L', 'o', 'o', 'p', 'E', 'x', 'i', 't'}
	RPCLoopFormContract  = types.Specifier{'L', 'o', 'o', 'p', 'F', 'o', 'r', 'm', 'C', 'o', 'n', 't', 'r', 'a', 'c', 't'}
	RPCLoopLock          = types.Specifier{'L', 'o', 'o', 'p', 'L', 'o', 'c', 'k'}
	RPCLoopRead          = types.Specifier{'L', 'o', 'o', 'p', 'R', 'e', 'a', 'd'}
	RPCLoopRenewContract = types.Specifier{'L', 'o', 'o', 'p', 'R', 'e', 'n', 'e', 'w'}
	RPCLoopSectorRoots   = types.Specifier{'L', 'o', 'o', 'p', 'S', 'e', 'c', 't', 'o', 'r', 'R', 'o', 'o', 't', 's'}
	RPCLoopSettings      = types.Specifier{'L', 'o', 'o', 'p', 'S', 'e', 't', 't', 'i', 'n', 'g', 's'}
	RPCLoopUnlock        = types.Specifier{'L', 'o', 'o', 'p', 'U', 'n', 'l', 'o', 'c', 'k'}
	RPCLoopWrite         = types.Specifier{'L', 'o', 'o', 'p', 'W', 'r', 'i', 't', 'e'}
)

// RPC ciphers
var (
	CipherChaCha20Poly1305 = types.Specifier{'C', 'h', 'a', 'C', 'h', 'a', '2', '0', 'P', 'o', 'l', 'y', '1', '3', '0', '5'}
	CipherNoOverlap        = types.Specifier{'N', 'o', 'O', 'v', 'e', 'r', 'l', 'a', 'p'}
)

var (
	// RPCChallengePrefix is the prefix prepended to the challenge data
	// supplied by the host when proving ownership of a contract's secret key.
	RPCChallengePrefix = types.Specifier{'c', 'h', 'a', 'l', 'l', 'e', 'n', 'g', 'e'}
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
	}

	// LoopSettingsResponse contains the response data for RPCLoopSettingsResponse.
	LoopSettingsResponse struct {
		Settings HostExternalSettings
	}

	// LoopWriteRequest contains the request parameters for RPCLoopWrite.
	LoopWriteRequest struct {
		Data []byte

		NewRevisionNumber    uint64
		NewValidProofValues  []types.Currency
		NewMissedProofValues []types.Currency
		Signature            []byte
	}

	// LoopWriteResponse contains the response data for RPCLoopWriteResponse.
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
	// plaintext may contain padding, so use Decoder instead of Unmarshal
	return encoding.NewDecoder(bytes.NewReader(plaintext)).Decode(obj)
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
	var unencryptedResp []byte
	if err == nil {
		unencryptedResp = encoding.MarshalAll((*RPCError)(nil), resp)
	} else {
		re, ok := err.(*RPCError)
		if !ok {
			re = &RPCError{Description: err.Error()}
		}
		unencryptedResp = encoding.Marshal(re)
	}
	return encoding.WritePrefixedBytes(w, crypto.EncryptWithNonce(unencryptedResp, aead))
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
	encryptedResp, err := encoding.ReadPrefixedBytes(r, maxLen)
	if err != nil {
		return err
	}
	decryptedResp, err := crypto.DecryptWithNonce(encryptedResp, aead)
	if err != nil {
		return err
	}
	dec := encoding.NewDecoder(bytes.NewReader(decryptedResp))
	var rpcErr *RPCError
	if err := dec.Decode(&rpcErr); err != nil {
		return err
	} else if rpcErr != nil {
		return rpcErr
	}
	return dec.Decode(resp)
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
	dec := encoding.NewDecoder(bytes.NewReader(fullAnnouncement))
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
	if funding.Cmp(host.ContractPrice.Add(txnFee)) <= 0 {
		err = errors.New("underflow detected, funding < contractPrice + txnFee")
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
