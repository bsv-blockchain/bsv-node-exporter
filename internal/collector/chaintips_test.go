package collector

import (
	"reflect"
	"testing"
)

func TestSummarizeTipsWindowsAndLengths(t *testing.T) {
	tips := []ChainTip{
		{Height: 1000, BranchLen: 0, Status: "active"},
		{Height: 856, BranchLen: 1, Status: "valid-fork"},    // 144 below: inside both windows (inclusive)
		{Height: 855, BranchLen: 1, Status: "valid-fork"},    // 145 below: 10000 window only
		{Height: 990, BranchLen: 3, Status: "valid-headers"}, // long, inside both
		{Height: 0, BranchLen: 2, Status: "headers-only"},    // 1000 below: 10000 window only
		{Height: 999, BranchLen: 1, Status: "invalid"},
	}
	got := SummarizeTips(tips)
	wantForks := map[int64]ForkCounts{
		144:   {Single: 2, Long: 1},
		10000: {Single: 3, Long: 2},
	}
	if !reflect.DeepEqual(got.Forks, wantForks) {
		t.Errorf("Forks = %+v, want %+v", got.Forks, wantForks)
	}
	wantStatus := map[string]int{"active": 1, "valid-fork": 2, "valid-headers": 1, "headers-only": 1, "invalid": 1, "other": 0}
	if !reflect.DeepEqual(got.ByStatus, wantStatus) {
		t.Errorf("ByStatus = %v, want %v", got.ByStatus, wantStatus)
	}
}

func TestSummarizeTipsUnknownStatusIsOther(t *testing.T) {
	got := SummarizeTips([]ChainTip{
		{Height: 10, Status: "active"},
		{Height: 9, BranchLen: 1, Status: "some-new-status"},
		{Height: 8, BranchLen: 1, Status: "x\"injected"},
	})
	if got.ByStatus["other"] != 2 {
		t.Errorf("other = %d, want 2", got.ByStatus["other"])
	}
	if len(got.ByStatus) != len(Statuses) {
		t.Errorf("ByStatus has %d keys, want exactly %d", len(got.ByStatus), len(Statuses))
	}
}

func TestSummarizeTipsNoActiveTip(t *testing.T) {
	got := SummarizeTips([]ChainTip{{Height: 5, BranchLen: 1, Status: "valid-fork"}})
	for _, w := range ForkWindows {
		if got.Forks[w] != (ForkCounts{}) {
			t.Errorf("window %d = %+v, want zero", w, got.Forks[w])
		}
	}
	if got.ByStatus["valid-fork"] != 1 {
		t.Errorf("statuses still counted without active tip: %v", got.ByStatus)
	}
}

func TestSummarizeTipsEmpty(t *testing.T) {
	got := SummarizeTips(nil)
	if len(got.Forks) != len(ForkWindows) || len(got.ByStatus) != len(Statuses) {
		t.Fatalf("empty input must still produce every bucket: %+v", got)
	}
}

func TestSummarizeTipsIgnoresZeroBranchLenNonActive(t *testing.T) {
	got := SummarizeTips([]ChainTip{
		{Height: 100, Status: "active"},
		{Height: 99, BranchLen: 0, Status: "valid-fork"},
	})
	if got.Forks[144] != (ForkCounts{}) {
		t.Errorf("branchlen 0 counted as fork: %+v", got.Forks[144])
	}
}

func TestSummarizeTipsIgnoresTipsAboveActive(t *testing.T) {
	// A node catching up sees headers beyond its validated tip; those are not forks.
	got := SummarizeTips([]ChainTip{
		{Height: 100, Status: "active"},
		{Height: 150, BranchLen: 50, Status: "headers-only"},
		{Height: 101, BranchLen: 1, Status: "valid-headers"},
		{Height: 99, BranchLen: 1, Status: "valid-fork"},
	})
	want := map[int64]ForkCounts{144: {Single: 1}, 10000: {Single: 1}}
	if !reflect.DeepEqual(got.Forks, want) {
		t.Errorf("Forks = %+v, want %+v (tips above the active height must not count)", got.Forks, want)
	}
	if got.ByStatus["headers-only"] != 1 || got.ByStatus["valid-headers"] != 1 {
		t.Errorf("tips above active must still count by status: %v", got.ByStatus)
	}
}
