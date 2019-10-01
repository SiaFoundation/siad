package filesystem

import (
	"encoding/json"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// metadataUpdate is a helper method which creates a writeaheadlog update for
// updating the siaDir metadata
func metadataUpdate(path string, metadata siadir.Metadata) (writeaheadlog.Update, error) {
	// Encode metadata
	data, err := json.Marshal(metadata)
	if err != nil {
		return writeaheadlog.Update{}, err
	}

	// Create update
	return writeaheadlog.Update{
		Name:         updateMetadataName,
		Instructions: encoding.MarshalAll(data, path),
	}, nil
}
