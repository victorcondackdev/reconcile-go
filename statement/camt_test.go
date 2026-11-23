package statement

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestParseCAMT053Sample(t *testing.T) {
	f, err := os.Open("../testdata/extrato-camt053.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if st.Format != FormatCAMT053 || st.AccountID != "0001123456" || st.BankID != "FICTBRRJXXX" || st.Currency != "BRL" {
		t.Errorf("header %+v", st)
	}
	if len(st.Entries) != 13 || len(st.Balances) != 2 || st.Balances[1].Kind != "CLBD" || st.Balances[1].AmountCents != 847355 {
		t.Fatalf("entries %d balances %+v", len(st.Entries), st.Balances)
	}
	e := st.Entries[0]
	if e.ID != "202611160001" || e.AmountCents != 125000 || e.Reference != "E99999999202611161015ABCDE123456" ||
		e.Counterparty != "João da Conceição" || e.Type != "PMNT/RCDT/OTHR" || e.Raw["Sts"] != "BOOK" {
		t.Errorf("entry 0 %+v", e)
	}
	if e := st.Entries[1]; e.Reference != "" {
		t.Errorf("NOTPROVIDED kept: %q", e.Reference)
	}
	if e := st.Entries[8]; e.AmountCents != -154000 || e.Counterparty != "Transportadora Serra Azul S.A." {
		t.Errorf("debit/creditor: %+v", e)
	}
	if e := st.Entries[11]; e.Description != "Tarifa pacote serviços" || e.Counterparty != "" {
		t.Errorf("no TxDtls: %+v", e)
	}
	if st.Start.Format("2006-01-02") != "2026-11-16" || st.End.Format("2006-01-02") != "2026-11-24" {
		t.Errorf("period %v %v", st.Start, st.End)
	}
}

