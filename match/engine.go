package match

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/victorcondackdev/reconcile-go/internal/decimal"
	"github.com/victorcondackdev/reconcile-go/internal/textnorm"
	"github.com/victorcondackdev/reconcile-go/statement"
)

// Engine reconciles statement entries against expected records. An Engine
// is immutable and safe for concurrent use.
type Engine struct {
	cfg Config
}

// New returns an Engine for cfg after validating it.
func New(cfg Config) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Engine{cfg: cfg}, nil
}

// Config returns the engine configuration.
func (e *Engine) Config() Config { return e.cfg }

// Reconcile is a convenience for New(cfg) followed by Engine.Reconcile.
func Reconcile(entries []statement.Entry, expected []Expected, cfg Config) (*Result, error) {
	e, err := New(cfg)
	if err != nil {
		return nil, err
	}
	return e.Reconcile(entries, expected), nil
}

// Reconcile runs the enabled passes in order (reference, exact, group,
// fuzzy). Each pass only sees the records left unmatched by the previous
// ones, so stronger evidence always wins over weaker evidence. Inputs are
// not modified.
func (e *Engine) Reconcile(entries []statement.Entry, expected []Expected) *Result {
	s := newState(e.cfg, entries, expected)
	if e.cfg.Passes.Reference {
		s.passReference()
	}
	if e.cfg.Passes.Exact {
		s.passExact()
	}
	if e.cfg.Passes.Group {
		s.passGroup()
	}
	if e.cfg.Passes.Fuzzy {
		s.passFuzzy()
	}
	return s.result()
}

type state struct {
	cfg Config
	en  []statement.Entry
	ex  []Expected

	eDay, xDay []int
	eHas, xHas []bool
	eFolds     []string // folded counterparty + description
	xCp        [][][]rune
	xDesc      [][][]rune
	eTok       [][][]rune
	xTags      []map[string]bool

	// eByDay and xByDay list dated records sorted by day, the blocking
	// index of the group pass.
	eByDay, xByDay []int

	eUsed, xUsed []bool
	matches      []Match
	truncated    int
}

