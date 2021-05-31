package host

import (
	"encoding/json"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/bolt"
	"gitlab.com/NebulousLabs/errors"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

// persistence is the data that is kept when the host is restarted.
type persistence struct {
	// Consensus Tracking.
	BlockHeight  types.BlockHeight         `json:"blockheight"`
	RecentChange modules.ConsensusChangeID `json:"recentchange"`

	// Host Identity.
	Announced        bool                         `json:"announced"`
	AutoAddress      modules.NetAddress           `json:"autoaddress"`
	FinancialMetrics modules.HostFinancialMetrics `json:"financialmetrics"`
	PublicKey        types.SiaPublicKey           `json:"publickey"`
	RevisionNumber   uint64                       `json:"revisionnumber"`
	SecretKey        crypto.SecretKey             `json:"secretkey"`
	Settings         modules.HostInternalSettings `json:"settings"`
	UnlockHash       types.UnlockHash             `json:"unlockhash"`
}

// persistData returns the data in the Host that will be saved to disk.
func (h *Host) persistData() persistence {
	return persistence{
		// Consensus Tracking.
		BlockHeight:  h.blockHeight,
		RecentChange: h.recentChange,

		// Host Identity.
		Announced:        h.announced,
		AutoAddress:      h.autoAddress,
		FinancialMetrics: h.financialMetrics,
		PublicKey:        h.publicKey,
		RevisionNumber:   h.revisionNumber,
		SecretKey:        h.secretKey,
		Settings:         h.settings,
		UnlockHash:       h.unlockHash,
	}
}

// establishDefaults configures the default settings for the host, overwriting
// any existing settings.
func (h *Host) establishDefaults() error {
	// Configure the settings object.
	h.settings = modules.HostInternalSettings{
		MaxDownloadBatchSize: uint64(modules.DefaultMaxDownloadBatchSize),
		MaxDuration:          modules.DefaultMaxDuration,
		MaxReviseBatchSize:   uint64(modules.DefaultMaxReviseBatchSize),
		WindowSize:           modules.DefaultWindowSize,

		Collateral:       modules.DefaultCollateral,
		CollateralBudget: defaultCollateralBudget,
		MaxCollateral:    modules.DefaultMaxCollateral,

		MinBaseRPCPrice:           modules.DefaultBaseRPCPrice,
		MinContractPrice:          modules.DefaultContractPrice,
		MinDownloadBandwidthPrice: modules.DefaultDownloadBandwidthPrice,
		MinSectorAccessPrice:      modules.DefaultSectorAccessPrice,
		MinStoragePrice:           modules.DefaultStoragePrice,
		MinUploadBandwidthPrice:   modules.DefaultUploadBandwidthPrice,

		EphemeralAccountExpiry:     modules.DefaultEphemeralAccountExpiry,
		MaxEphemeralAccountBalance: modules.DefaultMaxEphemeralAccountBalance,
		MaxEphemeralAccountRisk:    defaultMaxEphemeralAccountRisk,
	}

	// Load the host's key pair, use the same keys as the SiaMux.
	var sk crypto.SecretKey
	var pk crypto.PublicKey
	msk := h.staticMux.PrivateKey()
	mpk := h.staticMux.PublicKey()

	// Sanity check that the mux's key are the same length as the host keys
	// before copying them
	if len(sk) != len(msk) || len(pk) != len(mpk) {
		build.Critical("Expected the siamux keys to be of equal length as the host keys")
	}
	copy(sk[:], msk[:])
	copy(pk[:], mpk[:])

	h.publicKey = types.Ed25519PublicKey(pk)
	h.secretKey = sk

	return nil
}

// loadPersistObject will take a persist object and copy the data into the
// host.
func (h *Host) loadPersistObject(p *persistence) {
	// Copy over consensus tracking.
	h.blockHeight = p.BlockHeight
	h.recentChange = p.RecentChange

	// Copy over host identity.
	h.announced = p.Announced
	h.autoAddress = p.AutoAddress
	if err := p.AutoAddress.IsValid(); err != nil {
		h.log.Printf("WARN: AutoAddress '%v' loaded from persist is invalid: %v", p.AutoAddress, err)
		h.autoAddress = ""
	}
	h.financialMetrics = p.FinancialMetrics
	h.publicKey = p.PublicKey
	h.revisionNumber = p.RevisionNumber
	h.secretKey = p.SecretKey
	h.settings = p.Settings
	if err := p.Settings.NetAddress.IsValid(); err != nil {
		h.log.Printf("WARN: NetAddress '%v' loaded from persist is invalid: %v", p.Settings.NetAddress, err)
		h.settings.NetAddress = ""
	}
	h.unlockHash = p.UnlockHash
}

// initDB will check that the database has been initialized and if not, will
// initialize the database.
func (h *Host) initDB() (err error) {
	// Open the host's database and set up the stop function to close it.
	h.db, err = h.dependencies.OpenDatabase(dbMetadata, filepath.Join(h.persistDir, dbFilename))
	if err != nil {
		return err
	}
	h.tg.AfterStop(func() {
		err = h.db.Close()
		if err != nil {
			h.log.Println("Could not close the database:", err)
		}
	})

	return h.db.Update(func(tx *bolt.Tx) error {
		// The storage obligation bucket does not exist, which means the
		// database needs to be initialized. Create the database buckets.
		buckets := [][]byte{
			bucketActionItems,
			bucketStorageObligations,
		}
		for _, bucket := range buckets {
			_, err := tx.CreateBucketIfNotExists(bucket)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// load loads the Hosts's persistent data from disk.
func (h *Host) load() error {
	// Initialize the host database.
	err := h.initDB()
	if err != nil {
		err = build.ExtendErr("Could not initialize database:", err)
		h.log.Println(err)
		return err
	}

	// Load the old persistence object from disk. Simple task if the version is
	// the most recent version, but older versions need to be updated to the
	// more recent structures.
	p := new(persistence)
	err = h.dependencies.LoadFile(modules.Hostv151PersistMetadata, p, filepath.Join(h.persistDir, settingsFile))
	if err == nil {
		// Copy in the persistence.
		h.loadPersistObject(p)
	} else if os.IsNotExist(err) {
		// There is no host.json file, set up sane defaults.
		return h.establishDefaults()
	} else if errors.Contains(err, persist.ErrBadVersion) {
		// Attempt an upgrade from V112 to V120.
		err = h.upgradeFromV112ToV120()
		if err != nil {
			h.log.Println("WARNING: v112 to v120 host upgrade failed, trying v120 to v143 next", err)
		}
		// Attempt an upgrade from V120 to V143.
		err = h.upgradeFromV120ToV143()
		if err != nil {
			h.log.Println("WARNING: v120 to v143 host upgrade failed, trying v143 to v151 next", err)
		}
		// Attempt an upgrade from V143 to V151.
		err = h.upgradeFromV143ToV151()
		if err != nil {
			h.log.Println("WARNING: v143 to v151 host upgrade failed, nothing left to try", err)
			return err
		}

		h.log.Println("SUCCESS: successfully upgraded host to v143")
	} else {
		return err
	}

	// Compatv148 delete the old account file.
	af := filepath.Join(h.persistDir, v148AccountsFilename)
	if err := os.RemoveAll(af); err != nil {
		h.log.Printf("WARNING: failed to remove legacy account file at '%v', err: %v", af, err)
	}

	// Check if the host is currently using defaults that violate the ratio
	// restrictions between the SectorAccessPrice, BaseRPCPrice, and
	// DownloadBandwidthPrice
	var updated bool
	minBaseRPCPrice := h.settings.MinBaseRPCPrice
	maxBaseRPCPrice := h.settings.MaxBaseRPCPrice()
	if minBaseRPCPrice.Cmp(maxBaseRPCPrice) > 0 {
		h.settings.MinBaseRPCPrice = maxBaseRPCPrice
		updated = true
	}
	minSectorAccessPrice := h.settings.MinSectorAccessPrice
	maxSectorAccessPrice := h.settings.MaxSectorAccessPrice()
	if minSectorAccessPrice.Cmp(maxSectorAccessPrice) > 0 {
		h.settings.MinSectorAccessPrice = maxSectorAccessPrice
		updated = true
	}
	// If we updated the Price values we should save the changes to disk
	if updated {
		err = h.saveSync()
		if err != nil {
			return err
		}
	}

	// Get the contract count and locked collateral by observing all of the incomplete
	// storage obligations in the database.
	// TODO: both contract count and locked collateral are not correctly updated during
	// contract renewals. This leads to an offset to the real value over time.
	h.financialMetrics.ContractCount = 0
	h.financialMetrics.LockedStorageCollateral = types.NewCurrency64(0)
	err = h.db.View(func(tx *bolt.Tx) error {
		cursor := tx.Bucket(bucketStorageObligations).Cursor()
		for k, v := cursor.First(); k != nil; k, v = cursor.Next() {
			var so storageObligation
			err := json.Unmarshal(v, &so)
			if err != nil {
				return err
			}
			if so.ObligationStatus == obligationUnresolved {
				h.financialMetrics.ContractCount++
				h.financialMetrics.LockedStorageCollateral = h.financialMetrics.LockedStorageCollateral.Add(so.LockedCollateral)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

// saveSync stores all of the persist data to disk and then syncs to disk.
func (h *Host) saveSync() error {
	return persist.SaveJSON(modules.Hostv151PersistMetadata, h.persistData(), filepath.Join(h.persistDir, settingsFile))
}
