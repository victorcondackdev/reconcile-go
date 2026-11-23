package match

import (
	"bytes"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	"github.com/victorcondackdev/reconcile-go/statement"
)

func exportFixture(t *testing.T) *Result {
	es := []statement.Entry{
		ent("E1", 0, 10000, "PIX RECEBIDO MARIA"),
		ent("E2", 0, 10000, "PIX RECEBIDO MARIA"),
		ent("E3", 2, 10400, "DEPOSITO CARLOS"),
		ent("E4", 1, -3990, "TARIFA"),
	}
	xs := []Expected{exp("X1", 0, 10000, "Maria", ""), exp("X3", 2, 10050, "Carlos", ""), exp("X9", 0, 777, "Ninguém", "")}
	return run(t, DefaultConfig(), es, xs)
}

func TestWriteCSV(t *testing.T) {
	res := exportFixture(t)
	var b bytes.Buffer
	if err := res.WriteCSV(&b); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&b).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rows[0], ",") != strings.Join(CSVHeader, ",") {
		t.Errorf("header %v", rows[0])
	}
	var statuses []string
	for _, r := range rows[1:] {
		statuses = append(statuses, r[0])
	}
	if got := strings.Join(statuses, " "); got != "matched matched unmatched_entry unmatched_entry unmatched_expected" {
		t.Errorf("rows %q", got)
	}
	if rows[2][7] != "104.00" || rows[2][9] != "3.50" {
		t.Errorf("amount formatting %v", rows[2])
	}
	if err := res.WriteCSV(failWriter{}); err == nil {
		t.Error("write error not reported")
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestWriteReport(t *testing.T) {
	res := exportFixture(t)
	var b bytes.Buffer
	if err := res.WriteReport(&b, ReportOptions{Details: true}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{
		"Reconciliation report", "Statement entries         4", "exact                   1", "fuzzy                   1",
		"Unmatched statement entries", "E4", "Unmatched expected records", "X9", "Suspected duplicates",
		"statement E1, E2: same amount, booking date, reference and description",
		"Matches\n-------", "- amount", "x 0.35",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	b.Reset()
	if err := res.WriteReport(&b, ReportOptions{MaxRows: 1}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "... 1 more") || strings.Contains(b.String(), "Matches\n-------") {
		t.Errorf("row limit:\n%s", b.String())
	}
	// Low-confidence matches are listed for review.
	res.Matches[1].Confidence = 0.5
	b.Reset()
	res.WriteReport(&b, ReportOptions{MaxRows: -1})
	if !strings.Contains(b.String(), "Low-confidence matches (review)") {
		t.Errorf("low confidence section missing:\n%s", b.String())
	}
	res.Summary.GroupSearchTruncated = 2
	res.Summary.MatchesByKind[ManyToOne] = 1
	res.UnmatchedExpected[0].Date = res.UnmatchedExpected[0].Date.AddDate(-3000, 0, 0).Truncate(0)
	res.UnmatchedExpected = append(res.UnmatchedExpected, Expected{ID: "NODATE"})
	b.Reset()
	res.WriteReport(&b, ReportOptions{})
	if !strings.Contains(b.String(), "cut by budget: 2") || !strings.Contains(b.String(), "grouped: 1 many-to-one") {
		t.Errorf("budget/grouped lines missing:\n%s", b.String())
	}
}
