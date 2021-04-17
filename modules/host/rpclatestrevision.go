package host

import (
	"fmt"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
	"go.sia.tech/siad/modules"
)

// managedRPCLatestRevision handles the RPC that fetches the latest revision for
// a given contract from the host.
func (h *Host) managedRPCLatestRevision(stream siamux.Stream) (err error) {
	// Read request
	var lrr modules.RPCLatestRevisionRequest
	err = modules.RPCRead(stream, &lrr)
	if err != nil {
		return errors.AddContext(err, "failed to read LatestRevisionRequest")
	}

	// Read storage obligation.
	so, err := h.managedGetStorageObligationSnapshot(lrr.FileContractID)
	if err != nil {
		return errors.AddContext(err, fmt.Sprintf("failed to get storage obligation for contract with id %v", lrr.FileContractID))
	}

	// Send response.
	err = modules.RPCWrite(stream, modules.RPCLatestRevisionResponse{
		Revision: so.RecentRevision(),
	})
	if err != nil {
		return errors.AddContext(err, "failed to send LatestRevisionResponse")
	}

	// read the price table
	pt, err := h.staticReadPriceTableID(stream)
	if err != nil {
		return errors.AddContext(err, "failed to read price table")
	}

	// Process payment.
	pd, err := h.ProcessPayment(stream, pt.HostBlockHeight)
	if err != nil {
		return errors.AddContext(err, "failed to process payment")
	}

	// Check payment.
	if pd.Amount().Cmp(pt.LatestRevisionCost) < 0 {
		return modules.ErrInsufficientPaymentForRPC
	}

	// Refund excessive payment.
	refund := pd.Amount().Sub(pt.LatestRevisionCost)
	if !refund.IsZero() {
		err = h.staticAccountManager.callRefund(pd.AccountID(), refund)
		if err != nil {
			return errors.AddContext(err, "failed to refund excessive payment")
		}
	}
	return nil
}
