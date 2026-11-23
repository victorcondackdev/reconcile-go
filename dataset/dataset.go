// Package dataset generates synthetic reconciliation datasets with ground
// truth, and scores matcher output against it.
//
// A dataset models the receivables and payables of a fictitious Brazilian
// e-commerce company: Pix payments (with EndToEndId), boletos (with nosso
// número), card sales settled in daily acquirer batches net of a merchant
// discount rate, TEDs, supplier payments, bank fees and unknown deposits.
// Noise knobs add the defects that make real reconciliation hard: dropped
// references, settlement delays, typos and truncated names, fees withheld,
// split and combined payments, duplicated statement lines and unpaid
// orders.
//
// All names, banks and identifiers are invented. Generation is
// deterministic for a given Config.
package dataset

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/victorcondackdev/reconcile-go/internal/textnorm"
	"github.com/victorcondackdev/reconcile-go/match"
	"github.com/victorcondackdev/reconcile-go/statement"
)

// AcquirerName is the fictitious card acquirer whose batches the
// generator emits.
const AcquirerName = "PagFácil Adquirência"

// MDRBasisPoints is the merchant discount rate the acquirer withholds
// from each card sale (2.39%).
const MDRBasisPoints = 239

// Config controls generation. Rates are probabilities in [0, 1].
type Config struct {
	Seed   int64
	Orders int
	// Start is the first order date. Default 2026-11-02.
	Start time.Time
	// Days spreads orders over this many days. Default max(20, Orders/12),
	// which keeps the daily density of a small merchant.
	Days int
	// CardShare is the fraction of orders paid by card. Default 0.25.
	CardShare float64

	DropReference float64 // bank omits the reference / nosso número
	ShiftRate     float64 // payment booked later than expected
	MaxShiftDays  int
	TypoRate      float64 // typo in the payer name printed by the bank
	FeeRate       float64 // boleto credited net of a R$ 2,50 fee
	SplitRate     float64 // Pix order paid in 2-3 parts
	CombineRate   float64 // one Pix paying two orders of the same customer
	DuplicateRate float64 // statement line exported twice
	MissingRate   float64 // order never paid
	ExtraRate     float64 // statement entries with no expected record
}

// Noisy returns a Config whose noise rates scale with level (0 = clean,
// 1 = very noisy).
func Noisy(level float64, orders int, seed int64) Config {
	l := math.Max(0, math.Min(1, level))
	return Config{
		Seed: seed, Orders: orders,
		DropReference: 0.6 * l, ShiftRate: 0.6 * l, MaxShiftDays: 1 + int(3*l),
		TypoRate: 0.6 * l, FeeRate: 0.3 * l, SplitRate: 0.1 * l, CombineRate: 0.1 * l,
		DuplicateRate: 0.04 * l, MissingRate: 0.1 * l, ExtraRate: 0.1 * l,
	}
}

// Link is one ground-truth match.
type Link struct {
	EntryIDs    []string `json:"entry_ids"`
	ExpectedIDs []string `json:"expected_ids"`
	Kind        string   `json:"kind"`
}

// Dataset is a generated statement, expected records and the truth.
type Dataset struct {
	Entries  []statement.Entry `json:"entries"`
	Expected []match.Expected  `json:"expected"`
	Truth    []Link            `json:"truth"`
	// Aliases maps duplicated statement entries to the entry they copy;
	// matching either one is correct.
	Aliases map[string]string `json:"aliases"`
}

var (
	firstNames = []string{"Ana", "Bruno", "Carla", "Diego", "Eduarda", "Fábio", "Gabriela", "Heitor", "Isabela", "João",
		"Larissa", "Marcelo", "Natália", "Otávio", "Patrícia", "Rafael", "Sílvia", "Thiago", "Úrsula", "Vinícius",
		"Yasmin", "Lúcia", "Mônica", "Caio", "Renata", "André", "Beatriz", "Cláudio", "Débora", "Érico"}
	surnames = []string{"Albuquerque", "Barbosa", "Cardoso", "Damasceno", "Esteves", "Figueiredo", "Gonçalves", "Holanda",
		"Ivo", "Junqueira", "Lacerda", "Magalhães", "Nogueira", "Orsini", "Pacheco", "Queiroz", "Rezende", "Sampaio",
		"Tavares", "Uchôa", "Vasconcelos", "Xavier", "Zanetti", "Brandão", "Coutinho", "Peixoto", "Moraes", "Teixeira"}
	suppliers = []string{"Embalagens Rio Claro Ltda", "Transportadora Serra Azul S.A.", "Gráfica Ponto Final ME",
		"Nuvem Clara Hospedagem Ltda", "Distribuidora Vale do Café Ltda", "Contábil Horizonte EPP",
		"Papelaria Estrela Guia Ltda", "Logística Rota Sul S.A."}
	menu = []int64{4990, 9990, 14900, 19900}
)

