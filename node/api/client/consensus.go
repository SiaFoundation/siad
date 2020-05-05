package client

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"time"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/types"
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
	_, body, err := c.getReaderResponse(fmt.Sprintf("/consensus/subscribe/%s", ccid))
	if err != nil {
		return ccid, err
	}
	defer ioutil.ReadAll(body) // ensure that we fully read the body, even if we return early

	dec := encoding.NewDecoder(body, 1e6)
	for {
		var cc modules.ConsensusChange
		if err := dec.Decode(&cc); errors.Is(err, io.EOF) {
			return ccid, nil
		} else if err != nil {
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
// Subsequent errors may be handled asynchronously.
func (c *Client) ConsensusSetSubscribe(subscriber modules.ConsensusSetSubscriber, ccid modules.ConsensusChangeID, cancel <-chan struct{}) <-chan error {
	ch := make(chan error, 2)
	go func() {
		// poll until caught up
		for caughtUp := false; !caughtUp; {
			newID, err := c.ConsensusSubscribeSingle(subscriber, ccid, cancel)
			if err != nil {
				ch <- err
				return
			}
			caughtUp = newID == ccid
			ccid = newID
		}
		// caught up successfully
		ch <- nil
		// request every half-block-interval indefinitely
		for {
			time.Sleep(time.Duration(types.BlockFrequency) * time.Second / 2)
			newID, err := c.ConsensusSubscribeSingle(subscriber, ccid, cancel)
			if err != nil {
				ch <- err
				return
			}
			ccid = newID
		}
	}()
	return ch
}
