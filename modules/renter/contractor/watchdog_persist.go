package contractor

import (
	"gitlab.com/NebulousLabs/Sia/types"
)

// watchdogPersist defines what watchdog data persists across sessions.
type watchdogPersist struct {
	Contracts map[string]fileContractStatusPersist `json:"Contracts"`
}

// fileContractStatusPersist defines what information from fileContractStatus is persisted.
type fileContractStatusPersist struct {
	FormationSweepHeight types.BlockHeight `json:"FormationSweepHeight,omitempty"`
	ContractFound        bool              `json:"ContractFound,omitempty"`
	RevisionFound        uint64            `json:"RevisionFound,omitempty"`
	StorageProofFound    types.BlockHeight `json:"StorageProofFound,omitempty"`

	FormationTxnSet []types.Transaction     `json:"FormationTxnSet,omitempty"`
	ParentOutputs   []types.SiacoinOutputID `json:"ParentOutputs,omitempty"`

	SweepTxn     types.Transaction   `json:"SweepTransaction,omitempty"`
	SweepParents []types.Transaction `json:"SweepParents,omitempty"`

	WindowStart types.BlockHeight `json:"ExpirationWindowStart"`
	WindowEnd   types.BlockHeight `json:"ExpirationWindowEnd"`
}

// persistData returns the data that will be saved to disk for fileContractStatus.
func (d *fileContractStatus) persistData() fileContractStatusPersist {
	persistedParentOutputs := make([]types.SiacoinOutputID, 0, len(d.parentOutputs))
	for oid := range d.parentOutputs {
		persistedParentOutputs = append(persistedParentOutputs, oid)
	}
	return fileContractStatusPersist{
		FormationSweepHeight: d.formationSweepHeight,
		ContractFound:        d.contractFound,
		RevisionFound:        d.revisionFound,
		StorageProofFound:    d.storageProofFound,

		FormationTxnSet: d.formationTxnSet,
		ParentOutputs:   persistedParentOutputs,

		SweepTxn:     d.sweepTxn,
		SweepParents: d.sweepParents,
		WindowStart:  d.windowStart,
		WindowEnd:    d.windowEnd,
	}
}

// persistData returns the data in the watchdog that will be saved to disk.
func (w *watchdog) persistData() watchdogPersist {
	w.mu.Lock()
	defer w.mu.Unlock()

	data := watchdogPersist{
		Contracts: make(map[string]fileContractStatusPersist),
	}
	for fcID, contractData := range w.contracts {
		data.Contracts[fcID.String()] = contractData.persistData()
	}

	return data
}

// newWatchdogFromPersist creates a new watchdog and loads it with the
// information stored in persistData.
func newWatchdogFromPersist(contractor *Contractor, persistData watchdogPersist) (*watchdog, error) {
	w := newWatchdog(contractor)

	var fcID types.FileContractID
	for fcIDString, data := range persistData.Contracts {
		if err := fcID.LoadString(fcIDString); err != nil {
			return nil, err
		}

		// Add persisted contract data to the watchdog.
		contractData := &fileContractStatus{
			formationSweepHeight: data.FormationSweepHeight,
			contractFound:        data.ContractFound,
			revisionFound:        data.RevisionFound,
			storageProofFound:    data.StorageProofFound,

			formationTxnSet: data.FormationTxnSet,
			parentOutputs:   make(map[types.SiacoinOutputID]struct{}),

			sweepTxn:     data.SweepTxn,
			sweepParents: data.SweepParents,
			windowStart:  data.WindowStart,
			windowEnd:    data.WindowEnd,
		}
		for _, oid := range data.ParentOutputs {
			contractData.parentOutputs[oid] = struct{}{}
		}
		w.contracts[fcID] = contractData

		// Add all parent outputs the formation txn.
		parentOutputs := getParentOutputIDs(data.FormationTxnSet)
		for _, oid := range parentOutputs {
			w.addOutputDependency(oid, fcID)
		}
	}
	return w, nil
}
