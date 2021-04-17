package modules

import (
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/types"
)

// TestBudget tests the RPCBudget helper type.
func TestRPCBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		initial   uint64
		remaining []uint64
		withdraw  []uint64
		result    []bool
	}{
		{
			initial:   10,
			remaining: []uint64{0},
			withdraw:  []uint64{10},
			result:    []bool{true},
		},
		{
			initial:   0,
			remaining: []uint64{0},
			withdraw:  []uint64{0},
			result:    []bool{true},
		},
		{
			initial:   10,
			remaining: []uint64{5, 0},
			withdraw:  []uint64{5, 5},
			result:    []bool{true, true},
		},
		{
			initial:   5,
			remaining: []uint64{5},
			withdraw:  []uint64{6},
			result:    []bool{false},
		},
		{
			initial:   5,
			remaining: []uint64{2, 2},
			withdraw:  []uint64{3, 3},
			result:    []bool{true, false},
		},
	}
	for i, test := range tests {
		initial := types.NewCurrency64(test.initial)
		budget := NewBudget(initial)

		for j := range test.withdraw {
			remaining := types.NewCurrency64(test.remaining[j])
			withdraw := types.NewCurrency64(test.withdraw[j])
			result := test.result[j]

			if budget.Withdraw(withdraw) != result {
				t.Errorf("%v/%v: expected %v got %v", i, j, !result, result)
			}
			if !budget.Remaining().Equals(remaining) {
				t.Errorf("%v/%v: expected %v got %v", i, j, budget.Remaining(), remaining)
			}
		}
	}
}

// TestBudgetLimit tests the BudgetLimit.
func TestBudgetLimit(t *testing.T) {
	t.Parallel()

	initialBudget := uint64(100)
	readCost := uint64(1)
	writeCost := uint64(2)

	// Read full budget
	budget := NewBudget(types.NewCurrency64(initialBudget))
	limit := NewBudgetLimit(budget, types.NewCurrency64(readCost), types.NewCurrency64(writeCost))
	err := limit.RecordDownload(initialBudget / readCost)
	if err != nil {
		t.Fatal(err)
	}
	if limit.Downloaded() != initialBudget/readCost {
		t.Fatalf("expected %v but got %v", initialBudget/readCost, limit.Downloaded())
	}

	// Write full budget
	budget = NewBudget(types.NewCurrency64(initialBudget))
	limit = NewBudgetLimit(budget, types.NewCurrency64(readCost), types.NewCurrency64(writeCost))
	err = limit.RecordUpload(initialBudget / writeCost)
	if err != nil {
		t.Fatal(err)
	}
	if limit.Uploaded() != initialBudget/writeCost {
		t.Fatalf("expected %v but got %v", initialBudget/writeCost, limit.Uploaded())
	}

	// Do it half half.
	budget = NewBudget(types.NewCurrency64(initialBudget))
	limit = NewBudgetLimit(budget, types.NewCurrency64(readCost), types.NewCurrency64(writeCost))
	err = limit.RecordUpload(initialBudget / writeCost / 2)
	if err != nil {
		t.Fatal(err)
	}
	err = limit.RecordDownload(initialBudget / readCost / 2)
	if err != nil {
		t.Fatal(err)
	}
	if limit.Downloaded() != initialBudget/readCost/2 {
		t.Fatalf("expected %v but got %v", initialBudget/readCost/2, limit.Downloaded())
	}
	if limit.Uploaded() != initialBudget/writeCost/2 {
		t.Fatalf("expected %v but got %v", initialBudget/writeCost/2, limit.Uploaded())
	}

	// Enough budget for read but not write.
	budget = NewBudget(types.NewCurrency64(readCost))
	limit = NewBudgetLimit(budget, types.NewCurrency64(readCost), types.NewCurrency64(writeCost))
	err = limit.RecordUpload(1)
	if !errors.Contains(err, ErrInsufficientBandwidthBudget) {
		t.Fatal("expected error but got", err)
	}
	err = limit.RecordDownload(1)
	if err != nil {
		t.Fatal(err)
	}
	if limit.Downloaded() != 1 {
		t.Fatalf("expected %v but got %v", 1, limit.Downloaded())
	}
	if limit.Uploaded() != 0 {
		t.Fatalf("expected %v but got %v", 0, limit.Uploaded())
	}

	// Enough budget for write but not read.
	budget = NewBudget(types.NewCurrency64(readCost))
	limit = NewBudgetLimit(budget, types.NewCurrency64(writeCost), types.NewCurrency64(readCost))
	err = limit.RecordDownload(1)
	if !errors.Contains(err, ErrInsufficientBandwidthBudget) {
		t.Fatal("expected error but got", err)
	}
	err = limit.RecordUpload(1)
	if err != nil {
		t.Fatal(err)
	}
	if limit.Downloaded() != 0 {
		t.Fatalf("expected %v but got %v", 0, limit.Downloaded())
	}
	if limit.Uploaded() != 1 {
		t.Fatalf("expected %v but got %v", 1, limit.Uploaded())
	}

	// Not enough budget to either write or read at first but after the update.
	totalCost := types.NewCurrency64(readCost + writeCost)
	budget = NewBudget(totalCost)
	limit = NewBudgetLimit(budget, totalCost.Add64(1), totalCost.Add64(1))
	err = limit.RecordDownload(1)
	if !errors.Contains(err, ErrInsufficientBandwidthBudget) {
		t.Fatalf("expected error %v but got %v", ErrInsufficientBandwidthBudget, err)
	}
	err = limit.RecordUpload(1)
	if !errors.Contains(err, ErrInsufficientBandwidthBudget) {
		t.Fatalf("expected error %v but got %v", ErrInsufficientBandwidthBudget, err)
	}
	limit.UpdateCosts(types.NewCurrency64(readCost), types.NewCurrency64(writeCost))
	err = limit.RecordDownload(1)
	if err != nil {
		t.Fatal(err)
	}
	err = limit.RecordUpload(1)
	if err != nil {
		t.Fatal(err)
	}
	if limit.Downloaded() != 1 {
		t.Fatalf("expected %v but got %v", 1, limit.Downloaded())
	}
	if limit.Uploaded() != 1 {
		t.Fatalf("expected %v but got %v", 1, limit.Uploaded())
	}
	if !budget.Remaining().IsZero() {
		t.Fatal("budget should be empty")
	}
}

// TestRequiresReadonlyAndSnapshot is a unit test for the program's ReadOnly and
// RequiresSnapshot methods.
func TestRequiresReadonlyAndSnapshot(t *testing.T) {
	tests := []struct {
		specifier        InstructionSpecifier
		readonly         bool
		requiresSnapshot bool
	}{
		{
			SpecifierAppend,
			false,
			true,
		},
		{
			SpecifierDropSectors,
			false,
			true,
		},
		{
			SpecifierHasSector,
			true,
			false,
		},
		{
			SpecifierReadOffset,
			true,
			true,
		},
		{
			SpecifierReadSector,
			true,
			false,
		},
		{
			SpecifierRevision,
			true,
			true,
		},
		{
			SpecifierSwapSector,
			false,
			true,
		},
	}

	for i, test := range tests {
		p := Program{Instruction{Specifier: test.specifier}}
		readonly := test.readonly
		requiresSnapshot := test.requiresSnapshot
		if p.ReadOnly() != readonly {
			t.Fatalf("%v: expected %v but got %v", i, readonly, p.ReadOnly())
		}
		if p.RequiresSnapshot() != requiresSnapshot {
			t.Fatalf("%v: expected %v but got %v", i, requiresSnapshot, p.RequiresSnapshot())
		}
	}
}
