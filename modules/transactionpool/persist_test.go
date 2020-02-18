package transactionpool

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/bolt"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestRecan corrupts the tpool persist to trigger rescans by first deleting the
// persist directory, and a second time by corrupting a database value. After
// each scan, the test verifies that the tpool state is intact by checking that
// re-sending a known transaction set still triggers a duplicate error.
func TestRescan(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	tpt, err := createTpoolTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer tpt.Close()

	// Create a valid transaction set using the wallet.
	txns, err := tpt.wallet.SendSiacoins(types.NewCurrency64(100), types.UnlockHash{})
	if err != nil {
		t.Fatal(err)
	}
	if len(tpt.tpool.transactionSets) != 1 {
		t.Error("sending coins did not increase the transaction sets by 1")
	}
	// Mine the transaction into a block, so that it's in the consensus set.
	_, err = tpt.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Close the tpool, delete the persistence, then restart the tpool. The
	// tpool should still recognize the transaction set as a duplicate.
	persistDir := tpt.tpool.persistDir
	err = tpt.tpool.Close()
	if err != nil {
		t.Fatal(err)
	}
	err = os.RemoveAll(persistDir)
	if err != nil {
		t.Fatal(err)
	}
	tpt.tpool, err = New(tpt.cs, tpt.gateway, persistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Wait until the tpool syncs before re-sending.
	height := tpt.cs.Height()
	err = build.Retry(20, 250*time.Millisecond, func() error {
		tpt.tpool.mu.Lock()
		tpoolHeight := tpt.tpool.blockHeight
		tpt.tpool.mu.Unlock()

		if tpoolHeight < height {
			return errors.New("expected tpool height to reach cs height")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1st send should be a duplicate transaction set.
	err = tpt.tpool.AcceptTransactionSet(txns)
	if err != modules.ErrDuplicateTransactionSet {
		t.Fatal("expecting modules.ErrDuplicateTransactionSet, got: ", err)
	}

	// Close the tpool, corrupt the database, then restart the tpool. The tpool
	// should still recognize the transaction set as a duplicate.
	err = tpt.tpool.Close()
	if err != nil {
		t.Fatal(err)
	}
	db, err := persist.OpenDatabase(dbMetadata, filepath.Join(persistDir, dbFilename))
	if err != nil {
		t.Fatal(err)
	}

	// Save a copy of the most recent CC to check that the re-sync happens
	// correctly after we corrupt the value in the db.
	var copyRecentCC modules.ConsensusChangeID
	err = db.Update(func(tx *bolt.Tx) error {
		ccBytes := tx.Bucket(bucketRecentConsensusChange).Get(fieldRecentConsensusChange)
		// copy the bytes due to bolt's mmap.
		newCCBytes := make([]byte, len(ccBytes))
		copy(copyRecentCC[:], ccBytes) // save a copy of the CCID.
		copy(newCCBytes, ccBytes)      // make a copy to corrupt and write into the db.
		newCCBytes[0]++                // corrupt the value.
		return tx.Bucket(bucketRecentConsensusChange).Put(fieldRecentConsensusChange, newCCBytes)
	})
	if err != nil {
		t.Fatal(err)
	}
	err = db.Close()
	if err != nil {
		t.Fatal(err)
	}
	tpt.tpool, err = New(tpt.cs, tpt.gateway, persistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Wait until the tpool syncs by checking its height and checking that the most recent
	// consensus change ID is fixed after we corrupted it.
	err = build.Retry(50, 250*time.Millisecond, func() error {
		tpt.tpool.mu.Lock()
		defer tpt.tpool.mu.Unlock()

		tpoolHeight := tpt.tpool.blockHeight
		if tpoolHeight < height {
			return errors.New("expected tpool height to reach cs height")
		}

		// Begin a new tx
		err = tpt.tpool.dbTx.Commit()
		if err != nil {
			return errors.AddContext(err, "dbTx Commit err")
		}
		tpt.tpool.dbTx, err = tpt.tpool.db.Begin(true)
		if err != nil {
			return errors.AddContext(err, "dbTx Begin err")
		}

		// Check that the CCs match.
		cc, err := tpt.tpool.getRecentConsensusChange(tpt.tpool.dbTx)
		if err != nil {
			return err
		}
		if cc != copyRecentCC {
			return errors.New("cc should match")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// This send should still be a duplicate transaction set.
	err = tpt.tpool.AcceptTransactionSet(txns)
	if err != modules.ErrDuplicateTransactionSet {
		t.Fatal("expecting modules.ErrDuplicateTransactionSet, got:", err)
	}
}
