package match

import (
	"sort"
	"time"
)

// subsetProblem asks for a subset of items (positive amounts) whose sum s
// satisfies accept(s, a, k) for its size k, with minSize <= k <= maxSize,
// minimizing cost(s, a, k). a is the sum of the same subset over aux (a
// second value per item, such as the amount net of fees); aux may be nil.
// lo and hi are necessary bounds on s used for pruning: no accepted subset
// may have a sum outside [lo, hi].
type subsetProblem struct {
	items []int64
	aux   []int64
	// rank breaks cost ties: among subsets of equal cost and size, the one
	// with the smaller total rank wins (for example, total date distance).
	rank     []int64
	lo, hi   int64
	minSize  int
	maxSize  int
	maxNodes int
	deadline time.Time
	accept   func(sum, aux int64, k int) bool
	cost     func(sum, aux int64, k int) float64
}

type subsetResult struct {
	idx       []int // indices into items, ascending
	sum       int64
	cost      float64
	found     bool
	ambiguous bool // another subset reached the same best cost
	nodes     int
	truncated bool // node or time budget exhausted
}

// solveSubset runs a depth-first branch-and-bound search over items sorted
// by decreasing amount. A branch is cut when
//
//   - the partial sum already exceeds hi (all amounts are positive),
//   - the partial sum plus every remaining amount stays below lo,
//   - the subset reached maxSize, or
//   - the node budget or deadline is exhausted.
//
// Worst case the search visits sum_{k<=K} C(n, k) = O(n^K) nodes for n
// candidates and maximum group size K, which is why both n (MaxCandidates)
// and the node count (MaxNodes) are bounded. With amounts sorted the hi
// bound prunes most of the tree in practice. The search order and the
// tie-breaks (lower cost, then fewer items, then first found) make the
// result deterministic for a given node budget.
func solveSubset(p subsetProblem) subsetResult {
	n := len(p.items)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return p.items[order[a]] > p.items[order[b]] })
	amt := make([]int64, n)
	aux := make([]int64, n)
	rank := make([]int64, n)
	for i, o := range order {
		amt[i] = p.items[o]
		if p.aux != nil {
			aux[i] = p.aux[o]
		}
		if p.rank != nil {
			rank[i] = p.rank[o]
		}
	}
	minSize := max(p.minSize, 1)
	suffix := make([]int64, n+1)
	for i := n - 1; i >= 0; i-- {
		suffix[i] = suffix[i+1] + amt[i]
	}
	var res subsetResult
	best := struct {
		cost float64
		k    int
		rank int64
		idx  []int
		sum  int64
	}{}
	var rankSum int64
	stack := make([]int, 0, p.maxSize)
	checkTime := !p.deadline.IsZero()
	var dfs func(start int, sum, asum int64) bool
	dfs = func(start int, sum, asum int64) bool {
		for i := start; i < n; i++ {
			res.nodes++
			if res.nodes > p.maxNodes || (checkTime && res.nodes&1023 == 0 && time.Now().After(p.deadline)) {
				res.truncated = true
				return false
			}
			if sum+suffix[i] < p.lo {
				return true // even taking everything left is not enough
			}
			s := sum + amt[i]
			if s > p.hi {
				continue // a smaller amount may still fit
			}
			as := asum + aux[i]
			stack = append(stack, i)
			rankSum += rank[i]
			k := len(stack)
			if k >= minSize && s >= p.lo && p.accept(s, as, k) {
				c := p.cost(s, as, k)
				better := !res.found || c < best.cost-1e-9 ||
					(c <= best.cost+1e-9 && (k < best.k || (k == best.k && rankSum < best.rank)))
				switch {
				case better:
					// A tie on cost and size that is only broken by rank
					// is still ambiguous.
					res.ambiguous = res.found && c <= best.cost+1e-9 && k == best.k
					res.found = true
					best.cost, best.k, best.sum, best.rank = c, k, s, rankSum
					best.idx = append(best.idx[:0], stack...)
				case c <= best.cost+1e-9 && k == best.k:
					res.ambiguous = true
				}
			}
			if k < p.maxSize {
				if !dfs(i+1, s, as) {
					stack = stack[:len(stack)-1]
					rankSum -= rank[i]
					return false
				}
			}
			stack = stack[:len(stack)-1]
			rankSum -= rank[i]
		}
		return true
	}
	dfs(0, 0, 0)
	if res.found {
		res.idx = make([]int, len(best.idx))
		for i, b := range best.idx {
			res.idx[i] = order[b]
		}
		sort.Ints(res.idx)
		res.sum, res.cost = best.sum, best.cost
	}
	return res
}