type gen struct {
	cfg   Config
	rng   *rand.Rand
	ds    *Dataset
	fitid int
	brt   *time.Location
}

func (g *gen) chance(p float64) bool { return p > 0 && g.rng.Float64() < p }

func (g *gen) nextID() string {
	g.fitid++
	return fmt.Sprintf("FIT%07d", g.fitid)
}

func (g *gen) name() string {
	n := firstNames[g.rng.Intn(len(firstNames))] + " " + surnames[g.rng.Intn(len(surnames))]
	if g.chance(0.4) {
		n += " " + surnames[g.rng.Intn(len(surnames))]
	}
	return n
}

func (g *gen) amount() int64 {
	if g.chance(0.15) {
		return menu[g.rng.Intn(len(menu))]
	}
	// log-uniform between R$ 20 and R$ 3.000
	v := math.Exp(math.Log(2000) + g.rng.Float64()*(math.Log(300000)-math.Log(2000)))
	return int64(v)
}

// bankName renders a party name the way bank statements do: upper case,
// no accents, truncated to 30 characters, sometimes with a typo.
func (g *gen) bankName(n string) string {
	s := strings.ToUpper(textnorm.Fold(n))
	if len(s) > 4 && g.chance(g.cfg.TypoRate) {
		r := []byte(s)
		// pick a position inside a letter run, away from the first letter
		for tries := 0; tries < 10; tries++ {
			i := 1 + g.rng.Intn(len(r)-2)
			if r[i] == ' ' || r[i-1] == ' ' || r[i+1] == ' ' {
				continue
			}
			switch g.rng.Intn(3) {
			case 0:
				r[i] = byte('A' + g.rng.Intn(26))
			case 1:
				r = append(r[:i], r[i+1:]...)
			default:
				r[i], r[i+1] = r[i+1], r[i]
			}
			break
		}
		s = string(r)
	}
	if len(s) > 30 {
		s = strings.TrimSpace(s[:30])
	}
	return s
}

func (g *gen) e2e(t time.Time) string {
	const alnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 11)
	for i := range b {
		b[i] = alnum[g.rng.Intn(len(alnum))]
	}
	return fmt.Sprintf("E%08d%s%s", 10000000+g.rng.Intn(89999999), t.UTC().Format("200601021504"), b)
}

func (g *gen) shift() int {
	if g.chance(g.cfg.ShiftRate) && g.cfg.MaxShiftDays > 0 {
		return 1 + g.rng.Intn(g.cfg.MaxShiftDays)
	}
	return 0
}

func (g *gen) entry(day time.Time, amount int64, desc string) statement.Entry {
	return statement.Entry{
		ID: g.nextID(), BookingDate: day, ValueDate: day, AmountCents: amount, Currency: "BRL",
		Description: desc, Reference: statement.FindEndToEndID(desc),
	}
}

func (g *gen) link(kind string, es []statement.Entry, xs ...match.Expected) {
	l := Link{Kind: kind}
	for _, e := range es {
		l.EntryIDs = append(l.EntryIDs, e.ID)
	}
	for _, x := range xs {
		l.ExpectedIDs = append(l.ExpectedIDs, x.ID)
	}
	g.ds.Truth = append(g.ds.Truth, l)
	g.ds.Entries = append(g.ds.Entries, es...)
}

