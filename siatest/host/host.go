package host

import (
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/types"
)

// hostPersistMetadata is a copy of the host module persistance metadata
var hostPersistMetadata = persist.Metadata{
	Header:  "Sia Host",
	Version: "1.2.0",
}

// persistence is a copy of the host module persistance struct
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

// readHostPersistance is a helper function for the host siatests to read the
// host's persistance on disk
func readHostPersistance(testDir string) (persistence, error) {
	relPath := filepath.Join("host", modules.HostDir+".json")
	fullpath := filepath.Join(testDir, relPath)
	p := persistence{}
	err := persist.LoadJSON(hostPersistMetadata, &p, fullpath)
	return p, err
}
