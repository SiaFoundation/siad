package modules

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/types"
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
