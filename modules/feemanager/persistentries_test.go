package feemanager

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/encoding"
)

// TestPersistEntryPayloadSize ensures that the payload size plus the size of
// the rest of the persist entry matches up to the persistEntrySize.
func TestPersistEntryPayloadSize(t *testing.T) {
	var pe persistEntry
	data := encoding.Marshal(pe)
	if len(data) != persistEntrySize {
		t.Fatal("encoded persistEntry must be persistEntrySize")
	}
}
