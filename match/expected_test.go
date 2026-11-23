package match

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func sampleMapping(t *testing.T) *ExpectedMapping {
	t.Helper()
	b, err := os.ReadFile("../testdata/mapping.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Expected *ExpectedMapping `json:"expected"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f.Expected
}

func TestReadExpectedCSVSample(t *testing.T) {
	f, err := os.Open("../testdata/pedidos.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	xs, err := ReadExpectedCSV(f, sampleMapping(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(xs) != 14 {
		t.Fatalf("records %d", len(xs))
	}
	x := xs[0]
	if x.ID != "PED-1001" || x.AmountCents != 125000 || x.Reference != "E99999999202611161015ABCDE123456" ||
		x.Counterparty != "João da Conceição" || x.Date.Format("2006-01-02") != "2026-11-16" || x.Tags[0] != "pix" {
		t.Errorf("first record %+v", x)
	}
	if c := xs[3]; len(c.Tags) != 2 || c.Tags[1] != "pagfacil" {
		t.Errorf("tags %+v", c.Tags)
	}
	if p := xs[9]; p.AmountCents != -154000 {
		t.Errorf("payable %+v", p)
	}
}

func TestReadExpectedCSVGuess(t *testing.T) {
	in := "order_id,amount,due date,customer,channel\nA1,10.50,2026-11-20,ACME,card;visa\n,3,,,\n"
	xs, err := ReadExpectedCSV(strings.NewReader(in), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(xs) != 2 || xs[0].ID != "A1" || xs[0].AmountCents != 1050 || xs[0].Counterparty != "ACME" || xs[1].ID != "line-3" || !xs[1].Date.IsZero() {
		t.Errorf("%+v", xs)
	}
	m := &ExpectedMapping{TagSeparator: ";", Negate: true, Columns: ExpectedColumns{ID: "#1", Amount: "#2", Tags: "#5"}}
	xs, err = ReadExpectedCSV(strings.NewReader(in), m)
	if err != nil || xs[0].AmountCents != -1050 || len(xs[0].Tags) != 2 {
		t.Errorf("explicit mapping: %+v %v", xs, err)
	}
}

func TestReadExpectedErrors(t *testing.T) {
	cols := ExpectedColumns{ID: "id", Amount: "valor", Date: "data"}
	tests := []struct {
		name string
		in   string
		m    *ExpectedMapping
	}{
		{"unguessable", "foo,bar\n1,2\n", nil},
		{"missing column", "id;valor\n", &ExpectedMapping{Columns: ExpectedColumns{ID: "id", Amount: "total"}}},
		{"no id", "id;valor\n", &ExpectedMapping{Columns: ExpectedColumns{Amount: "valor"}}},
		{"bad amount", "id;valor;data\n1;abc;01/01/2026\n", &ExpectedMapping{Columns: cols}},
		{"bad date", "id;valor;data\n1;1,00;ontem\n", &ExpectedMapping{Columns: cols}},
		{"bad encoding", "id;valor\n", &ExpectedMapping{Encoding: "x", Columns: cols}},
	}
	for _, tt := range tests {
		if _, err := ReadExpectedCSV(strings.NewReader(tt.in), tt.m); err == nil {
			t.Errorf("%s: no error", tt.name)
		}
	}
	if _, err := ReadExpectedCSV(errReader{}, nil); err == nil {
		t.Error("read error")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, os.ErrClosed }

func TestReadExpectedJSON(t *testing.T) {
	xs, err := ReadExpectedJSON(strings.NewReader(`[{"id":"A","amount_cents":100,"date":"2026-11-20T00:00:00Z","tags":["pix"]}]`))
	if err != nil || len(xs) != 1 || xs[0].AmountCents != 100 || xs[0].Tags[0] != "pix" {
		t.Errorf("%+v %v", xs, err)
	}
	if _, err := ReadExpectedJSON(strings.NewReader(`[{"id":"A","valor":1}]`)); err == nil {
		t.Error("unknown field accepted")
	}
}
