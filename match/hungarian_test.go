package match

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// bruteForce returns the minimal total cost over all assignments of
// min(n, m) pairs.
func bruteForce(cost [][]int64) int64 {
	n := len(cost)
	m := len(cost[0])
	best := int64(1) << 62
	used := make([]bool, m)
	var rec func(i int, acc int64, assigned int)
	need := min(n, m)
	rec = func(i int, acc int64, assigned int) {
		if assigned == need {
			if acc < best {
				best = acc
			}
			return
		}
		if n-i < need-assigned {
			return
		}
		// row i may be skipped only when rows outnumber columns
		if n > m {
			rec(i+1, acc, assigned)
		}
		for j := 0; j < m; j++ {
			if !used[j] {
				used[j] = true
				rec(i+1, acc+cost[i][j], assigned+1)
				used[j] = false
			}
		}
	}
	rec(0, 0, 0)
	return best
}

func randomMatrix(r *rand.Rand, n, m int, lo, hi int64) [][]int64 {
	c := make([][]int64, n)
	for i := range c {
		c[i] = make([]int64, m)
		for j := range c[i] {
			c[i][j] = lo + r.Int63n(hi-lo+1)
		}
	}
	return c
}

func TestHungarianMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 3000; iter++ {
		n, m := 1+r.Intn(6), 1+r.Intn(6)
		var lo, hi int64 = -50, 50
		if iter%3 == 0 {
			lo, hi = 0, 3 // many ties
		}
		cost := randomMatrix(r, n, m, lo, hi)
		assign, total, err := Hungarian(cost)
		if err != nil {
			t.Fatal(err)
		}
		if want := bruteForce(cost); total != want {
			t.Fatalf("%dx%d %v: total %d, brute force %d", n, m, cost, total, want)
		}
		// The assignment is a valid matching of size min(n, m) whose cost
		// is the reported total.
		seen := map[int]bool{}
		var sum int64
		count := 0
		for i, j := range assign {
			if j < 0 {
				continue
			}
			if seen[j] || j >= m {
				t.Fatalf("column %d reused or out of range: %v", j, assign)
			}
			seen[j] = true
			sum += cost[i][j]
			count++
		}
		if count != min(n, m) || sum != total {
			t.Fatalf("assignment %v: count %d sum %d total %d", assign, count, sum, total)
		}
	}
}

func TestHungarianEdgeCases(t *testing.T) {
	if a, total, err := Hungarian(nil); a != nil || total != 0 || err != nil {
		t.Error("empty matrix")
	}
	a, _, err := Hungarian([][]int64{{}, {}})
	if err != nil || len(a) != 2 || a[0] != -1 || a[1] != -1 {
		t.Errorf("zero columns: %v %v", a, err)
	}
	if _, _, err := Hungarian([][]int64{{1, 2}, {3}}); !errors.Is(err, ErrCostRange) {
		t.Errorf("ragged: %v", err)
	}
	if _, _, err := Hungarian([][]int64{{MaxHungarianCost + 1}}); !errors.Is(err, ErrCostRange) {
		t.Errorf("range: %v", err)
	}
	// Large costs at the limit still produce the right answer.
	big := MaxHungarianCost
	a, total, err := Hungarian([][]int64{{big, -big}, {-big, big}})
	if err != nil || total != -2*big || a[0] != 1 || a[1] != 0 {
		t.Errorf("big costs: %v %d %v", a, total, err)
	}
}

func ExampleHungarian() {
	// Three statement entries (rows) and three expected records (columns);
	// costs are negated match scores.
	cost := [][]int64{
		{-90, -85, 0},
		{-88, 0, 0},
		{0, -80, -70},
	}
	assign, total, _ := Hungarian(cost)
	fmt.Println(assign, total)
	// Greedy would take (0,0) first (score 90) and end with 90+80 = 170;
	// the optimum pairs entry 0 with record 1.
	// Output: [1 0 2] -243
}

func BenchmarkHungarian300(b *testing.B) {
	r := rand.New(rand.NewSource(42))
	cost := randomMatrix(r, 300, 300, -1_000_000, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := Hungarian(cost); err != nil {
			b.Fatal(err)
		}
	}
}
