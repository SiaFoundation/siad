package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/node/api"
	"go.sia.tech/siad/types"
)

// ConsensusGet requests the /consensus api resource
func (c *Client) ConsensusGet() (cg api.ConsensusGET, err error) {
	err = c.get("/consensus", &cg)
	return
}

// ConsensusBlocksIDGet requests the /consensus/blocks api resource
func (c *Client) ConsensusBlocksIDGet(id types.BlockID) (cbg api.ConsensusBlocksGet, err error) {
	err = c.get("/consensus/blocks?id="+id.String(), &cbg)
	return
}

// ConsensusBlocksHeightGet requests the /consensus/blocks api resource
func (c *Client) ConsensusBlocksHeightGet(height types.BlockHeight) (cbg api.ConsensusBlocksGet, err error) {
	err = c.get("/consensus/blocks?height="+fmt.Sprint(height), &cbg)
	return
}

// ConsensusSubscribeSingle streams consensus changes from the
// /consensus/subscribe endpoint to the provided subscriber. Multiple calls may
// be required before the subscriber is fully caught up. It returns the latest
// change ID; if no changes were sent, this will be the same as the input ID.
func (c *Client) ConsensusSubscribeSingle(subscriber modules.ConsensusSetSubscriber, ccid modules.ConsensusChangeID, cancel <-chan struct{}) (modules.ConsensusChangeID, error) {
	// We need to cancel the request when the cancel chan closes, so we have to
	// construct it manually.
	req, err := c.NewRequest("GET", fmt.Sprintf("/consensus/subscribe/%s", ccid), nil)
	if err != nil {
		return ccid, err
	}
	req.Cancel = cancel
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ccid, err
	}
	defer drainAndClose(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return ccid, readAPIError(resp.Body)
	}

	for {
		select {
		case <-cancel:
			return ccid, context.Canceled
		default:
		}
		dec := encoding.NewDecoder(resp.Body, 100e6) // consensus changes can be arbitrarily large
		var cc modules.ConsensusChange
		if err := dec.Decode(&cc); errors.Is(err, io.EOF) {
			return ccid, nil
		} else if err != nil {
			resp.Body.Close()
			return ccid, err
		}
		subscriber.ProcessConsensusChange(cc)
		ccid = cc.ID
	}
}

// ConsensusSetSubscribe polls the /consensus/subscribe endpoint, streaming
// consensus changes to the subscriber indefinitely. First, it will stream
// changes until the subscriber is fully caught up. It will send any error
// encountered down the returned channel, or nil. Consequently, the caller
// should always wait for the first error before proceeding with initialization.
// Subsequent errors may be handled asynchronously. It also returns a function
// that can be called to unsubscribe from further changes.
func (c *Client) ConsensusSetSubscribe(subscriber modules.ConsensusSetSubscriber, ccid modules.ConsensusChangeID, cancel <-chan struct{}) (<-chan error, func()) {
	ch := make(chan error, 2)
	cancelMux := make(chan struct{})
	unsub := make(chan struct{})
	go func() {
		select {
		case <-cancel:
			close(cancelMux)
		case <-unsub:
			close(cancelMux)
		}
	}()

	// helper function: poll repeatedly until we're caught up
	catchUp := func() error {
		for {
			newID, err := c.ConsensusSubscribeSingle(subscriber, ccid, cancelMux)
			if err != nil {
				return err
			} else if newID == ccid {
				return nil // done
			}
			ccid = newID
		}
	}

	go func() {
		// initial sync
		ch <- catchUp()

		// re-sync every half-block-interval, indefinitely
		for {
			select {
			case <-time.After(time.Duration(types.BlockFrequency) * time.Second / 2):
			case <-unsub:
				return
			}
			if err := catchUp(); err != nil {
				ch <- err
				return
			}
		}
	}()
	return ch, func() { close(unsub) }
}
