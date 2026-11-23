package match

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/victorcondackdev/reconcile-go/statement"
)

var base = time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)

func day(d int) time.Time { return base.AddDate(0, 0, d).Add(12 * time.Hour) }

func ent(id string, d int, cents int64, desc string) statement.Entry {
	return statement.Entry{ID: id, BookingDate: day(d), ValueDate: day(d), AmountCents: cents, Currency: "BRL", Description: desc}
}

func exp(id string, d int, cents int64, cp, ref string, tags ...string) Expected {
	return Expected{ID: id, Date: day(d), AmountCents: cents, Counterparty: cp, Reference: ref, Tags: tags}
}

func run(t *testing.T, cfg Config, es []statement.Entry, xs []Expected) *Result {
	t.Helper()
	res, err := Reconcile(es, xs, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// pairs renders matches as "E1+E2=X1+X2" strings for compact assertions.
func pairs(res *Result) []string {
	var out []string
	for _, m := range res.Matches {
		out = append(out, strings.Join(m.EntryIDs, "+")+"="+strings.Join(m.ExpectedIDs, "+"))
	}
	return out
}

func wantPairs(t *testing.T, res *Result, want ...string) {
	t.Helper()
	got := strings.Join(pairs(res), " ")
	if got != strings.Join(want, " ") {
		t.Errorf("matches = %q, want %q", got, strings.Join(want, " "))
	}
}

func TestReferencePass(t *testing.T) {
	es := []statement.Entry{
		{ID: "F1", BookingDate: day(0), AmountCents: 10000, Reference: "E99999999202611161200AAAAAAAAAAA"},
		ent("F2", 1, 34900, "LIQUIDACAO BOLETO NN 000.123.456-78"),
		ent("PED-998877", 1, 5000, "DEPOSITO"),
		ent("F4", 2, 20000, "BOLETO NN 55555555"),     // amount differs by more than 50%
		ent("F5", 2, -7000, "PAGAMENTO NN 66666666"),  // sign differs
		ent("F6", 90, 1000, "LIQUIDACAO NN 77777777"), // too far in time
		ent("F7", 3, 9750, "LIQUIDACAO BOLETO NN 88888888"),
	}
	xs := []Expected{
		exp("X1", 0, 10000, "Maria", "E99999999202611161200AAAAAAAAAAA"),
		exp("X2", 0, 34900, "Carlos", "0012345678"),
		exp("PED-998877", 0, 5000, "", ""),
		exp("X4", 0, 50000, "", "55555555"),
		exp("X5", 0, 7000, "", "66666666"),
		exp("X6", 0, 1000, "", "77777777"),
		exp("X7", 1, 10000, "", "88888888"),
	}
	cfg := DefaultConfig()
	cfg.Passes = Passes{Reference: true}
	res := run(t, cfg, es, xs)
	wantPairs(t, res, "F1=X1", "F2=X2", "PED-998877=PED-998877", "F7=X7")
	m := res.Matches[3]
	if m.DiffCents != -250 || m.Confidence >= 0.95 || !strings.Contains(m.Explanation.Rule, "amount differs by -2.50") {
		t.Errorf("amount difference not explained: %+v", m)
	}
	if res.Matches[0].Confidence != 0.99 || res.Matches[0].Explanation.Features[0].Name != "reference" {
		t.Errorf("strong reference: %+v", res.Matches[0])
	}
	if !strings.Contains(res.Matches[2].Explanation.Rule, "statement ID equals expected ID") {
		t.Errorf("ID rule: %s", res.Matches[2].Explanation.Rule)
	}
	// Without description search the boleto tokens are ignored.
	cfg.Reference.SearchDescription = false
	cfg.Reference.MatchExpectedID = false
	wantPairs(t, run(t, cfg, es, xs), "F1=X1")
}

func TestExactPassOptimalAssignment(t *testing.T) {
	// Scores: E1-X1 0.75, E1-X2 0.625, E2-X1 0.375, E2-X2 infeasible
	// (4 days apart). Greedy takes E1-X1 and strands E2; Hungarian pairs
	// E1-X2 and E2-X1.
	es := []statement.Entry{ent("E1", 1, 10000, ""), ent("E2", 4, 10000, "")}
	xs := []Expected{exp("X1", 1, 10000, "", ""), exp("X2", 0, 10000, "", "")}
	cfg := DefaultConfig()
	cfg.Passes = Passes{Exact: true}
	wantPairs(t, run(t, cfg, es, xs), "E1=X2", "E2=X1")
	cfg.Assignment.HungarianMaxSize = 0
	wantPairs(t, run(t, cfg, es, xs), "E1=X1")
}

func TestExactPassCounterparty(t *testing.T) {
	es := []statement.Entry{
		ent("E1", 0, 4990, "PIX RECEBIDO MARIA APARECIDA SOUZA"),
		ent("E2", 0, 4990, "PIX RECEBIDO JOAO CARLOS PEREIRA"),
		ent("E3", 0, 4990, "PIX RECEBIDO ROBERTO NUNES"),
	}
	xs := []Expected{
		exp("X1", 0, 4990, "João Carlos Pereira", ""),
		exp("X2", 0, 4990, "Maria Aparecida Souza", ""),
		exp("X3", 0, 4990, "Fernanda Lima", ""),
	}
	cfg := DefaultConfig()
	cfg.Passes = Passes{Exact: true}
	res := run(t, cfg, es, xs)
	wantPairs(t, res, "E1=X2", "E2=X1")
	if len(res.UnmatchedEntries) != 1 || res.UnmatchedEntries[0].ID != "E3" {
		t.Errorf("contradicting names must not match: %+v", res.UnmatchedEntries)
	}
	f := res.Matches[0].Explanation.Features
	if len(f) != 3 || f[2].Name != "counterparty" || f[2].Score != 1 {
		t.Errorf("features %+v", f)
	}
}

func pagfacil() GroupRule {
	return GroupRule{Name: "pagfacil", Match: "PAGFACIL", Tag: "pagfacil", FeeMinPercent: 2.39, FeeMaxPercent: 2.39, ToleranceCents: 1}
}

func TestGroupManyToOne(t *testing.T) {
	// 120.00 + 75.50 + 210.35 = 405.85 gross; fees 2.87 + 1.80 + 5.03.
	es := []statement.Entry{
		ent("LOTE", 2, 39615, "PAGFACIL ADQUIRENCIA LIQ LOTE 000123"),
		ent("ONE", 2, 19522, "PAGFACIL ADQUIRENCIA LIQ LOTE 000124"),
		ent("COMBO", 3, 23000, "PIX RECEBIDO LUCAS FERREIRA"),
		ent("STRANGER", 3, 23000, "PIX RECEBIDO ALGUEM"),
	}
	xs := []Expected{
		exp("C1", 1, 12000, "PagFácil", "", "card", "pagfacil"),
		exp("C2", 1, 7550, "PagFácil", "", "card", "pagfacil"),
		exp("C3", 1, 21035, "PagFácil", "", "card", "pagfacil"),
		exp("C4", 1, 20000, "PagFácil", "", "card", "pagfacil"),
		exp("NOTAG", 1, 9999, "PagFácil", ""),
		exp("L1", 3, 15000, "Lucas Ferreira", ""),
		exp("L2", 3, 8000, "Lucas Ferreira", ""),
	}
	cfg := DefaultConfig()
	cfg.Passes = Passes{Group: true}
	cfg.Group.Rules = append([]GroupRule{pagfacil()}, cfg.Group.Rules...)
	res := run(t, cfg, es, xs)
	wantPairs(t, res, "LOTE=C1+C2+C3", "ONE=C4", "COMBO=L1+L2")
	m := res.Matches[0]
	if m.Kind != ManyToOne || m.DiffCents != -970 || m.ExpectedCents != 40585 {
		t.Errorf("batch: %+v", m)
	}
	if !strings.Contains(m.Explanation.Rule, "rule pagfacil") || !strings.Contains(m.Explanation.Rule, "(2.39%)") {
		t.Errorf("rule text %q", m.Explanation.Rule)
	}
	if res.Matches[1].Kind != OneToOne || !strings.Contains(res.Matches[1].Explanation.Rule, "net of fees") {
		t.Errorf("single-record settlement: %+v", res.Matches[1])
	}
	if res.Summary.MatchesByKind[ManyToOne] != 2 {
		t.Errorf("summary %+v", res.Summary)
	}
	// Without the acquirer rule the batch is left alone.
	cfg.Group.Rules = cfg.Group.Rules[1:]
	wantPairs(t, run(t, cfg, es, xs), "COMBO=L1+L2")
}

func TestGroupOneToMany(t *testing.T) {
	es := []statement.Entry{
		ent("P1", 2, 30000, "PIX RECEBIDO FERNANDA GONCALVES"),
		ent("P2", 3, 20000, "PIX RECEBIDO FERNANDA GONÇALVES"),
		ent("P3", 3, 20000, "PIX RECEBIDO OUTRA PESSOA"),
	}
	xs := []Expected{exp("X", 2, 50000, "Fernanda Gonçalves", "")}
	cfg := DefaultConfig()
	cfg.Passes = Passes{Group: true}
	res := run(t, cfg, es, xs)
	wantPairs(t, res, "P1+P2=X")
	if res.Matches[0].Kind != OneToMany || res.Matches[0].EntryCents != 50000 {
		t.Errorf("%+v", res.Matches[0])
	}
	cfg.Group.Split.Enabled = false
	wantPairs(t, run(t, cfg, es, xs))
}

func TestGroupAmbiguityAndBudget(t *testing.T) {
	es := []statement.Entry{ent("E", 1, 10000, "PIX RECEBIDO ANA LIMA")}
	xs := []Expected{
		exp("A", 1, 5000, "Ana Lima", ""), exp("B", 1, 5000, "Ana Lima", ""),
		exp("C", 1, 5000, "Ana Lima", ""),
	}
	cfg := DefaultConfig()
	cfg.Passes = Passes{Group: true}
	cfg.Duplicates.Enabled = false
	res := run(t, cfg, es, xs)
	if len(res.Matches) != 1 {
		t.Fatalf("matches %v", pairs(res))
	}
	var amb bool
	for _, f := range res.Matches[0].Explanation.Features {
		amb = amb || f.Name == "ambiguity"
	}
	if !amb || res.Matches[0].Confidence > 0.8 {
		t.Errorf("ambiguity not flagged: %+v", res.Matches[0])
	}
	cfg.Group.MaxNodes = 1
	res = run(t, cfg, es, xs)
	if res.Summary.GroupSearchTruncated == 0 {
		t.Errorf("truncation not reported: %+v", res.Summary)
	}
}

func TestFuzzyPass(t *testing.T) {
	es := []statement.Entry{
		ent("B1", 3, 34650, "LIQUIDACAO BOLETO CARLOS EDUARDO LIMA"), // net of a 2.50 fee, 2 days late
		ent("B2", 1, 4990, "PIX RECEBIDO PATRICIA MORAES"),           // different person, same amount
		ent("B3", 4, 10100, "DEPOSITO"),                              // no names on either side
	}
	xs := []Expected{
		exp("X1", 1, 34900, "Carlos Eduardo Lima", ""),
		exp("X2", 0, 4990, "Rodrigo Teixeira", ""),
		{ID: "X3", Date: day(3), AmountCents: 10000, Description: "deposito em especie"},
	}
	cfg := DefaultConfig()
	cfg.Passes = Passes{Fuzzy: true}
	res := run(t, cfg, es, xs)
	wantPairs(t, res, "B1=X1", "B3=X3")
	m := res.Matches[0]
	if m.DiffCents != -250 || m.Pass != PassFuzzy || len(m.Explanation.Features) != 4 {
		t.Errorf("%+v", m)
	}
	if res.Matches[1].Explanation.Features[2].Weight != 0 {
		t.Errorf("missing counterparty must carry no weight: %+v", res.Matches[1].Explanation.Features)
	}
	cfg.Fuzzy.MinScore = 0.99
	wantPairs(t, run(t, cfg, es, xs))
}

func TestUnknownDates(t *testing.T) {
	es := []statement.Entry{{ID: "E", AmountCents: 100}}
	xs := []Expected{{ID: "X", AmountCents: 100}}
	res := run(t, DefaultConfig(), es, xs)
	wantPairs(t, res, "E=X")
	if f := res.Matches[0].Explanation.Features[1]; f.Detail != "date unknown" {
		t.Errorf("%+v", f)
	}
	cfg := DefaultConfig()
	cfg.Passes = Passes{Fuzzy: true}
	wantPairs(t, run(t, cfg, []statement.Entry{{ID: "E", AmountCents: 100}}, []Expected{{ID: "X", AmountCents: 101}}), "E=X")
}

func TestDuplicates(t *testing.T) {
	es := []statement.Entry{
		ent("A", 0, 8990, "PIX RECEBIDO MARIA"),
		ent("B", 0, 8990, "PIX  RECEBIDO MARIA"),
		ent("C", 1, 100, "X"), ent("C", 2, 200, "Y"),
		{ID: "Z", AmountCents: 5},
	}
	xs := []Expected{
		exp("P1", 0, 8990, "Maria", ""), exp("P1", 0, 100, "", ""),
		exp("P2", 1, 500, "Ana Lima", "R1"), exp("P3", 1, 500, "ANA LIMA", "r1"),
		{ID: "P4", AmountCents: 1},
	}
	res := run(t, DefaultConfig(), es, xs)
	var got []string
	for _, d := range res.Duplicates {
		got = append(got, d.Side+":"+strings.Join(d.IDs, ","))
	}
	want := "statement:C,C statement:A,B expected:P1,P1 expected:P2,P3"
	if strings.Join(got, " ") != want {
		t.Errorf("duplicates %q, want %q", strings.Join(got, " "), want)
	}
	if res.Summary.DuplicateGroups != 4 {
		t.Errorf("summary %+v", res.Summary)
	}
	cfg := DefaultConfig()
	cfg.Duplicates.Enabled = false
	if res := run(t, cfg, es, xs); len(res.Duplicates) != 0 {
		t.Error("duplicates reported while disabled")
	}
}

func TestSummaryAndOrdering(t *testing.T) {
	es := []statement.Entry{
		ent("E1", 0, 100, ""), {ID: "E2", BookingDate: day(0), AmountCents: 200, Reference: "R2"}, ent("E3", 0, -50, "TARIFA"),
	}
	xs := []Expected{exp("X1", 0, 100, "", ""), exp("X2", 0, 200, "", "R2"), exp("X3", 0, 999, "", "")}
	res := run(t, DefaultConfig(), es, xs)
	wantPairs(t, res, "E2=X2", "E1=X1")
	s := res.Summary
	if s.Matches != 2 || s.MatchesByPass[PassReference] != 1 || s.MatchesByPass[PassExact] != 1 ||
		s.UnmatchedEntries != 1 || s.UnmatchedExpected != 1 || s.EntryCreditCents != 300 || s.EntryDebitCents != -50 ||
		s.UnmatchedEntryCents != -50 || s.UnmatchedExpectedCents != 999 || res.Matches[0].ID != "M0001" {
		t.Errorf("summary %+v", s)
	}
	empty := run(t, DefaultConfig(), nil, nil)
	if empty.Matches == nil || empty.UnmatchedEntries == nil || empty.UnmatchedExpected == nil || empty.Duplicates == nil {
		t.Error("empty result must use empty slices for stable JSON")
	}
}

func TestDeterminism(t *testing.T) {
	var es []statement.Entry
	var xs []Expected
	for i := 0; i < 60; i++ {
		amt := int64(1000 + (i%7)*100)
		es = append(es, ent("E"+string(rune('A'+i%26))+string(rune('a'+i/26)), i%5, amt, "PIX RECEBIDO CLIENTE"))
		xs = append(xs, exp("X"+string(rune('A'+i%26))+string(rune('a'+i/26)), (i+1)%5, amt, "Cliente", ""))
	}
	var first []byte
	for i := 0; i < 5; i++ {
		var b bytes.Buffer
		if err := run(t, DefaultConfig(), es, xs).WriteJSON(&b); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = b.Bytes()
		} else if !bytes.Equal(first, b.Bytes()) {
			t.Fatal("results differ between runs")
		}
	}
}

func TestNewInvalidConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Group.MaxGroupSize = 1
	if _, err := New(cfg); !errors.Is(err, ErrConfig) {
		t.Errorf("New: %v", err)
	}
	if _, err := Reconcile(nil, nil, cfg); !errors.Is(err, ErrConfig) {
		t.Errorf("Reconcile: %v", err)
	}
	e, err := New(DefaultConfig())
	if err != nil || e.Config().Exact.DateWindowDays != 3 {
		t.Errorf("Config(): %v", err)
	}
}

