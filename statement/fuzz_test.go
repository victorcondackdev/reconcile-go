package statement

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

func addSamples(f *testing.F, names ...string) {
	for _, n := range names {
		b, err := os.ReadFile("../testdata/" + n)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
}

// checkStatement asserts invariants every successful parse must satisfy.
func checkStatement(t *testing.T, st *Statement) {
	t.Helper()
	for i, e := range st.Entries {
		if e.ID == "" {
			t.Fatalf("entry %d has no ID", i)
		}
		if e.BookingDate.IsZero() {
			t.Fatalf("entry %d has no booking date", i)
		}
		for _, s := range []string{e.Description, e.Counterparty, e.Reference, e.Currency} {
			if !utf8.ValidString(s) {
				t.Fatalf("entry %d has invalid UTF-8 %q", i, s)
			}
		}
	}
}

func FuzzParseOFX(f *testing.F) {
	addSamples(f, "extrato-ofx1.ofx", "extrato-ofx2.ofx")
	f.Add([]byte(ofx1("<STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20261121120000[-3:BRT]<TRNAMT>-1,00<FITID>x<MEMO>a&amp;b</STMTTRN>")))
	f.Add([]byte("<OFX><STMTTRN><DTPOSTED>20261121[+5.30:IST]<TRNAMT>1</STMTTRN></OFX>"))
	f.Fuzz(func(t *testing.T, data []byte) {
		st, err := ParseWithOptions(bytes.NewReader(data), Options{Format: "ofx", FixDebitSign: true})
		if err != nil {
			return
		}
		checkStatement(t, st)
	})
}

func FuzzParseCAMT(f *testing.F) {
	addSamples(f, "extrato-camt053.xml", "extrato-camt054.xml")
	f.Add([]byte(camt(`<Ntry><Amt>1.00</Amt><CdtDbtInd>DBIT</CdtDbtInd><BookgDt><DtTm>2026-11-21T10:00:00Z</DtTm></BookgDt></Ntry>`)))
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, split := range []bool{false, true} {
			st, err := ParseWithOptions(bytes.NewReader(data), Options{Format: "camt", SplitBatches: split})
			if err != nil {
				return
			}
			checkStatement(t, st)
		}
	})
}

func FuzzParseCSV(f *testing.F) {
	addSamples(f, "extrato.csv")
	f.Add([]byte("Data;Histórico;Valor\n01/11/2026;PIX;1.234,56\n"))
	f.Add([]byte("Data,Crédito,Débito\n2026-11-01,,\"1,00\"\n"))
	m := &CSVMapping{SkipRows: 2, DecimalSeparator: ",", ThousandsSeparator: ".", DateFormat: "dd/mm/yyyy",
		Columns: CSVColumns{BookingDate: "Data", ID: "Documento", Description: "Histórico", Amount: "Valor (R$)", Direction: "D/C"}}
	f.Fuzz(func(t *testing.T, data []byte) {
		if st, err := ParseWithOptions(bytes.NewReader(data), Options{CSV: m}); err == nil {
			checkStatement(t, st)
		}
		if st, err := ParseWithOptions(bytes.NewReader(data), Options{Format: FormatCSV}); err == nil {
			checkStatement(t, st)
		}
	})
}

// FuzzParse exercises format detection with every parser behind it.
func FuzzParse(f *testing.F) {
	addSamples(f, "extrato-ofx1.ofx", "extrato-ofx2.ofx", "extrato-camt053.xml", "extrato-camt054.xml", "extrato.csv")
	f.Fuzz(func(t *testing.T, data []byte) {
		st, err := Parse(bytes.NewReader(data))
		if err != nil {
			if err.Error() == "" || strings.Contains(err.Error(), "%!") {
				t.Fatalf("bad error message %q", err)
			}
			return
		}
		checkStatement(t, st)
	})
}
