package client

import (
	"fmt"
	"net/url"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/types"
)

// FeeManagerGet uses the /feemanager GET endpoint to return information about
// the FeeManager
func (c *Client) FeeManagerGet() (fmg api.FeeManagerGET, err error) {
	err = c.get("/feemanager", &fmg)
	return
}

// FeeManagerAddPost use the /feemanager/add POST endpoint to add a fee for the
// FeeManager to manage
func (c *Client) FeeManagerAddPost(address types.UnlockHash, amount types.Currency, appUID modules.AppUID, recurring bool) (fmap api.FeeManagerAddFeePOST, err error) {
	values := url.Values{}
	values.Set("address", address.String())
	values.Set("amount", amount.String())
	values.Set("appuid", string(appUID))
	values.Set("recurring", fmt.Sprint(recurring))
	err = c.post("/feemanager/add", values.Encode(), &fmap)
	return
}

// FeeManagerCancelPost uses the /feemanager/cancel POST endpoint to cancel a
// fee being managed by the FeeManager
func (c *Client) FeeManagerCancelPost(feeUID modules.FeeUID) (err error) {
	values := url.Values{}
	values.Set("feeuid", string(feeUID))
	err = c.post("/feemanager/cancel", values.Encode(), nil)
	return
}

// FeeManagerPaidFeesGet uses the /feemanager/paidfees GET endpoint to return
// the paid fees the FeeManager is tracking
func (c *Client) FeeManagerPaidFeesGet() (pfg api.FeeManagerPaidFeesGET, err error) {
	err = c.get("/feemanager/paidfees", &pfg)
	return
}

// FeeManagerPendingFeesGet uses the /feemanager/pendingfees GET endpoint to
// return the pending fees the FeeManager is tracking
func (c *Client) FeeManagerPendingFeesGet() (pfg api.FeeManagerPendingFeesGET, err error) {
	err = c.get("/feemanager/pendingfees", &pfg)
	return
}
