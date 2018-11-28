package host

import (
	"errors"
	"io"
	"net"
	"time"

	"github.com/coreos/bbolt"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// managedRPCLoop reads new RPCs from the renter, each consisting of a single
// request and response. The loop terminates when the an RPC encounters an
// error or the renter sends modules.RPCLoopExit.
func (h *Host) managedRPCLoop(conn net.Conn) error {
	// perform initial handshake
	conn.SetDeadline(time.Now().Add(rpcRequestInterval))
	var req modules.LoopHandshakeRequest
	if err := encoding.NewDecoder(conn).Decode(&req); err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}

	// check handshake version and ciphers
	if req.Version != 1 {
		err := errors.New("protocol version not supported")
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}
	var supportsPlaintext bool
	for _, c := range req.Ciphers {
		if c == modules.CipherPlaintext {
			supportsPlaintext = true
		}
	}
	if !supportsPlaintext {
		err := errors.New("no supported ciphers")
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}

	// send handshake response
	var challenge [16]byte
	fastrand.Read(challenge[:])
	resp := modules.LoopHandshakeResponse{
		Cipher:    modules.CipherPlaintext,
		Challenge: challenge,
	}
	if err := modules.WriteRPCResponse(conn, resp, nil); err != nil {
		return err
	}

	// read challenge response
	var cresp modules.LoopChallengeResponse
	if err := encoding.NewDecoder(conn).Decode(&cresp); err != nil {
		modules.WriteRPCResponse(conn, nil, err)
		return err
	}

	// if a contract was supplied, look it up, verify the challenge response,
	// and lock the storage obligation
	var so storageObligation
	if req.ContractID != (types.FileContractID{}) {
		// NOTE: if we encounter an error here, we send it to the renter and
		// close the connection immediately. From the renter's perspective,
		// this error may arrive either before or after sending their first
		// RPC request.

		// look up the renter's public key
		var err error
		h.mu.RLock()
		err = h.db.View(func(tx *bolt.Tx) error {
			so, err = getStorageObligation(tx, req.ContractID)
			return err
		})
		h.mu.RUnlock()
		if err != nil {
			modules.WriteRPCResponse(conn, nil, errors.New("no record of that contract"))
			return extendErr("could not lock contract "+req.ContractID.String()+": ", err)
		}

		// verify the challenge response
		rev := so.RevisionTransactionSet[len(so.RevisionTransactionSet)-1].FileContractRevisions[0]
		hash := crypto.HashAll(modules.RPCChallengePrefix, challenge)
		var renterPK crypto.PublicKey
		var renterSig crypto.Signature
		copy(renterPK[:], rev.UnlockConditions.PublicKeys[0].Key)
		copy(renterSig[:], cresp.Signature)
		if crypto.VerifyHash(hash, renterPK, renterSig) != nil {
			err := errors.New("challenge signature is invalid")
			modules.WriteRPCResponse(conn, nil, err)
			return err
		}

		// lock the storage obligation until the end of the RPC loop
		if err := h.managedTryLockStorageObligation(req.ContractID); err != nil {
			modules.WriteRPCResponse(conn, nil, err)
			return extendErr("could not lock contract "+req.ContractID.String()+": ", err)
		}
		defer h.managedUnlockStorageObligation(req.ContractID)
	}

	// enter RPC loop
	for {
		conn.SetDeadline(time.Now().Add(rpcRequestInterval))

		var id types.Specifier
		if _, err := io.ReadFull(conn, id[:]); err != nil {
			h.log.Debugf("WARN: renter sent invalid RPC ID: %v", id)
			return errors.New("invalid RPC ID " + id.String())
		}

		var err error
		switch id {
		case modules.RPCLoopSettings:
			err = extendErr("incoming RPCLoopSettings failed: ", h.managedRPCLoopSettings(conn))
		case modules.RPCLoopRecentRevision:
			err = extendErr("incoming RPCLoopRecentRevision failed: ", h.managedRPCLoopRecentRevision(conn, &so, challenge))
		case modules.RPCLoopUpload:
			err = extendErr("incoming RPCLoopUpload failed: ", h.managedRPCLoopUpload(conn, &so))
		case modules.RPCLoopDownload:
			err = extendErr("incoming RPCLoopDownload failed: ", h.managedRPCLoopDownload(conn, &so))
		case modules.RPCLoopSectorRoots:
			err = extendErr("incoming RPCLoopSectorRoots failed: ", h.managedRPCLoopSectorRoots(conn, &so))
		case modules.RPCLoopExit:
			return nil
		default:
			return errors.New("invalid or unknown RPC ID: " + id.String())
		}
		if err != nil {
			return err
		}
	}
}