func TestResultJSONShape(t *testing.T) {
	res := run(t, DefaultConfig(), []statement.Entry{ent("E", 0, 100, "")}, []Expected{exp("X", 0, 100, "", "")})
	var b bytes.Buffer
	if err := res.WriteJSON(&b); err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	m := v["matches"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "pass", "kind", "entry_ids", "expected_ids", "confidence", "explanation", "diff_cents"} {
		if _, ok := m[k]; !ok {
			t.Errorf("match JSON lacks %q: %v", k, m)
		}
	}
}

func TestGroupSameDateAndLag(t *testing.T) {
	// Sales of day 1 settle on day 2. Day 2 also has sales (not yet
	// settleable with a one-day lag) and day 0 has sales whose amounts
	// could be combined with day-1 sales to hit the same net total.
	es := []statement.Entry{ent("LOTE", 2, 19522+9761, "PAGFACIL LIQ")}
	xs := []Expected{
		exp("D1a", 1, 20000, "", "", "pagfacil"), exp("D1b", 1, 10000, "", "", "pagfacil"),
		exp("D0a", 0, 10000, "", "", "pagfacil"), exp("D0b", 0, 20000, "", "", "pagfacil"),
		exp("D2a", 2, 10000, "", "", "pagfacil"), exp("D2b", 2, 20000, "", "", "pagfacil"),
		exp("D2c", 2, 777, "", "", "pagfacil"),
	}
	cfg := DefaultConfig()
	cfg.Passes = Passes{Group: true}
	cfg.Duplicates.Enabled = false
	r := pagfacil()
	r.SameDate, r.MinLagDays = true, 1
	cfg.Group.Rules = []GroupRule{r}
	res := run(t, cfg, es, xs)
	wantPairs(t, res, "LOTE=D1a+D1b")
	// Without the lag the same-day sales are preferred (nearest date).
	cfg.Group.Rules[0].MinLagDays = 0
	wantPairs(t, run(t, cfg, es, xs), "LOTE=D2a+D2b")
	// Without SameDate the mix is ambiguous across days.
	cfg.Group.Rules[0].SameDate = false
	res = run(t, cfg, es, xs)
	if len(res.Matches) != 1 || res.Matches[0].Confidence > 0.75 {
		t.Errorf("expected one ambiguous match: %+v", res.Matches)
	}
	// Budget exhaustion inside a date bucket is reported.
	cfg.Group.Rules[0].SameDate = true
	cfg.Group.MaxNodes = 1
	if res := run(t, cfg, es, xs); res.Summary.GroupSearchTruncated == 0 {
		t.Errorf("truncation not counted: %+v", res.Summary)
	}
	// Rules with a fixed fee per record are explained as fees.
	cfg.Group.MaxNodes = 1000
	cfg.Group.Rules = []GroupRule{{Name: "fixed", Tag: "pagfacil", FeeFixedCents: 50, ToleranceCents: 0, SameDate: true, MinLagDays: 1}}
	res = run(t, cfg, []statement.Entry{ent("F", 2, 29900, "LIQ")}, xs)
	if len(res.Matches) != 1 || !strings.Contains(res.Matches[0].Explanation.Features[2].Detail, "0.50 per record") {
		t.Errorf("fixed fee: %+v", res.Matches)
	}
	// A generic rule with rounding tolerance reports the rounding.
	cfg.Group.Rules = []GroupRule{{Name: "round", ToleranceCents: 1}}
	res = run(t, cfg, []statement.Entry{ent("R", 1, 29999, "")}, xs[:2])
	if len(res.Matches) != 1 || res.Matches[0].Explanation.Features[2].Name != "rounding" {
		t.Errorf("rounding: %+v", res.Matches)
	}
}

func TestRankAndTrim(t *testing.T) {
	c := rankAndTrim([]ranked{{idx: 3, sim: 0.5, dist: 1}, {idx: 1, sim: 0.9, dist: 2}, {idx: 2, sim: 0.5, dist: 0}, {idx: 0, sim: 0.5, dist: 0}}, 3)
	if len(c) != 3 || c[0].idx != 1 || c[1].idx != 0 || c[2].idx != 2 {
		t.Errorf("%+v", c)
	}
	if got := firstText(statement.Entry{Counterparty: "ACME", Description: "TED"}); got != "ACME TED" {
		t.Errorf("firstText %q", got)
	}
}
