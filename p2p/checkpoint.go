package p2p

import (
	"context"
	"errors"
	"fmt"
	"net"

	"go.sia.tech/core/consensus"
	"go.sia.tech/core/net/gateway"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
)

// DownloadCheckpoint connects to addr and downloads the checkpoint at the
// specified index.
func DownloadCheckpoint(ctx context.Context, addr string, genesisID types.BlockID, index types.ChainIndex) (consensus.Checkpoint, error) {
	// dial peer
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return consensus.Checkpoint{}, fmt.Errorf("couldn't establish connection to peer: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	peer, err := gateway.DialSession(conn, genesisID, gateway.GenerateUniqueID())
	if err != nil {
		return consensus.Checkpoint{}, fmt.Errorf("couldn't establish session with peer: %w", err)
	}
	// reject all peer-initiated streams
	go func() {
		for {
			stream, err := peer.AcceptStream()
			if err != nil {
				return
			}
			stream.Close()
		}
	}()

	// request checkpoint
	stream, err := peer.DialStream()
	if err != nil {
		return consensus.Checkpoint{}, fmt.Errorf("couldn't establish stream with peer: %w", err)
	}
	defer stream.Close()
	if err := rpc.WriteRequest(stream, rpcGetCheckpoint, &msgGetCheckpoint{Index: index}); err != nil {
		return consensus.Checkpoint{}, fmt.Errorf("couldn't write RPC request: %w", err)
	}
	var resp msgCheckpoint
	if err := rpc.ReadResponse(stream, &resp); err != nil {
		return consensus.Checkpoint{}, fmt.Errorf("couldn't read RPC response: %w", err)
	}

	// validate response
	if resp.Block.Index() != index || resp.Block.Header.ParentIndex() != resp.ParentContext.Index {
		return consensus.Checkpoint{}, errors.New("wrong checkpoint header")
	}
	commitment := resp.ParentContext.Commitment(resp.Block.Header.MinerAddress, resp.Block.Transactions)
	if commitment != resp.Block.Header.Commitment {
		return consensus.Checkpoint{}, errors.New("wrong checkpoint commitment")
	}

	return consensus.Checkpoint{
		Block:   resp.Block,
		Context: consensus.ApplyBlock(resp.ParentContext, resp.Block).Context,
	}, nil
}
