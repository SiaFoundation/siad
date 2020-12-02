package renter

import (
	"context"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestJobReadMetadata verifies the job metadata is set on the job read response
func TestJobReadMetadata(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	w := wt.worker

	// allow the worker some time to fetch a PT and fund its EA
	err = build.Retry(600, 100*time.Millisecond, func() error {
		if w.staticAccount.managedMinExpectedBalance().IsZero() {
			return errors.New("account not funded yet")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// add sector data to the host
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	sectorRoot := crypto.MerkleRoot(sectorData)
	err = wt.host.AddSector(sectorRoot, sectorData)
	if err != nil {
		t.Fatal(err)
	}

	// add job to the worker
	ctx := context.Background()
	responseChan := make(chan *jobReadResponse)

	jhs := &jobReadSector{
		jobRead: jobRead{
			staticResponseChan: responseChan,
			staticLength:       modules.SectorSize,

			// set metadata, set it to something different than the sector root
			// to ensure the response contains the sector given in the metadata
			staticSector: crypto.Hash{1, 2, 3},

			jobGeneric: &jobGeneric{
				staticCtx:   ctx,
				staticQueue: w.staticJobReadQueue,
			},
		},
		staticSector: sectorRoot,
		staticOffset: 0,
	}
	if !w.staticJobReadQueue.callAdd(jhs) {
		t.Fatal("Could not add job to queue")
	}

	// receive response and verify if metadata is set
	jrr := <-responseChan
	if jrr.staticSectorRoot != (crypto.Hash{1, 2, 3}) {
		t.Fatal("unexpected", jrr.staticSectorRoot, sectorRoot)
	}
	if jrr.staticWorker == nil || jrr.staticWorker.staticHostPubKeyStr != wt.host.PublicKey().String() {
		t.Fatal("unexpected")
	}
}