// Generate builds a dataset from cfg.
func Generate(cfg Config) *Dataset {
	if cfg.Orders <= 0 {
		cfg.Orders = 200
	}
	if cfg.Start.IsZero() {
		cfg.Start = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	}
	if cfg.Days <= 0 {
		cfg.Days = max(20, cfg.Orders/12)
	}
	if cfg.CardShare == 0 {
		cfg.CardShare = 0.25
	}
	g := &gen{cfg: cfg, rng: rand.New(rand.NewSource(cfg.Seed)), ds: &Dataset{Aliases: map[string]string{}}, brt: time.FixedZone("BRT", -3*3600)}
	at := func(day int, hour int) time.Time {
		d := cfg.Start.AddDate(0, 0, day)
		return time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, g.brt)
	}
	cards := map[int][]match.Expected{}
	orderNo := 1000
	newOrder := func(day int, amount int64, cp, ref, channel string) match.Expected {
		orderNo++
		return match.Expected{
			ID: fmt.Sprintf("PED-%06d", orderNo), AmountCents: amount, Date: at(day, 10),
			Reference: ref, Counterparty: cp, Tags: []string{channel},
		}
	}
	for k := 0; k < cfg.Orders; k++ {
		day := g.rng.Intn(cfg.Days)
		amount := g.amount()
		customer := g.name()
		roll := g.rng.Float64()
		switch {
		case roll < 0.40: // Pix
			pay := at(day+g.shift(), 12)
			e2e := g.e2e(pay)
			ref := ""
			if g.chance(0.7) {
				ref = e2e
			}
			x := newOrder(day, amount, customer, ref, "pix")
			switch {
			case g.chance(cfg.MissingRate):
				g.ds.Expected = append(g.ds.Expected, x)
			case g.chance(cfg.SplitRate) && amount >= 2000:
				x.Reference = ""
				g.ds.Expected = append(g.ds.Expected, x)
				parts := 2 + g.rng.Intn(2)
				var es []statement.Entry
				left := amount
				for p := 0; p < parts; p++ {
					a := left
					if p < parts-1 {
						a = left/int64(parts-p) + int64(g.rng.Intn(500)) - 250
						a = max(a, 100)
					}
					left -= a
					es = append(es, g.entry(pay.AddDate(0, 0, g.rng.Intn(2)), a, "PIX RECEBIDO "+g.bankName(customer)))
				}
				g.link("split", es, x)
			case g.chance(cfg.CombineRate):
				x.Reference = ""
				y := newOrder(day, g.amount(), customer, "", "pix")
				g.ds.Expected = append(g.ds.Expected, x, y)
				e := g.entry(pay, x.AmountCents+y.AmountCents, "PIX RECEBIDO "+g.bankName(customer))
				g.link("combined", []statement.Entry{e}, x, y)
			default:
				g.ds.Expected = append(g.ds.Expected, x)
				desc := "PIX RECEBIDO " + g.bankName(customer)
				if !g.chance(cfg.DropReference) {
					desc += " E2E " + e2e
				}
				g.link("pix", []statement.Entry{g.entry(pay, amount, desc)}, x)
			}
		case roll < 0.60: // boleto
			nn := fmt.Sprintf("%011d", 10000000+g.rng.Int63n(9_000_000_000))
			x := newOrder(day, amount, customer, nn, "boleto")
			g.ds.Expected = append(g.ds.Expected, x)
			if g.chance(cfg.MissingRate) {
				continue
			}
			pay := at(day+1+g.shift(), 12)
			desc := "LIQUIDACAO BOLETO NN " + nn
			if g.chance(cfg.DropReference) {
				desc = "LIQUIDACAO BOLETO " + g.bankName(customer)
			}
			a := amount
			if g.chance(cfg.FeeRate) {
				a -= 250
			}
			g.link("boleto", []statement.Entry{g.entry(pay, a, desc)}, x)
		case roll < 0.60+cfg.CardShare: // card, settled in batches
			x := newOrder(day, amount, AcquirerName, "", "card")
			x.Tags = append(x.Tags, "pagfacil")
			x.Description = "Venda cartão " + customer
			g.ds.Expected = append(g.ds.Expected, x)
			cards[day] = append(cards[day], x)
		default: // TED
			x := newOrder(day, amount, customer, "", "ted")
			g.ds.Expected = append(g.ds.Expected, x)
			if g.chance(cfg.MissingRate) {
				continue
			}
			pay := at(day+g.shift(), 12)
			g.link("ted", []statement.Entry{g.entry(pay, amount, "TED RECEBIDA "+g.bankName(customer))}, x)
		}
	}
	// Acquirer batches: card sales of a day are credited the next business
	// day (sometimes later), net of MDR, at most 6 sales per batch.
	days := make([]int, 0, len(cards))
	for d := range cards {
		days = append(days, d)
	}
	sort.Ints(days)
	lot := 0
	for _, d := range days {
		sales := cards[d]
		pay := at(d+1+min(g.shift(), 1), 9)
		for len(sales) > 0 {
			n := min(len(sales), 6)
			batch := sales[:n]
			sales = sales[n:]
			var net int64
			for _, s := range batch {
				net += s.AmountCents - (s.AmountCents*MDRBasisPoints+5000)/10000
			}
			lot++
			e := g.entry(pay, net, fmt.Sprintf("PAGFACIL ADQUIRENCIA LIQ LOTE %06d", lot))
			kind := "card-batch"
			if n == 1 {
				kind = "card"
			}
			g.link(kind, []statement.Entry{e}, batch...)
		}
	}
	// Supplier payments.
	for k := 0; k < cfg.Orders/10; k++ {
		day := g.rng.Intn(cfg.Days)
		sup := suppliers[g.rng.Intn(len(suppliers))]
		amount := -(int64(5000) + g.rng.Int63n(800000))
		x := match.Expected{ID: fmt.Sprintf("PAG-%05d", k+1), AmountCents: amount, Date: at(day, 10), Counterparty: sup, Tags: []string{"payable"}}
		g.ds.Expected = append(g.ds.Expected, x)
		pay := at(day+min(g.shift(), 1), 15)
		g.link("payable", []statement.Entry{g.entry(pay, amount, "PIX ENVIADO "+g.bankName(sup))}, x)
	}
	// Entries nobody expects.
	extras := int(math.Round(float64(cfg.Orders) * cfg.ExtraRate))
	for k := 0; k < extras; k++ {
		day := at(g.rng.Intn(cfg.Days), 8)
		if k%3 == 0 {
			g.ds.Entries = append(g.ds.Entries, g.entry(day, -3990, "TARIFA PACOTE SERVICOS"))
		} else {
			g.ds.Entries = append(g.ds.Entries, g.entry(day, g.amount(), "PIX RECEBIDO "+g.bankName(g.name())))
		}
	}
	// Duplicated lines.
	n := len(g.ds.Entries)
	for i := 0; i < n; i++ {
		if g.chance(cfg.DuplicateRate) {
			d := g.ds.Entries[i]
			orig := d.ID
			d.ID = g.nextID()
			g.ds.Aliases[d.ID] = orig
			g.ds.Entries = append(g.ds.Entries, d)
		}
	}
	sort.SliceStable(g.ds.Entries, func(a, b int) bool {
		ea, eb := g.ds.Entries[a], g.ds.Entries[b]
		if !ea.BookingDate.Equal(eb.BookingDate) {
			return ea.BookingDate.Before(eb.BookingDate)
		}
		return ea.ID < eb.ID
	})
	sort.SliceStable(g.ds.Expected, func(a, b int) bool {
		return g.ds.Expected[a].Date.Before(g.ds.Expected[b].Date)
	})
	return g.ds
}

