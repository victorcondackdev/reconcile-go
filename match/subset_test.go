package match

import (
	"math/rand"
	"sort"
	"testing"
	"time"
)

// bruteSubset enumerates every subset and returns the best cost and size
// following solveSubset's ordering (cost, then size).
func bruteSubset(p subsetProblem) (found bool, cost float64, k int) {
	n := len(p.items)
	for mask := 1; mask < 1<<n; mask++ {
		var sum, aux int64
		size := 0
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				sum += p.items[i]
				if p.aux != nil {
					aux += p.aux[i]
				}
				size++
			}
		}
		if size < max(p.minSize, 1) || size > p.maxSize || sum < p.lo || sum > p.hi || !p.accept(sum, aux, size) {
			continue
		}
		c := p.cost(sum, aux, size)
		if !found || c < cost-1e-9 || (c <= cost+1e-9 && size < k) {
			found, cost, k = true, c, size
		}
	}
	return
}

func TestSubsetSumMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for iter := 0; iter < 2000; iter++ {
		n := 1 + r.Intn(12)
		items := make([]int64, n)
		aux := make([]int64, n)
		var total int64
		for i := range items {
			items[i] = 1 + r.Int63n(500)
			aux[i] = items[i] - items[i]/40
			total += items[i]
		}
		target := 1 + r.Int63n(total)
		tol := r.Int63n(20)
		p := subsetProblem{
			items: items, aux: aux, lo: target - tol, hi: target + tol,
			minSize: 1 + r.Intn(2), maxSize: 1 + r.Intn(n), maxNodes: 1 << 30,
			accept: func(sum, _ int64, k int) bool { return sum >= target-tol && sum <= target+tol },
			cost:   func(sum, a int64, k int) float64 { return float64(abs64(a - target)) },
		}
		got := solveSubset(p)
		found, cost, k := bruteSubset(p)
		if got.found != found {
			t.Fatalf("iter %d: found %v, brute %v (items %v target %d tol %d)", iter, got.found, found, items, target, tol)
		}
		if !found {
			continue
		}
		if got.cost != cost || len(got.idx) != k {
			t.Fatalf("iter %d: cost %v size %d, brute %v size %d", iter, got.cost, len(got.idx), cost, k)
		}
		var sum int64
		for _, i := range got.idx {
			sum += items[i]
		}
		if sum != got.sum || !sort.IntsAreSorted(got.idx) {
			t.Fatalf("iter %d: reported sum %d, actual %d, idx %v", iter, got.sum, sum, got.idx)
		}
		if got.truncated {
			t.Fatalf("iter %d: truncated with unlimited budget", iter)
		}
	}
}

func TestSubsetBudgetsAndTies(t *testing.T) {
	items := make([]int64, 40)
	for i := range items {
		items[i] = int64(1000 + i*7)
	}
	p := subsetProblem{
		items: items, lo: 0, hi: 1 << 40, minSize: 2, maxSize: 8, maxNodes: 100,
		accept: func(int64, int64, int) bool { return false },
		cost:   func(int64, int64, int) float64 { return 0 },
	}
	if res := solveSubset(p); !res.truncated || res.found || res.nodes <= 100 {
		t.Errorf("node budget: %+v", res)
	}
	p.lo, p.hi = 0, 1<<40
	p.maxNodes = 1 << 30
	p.deadline = time.Now().Add(-time.Second)
	if res := solveSubset(p); !res.truncated {
		t.Errorf("deadline not honoured: %+v", res)
	}
	// Ties on cost and size are reported as ambiguous; rank picks the
	// subset with the lowest total rank.
	tie := subsetProblem{
		items: []int64{50, 50, 30}, rank: []int64{5, 1, 0}, lo: 80, hi: 80, minSize: 2, maxSize: 2, maxNodes: 1000,
		accept: func(s, _ int64, _ int) bool { return s == 80 },
		cost:   func(int64, int64, int) float64 { return 0 },
	}
	res := solveSubset(tie)
	if !res.found || !res.ambiguous || len(res.idx) != 2 || res.idx[0] != 1 || res.idx[1] != 2 {
		t.Errorf("tie: %+v", res)
	}
}
