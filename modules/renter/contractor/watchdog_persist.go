package contractor

import (
	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// watchdogPersist defines what watchdog data persists across sessions.
type watchdogPersist struct {
	Contracts         map[string]fileContractStatusPersist   `json:"contracts"`
	ArchivedContracts map[string]modules.ContractWatchStatus `json:"archivedcontracts"`
}

// fileContractStatusPersist defines what information from fileContractStatus is persisted.
type fileContractStatusPersist struct {
	FormationSweepHeight types.BlockHeight `json:"formationsweepheight,omitempty"`
	ContractFound        bool              `json:"contractfound,omitempty"`
	RevisionFound        uint64            `json:"revisionfound,omitempty"`
	StorageProofFound    types.BlockHeight `json:"storageprooffound,omitempty"`

	FormationTxnSet []types.Transaction     `json:"formationtxnset,omitempty"`
	ParentOutputs   []types.SiacoinOutputID `json:"parentoutputs,omitempty"`

	SweepTxn     types.Transaction   `json:"sweeptxn,omitempty"`
	SweepParents []types.Transaction `json:"sweepparents,omitempty"`

	WindowStart types.BlockHeight `json:"windowstart"`
	WindowEnd   types.BlockHeight `json:"windowend"`
}

// persistData returns the data that will be saved to disk for
// fileContractStatus.
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
		FormationTxnSet:      d.formationTxnSet,
		ParentOutputs:        persistedParentOutputs,
		SweepTxn:             d.sweepTxn,
		SweepParents:         d.sweepParents,
		WindowStart:          d.windowStart,
		WindowEnd:            d.windowEnd,
	}
}

// callPersistData returns the data in the watchdog that will be saved to disk.
func (w *watchdog) callPersistData() watchdogPersist {
	w.mu.Lock()
	defer w.mu.Unlock()

	data := watchdogPersist{
		Contracts:         make(map[string]fileContractStatusPersist),
		ArchivedContracts: make(map[string]modules.ContractWatchStatus),
	}
	for fcID, contractData := range w.contracts {
		data.Contracts[fcID.String()] = contractData.persistData()
	}
	for fcID, archivedData := range w.archivedContracts {
		data.ArchivedContracts[fcID.String()] = archivedData
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

	for fcIDString, data := range persistData.ArchivedContracts {
		if err := fcID.LoadString(fcIDString); err != nil {
			return nil, err
		}
		if _, ok := w.contracts[fcID]; ok {
			return nil, errors.New("(watchdog) archived contract still in regular contracts map")
		}

		// Add persisted contract data to the watchdog.
		w.archivedContracts[fcID] = data
	}

	return w, nil
}
