package dataset

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/victorcondackdev/reconcile-go/match"
	"github.com/victorcondackdev/reconcile-go/statement"
)

func TestGenerateDeterministic(t *testing.T) {
	a, _ := json.Marshal(Generate(Noisy(0.5, 300, 9)))
	b, _ := json.Marshal(Generate(Noisy(0.5, 300, 9)))
	c, _ := json.Marshal(Generate(Noisy(0.5, 300, 10)))
	if !bytes.Equal(a, b) {
		t.Error("same seed produced different datasets")
	}
	if bytes.Equal(a, c) {
		t.Error("different seeds produced the same dataset")
	}
	d := Generate(Config{})
	if len(d.Expected) == 0 || len(d.Entries) == 0 || len(d.Truth) == 0 {
		t.Errorf("zero config: %d entries %d expected", len(d.Entries), len(d.Expected))
	}
	if Noisy(-1, 1, 1).TypoRate != 0 || Noisy(9, 1, 1).TypoRate != 0.6 {
		t.Error("noise level not clamped")
	}
}

func TestGeneratedInvariants(t *testing.T) {
	d := Generate(Noisy(1, 800, 3))
	ids := map[string]bool{}
	for _, e := range d.Entries {
		if ids[e.ID] {
			t.Fatalf("duplicate entry ID %s", e.ID)
		}
		ids[e.ID] = true
	}
	xids := map[string]bool{}
	for _, x := range d.Expected {
		xids[x.ID] = true
	}
	kinds := map[string]int{}
	for _, l := range d.Truth {
		kinds[l.Kind]++
		for _, e := range l.EntryIDs {
			if !ids[e] {
				t.Fatalf("truth references unknown entry %s", e)
			}
		}
		for _, x := range l.ExpectedIDs {
			if !xids[x] {
				t.Fatalf("truth references unknown expected %s", x)
			}
		}
	}
	for _, k := range []string{"pix", "boleto", "card-batch", "ted", "payable", "split", "combined"} {
		if kinds[k] == 0 {
			t.Errorf("no %s links generated: %v", k, kinds)
		}
	}
	if len(d.Aliases) == 0 {
		t.Error("no duplicates generated at noise 1")
	}
}

func TestMatcherQuality(t *testing.T) {
	floors := map[float64][2]float64{0: {0.995, 0.995}, 0.25: {0.99, 0.99}, 0.5: {0.99, 0.99}, 1: {0.98, 0.97}}
	for noise, floor := range floors {
		for seed := int64(1); seed <= 3; seed++ {
			d := Generate(Noisy(noise, 1000, seed))
			res, err := match.Reconcile(d.Entries, d.Expected, MatchConfig())
			if err != nil {
				t.Fatal(err)
			}
			m := Evaluate(res, d)
			if m.Precision < floor[0] || m.Recall < floor[1] {
				t.Errorf("noise %.2f seed %d: precision %.4f recall %.4f below %v", noise, seed, m.Precision, m.Recall, floor)
			}
		}
	}
}

// TestCleanErrorsAreInterchangeable checks that on noise-free data every
// wrong pair links records that cannot be told apart: the predicted and
// the true expected record share amount, date and counterparty.
func TestCleanErrorsAreInterchangeable(t *testing.T) {
	for seed := int64(1); seed <= 5; seed++ {
		d := Generate(Noisy(0, 2000, seed))
		res, err := match.Reconcile(d.Entries, d.Expected, MatchConfig())
		if err != nil {
			t.Fatal(err)
		}
		byID := map[string]match.Expected{}
		for _, x := range d.Expected {
			byID[x.ID] = x
		}
		truthOf := map[string][]string{}
		for _, l := range d.Truth {
			for _, e := range l.EntryIDs {
				truthOf[e] = append(truthOf[e], l.ExpectedIDs...)
			}
		}
		key := func(x match.Expected) string {
			return x.Date.Format("2006-01-02") + "|" + x.Counterparty
		}
		for _, m := range res.Matches {
			for _, e := range m.EntryIDs {
				want := map[string]bool{}
				for _, x := range truthOf[e] {
					want[x] = true
				}
				for _, x := range m.ExpectedIDs {
					if want[x] {
						continue
					}
					ok := false
					for w := range want {
						if key(byID[w]) == key(byID[x]) && byID[w].AmountCents == byID[x].AmountCents {
							ok = true
						}
					}
					if !ok {
						t.Errorf("seed %d: %s matched to %s, truth %v, and they are distinguishable", seed, e, x, truthOf[e])
					}
				}
			}
		}
	}
}