func civilDay(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func newState(cfg Config, en []statement.Entry, ex []Expected) *state {
	s := &state{
		cfg: cfg, en: en, ex: ex,
		eDay: make([]int, len(en)), xDay: make([]int, len(ex)),
		eHas: make([]bool, len(en)), xHas: make([]bool, len(ex)),
		eFolds: make([]string, len(en)),
		eTok:   make([][][]rune, len(en)),
		xCp:    make([][][]rune, len(ex)), xDesc: make([][][]rune, len(ex)),
		xTags: make([]map[string]bool, len(ex)),
		eUsed: make([]bool, len(en)), xUsed: make([]bool, len(ex)),
	}
	for i, e := range en {
		if !e.BookingDate.IsZero() {
			s.eDay[i], s.eHas[i] = civilDay(e.BookingDate), true
		}
		s.eFolds[i] = textnorm.Fold(e.Counterparty + " " + e.Description)
		s.eTok[i] = tokenize(e.Counterparty + " " + e.Description)
	}
	for i, x := range ex {
		if !x.Date.IsZero() {
			s.xDay[i], s.xHas[i] = civilDay(x.Date), true
		}
		s.xCp[i] = tokenize(x.Counterparty)
		s.xDesc[i] = tokenize(x.Description)
		if len(x.Tags) > 0 {
			s.xTags[i] = make(map[string]bool, len(x.Tags))
			for _, t := range x.Tags {
				s.xTags[i][textnorm.Fold(strings.TrimSpace(t))] = true
			}
		}
	}
	for i := range en {
		if s.eHas[i] {
			s.eByDay = append(s.eByDay, i)
		}
	}
	for i := range ex {
		if s.xHas[i] {
			s.xByDay = append(s.xByDay, i)
		}
	}
	sort.SliceStable(s.eByDay, func(a, b int) bool { return s.eDay[s.eByDay[a]] < s.eDay[s.eByDay[b]] })
	sort.SliceStable(s.xByDay, func(a, b int) bool { return s.xDay[s.xByDay[a]] < s.xDay[s.xByDay[b]] })
	return s
}

// dayRange returns the records of idx (sorted by day) whose day lies in
// [lo, hi].
func dayRange(idx []int, day []int, lo, hi int) []int {
	i := sort.Search(len(idx), func(k int) bool { return day[idx[k]] >= lo })
	j := sort.Search(len(idx), func(k int) bool { return day[idx[k]] > hi })
	return idx[i:j]
}

func sameSign(a, b int64) bool {
	return (a > 0 && b > 0) || (a < 0 && b < 0) || (a == 0 && b == 0)
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func money(c int64) string { return decimal.Format(c) }

func days(d int) string {
	switch {
	case d == 0:
		return "same day"
	case d == 1:
		return "+1 day"
	case d == -1:
		return "-1 day"
	case d > 0:
		return fmt.Sprintf("+%d days", d)
	}
	return fmt.Sprintf("%d days", d)
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// dateDiff returns entry day minus expected day and whether both are known.
func (s *state) dateDiff(e, x int) (int, bool) {
	if !s.eHas[e] || !s.xHas[x] {
		return 0, false
	}
	return s.eDay[e] - s.xDay[x], true
}

// party returns how well the expected party name is found in the entry's
// counterparty and description, and whether both sides have a name.
func (s *state) party(e, x int) (float64, bool) {
	if len(s.xCp[x]) == 0 || len(s.eTok[e]) == 0 {
		return 0, false
	}
	return containment(s.eTok[e], s.xCp[x]), true
}

func (s *state) partyFeature(e, x int, weight float64) Feature {
	sim, ok := s.party(e, x)
	if !ok {
		return Feature{Name: "counterparty", Detail: "not available on both sides", Score: 0.5, Weight: weight}
	}
	return Feature{Name: "counterparty", Detail: fmt.Sprintf("%.2f %q ~ %q", sim, s.ex[x].Counterparty, firstText(s.en[e])), Score: round3(sim), Weight: weight}
}

func firstText(e statement.Entry) string {
	if e.Counterparty == "" || strings.Contains(e.Description, e.Counterparty) {
		return e.Description
	}
	return e.Counterparty + " " + e.Description
}

func (s *state) dateFeature(e, x, window int, weight float64) Feature {
	dd, ok := s.dateDiff(e, x)
	if !ok {
		return Feature{Name: "date", Detail: "date unknown", Score: 0.5, Weight: weight}
	}
	return Feature{Name: "date", Detail: days(dd), Score: round3(1 - float64(absInt(dd))/float64(window+1)), Weight: weight}
}

// cand is a scored one-to-one candidate pair.
type cand struct {
	e, x  int
	score float64
	conf  float64
	pass  Pass
	rule  string
	feats []Feature
}

// ---------------------------------------------------------------- pass 1

func (s *state) passReference() {
	cfg := s.cfg.Reference
	refIdx := map[string][]int{}
	idIdx := map[string][]int{}
	for x, r := range s.ex {
		if s.xUsed[x] {
			continue
		}
		if k := textnorm.RefKey(r.Reference); k != "" {
			refIdx[k] = append(refIdx[k], x)
		}
		if cfg.MatchExpectedID {
			if k := textnorm.RefKey(r.ID); len(k) >= cfg.MinTokenLength {
				idIdx[k] = append(idIdx[k], x)
			}
		}
	}
	var cands []cand
	for e, en := range s.en {
		if s.eUsed[e] {
			continue
		}
		seen := map[int]bool{}
		try := func(key, source string, strength float64, minLen int) {
			if len(key) < minLen {
				return
			}
			for _, list := range [2]struct {
				idx  []int
				what string
			}{{refIdx[key], "expected reference"}, {idIdx[key], "expected ID"}} {
				for _, x := range list.idx {
					if seen[x] || s.xUsed[x] {
						continue
					}
					if c, ok := s.referenceCand(e, x, key, source+" equals "+list.what, strength); ok {
						seen[x] = true
						cands = append(cands, c)
					}
				}
			}
		}
		try(textnorm.RefKey(en.Reference), "statement reference", 1.0, 1)
		try(textnorm.RefKey(en.ID), "statement ID", 0.98, cfg.MinTokenLength)
		if cfg.SearchDescription {
			for _, tok := range strings.FieldsFunc(en.Description, func(r rune) bool {
				return !(r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r == '-' || r == '/' || r == '.')
			}) {
				try(textnorm.RefKey(tok), "token in statement description", 0.96, cfg.MinTokenLength)
			}
		}
	}
	s.commitPairs(s.assign(cands))
}

func (s *state) referenceCand(e, x int, key, how string, strength float64) (cand, bool) {
	en, ex := s.en[e], s.ex[x]
	if !sameSign(en.AmountCents, ex.AmountCents) {
		return cand{}, false
	}
	dd, known := s.dateDiff(e, x)
	if maxDays := s.cfg.Reference.MaxDateDiffDays; known && maxDays > 0 && absInt(dd) > maxDays {
		return cand{}, false
	}
	diff := en.AmountCents - ex.AmountCents
	amountScore := 1.0
	amountDetail := "equal"
	if diff != 0 {
		ratio := float64(abs64(diff)) / float64(max(abs64(ex.AmountCents), 1))
		if ratio > 0.5 {
			return cand{}, false
		}
		amountScore = 1 - ratio
		amountDetail = fmt.Sprintf("differs by %s (%.2f%%)", money(diff), ratio*100)
	}
	feats := []Feature{
		{Name: "reference", Detail: fmt.Sprintf("%s (%s)", how, key), Score: 1, Weight: 0},
		{Name: "amount", Detail: amountDetail, Score: round3(amountScore), Weight: 0},
	}
	feats = append(feats, s.dateFeature(e, x, max(s.cfg.Reference.MaxDateDiffDays, 1), 0))
	conf := 0.99 * strength
	if diff != 0 {
		conf = strength * (0.6 + 0.3*amountScore)
	}
	rule := "identifier match: " + how
	if diff != 0 {
		rule += "; amount differs by " + money(diff)
	}
	return cand{e: e, x: x, score: strength * (0.5 + 0.5*amountScore), conf: round3(conf), pass: PassReference, rule: rule, feats: feats}, true
}

// ---------------------------------------------------------------- pass 2

func (s *state) passExact() {
	cfg := s.cfg.Exact
	byAmt := map[int64][]int{}
	for x, r := range s.ex {
		if !s.xUsed[x] {
			byAmt[r.AmountCents] = append(byAmt[r.AmountCents], x)
		}
	}
	var cands []cand
	for e, en := range s.en {
		if s.eUsed[e] {
			continue
		}
		for _, x := range byAmt[en.AmountCents] {
			dd, known := s.dateDiff(e, x)
			if known && absInt(dd) > cfg.DateWindowDays {
				continue
			}
			sim, hasParty := s.party(e, x)
			if hasParty && sim < cfg.MinCounterpartySimilarity {
				continue
			}
			df := s.dateFeature(e, x, cfg.DateWindowDays, 0.5)
			pf := s.partyFeature(e, x, 0.5)
			score := 0.5*df.Score + 0.5*pf.Score
			feats := []Feature{
				{Name: "amount", Detail: "equal (" + money(en.AmountCents) + ")", Score: 1, Weight: 0},
				df, pf,
			}
			rule := fmt.Sprintf("same amount, booked %s relative to expected date", df.Detail)
			if hasParty {
				rule += fmt.Sprintf(", counterparty similarity %.2f", sim)
			}
			cands = append(cands, cand{e: e, x: x, score: score, conf: round3(0.8 + 0.18*score), pass: PassExact, rule: rule, feats: feats})
		}
	}
	s.commitPairs(s.assign(cands))
}

// ---------------------------------------------------------------- pass 3

func (s *state) passGroup() {
	cfg := s.cfg.Group
	var deadline time.Time
	if cfg.TimeBudget > 0 {
		deadline = time.Now().Add(time.Duration(cfg.TimeBudget))
	}
	if len(cfg.Rules) > 0 {
		order := make([]int, 0, len(s.en))
		for e := range s.en {
			if !s.eUsed[e] && s.eHas[e] && s.en[e].AmountCents != 0 {
				order = append(order, e)
			}
		}
		sort.SliceStable(order, func(a, b int) bool {
			ea, eb := order[a], order[b]
			if s.eDay[ea] != s.eDay[eb] {
				return s.eDay[ea] < s.eDay[eb]
			}
			return abs64(s.en[ea].AmountCents) > abs64(s.en[eb].AmountCents)
		})
		for _, e := range order {
			for ri := range cfg.Rules {
				if s.manyToOne(e, &cfg.Rules[ri], deadline) {
					break
				}
			}
		}
	}
	if cfg.Split.Enabled {
		order := make([]int, 0, len(s.ex))
		for x := range s.ex {
			if !s.xUsed[x] && s.xHas[x] && s.ex[x].AmountCents != 0 && len(s.xCp[x]) > 0 {
				order = append(order, x)
			}
		}
		sort.SliceStable(order, func(a, b int) bool {
			xa, xb := order[a], order[b]
			if s.xDay[xa] != s.xDay[xb] {
				return s.xDay[xa] < s.xDay[xb]
			}
			return abs64(s.ex[xa].AmountCents) > abs64(s.ex[xb].AmountCents)
		})
		for _, x := range order {
			s.oneToMany(x, deadline)
		}
	}
}

type ranked struct {
	idx  int
	sim  float64
	dist int
}

func rankAndTrim(c []ranked, n int) []ranked {
	sort.SliceStable(c, func(a, b int) bool {
		if c[a].sim != c[b].sim {
			return c[a].sim > c[b].sim
		}
		if c[a].dist != c[b].dist {
			return c[a].dist < c[b].dist
		}
		return c[a].idx < c[b].idx
	})
	if len(c) > n {
		c = c[:n]
	}
	return c
}

func (s *state) manyToOne(e int, r *GroupRule, deadline time.Time) bool {
	cfg := s.cfg.Group
	en := &s.en[e]
	if r.Match != "" && !strings.Contains(s.eFolds[e], textnorm.Fold(r.Match)) {
		return false
	}
	target := abs64(en.AmountCents)
	maxK := cfg.MaxGroupSize
	hi := int64(math.Floor(float64(target+(r.FeeFixedCents+r.ToleranceCents)*int64(maxK))/(1-r.FeeMaxPercent/100))) + 1
	lo := target - r.ToleranceCents*int64(maxK)
	tag := textnorm.Fold(strings.TrimSpace(r.Tag))
	var cands []ranked
	for _, x := range dayRange(s.xByDay, s.xDay, s.eDay[e]-cfg.LookbackDays, s.eDay[e]+cfg.LookaheadDays) {
		ex := &s.ex[x]
		if s.xUsed[x] || !sameSign(en.AmountCents, ex.AmountCents) || abs64(ex.AmountCents) > hi {
			continue
		}
		dd := s.eDay[e] - s.xDay[x]
		if dd < r.MinLagDays {
			continue
		}
		if tag != "" && !s.xTags[x][tag] {
			continue
		}
		sim, ok := s.party(e, x)
		if r.RequireCounterparty && (!ok || sim < s.cfg.Exact.MinCounterpartySimilarity) {
			continue
		}
		cands = append(cands, ranked{idx: x, sim: round3(sim), dist: absInt(dd)})
	}
	if len(cands) == 0 || (len(cands) < 2 && r.Match == "" && r.Tag == "") {
		return false
	}
	// Items are gross amounts (used for pruning and the fee range check);
	// aux holds each record's amount net of the rule's mid-range fee,
	// rounded per record as acquirers do, so the cost of a subset is how
	// far its expected net total is from the credited amount.
	midPct := (r.FeeMinPercent + r.FeeMaxPercent) / 2
	minSize := 2
	if r.Match != "" || r.Tag != "" {
		minSize = 1 // a settlement rule may also cover a single record net of fees
	}
	search := func(cands []ranked) subsetResult {
		items := make([]int64, len(cands))
		aux := make([]int64, len(cands))
		rank := make([]int64, len(cands))
		for i, c := range cands {
			g := abs64(s.ex[c.idx].AmountCents)
			items[i] = g
			rank[i] = int64(c.dist)
			aux[i] = g - int64(math.Round(float64(g)*midPct/100)) - r.FeeFixedCents
		}
		return solveSubset(subsetProblem{
			items: items, aux: aux, rank: rank, lo: lo, hi: hi, minSize: minSize, maxSize: min(maxK, len(items)),
			maxNodes: cfg.MaxNodes, deadline: deadline,
			accept: func(sum, _ int64, k int) bool {
				if k == 1 && sum == target {
					return false // plain one-to-one; left to the exact pass
				}
				fee := float64(sum - target)
				fixed := float64(r.FeeFixedCents * int64(k))
				tol := float64(r.ToleranceCents * int64(k))
				g := float64(sum)
				return fee >= g*r.FeeMinPercent/100+fixed-tol-1e-9 && fee <= g*r.FeeMaxPercent/100+fixed+tol+1e-9
			},
			cost: func(_, net int64, k int) float64 {
				return float64(abs64(net - target))
			},
		})
	}
	var res subsetResult
	if r.SameDate {
		// One search per expected date, nearest date first; the best
		// result wins (lower cost, then fewer records, then nearer date).
		buckets := map[int][]ranked{}
		var keys []int
		for _, c := range cands {
			d := s.xDay[c.idx]
			if _, ok := buckets[d]; !ok {
				keys = append(keys, d)
			}
			buckets[d] = append(buckets[d], c)
		}
		sort.Slice(keys, func(a, b int) bool {
			da, db := absInt(s.eDay[e]-keys[a]), absInt(s.eDay[e]-keys[b])
			if da != db {
				return da < db
			}
			return keys[a] > keys[b]
		})
		var best []ranked
		for _, k := range keys {
			b := rankAndTrim(buckets[k], cfg.MaxCandidates)
			if len(b) < minSize {
				continue
			}
			r := search(b)
			if r.truncated {
				res.truncated = true
			}
			if r.found && (!res.found || r.cost < res.cost-1e-9 || (r.cost <= res.cost+1e-9 && len(r.idx) < len(res.idx))) {
				r.truncated = r.truncated || res.truncated
				res, best = r, b
			}
		}
		cands = best
	} else {
		cands = rankAndTrim(cands, cfg.MaxCandidates)
		res = search(cands)
	}
	if res.truncated {
		s.truncated++
	}
	if !res.found {
		return false
	}
	xs := make([]int, len(res.idx))
	var simSum float64
	minDay, maxDay := math.MaxInt, math.MinInt
	for i, k := range res.idx {
		xs[i] = cands[k].idx
		simSum += cands[k].sim
		minDay = min(minDay, s.xDay[xs[i]])
		maxDay = max(maxDay, s.xDay[xs[i]])
	}
	sort.Ints(xs)
	gross := res.sum
	fee := gross - target
	feePct := float64(fee) / float64(gross) * 100
	k := len(xs)
	feats := []Feature{
		{Name: "rule", Detail: r.Name, Score: 1, Weight: 0},
		{Name: "sum", Detail: fmt.Sprintf("%d records totalling %s; entry %s", k, money(gross), money(target)), Score: 1, Weight: 0},
	}
	conf := 0.9
	if r.FeeMaxPercent > 0 || r.FeeFixedCents > 0 {
		allowed := fmt.Sprintf("rule rate %.2f%%", r.FeeMinPercent)
		if r.FeeMaxPercent != r.FeeMinPercent {
			allowed = fmt.Sprintf("rule allows %.2f%%-%.2f%%", r.FeeMinPercent, r.FeeMaxPercent)
		}
		if r.FeeFixedCents > 0 {
			allowed += fmt.Sprintf(" + %s per record", money(r.FeeFixedCents))
		}
		feats = append(feats, Feature{Name: "fee", Detail: fmt.Sprintf("%s withheld (%.2f%% of gross; %s)", money(fee), feePct, allowed), Score: 1, Weight: 0})
		conf = 0.86
	} else if fee != 0 {
		feats = append(feats, Feature{Name: "rounding", Detail: money(fee), Score: 1, Weight: 0})
		conf = 0.88
	}
	dateDetail := fmt.Sprintf("expected dates %s to %s relative to entry", days(minDay-s.eDay[e]), days(maxDay-s.eDay[e]))
	if minDay == maxDay {
		dateDetail = fmt.Sprintf("all expected on the same date, %s relative to entry", days(minDay-s.eDay[e]))
	}
	feats = append(feats, Feature{Name: "date", Detail: dateDetail, Score: 1, Weight: 0})
	if r.RequireCounterparty {
		feats = append(feats, Feature{Name: "counterparty", Detail: fmt.Sprintf("mean similarity %.2f", simSum/float64(k)), Score: round3(simSum / float64(k)), Weight: 0})
	}
	conf -= 0.02 * float64(max(k-2, 0))
	if res.ambiguous {
		conf -= 0.15
		feats = append(feats, Feature{Name: "ambiguity", Detail: "another subset of the same size fits equally well", Score: 0, Weight: 0})
	}
	if res.truncated {
		feats = append(feats, Feature{Name: "search", Detail: fmt.Sprintf("budget exhausted after %d nodes; best subset found so far", res.nodes), Score: 0, Weight: 0})
		conf -= 0.05
	}
	kind := ManyToOne
	rule := fmt.Sprintf("one entry settles %d expected records (rule %s): gross %s, net %s", k, r.Name, money(gross), money(target))
	if k == 1 {
		kind = OneToOne
		rule = fmt.Sprintf("entry settles one expected record net of fees (rule %s): gross %s, net %s", r.Name, money(gross), money(target))
	}
	if fee != 0 {
		rule += fmt.Sprintf(", difference %s (%.2f%%)", money(-fee), feePct)
	}
	s.commit(Match{
		Pass: PassGroup, Kind: kind, Confidence: round3(math.Max(conf, 0.3)),
		Explanation: Explanation{Rule: rule, Features: feats},
		entries:     []int{e}, expected: xs,
	})
	return true
}

func (s *state) oneToMany(x int, deadline time.Time) bool {
	cfg := s.cfg.Group
	sp := cfg.Split
	ex := &s.ex[x]
	target := abs64(ex.AmountCents)
	var cands []ranked
	for _, e := range dayRange(s.eByDay, s.eDay, s.xDay[x]-sp.WindowDays, s.xDay[x]+sp.WindowDays) {
		en := &s.en[e]
		if s.eUsed[e] || !sameSign(en.AmountCents, ex.AmountCents) || abs64(en.AmountCents) > target+sp.ToleranceCents {
			continue
		}
		dd := s.eDay[e] - s.xDay[x]
		sim, ok := s.party(e, x)
		if !ok || sim < sp.MinCounterpartySimilarity {
			continue
		}
		cands = append(cands, ranked{idx: e, sim: round3(sim), dist: absInt(dd)})
	}
	if len(cands) < 2 {
		return false
	}
	cands = rankAndTrim(cands, cfg.MaxCandidates)
	items := make([]int64, len(cands))
	rank := make([]int64, len(cands))
	for i, c := range cands {
		items[i] = abs64(s.en[c.idx].AmountCents)
		rank[i] = int64(c.dist)
	}
	res := solveSubset(subsetProblem{
		items: items, rank: rank, lo: target - sp.ToleranceCents, hi: target + sp.ToleranceCents,
		minSize: 2, maxSize: min(cfg.MaxGroupSize, len(items)), maxNodes: cfg.MaxNodes, deadline: deadline,
		accept: func(sum, _ int64, k int) bool { return abs64(sum-target) <= sp.ToleranceCents },
		cost:   func(sum, _ int64, k int) float64 { return float64(abs64(sum - target)) },
	})
	if res.truncated {
		s.truncated++
	}
	if !res.found {
		return false
	}
	es := make([]int, len(res.idx))
	var simSum float64
	for i, k := range res.idx {
		es[i] = cands[k].idx
		simSum += cands[k].sim
	}
	sort.Ints(es)
	k := len(es)
	conf := 0.88 - 0.02*float64(k-2)
	feats := []Feature{
		{Name: "sum", Detail: fmt.Sprintf("%d entries totalling %s; expected %s", k, money(res.sum), money(target)), Score: 1, Weight: 0},
		{Name: "counterparty", Detail: fmt.Sprintf("mean similarity %.2f to %q", simSum/float64(k), ex.Counterparty), Score: round3(simSum / float64(k)), Weight: 0},
	}
	if res.ambiguous {
		conf -= 0.15
		feats = append(feats, Feature{Name: "ambiguity", Detail: "another subset of the same size fits equally well", Score: 0, Weight: 0})
	}
	if res.truncated {
		conf -= 0.05
		feats = append(feats, Feature{Name: "search", Detail: fmt.Sprintf("budget exhausted after %d nodes; best subset found so far", res.nodes), Score: 0, Weight: 0})
	}
	s.commit(Match{
		Pass: PassGroup, Kind: OneToMany, Confidence: round3(math.Max(conf, 0.3)),
		Explanation: Explanation{Rule: fmt.Sprintf("expected record paid in %d parts by the same counterparty, total %s", k, money(res.sum)), Features: feats},
		entries:     es, expected: []int{x},
	})
	return true
}

// ---------------------------------------------------------------- pass 4

func (s *state) passFuzzy() {
	cfg := s.cfg.Fuzzy
	w := cfg.Weights
	xs := make([]int, 0, len(s.ex))
	for x := range s.ex {
		if !s.xUsed[x] {
			xs = append(xs, x)
		}
	}
	sort.SliceStable(xs, func(a, b int) bool { return s.ex[xs[a]].AmountCents < s.ex[xs[b]].AmountCents })
	var cands []cand
	for e, en := range s.en {
		if s.eUsed[e] {
			continue
		}
		a := en.AmountCents
		tol := max(cfg.AmountToleranceCents, int64(math.Round(float64(abs64(a))*cfg.AmountTolerancePercent/100)))
		start := sort.Search(len(xs), func(i int) bool { return s.ex[xs[i]].AmountCents >= a-tol })
		for _, x := range xs[start:] {
			ex := s.ex[x]
			if ex.AmountCents > a+tol {
				break
			}
			if !sameSign(a, ex.AmountCents) {
				continue
			}
			dd, known := s.dateDiff(e, x)
			if known && absInt(dd) > cfg.DateToleranceDays {
				continue
			}
			diff := a - ex.AmountCents
			af := Feature{Name: "amount", Detail: "equal", Score: 1, Weight: w.Amount}
			if diff != 0 {
				af.Score = round3(1 - float64(abs64(diff))/float64(tol+1))
				af.Detail = fmt.Sprintf("differs by %s (%.2f%% of expected)", money(diff), 100*float64(abs64(diff))/float64(max(abs64(ex.AmountCents), 1)))
			}
			df := s.dateFeature(e, x, cfg.DateToleranceDays, w.Date)
			// Features that cannot be evaluated (no party name, no
			// description) get weight 0 and the score is renormalized over
			// the remaining ones, so missing data neither helps nor hurts.
			pf := s.partyFeature(e, x, w.Counterparty)
			if sim, ok := s.party(e, x); !ok {
				pf.Weight = 0
			} else if sim < cfg.MinCounterpartySimilarity {
				continue // names contradict each other
			}
			sf := Feature{Name: "description", Detail: "no expected description", Score: 0, Weight: 0}
			if len(s.xDesc[x]) > 0 && len(s.eTok[e]) > 0 {
				sim := containment(s.eTok[e], s.xDesc[x])
				sf.Score = round3(sim)
				sf.Detail = fmt.Sprintf("%.2f %q", sim, ex.Description)
				sf.Weight = w.Description
			}
			if !known {
				df.Weight = 0
			}
			wsum := af.Weight + df.Weight + pf.Weight + sf.Weight
			score := (af.Score*af.Weight + df.Score*df.Weight + pf.Score*pf.Weight + sf.Score*sf.Weight) / wsum
			if score < cfg.MinScore {
				continue
			}
			rule := fmt.Sprintf("best-scoring candidate within tolerances: amount %s, date %s, counterparty %.2f, description %.2f; score %.2f",
				af.Detail, df.Detail, pf.Score, sf.Score, score)
			cands = append(cands, cand{e: e, x: x, score: score, conf: round3(0.9 * score), pass: PassFuzzy, rule: rule, feats: []Feature{af, df, pf, sf}})
		}
	}
	s.commitPairs(s.assign(cands))
}

// ---------------------------------------------------------------- assignment

// assign resolves competing one-to-one candidates. Candidates are split
// into connected components of the bipartite candidate graph (a form of
// blocking: records that share no candidate never interact). Components
// whose larger side is at most Assignment.HungarianMaxSize are solved
// optimally (maximum total score) with Hungarian; larger ones greedily by
// descending score.
func (s *state) assign(cands []cand) []cand {
	if len(cands) == 0 {
		return nil
	}
	ne := len(s.en)
	parent := make([]int, ne+len(s.ex))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(a int) int {
		for parent[a] != a {
			parent[a] = parent[parent[a]]
			a = parent[a]
		}
		return a
	}
	for _, c := range cands {
		ra, rb := find(c.e), find(ne+c.x)
		if ra != rb {
			parent[max(ra, rb)] = min(ra, rb)
		}
	}
	comps := map[int][]int{}
	var roots []int
	for i, c := range cands {
		r := find(c.e)
		if _, ok := comps[r]; !ok {
			roots = append(roots, r)
		}
		comps[r] = append(comps[r], i)
	}
	var out []cand
	limit := s.cfg.Assignment.HungarianMaxSize
	for _, r := range roots {
		idx := comps[r]
		if len(idx) == 1 {
			out = append(out, cands[idx[0]])
			continue
		}
		rows, cols := map[int]int{}, map[int]int{}
		var rowIDs, colIDs []int
		for _, i := range idx {
			if _, ok := rows[cands[i].e]; !ok {
				rows[cands[i].e] = 0
				rowIDs = append(rowIDs, cands[i].e)
			}
			if _, ok := cols[cands[i].x]; !ok {
				cols[cands[i].x] = 0
				colIDs = append(colIDs, cands[i].x)
			}
		}
		sort.Ints(rowIDs)
		sort.Ints(colIDs)
		if max(len(rowIDs), len(colIDs)) <= limit {
			for k, v := range rowIDs {
				rows[v] = k
			}
			for k, v := range colIDs {
				cols[v] = k
			}
			cost := make([][]int64, len(rowIDs))
			at := make([][]int, len(rowIDs))
			for k := range cost {
				cost[k] = make([]int64, len(colIDs))
				at[k] = make([]int, len(colIDs))
				for j := range at[k] {
					at[k][j] = -1
				}
			}
			for _, i := range idx {
				ri, ci := rows[cands[i].e], cols[cands[i].x]
				c := -int64(math.Round(cands[i].score*1e6)) - 1
				if at[ri][ci] < 0 || c < cost[ri][ci] {
					cost[ri][ci] = c
					at[ri][ci] = i
				}
			}
			assign, _, err := Hungarian(cost)
			if err == nil {
				for ri, ci := range assign {
					if ci >= 0 && at[ri][ci] >= 0 {
						out = append(out, cands[at[ri][ci]])
					}
				}
				continue
			}
		}
		out = append(out, greedy(cands, idx)...)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].e != out[b].e {
			return out[a].e < out[b].e
		}
		return out[a].x < out[b].x
	})
	return out
}

func greedy(cands []cand, idx []int) []cand {
	order := append([]int(nil), idx...)
	sort.SliceStable(order, func(a, b int) bool {
		ca, cb := cands[order[a]], cands[order[b]]
		if ca.score != cb.score {
			return ca.score > cb.score
		}
		if ca.e != cb.e {
			return ca.e < cb.e
		}
		return ca.x < cb.x
	})
	usedE, usedX := map[int]bool{}, map[int]bool{}
	var out []cand
	for _, i := range order {
		c := cands[i]
		if usedE[c.e] || usedX[c.x] {
			continue
		}
		usedE[c.e], usedX[c.x] = true, true
		out = append(out, c)
	}
	return out
}

func (s *state) commitPairs(cs []cand) {
	for _, c := range cs {
		s.commit(Match{
			Pass: c.pass, Kind: OneToOne, Confidence: c.conf,
			Explanation: Explanation{Rule: c.rule, Features: c.feats},
			entries:     []int{c.e}, expected: []int{c.x},
		})
	}
}

func (s *state) commit(m Match) {
	for _, e := range m.entries {
		s.eUsed[e] = true
		m.EntryIDs = append(m.EntryIDs, s.en[e].ID)
		m.EntryCents += s.en[e].AmountCents
	}
	for _, x := range m.expected {
		s.xUsed[x] = true
		m.ExpectedIDs = append(m.ExpectedIDs, s.ex[x].ID)
		m.ExpectedCents += s.ex[x].AmountCents
	}
	m.DiffCents = m.EntryCents - m.ExpectedCents
	s.matches = append(s.matches, m)
}

// ---------------------------------------------------------------- result

var passRank = map[Pass]int{PassReference: 0, PassExact: 1, PassGroup: 2, PassFuzzy: 3}

func (s *state) result() *Result {
	sort.SliceStable(s.matches, func(a, b int) bool {
		ma, mb := s.matches[a], s.matches[b]
		if passRank[ma.Pass] != passRank[mb.Pass] {
			return passRank[ma.Pass] < passRank[mb.Pass]
		}
		if ma.entries[0] != mb.entries[0] {
			return ma.entries[0] < mb.entries[0]
		}
		return ma.expected[0] < mb.expected[0]
	})
	r := &Result{
		Matches:           s.matches,
		UnmatchedEntries:  []statement.Entry{},
		UnmatchedExpected: []Expected{},
		Duplicates:        []DuplicateGroup{},
	}
	if r.Matches == nil {
		r.Matches = []Match{}
	}
	sum := Summary{
		Entries: len(s.en), Expected: len(s.ex), Matches: len(s.matches),
		MatchesByPass: map[Pass]int{}, MatchesByKind: map[Kind]int{},
		GroupSearchTruncated: s.truncated,
	}
	for i := range r.Matches {
		m := &r.Matches[i]
		m.ID = fmt.Sprintf("M%04d", i+1)
		sum.MatchesByPass[m.Pass]++
		sum.MatchesByKind[m.Kind]++
		sum.MatchedEntries += len(m.entries)
		sum.MatchedExpected += len(m.expected)
		sum.DifferenceCents += m.DiffCents
	}
	for e, en := range s.en {
		if en.AmountCents > 0 {
			sum.EntryCreditCents += en.AmountCents
		} else {
			sum.EntryDebitCents += en.AmountCents
		}
		if !s.eUsed[e] {
			r.UnmatchedEntries = append(r.UnmatchedEntries, en)
			sum.UnmatchedEntryCents += en.AmountCents
		}
	}
	for x, ex := range s.ex {
		if !s.xUsed[x] {
			r.UnmatchedExpected = append(r.UnmatchedExpected, ex)
			sum.UnmatchedExpectedCents += ex.AmountCents
		}
	}
	sum.UnmatchedEntries = len(r.UnmatchedEntries)
	sum.UnmatchedExpected = len(r.UnmatchedExpected)
	if s.cfg.Duplicates.Enabled {
		r.Duplicates = append(r.Duplicates, findEntryDuplicates(s.en)...)
		r.Duplicates = append(r.Duplicates, findExpectedDuplicates(s.ex)...)
	}
	sum.DuplicateGroups = len(r.Duplicates)
	r.Summary = sum
	return r
}

type dupKey struct {
	amount int64
	day    int
	ref    string
	text   string
}

func groupDuplicates(n int, side string, id func(int) string, key func(int) (dupKey, bool), idReason, keyReason string) []DuplicateGroup {
	var out []DuplicateGroup
	byID := map[string][]int{}
	var idOrder []string
	for i := 0; i < n; i++ {
		k := id(i)
		if k == "" {
			continue
		}
		if _, ok := byID[k]; !ok {
			idOrder = append(idOrder, k)
		}
		byID[k] = append(byID[k], i)
	}
	inID := map[int]bool{}
	for _, k := range idOrder {
		if g := byID[k]; len(g) > 1 {
			ids := make([]string, len(g))
			for j, i := range g {
				ids[j] = id(i)
				inID[i] = true
			}
			out = append(out, DuplicateGroup{Side: side, IDs: ids, Reason: idReason})
		}
	}
	byKey := map[dupKey][]int{}
	var keyOrder []dupKey
	for i := 0; i < n; i++ {
		if inID[i] {
			continue
		}
		k, ok := key(i)
		if !ok {
			continue
		}
		if _, seen := byKey[k]; !seen {
			keyOrder = append(keyOrder, k)
		}
		byKey[k] = append(byKey[k], i)
	}
	for _, k := range keyOrder {
		if g := byKey[k]; len(g) > 1 {
			ids := make([]string, len(g))
			for j, i := range g {
				ids[j] = id(i)
			}
			out = append(out, DuplicateGroup{Side: side, IDs: ids, Reason: keyReason})
		}
	}
	return out
}

func findEntryDuplicates(en []statement.Entry) []DuplicateGroup {
	return groupDuplicates(len(en), "statement",
		func(i int) string { return en[i].ID },
		func(i int) (dupKey, bool) {
			e := en[i]
			if e.BookingDate.IsZero() {
				return dupKey{}, false
			}
			return dupKey{e.AmountCents, civilDay(e.BookingDate), textnorm.RefKey(e.Reference), textnorm.Fold(strings.Join(strings.Fields(e.Description), " "))}, true
		},
		"same entry identifier appears more than once",
		"same amount, booking date, reference and description")
}

func findExpectedDuplicates(ex []Expected) []DuplicateGroup {
	return groupDuplicates(len(ex), "expected",
		func(i int) string { return ex[i].ID },
		func(i int) (dupKey, bool) {
			x := ex[i]
			if x.Date.IsZero() {
				return dupKey{}, false
			}
			return dupKey{x.AmountCents, civilDay(x.Date), textnorm.RefKey(x.Reference), Normalize(x.Counterparty)}, true
		},
		"same expected ID appears more than once",
		"same amount, date, reference and counterparty")
}
