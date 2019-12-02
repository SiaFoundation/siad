package mdm

import (
	"testing"

	"gitlab.com/NebulousLabs/errors"
)

func TestCostSub(t *testing.T) {
	cost := Cost{
		Compute:      10,
		DiskAccesses: 10,
		DiskRead:     10,
		DiskWrite:    10,
		Memory:       10,
	}

	// Check if consuming exactly all resources works.
	result, err := cost.Sub(Cost{
		Compute:      10,
		DiskAccesses: 10,
		DiskRead:     10,
		DiskWrite:    10,
		Memory:       10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Compute+result.DiskAccesses+result.DiskRead+result.DiskWrite+result.Memory > 0 {
		t.Fatal("expected all resources to be consumed")
	}
	// Check if consuming fewer resources works.
	result, err = cost.Sub(Cost{
		Compute:      9,
		DiskAccesses: 9,
		DiskRead:     9,
		DiskWrite:    9,
		Memory:       9,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Compute+result.DiskAccesses+result.DiskRead+result.DiskWrite+result.Memory != 5 {
		t.Fatal("expected 5 resources to be left")
	}
	// Underflow all resources.
	result, err = cost.Sub(Cost{
		Compute:      11,
		DiskAccesses: 11,
		DiskRead:     11,
		DiskWrite:    11,
		Memory:       11,
	})
	if err == nil {
		t.Fatal("expected underflow error")
	}
	if !errors.Contains(err, ErrInsufficientBudget) {
		t.Fatal("expected err to contain", ErrInsufficientBudget)
	}
	if !errors.Contains(err, ErrInsufficientComputeBudget) {
		t.Fatal("expected err to contain", ErrInsufficientComputeBudget)
	}
	if !errors.Contains(err, ErrInsufficientDiskAccessesBudget) {
		t.Fatal("expected err to contain", ErrInsufficientDiskAccessesBudget)
	}
	if !errors.Contains(err, ErrInsufficientDiskReadBudget) {
		t.Fatal("expected err to contain", ErrInsufficientDiskReadBudget)
	}
	if !errors.Contains(err, ErrInsufficientDiskWriteBudget) {
		t.Fatal("expected err to contain", ErrInsufficientDiskWriteBudget)
	}
	if !errors.Contains(err, ErrInsufficientMemoryBudget) {
		t.Fatal("expected err to contain", ErrInsufficientMemoryBudget)
	}
}
