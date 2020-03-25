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

// FeeManagerCancelPost uses the /feemanager/cancel POST endpoint to cancel a
// fee being managed by the FeeManager
func (c *Client) FeeManagerCancelPost(feeUID modules.FeeUID) (err error) {
	values := url.Values{}
	values.Set("feeuid", string(feeUID))
	err = c.post("/feemanager/cancel", values.Encode(), nil)
	return
}

// FeeManagerSetPost use the /feemanager/set POST endpoint to set a fee for the
// FeeManager to manage
func (c *Client) FeeManagerSetPost(address types.UnlockHash, amount types.Currency, appUID modules.AppUID, reoccuring bool) (err error) {
	values := url.Values{}
	values.Set("address", address.String())
	values.Set("amount", amount.String())
	values.Set("appuid", string(appUID))
	values.Set("reoccuring", fmt.Sprint(reoccuring))
	err = c.post("/feemanager/set", values.Encode(), nil)
	return
}
