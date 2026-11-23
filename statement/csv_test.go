package statement

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseCSVSample(t *testing.T) {
	mf, err := os.ReadFile("../testdata/mapping.json")
	if err != nil {
		t.Fatal(err)
	}
	// The sample mapping file wraps the statement mapping.
	i := strings.Index(string(mf), `"statement":`)
	j := strings.Index(string(mf), `"expected":`)
	section := strings.TrimSpace(string(mf)[i+len(`"statement":`) : j])
	section = strings.TrimSuffix(section, ",")
	m, err := LoadCSVMapping(strings.NewReader(section))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("../testdata/extrato.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := ParseWithOptions(f, Options{CSV: m})
	if err != nil {
		t.Fatal(err)
	}
	if st.Format != FormatCSV || len(st.Entries) != 13 {
		t.Fatalf("format %s entries %d", st.Format, len(st.Entries))
	}
	e := st.Entries[0]
	if e.AmountCents != 125000 || e.Reference != "E99999999202611161015ABCDE123456" || e.ID != "202611160001" || e.Currency != "BRL" {
		t.Errorf("entry 0: %+v", e)
	}
	if !strings.HasPrefix(e.Description, "PIX RECEBIDO JOÃO DA CONCEIÇÃO") {
		t.Errorf("description %q", e.Description)
	}
	if d := st.Entries[8]; d.AmountCents != -154000 {
		t.Errorf("D/C column: %+v", d)
	}
	if st.Start.Format("02/01") != "16/11" || st.End.Format("02/01") != "24/11" {
		t.Errorf("period %v %v", st.Start, st.End)
	}
	if st.Entries[0].Raw["Valor (R$)"] != "1.250,00" {
		t.Errorf("raw %v", st.Entries[0].Raw)
	}
}

func TestParseCSVTable(t *testing.T) {
	brt := time.FixedZone("BRT", -3*3600)
	tests := []struct {
		name  string
		m     CSVMapping
		in    string
		loc   *time.Location
		want  []int64
		check func(t *testing.T, st *Statement)
	}{
		{
			name: "credit and debit columns, latin1, quoted",
			m: CSVMapping{Encoding: "iso-8859-1", DecimalSeparator: ",", ThousandsSeparator: ".",
				Columns: CSVColumns{BookingDate: "data", Description: "historico", Credit: "credito", Debit: "debito"}},
			in:   "Data;Hist\xf3rico;Cr\xe9dito;D\xe9bito\r\n01/11/2026;\"Dep\xf3sito; esp\xe9cie\";1.000,00;\r\n02/11/2026;Tarifa;;12,50\r\n",
			want: []int64{100000, -1250},
			check: func(t *testing.T, st *Statement) {
				if st.Entries[0].Description != "Depósito; espécie" || st.Entries[0].ID != "row-2" {
					t.Errorf("%+v", st.Entries[0])
				}
			},
		},
		{
			name: "comma separated, US amounts, ISO dates, value date, currency, reference, counterparty",
			m: CSVMapping{Delimiter: ",", DateFormat: "yyyy-mm-dd", Columns: CSVColumns{
				ID: "id", BookingDate: "booked", ValueDate: "value", Amount: "amount", Currency: "ccy",
				Counterparty: "party", Reference: "ref", Description: "memo"}},
			in:   "id,booked,value,amount,ccy,party,ref,memo\nT1,2026-11-01,2026-11-03,\"1,234.56\",usd,ACME,R-1,wire\n",
			want: []int64{123456},
			check: func(t *testing.T, st *Statement) {
				e := st.Entries[0]
				if e.Currency != "USD" || e.Counterparty != "ACME" || e.Reference != "R-1" || e.ValueDate.Day() != 3 {
					t.Errorf("%+v", e)
				}
			},
		},
		{
			name: "direction column with custom debit values and negate",
			m: CSVMapping{Negate: true, DebitValues: []string{"SAÍDA"},
				Columns: CSVColumns{BookingDate: "#1", Amount: "#2", Direction: "#3"}, NoHeader: true},
			in:   "01/11/2026;10,00;ENTRADA\n01/11/2026;5,00;saida\n",
			want: []int64{-1000, 500},
		},
		{
			name: "skip descriptions and blank lines, location",
			m:    CSVMapping{SkipDescriptions: []string{"saldo anterior", " "}, Columns: CSVColumns{BookingDate: "Data", Amount: "Valor", Description: "Lançamento"}},
			in:   "Data;Lançamento;Valor\n01/11/2026;SALDO ANTERIOR;100,00\n\n;;\n02/11/2026 10:30;PIX;-3,00\n",
			loc:  brt,
			want: []int64{-300},
			check: func(t *testing.T, st *Statement) {
				if _, off := st.Entries[0].BookingDate.Zone(); off != -3*3600 {
					t.Errorf("location not applied")
				}
			},
		},
		{
			name: "pix e2e found in description",
			m:    CSVMapping{Columns: CSVColumns{BookingDate: "Data", Amount: "Valor", Description: "Histórico"}},
			in:   "Data;Histórico;Valor\n01/11/2026;PIX RECEBIDO E99999999202611011200AAAAAAAAAAA FULANO;R$ 7,00\n",
			want: []int64{700},
			check: func(t *testing.T, st *Statement) {
				if st.Entries[0].Reference != "E99999999202611011200AAAAAAAAAAA" {
					t.Errorf("reference %q", st.Entries[0].Reference)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st, err := ParseWithOptions(strings.NewReader(tt.in), Options{CSV: &tt.m, Location: tt.loc})
			if err != nil {
				t.Fatal(err)
			}
			if len(st.Entries) != len(tt.want) {
				t.Fatalf("entries %d, want %d: %+v", len(st.Entries), len(tt.want), st.Entries)
			}
			for i, w := range tt.want {
				if st.Entries[i].AmountCents != w {
					t.Errorf("entry %d amount %d, want %d", i, st.Entries[i].AmountCents, w)
				}
			}
			if tt.check != nil {
				tt.check(t, st)
			}
		})
	}
}

func TestParseCSVErrors(t *testing.T) {
	cols := CSVColumns{BookingDate: "Data", Amount: "Valor"}
	tests := []struct {
		name string
		m    CSVMapping
		in   string
		want error
		line int
	}{
		{"missing column", CSVMapping{Columns: CSVColumns{BookingDate: "Data", Amount: "Montante"}}, "Data;Valor\n", ErrMissing, 0},
		{"no date column", CSVMapping{Columns: CSVColumns{Amount: "Valor"}}, "Data;Valor\n", ErrMissing, 0},
		{"no amount column", CSVMapping{Columns: CSVColumns{BookingDate: "Data"}}, "Data;Valor\n", ErrMissing, 0},
		{"bad amount", CSVMapping{Columns: cols}, "Data;Valor\n01/11/2026;dez reais\n", ErrAmount, 2},
		{"empty amount", CSVMapping{Columns: cols}, "Data;Valor\n01/11/2026;\n", ErrMissing, 2},
		{"bad date", CSVMapping{Columns: cols}, "Data;Valor\n32/11/2026;1,00\n", ErrDate, 2},
		{"empty date", CSVMapping{Columns: cols}, "Data;Valor\n;1,00\n", ErrMissing, 2},
		{"bad value date", CSVMapping{Columns: CSVColumns{BookingDate: "Data", Amount: "Valor", ValueDate: "VD"}}, "Data;Valor;VD\n01/11/2026;1,00;x\n", ErrDate, 2},
		{"bad credit", CSVMapping{Columns: CSVColumns{BookingDate: "Data", Credit: "C", Debit: "D"}}, "Data;C;D\n01/11/2026;x;\n", ErrAmount, 2},
		{"bad debit", CSVMapping{Columns: CSVColumns{BookingDate: "Data", Credit: "C", Debit: "D"}}, "Data;C;D\n01/11/2026;;x\n", ErrAmount, 2},
		{"no credit nor debit", CSVMapping{Columns: CSVColumns{BookingDate: "Data", Credit: "C", Debit: "D"}}, "Data;C;D\n01/11/2026;;\n", ErrMissing, 2},
		{"bad decimal separator", CSVMapping{DecimalSeparator: ",,", Columns: cols}, "Data;Valor\n", nil, 0},
		{"bad thousands separator", CSVMapping{ThousandsSeparator: "..", Columns: cols}, "Data;Valor\n", nil, 0},
		{"bad encoding", CSVMapping{Encoding: "ebcdic", Columns: cols}, "Data;Valor\n", ErrSyntax, 0},
	}
	for _, tt := range tests {
		_, err := ParseCSV(strings.NewReader(tt.in), tt.m)
		if err == nil {
			t.Errorf("%s: no error", tt.name)
			continue
		}
		if tt.want != nil && !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
		var pe *ParseError
		if !errors.As(err, &pe) || pe.Index != tt.line {
			t.Errorf("%s: %v (index want %d)", tt.name, err, tt.line)
		}
	}
	if _, err := LoadCSVMapping(strings.NewReader(`{"columnz": {}}`)); err == nil {
		t.Error("unknown mapping field accepted")
	}
}

func TestGuessCSVMapping(t *testing.T) {
	m, ok := GuessCSVMapping([]string{"Data Lançamento", "Histórico", "Documento", "Valor (R$)", "Saldo"})
	if !ok || m.Columns.BookingDate != "Data Lançamento" || m.Columns.Description != "Histórico" || m.Columns.ID != "Documento" || m.Columns.Amount != "Valor (R$)" {
		t.Errorf("guess: %+v %v", m, ok)
	}
	if _, ok := GuessCSVMapping([]string{"foo", "bar"}); ok {
		t.Error("guess should fail")
	}
	// Parse without a mapping guesses from the header.
	st, err := Parse(strings.NewReader("Data;Descrição;Crédito;Débito\n05/11/2026;PIX;50,00;\n06/11/2026;TED;;20,00\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Entries) != 2 || st.Entries[0].AmountCents != 5000 || st.Entries[1].AmountCents != -2000 {
		t.Errorf("guessed parse: %+v", st.Entries)
	}
	if _, err := Parse(strings.NewReader("a;b\n1;2\n")); !errors.Is(err, ErrMissing) {
		t.Errorf("unguessable header: %v", err)
	}
}