func TestEvaluate(t *testing.T) {
	d := &Dataset{
		Truth:   []Link{{EntryIDs: []string{"E1"}, ExpectedIDs: []string{"X1"}}, {EntryIDs: []string{"E2"}, ExpectedIDs: []string{"X2", "X3"}}},
		Aliases: map[string]string{"E9": "E1"},
	}
	res := &match.Result{Matches: []match.Match{
		{EntryIDs: []string{"E9"}, ExpectedIDs: []string{"X1"}},
		{EntryIDs: []string{"E2"}, ExpectedIDs: []string{"X2", "X4"}},
	}}
	m := Evaluate(res, d)
	if m.TruePositives != 2 || m.FalsePositives != 1 || m.FalseNegatives != 1 || m.Precision != 2.0/3 || m.Recall != 2.0/3 {
		t.Errorf("%+v", m)
	}
	m = Evaluate(&match.Result{}, &Dataset{})
	if m.Precision != 1 || m.Recall != 1 || m.F1 != 1 {
		t.Errorf("empty: %+v", m)
	}
	m = Evaluate(&match.Result{}, d)
	if m.Precision != 1 || m.Recall != 0 || m.F1 != 0 {
		t.Errorf("nothing predicted: %+v", m)
	}
}

func ids(res *match.Result) string {
	var b strings.Builder
	for _, m := range res.Matches {
		b.WriteString(strings.Join(m.EntryIDs, "+") + "=" + strings.Join(m.ExpectedIDs, "+") + " ")
	}
	return b.String()
}

// TestFileRoundTrip writes the dataset as files, parses them back through
// the public readers and checks that reconciliation gives the same answer
// as on the in-memory records.
func TestFileRoundTrip(t *testing.T) {
	d := Generate(Noisy(0.5, 400, 4))
	want, err := match.Reconcile(d.Entries, d.Expected, MatchConfig())
	if err != nil {
		t.Fatal(err)
	}
	var xb bytes.Buffer
	if err := d.WriteExpectedCSV(&xb); err != nil {
		t.Fatal(err)
	}
	xm := ExpectedMapping()
	exp, err := match.ReadExpectedCSV(&xb, &xm)
	if err != nil {
		t.Fatal(err)
	}
	sm := StatementMapping()
	for _, format := range []string{"csv", "ofx"} {
		var sb bytes.Buffer
		if format == "csv" {
			err = d.WriteStatementCSV(&sb)
		} else {
			err = d.WriteStatementOFX(&sb)
		}
		if err != nil {
			t.Fatal(err)
		}
		st, err := statement.ParseWithOptions(&sb, statement.Options{CSV: &sm})
		if err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if len(st.Entries) != len(d.Entries) {
			t.Fatalf("%s: %d entries, want %d", format, len(st.Entries), len(d.Entries))
		}
		got, err := match.Reconcile(st.Entries, exp, MatchConfig())
		if err != nil {
			t.Fatal(err)
		}
		if ids(got) != ids(want) {
			t.Errorf("%s round trip changed the result", format)
		}
	}
	var mb, tb bytes.Buffer
	if err := WriteMapping(&mb); err != nil || !json.Valid(mb.Bytes()) {
		t.Errorf("mapping: %v", err)
	}
	if err := d.WriteTruth(&tb); err != nil || !json.Valid(tb.Bytes()) {
		t.Errorf("truth: %v", err)
	}
}
