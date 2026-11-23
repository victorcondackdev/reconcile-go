package match_test

import (
	"fmt"
	"time"

	"github.com/victorcondackdev/reconcile-go/match"
	"github.com/victorcondackdev/reconcile-go/statement"
)

func ExampleReconcile() {
	d := func(day int) time.Time { return time.Date(2026, 11, day, 12, 0, 0, 0, time.UTC) }
	entries := []statement.Entry{
		{ID: "F1", BookingDate: d(16), AmountCents: 125000, Reference: "E99999999202611161015ABCDE123456"},
		{ID: "F2", BookingDate: d(18), AmountCents: 39615, Description: "PAGFACIL ADQUIRENCIA LIQ LOTE 000123"},
		{ID: "F3", BookingDate: d(20), AmountCents: 64000, Description: "TED RECEBIDA RICARDO ALMEIDA"},
		{ID: "F4", BookingDate: d(20), AmountCents: -3990, Description: "TARIFA PACOTE SERVICOS"},
	}
	expected := []match.Expected{
		{ID: "PED-1001", Date: d(16), AmountCents: 125000, Reference: "E99999999202611161015ABCDE123456"},
		{ID: "PED-1004", Date: d(17), AmountCents: 12000, Tags: []string{"pagfacil"}},
		{ID: "PED-1005", Date: d(17), AmountCents: 7550, Tags: []string{"pagfacil"}},
		{ID: "PED-1006", Date: d(17), AmountCents: 21035, Tags: []string{"pagfacil"}},
		{ID: "PED-1009", Date: d(19), AmountCents: 64000, Counterparty: "Ricardo Almeida"},
	}
	cfg := match.DefaultConfig()
	cfg.Group.Rules = append(cfg.Group.Rules, match.GroupRule{
		Name: "pagfacil", Match: "PAGFACIL", Tag: "pagfacil",
		FeeMinPercent: 2.39, FeeMaxPercent: 2.39, ToleranceCents: 1,
	})
	res, err := match.Reconcile(entries, expected, cfg)
	if err != nil {
		panic(err)
	}
	for _, m := range res.Matches {
		fmt.Printf("%s %-9s %.2f %v <- %v\n", m.ID, m.Pass, m.Confidence, m.EntryIDs, m.ExpectedIDs)
		fmt.Println("     ", m.Explanation.Rule)
	}
	fmt.Println("unmatched entries:", len(res.UnmatchedEntries))
	// Output:
	// M0001 reference 0.99 [F1] <- [PED-1001]
	//       identifier match: statement reference equals expected reference
	// M0002 exact     0.96 [F3] <- [PED-1009]
	//       same amount, booked +1 day relative to expected date, counterparty similarity 1.00
	// M0003 group     0.84 [F2] <- [PED-1004 PED-1005 PED-1006]
	//       one entry settles 3 expected records (rule pagfacil): gross 405.85, net 396.15, difference -9.70 (2.39%)
	// unmatched entries: 1
}

func ExampleJaroWinkler() {
	fmt.Printf("%.3f\n", match.JaroWinkler("MARTHA", "MARHTA"))
	fmt.Printf("%.3f\n", match.TokenSimilarity("PIX RECEBIDO SOUZA MARIA A", "Maria Aparecida Souza"))
	// Output:
	// 0.961
	// 0.937
}
