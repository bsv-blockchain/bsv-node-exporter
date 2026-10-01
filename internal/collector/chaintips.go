package collector

// ChainTip is one entry of getchaintips.
type ChainTip struct {
	Height    int64  `json:"height"`
	Hash      string `json:"hash"`
	BranchLen int64  `json:"branchlen"`
	Status    string `json:"status"`
}

// ForkWindows are the look-back windows, in blocks, for fork counting.
var ForkWindows = []int64{144, 10000}

// Statuses is every status label value emitted. Unknown statuses map to "other"
// so a node cannot create arbitrary label values.
var Statuses = []string{"active", "valid-fork", "valid-headers", "headers-only", "invalid", "other"}

// ForkCounts splits non-active tips by branch length: Single is 1, Long is more than 1.
type ForkCounts struct {
	Single int
	Long   int
}

// TipSummary always holds every status in Statuses and every window in ForkWindows.
type TipSummary struct {
	ByStatus map[string]int
	Forks    map[int64]ForkCounts
}

func normalizeStatus(s string) string {
	for _, known := range Statuses[:len(Statuses)-1] {
		if s == known {
			return s
		}
	}
	return "other"
}

// SummarizeTips counts tips by status, and counts non-active tips with
// activeHeight-height <= window per window. The bound is inclusive, as in the
// metrics this replaces. With no active tip every fork bucket is zero.
func SummarizeTips(tips []ChainTip) TipSummary {
	s := TipSummary{ByStatus: make(map[string]int, len(Statuses)), Forks: make(map[int64]ForkCounts, len(ForkWindows))}
	for _, st := range Statuses {
		s.ByStatus[st] = 0
	}
	for _, w := range ForkWindows {
		s.Forks[w] = ForkCounts{}
	}

	active := int64(-1)
	for _, t := range tips {
		s.ByStatus[normalizeStatus(t.Status)]++
		if t.Status == "active" && t.Height > active {
			active = t.Height
		}
	}
	if active < 0 {
		return s
	}

	for _, t := range tips {
		if t.Status == "active" || t.BranchLen < 1 {
			continue
		}
		for _, w := range ForkWindows {
			if active-t.Height > w {
				continue
			}
			fc := s.Forks[w]
			if t.BranchLen == 1 {
				fc.Single++
			} else {
				fc.Long++
			}
			s.Forks[w] = fc
		}
	}
	return s
}