func TestParseCAMT054Sample(t *testing.T) {
	data, err := os.ReadFile("../testdata/extrato-camt054.xml")
	if err != nil {
		t.Fatal(err)
	}
	st, err := ParseCAMT(strings.NewReader(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	if st.Format != FormatCAMT054 || len(st.Entries) != 2 || st.AccountID != "BR1800360305000010009795493C1" {
		t.Fatalf("%+v", st)
	}
	b := st.Entries[0]
	if b.AmountCents != 182040 || b.Raw["TxDtls"] != "2" || b.Raw["EndToEndIds"] != "E99999999202611211014QWERTY12345" || b.Reference != "" {
		t.Errorf("batch entry: %+v", b)
	}
	if b.BookingDate.Format("15:04") != "10:14" || b.ValueDate.Format("2006-01-02") != "2026-11-21" {
		t.Errorf("DtTm: %v %v", b.BookingDate, b.ValueDate)
	}
	if r := st.Entries[1]; r.AmountCents != -7500 || r.Raw["RvslInd"] != "true" || r.ValueDate != r.BookingDate {
		t.Errorf("reversal: %+v", r)
	}
	// Splitting batches yields one entry per TxDtls with its own party and
	// reference; the ISO-8859-1 declaration is honoured.
	st, err = ParseWithOptions(strings.NewReader(string(data)), Options{SplitBatches: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Entries) != 3 {
		t.Fatalf("split entries %d", len(st.Entries))
	}
	a, c := st.Entries[0], st.Entries[1]
	if a.ID != "CN-554433/1" || a.AmountCents != 120000 || a.Counterparty != "Padaria São João Ltda" || a.Reference != "NF-2026-0815" {
		t.Errorf("tx 1 %+v", a)
	}
	if c.AmountCents != 62040 || c.Reference != "E99999999202611211014QWERTY12345" || c.Counterparty != "Açougue Boa Carne" ||
		!strings.Contains(c.Description, "Fatura 0816") {
		t.Errorf("tx 2 %+v", c)
	}
}

const camtTmpl = `<?xml version="1.0"?><Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.02"><BkToCstmrStmt><Stmt><Id>S</Id><Acct><Id><IBAN>X</IBAN></Id></Acct>%s</Stmt></BkToCstmrStmt></Document>`

func camt(ntry string) string { return strings.Replace(camtTmpl, "%s", ntry, 1) }

func TestCAMTVariants(t *testing.T) {
	// Version .02 layout: plain Sts, Dbtr/Nm, TxId fallback, DtTm without
	// offset, entry without references.
	doc := camt(`<Ntry><Amt Ccy="EUR">10.5</Amt><CdtDbtInd>CRDT</CdtDbtInd><Sts>BOOK</Sts><BookgDt><DtTm>2026-11-21T10:00:00</DtTm></BookgDt>
<NtryDtls><TxDtls><Refs><TxId>TX-1</TxId></Refs><RltdPties><Dbtr><Nm>Hans</Nm></Dbtr></RltdPties><AddtlTxInf>extra</AddtlTxInf></TxDtls></NtryDtls></Ntry>
<Ntry><Amt Ccy="EUR">1.00</Amt><CdtDbtInd>DBIT</CdtDbtInd><Sts><Prtry>PDNG</Prtry></Sts><ValDt><Dt>2026-11-22</Dt></ValDt>
<NtryDtls><TxDtls><RltdPties><UltmtCdtr><Pty><Nm>Ultimate</Nm></Pty></UltmtCdtr></RltdPties></TxDtls></NtryDtls></Ntry>`)
	st, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	a, b := st.Entries[0], st.Entries[1]
	if a.ID != "camt-1-1" || a.AmountCents != 1050 || a.Currency != "EUR" || a.Reference != "TX-1" || a.Counterparty != "Hans" || a.Description != "extra" {
		t.Errorf("a %+v", a)
	}
	if b.AmountCents != -100 || b.Counterparty != "Ultimate" || b.BookingDate.Format("2006-01-02") != "2026-11-22" || b.Raw["Sts"] != "PDNG" {
		t.Errorf("b %+v", b)
	}
	// Split with a TxDtls lacking an amount falls back to one entry.
	doc = camt(`<Ntry><Amt>3.00</Amt><CdtDbtInd>CRDT</CdtDbtInd><BookgDt><Dt>2026-11-21</Dt></BookgDt><NtryDtls><TxDtls><Amt>1.00</Amt></TxDtls><TxDtls></TxDtls></NtryDtls></Ntry>`)
	st, err = ParseWithOptions(strings.NewReader(doc), Options{SplitBatches: true})
	if err != nil || len(st.Entries) != 1 || st.Entries[0].AmountCents != 300 || st.Entries[0].Currency != "BRL" {
		t.Errorf("fallback: %+v %v", st, err)
	}
	// Amounts in AmtDtls/TxAmt are used when TxDtls/Amt is absent.
	doc = camt(`<Ntry><Amt>3.00</Amt><CdtDbtInd>CRDT</CdtDbtInd><BookgDt><Dt>2026-11-21</Dt></BookgDt><NtryDtls><TxDtls><AmtDtls><TxAmt><Amt>1.00</Amt></TxAmt></AmtDtls></TxDtls><TxDtls><Amt>2.00</Amt></TxDtls></NtryDtls></Ntry>`)
	st, err = ParseWithOptions(strings.NewReader(doc), Options{SplitBatches: true})
	if err != nil || len(st.Entries) != 2 || st.Entries[0].AmountCents != 100 {
		t.Errorf("AmtDtls: %+v %v", st, err)
	}
	// camt.052 report.
	doc = `<Document><BkToCstmrAcctRpt><Rpt><Ntry><Amt>1</Amt><CdtDbtInd>CRDT</CdtDbtInd><BookgDt><Dt>2026-11-21</Dt></BookgDt></Ntry></Rpt></BkToCstmrAcctRpt></Document>`
	st, err = Parse(strings.NewReader(doc))
	if err != nil || st.Format != FormatCAMT052 || len(st.Entries) != 1 {
		t.Errorf("camt.052: %+v %v", st, err)
	}
}

func TestCAMTErrors(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want error
	}{
		{"malformed xml", camt(`<Ntry>`), ErrSyntax},
		{"wrong root", `<Other><BkToCstmrStmt/></Other>`, ErrSyntax},
		{"no statements", `<Document><BkToCstmrStmt></BkToCstmrStmt></Document>`, ErrSyntax},
		{"bad amount", camt(`<Ntry><Amt>1,00</Amt><CdtDbtInd>CRDT</CdtDbtInd><BookgDt><Dt>2026-11-21</Dt></BookgDt></Ntry>`), ErrAmount},
		{"negative amount", camt(`<Ntry><Amt>-1.00</Amt><CdtDbtInd>CRDT</CdtDbtInd><BookgDt><Dt>2026-11-21</Dt></BookgDt></Ntry>`), ErrAmount},
		{"missing indicator", camt(`<Ntry><Amt>1.00</Amt><BookgDt><Dt>2026-11-21</Dt></BookgDt></Ntry>`), ErrMissing},
		{"bad booking date", camt(`<Ntry><Amt>1.00</Amt><CdtDbtInd>CRDT</CdtDbtInd><BookgDt><Dt>21/11/2026</Dt></BookgDt></Ntry>`), ErrDate},
		{"no dates", camt(`<Ntry><Amt>1.00</Amt><CdtDbtInd>CRDT</CdtDbtInd></Ntry>`), ErrMissing},
		{"bad balance", camt(`<Bal><Amt>x</Amt><CdtDbtInd>CRDT</CdtDbtInd></Bal>`), ErrAmount},
		{"unknown charset", `<?xml version="1.0" encoding="EBCDIC"?><Document/>`, ErrSyntax},
	}
	for _, tt := range tests {
		_, err := ParseCAMT(strings.NewReader(tt.doc))
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
	}
	doc := camt(`<Ntry><Amt>3.00</Amt><CdtDbtInd>CRDT</CdtDbtInd><BookgDt><Dt>2026-11-21</Dt></BookgDt><NtryDtls><TxDtls><Amt>1.00</Amt></TxDtls><TxDtls><Amt>1.00</Amt></TxDtls></NtryDtls></Ntry>`)
	if _, err := ParseWithOptions(strings.NewReader(doc), Options{SplitBatches: true}); !errors.Is(err, ErrAmount) {
		t.Errorf("split sum mismatch: %v", err)
	}
	doc = camt(`<Ntry><Amt>3.00</Amt><CdtDbtInd>CRDT</CdtDbtInd><BookgDt><Dt>2026-11-21</Dt></BookgDt><NtryDtls><TxDtls><Amt>x</Amt></TxDtls><TxDtls><Amt>1.00</Amt></TxDtls></NtryDtls></Ntry>`)
	if _, err := ParseWithOptions(strings.NewReader(doc), Options{SplitBatches: true}); !errors.Is(err, ErrAmount) {
		t.Errorf("split bad amount: %v", err)
	}
}