// MatchConfig returns the matching configuration suited to generated data: the
// defaults plus a group rule for the fictitious acquirer's batches.
func MatchConfig() match.Config {
	cfg := match.DefaultConfig()
	cfg.Group.Rules = append([]match.GroupRule{{
		Name: "pagfacil", Match: "PAGFACIL", Tag: "pagfacil",
		FeeMinPercent: 2.39, FeeMaxPercent: 2.39, ToleranceCents: 1, SameDate: true, MinLagDays: 1,
	}}, cfg.Group.Rules...)
	return cfg
}

// Metrics scores predicted links against the truth at the level of
// (entry, expected) pairs: a group match of one entry and four orders
// contributes four pairs.
type Metrics struct {
	TruthPairs     int     `json:"truth_pairs"`
	PredictedPairs int     `json:"predicted_pairs"`
	TruePositives  int     `json:"true_positives"`
	FalsePositives int     `json:"false_positives"`
	FalseNegatives int     `json:"false_negatives"`
	Precision      float64 `json:"precision"`
	Recall         float64 `json:"recall"`
	F1             float64 `json:"f1"`
}

// Evaluate compares a matcher result with the dataset truth.
func Evaluate(res *match.Result, d *Dataset) Metrics {
	type pair struct{ e, x string }
	canon := func(id string) string {
		if o, ok := d.Aliases[id]; ok {
			return o
		}
		return id
	}
	truth := map[pair]bool{}
	for _, l := range d.Truth {
		for _, e := range l.EntryIDs {
			for _, x := range l.ExpectedIDs {
				truth[pair{e, x}] = true
			}
		}
	}
	pred := map[pair]bool{}
	for _, m := range res.Matches {
		for _, e := range m.EntryIDs {
			for _, x := range m.ExpectedIDs {
				pred[pair{canon(e), x}] = true
			}
		}
	}
	var m Metrics
	m.TruthPairs, m.PredictedPairs = len(truth), len(pred)
	for p := range pred {
		if truth[p] {
			m.TruePositives++
		} else {
			m.FalsePositives++
		}
	}
	m.FalseNegatives = m.TruthPairs - m.TruePositives
	if m.PredictedPairs > 0 {
		m.Precision = float64(m.TruePositives) / float64(m.PredictedPairs)
	} else {
		m.Precision = 1
	}
	if m.TruthPairs > 0 {
		m.Recall = float64(m.TruePositives) / float64(m.TruthPairs)
	} else {
		m.Recall = 1
	}
	if m.Precision+m.Recall > 0 {
		m.F1 = 2 * m.Precision * m.Recall / (m.Precision + m.Recall)
	}
	return m
}
