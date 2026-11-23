package match

import (
	"errors"
	"math"
)

// MaxHungarianCost bounds the magnitude of costs accepted by Hungarian so
// that potentials cannot overflow int64 for any matrix up to 5000 rows.
const MaxHungarianCost = int64(1) << 40

// ErrCostRange is returned by Hungarian for costs outside
// [-MaxHungarianCost, MaxHungarianCost] or ragged matrices.
var ErrCostRange = errors.New("match: hungarian: cost out of range or ragged matrix")

// Hungarian solves the rectangular assignment problem: given an n×m cost
// matrix it returns, for each row, the column assigned to it (or -1 when
// n > m and the row is left out) such that every column is used at most
// once, min(n, m) rows are assigned and the total cost is minimal.
//
// The implementation is the shortest augmenting path formulation with
// row and column potentials (Kuhn-Munkres as refined by Jonker-Volgenant
// style Dijkstra steps), O(n²·m) time for n <= m and O(n+m) extra memory
// besides the matrix. Ties are broken deterministically by index.
//
// To maximize a score, pass negated scores. For a matching where pairs may
// be left unassigned, give forbidden pairs cost 0 and allowed pairs a
// negative cost; the result then is a maximum-weight matching.
func Hungarian(cost [][]int64) (rowToCol []int, total int64, err error) {
	n := len(cost)
	if n == 0 {
		return nil, 0, nil
	}
	m := len(cost[0])
	for _, row := range cost {
		if len(row) != m {
			return nil, 0, ErrCostRange
		}
		for _, c := range row {
			if c > MaxHungarianCost || c < -MaxHungarianCost {
				return nil, 0, ErrCostRange
			}
		}
	}
	rowToCol = make([]int, n)
	for i := range rowToCol {
		rowToCol[i] = -1
	}
	if m == 0 {
		return rowToCol, 0, nil
	}
	if n > m {
		// Solve the transpose so that rows <= columns.
		t := make([][]int64, m)
		for j := range t {
			t[j] = make([]int64, n)
			for i := 0; i < n; i++ {
				t[j][i] = cost[i][j]
			}
		}
		colToRow := hungarian(t)
		for j, i := range colToRow {
			rowToCol[i] = j
			total += cost[i][j]
		}
		return rowToCol, total, nil
	}
	for i, j := range hungarian(cost) {
		rowToCol[i] = j
		total += cost[i][j]
	}
	return rowToCol, total, nil
}

// hungarian requires len(a) <= len(a[0]) and returns row -> column.
func hungarian(a [][]int64) []int {
	n, m := len(a), len(a[0])
	const inf = math.MaxInt64 / 4
	u := make([]int64, n+1)
	v := make([]int64, m+1)
	p := make([]int, m+1)   // p[j]: row (1-based) matched to column j
	way := make([]int, m+1) // previous column on the augmenting path
	minv := make([]int64, m+1)
	used := make([]bool, m+1)
	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		for j := range minv {
			minv[j] = inf
			used[j] = false
		}
		for {
			used[j0] = true
			i0 := p[j0]
			delta := int64(inf)
			j1 := 0
			for j := 1; j <= m; j++ {
				if used[j] {
					continue
				}
				cur := a[i0-1][j-1] - u[i0] - v[j]
				if cur < minv[j] {
					minv[j] = cur
					way[j] = j0
				}
				if minv[j] < delta {
					delta = minv[j]
					j1 = j
				}
			}
			for j := 0; j <= m; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for j0 != 0 {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
		}
	}
	res := make([]int, n)
	for j := 1; j <= m; j++ {
		if p[j] != 0 {
			res[p[j]-1] = j - 1
		}
	}
	return res
}
