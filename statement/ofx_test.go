package statement

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseOFXDate(t *testing.T) {
	tests := []struct {
		in     string
		want   string // RFC3339Nano
		offset int    // seconds east of UTC
	}{
		{"20261121", "2026-11-21T00:00:00Z", 0},
		{"202611211230", "2026-11-21T12:30:00Z", 0},
		{"20261121120000", "2026-11-21T12:00:00Z", 0},
		{"20261121120000[-3:BRT]", "2026-11-21T12:00:00-03:00", -3 * 3600},
		{"20261121120000[-03:BRT]", "2026-11-21T12:00:00-03:00", -3 * 3600},
		{"20261121120000.123[-3:BRT]", "2026-11-21T12:00:00.123-03:00", -3 * 3600},
		{"20261121000000[-3]", "2026-11-21T00:00:00-03:00", -3 * 3600},
		{"20261121000000[+0:GMT]", "2026-11-21T00:00:00Z", 0},
		{"20261121000000[0:GMT]", "2026-11-21T00:00:00Z", 0},
		{"20261121000000[+5.30:IST]", "2026-11-21T00:00:00+05:30", 5*3600 + 1800},
		{"20261121000000[-3.5:NST]", "2026-11-21T00:00:00-03:30", -(3*3600 + 1800)},
		{"20261121000000[]", "2026-11-21T00:00:00Z", 0},
		{" 20240229 ", "2024-02-29T00:00:00Z", 0},
	}
	for _, tt := range tests {
		got, err := ParseOFXDate(tt.in)
		if err != nil {
			t.Errorf("ParseOFXDate(%q): %v", tt.in, err)
			continue
		}
		if got.Format(time.RFC3339Nano) != tt.want {
			t.Errorf("ParseOFXDate(%q) = %s, want %s", tt.in, got.Format(time.RFC3339Nano), tt.want)
		}
		if _, off := got.Zone(); off != tt.offset {
			t.Errorf("ParseOFXDate(%q) offset %d, want %d", tt.in, off, tt.offset)
		}
	}
	// The calendar day is taken in the file's offset: 23:30 BRT is the
	// next day in UTC, but the booking day stays the 21st.
	d, _ := ParseOFXDate("20261121233000[-3:BRT]")
	if d.Day() != 21 || d.UTC().Day() != 22 {
		t.Errorf("day in offset: %v", d)
	}
	for _, bad := range []string{
		"", "2026112", "2026-11-21", "20261321", "20260230", "20261100", "20261121250000", "20261121126000",
		"20261121120000.", "20261121120000.abc", "20261121120000[x:BRT]", "20261121120000[-3.75:X]", "20261121[-15:X]",
		"20261121[:BRT]", "20261121[-3.123]", "202611211", "20261121[+]", "20261231235960",
	} {
		if _, err := ParseOFXDate(bad); !errors.Is(err, ErrDate) {
			t.Errorf("ParseOFXDate(%q) err = %v, want ErrDate", bad, err)
		}
	}
}

const ofx1Header = "OFXHEADER:100\nDATA:OFXSGML\nVERSION:102\nSECURITY:NONE\nENCODING:USASCII\nCHARSET:1252\nCOMPRESSION:NONE\nOLDFILEUID:NONE\nNEWFILEUID:NONE\n\n"

func ofx1(trns string) string {
	return ofx1Header + "<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>BRL<BANKACCTFROM><BANKID>0999<ACCTID>1-2</BANKACCTFROM><BANKTRANLIST><DTSTART>20261101<DTEND>20261130" +
		trns + "</BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>"
}

func TestParseOFX1Sample(t *testing.T) {
	f, err := os.Open("../testdata/extrato-ofx1.ofx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if st.Format != FormatOFX1 || st.BankID != "0999" || st.AccountID != "12345-6" || st.Currency != "BRL" {
		t.Errorf("header: %+v", st)
	}
	if len(st.Entries) != 13 {
		t.Fatalf("entries = %d", len(st.Entries))
	}
	e := st.Entries[0]
	if e.ID != "202611160001" || e.AmountCents != 125000 || e.Reference != "E99999999202611161015ABCDE123456" {
		t.Errorf("entry 0: %+v", e)
	}
	if !strings.Contains(e.Description, "JOÃO DA CONCEIÇÃO – E2E") {
		t.Errorf("Windows-1252 not converted: %q", e.Description)
	}
	if st.Entries[3].AmountCents != 34900 {
		t.Errorf("comma decimal TRNAMT: %d", st.Entries[3].AmountCents)
	}
	if st.Entries[11].Description != "TARIFA PACOTE SERVIÇOS" || st.Entries[11].AmountCents != -3990 || st.Entries[11].Type != "FEE" {
		t.Errorf("fee entry: %+v", st.Entries[11])
	}
	if len(st.Balances) != 2 || st.Balances[0].Kind != "LEDGERBAL" || st.Balances[0].AmountCents != 847355 {
		t.Errorf("balances: %+v", st.Balances)
	}
	if st.Start.Format("2006-01-02") != "2026-11-16" || st.End.Format("2006-01-02") != "2026-11-24" {
		t.Errorf("period %v - %v", st.Start, st.End)
	}
	cr, db := st.Totals()
	if cr != 595245 || db != -157990 {
		t.Errorf("totals %d %d", cr, db)
	}
}

func TestParseOFX2Sample(t *testing.T) {
	f, err := os.Open("../testdata/extrato-ofx2.ofx")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if st.Format != FormatOFX2 || st.AccountID != "998877-0" || len(st.Entries) != 3 {
		t.Fatalf("statement: %+v", st)
	}
	e := st.Entries[0]
	if e.Counterparty != "Café & Companhia Comércio Ltda" || e.Reference != "E99999999202611170930XYZ00000001" {
		t.Errorf("payee/entity/e2e: %+v", e)
	}
	if e.ValueDate.Format("2006-01-02") != "2026-11-17" {
		t.Errorf("DTAVAIL: %v", e.ValueDate)
	}
	if p := st.Entries[1]; p.Reference != "004512" || p.Description != "Energia Clara Distribuidora" || p.AmountCents != -31247 {
		t.Errorf("checknum/empty memo: %+v", p)
	}
	if x := st.Entries[2]; x.Currency != "USD" || x.Raw["CURRENCY.CURRATE"] != "5.4321" {
		t.Errorf("currency aggregate: %+v", x)
	}
	if len(st.Balances) != 1 || st.Balances[0].AmountCents != 1033753 {
		t.Errorf("balances %+v", st.Balances)
	}
}

func TestOFXCharsets(t *testing.T) {
	trn := "<STMTTRN><TRNTYPE>CREDIT<DTPOSTED>20261121<TRNAMT>1.00<FITID>1<MEMO>%s</STMTTRN>"
	tests := []struct {
		name   string
		header string
		memo   string
		want   string
	}{
		{"1252 declared and used", ofx1Header, "PAGAMENTO S\xc3O JO\xc3O \x96 N\xba 5", "PAGAMENTO SÃO JOÃO – Nº 5"},
		{"1252 declared but body is UTF-8", ofx1Header, "PAGAMENTO SÃO JOÃO", "PAGAMENTO SÃO JOÃO"},
		{"ISO-8859-1", strings.Replace(ofx1Header, "CHARSET:1252", "CHARSET:ISO-8859-1", 1), "A\xc7\xdaCAR", "AÇÚCAR"},
		{"UTF-8 declared, body is 1252", strings.Replace(ofx1Header, "ENCODING:USASCII", "ENCODING:UTF-8", 1), "MAR\xc7O", "MARÇO"},
		{"unknown charset falls back to 1252", strings.Replace(ofx1Header, "CHARSET:1252", "CHARSET:KLINGON", 1), "\x80 5", "€ 5"},
		{"no header", "", "caf\xe9", "café"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := strings.Replace(ofx1(strings.Replace(trn, "%s", tt.memo, 1)), ofx1Header, tt.header, 1)
			st, err := ParseOFX(strings.NewReader(doc))
			if err != nil {
				t.Fatal(err)
			}
			if got := st.Entries[0].Description; got != tt.want {
				t.Errorf("description %q, want %q", got, tt.want)
			}
		})
	}
	xmlDoc := `<?xml version="1.0" encoding="ISO-8859-1"?><?OFX OFXHEADER="200" VERSION="220"?><OFX><STMTTRN><TRNTYPE>CREDIT</TRNTYPE><DTPOSTED>20261121</DTPOSTED><TRNAMT>1.00</TRNAMT><FITID>X</FITID><NAME>Jos` + "\xe9" + `</NAME></STMTTRN></OFX>`
	st, err := Parse(strings.NewReader(xmlDoc))
	if err != nil || st.Format != FormatOFX2 || st.Entries[0].Counterparty != "José" {
		t.Errorf("OFX2 latin1: %+v %v", st, err)
	}
}

func TestOFXQuirks(t *testing.T) {
	// Missing </STMTTRN> between transactions, stray closing tag, comment,
	// self-closing tag, lowercase tags, entity, duplicate leaf.
	doc := ofx1(`<!-- exported by bank --><STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20261121<TRNAMT>25.00<FITID>A<NAME>Loja &lt;X&gt;<NAME>ignored<EXTRA/>
<STMTTRN><TRNTYPE>CREDIT<DTPOSTED>20261122<TRNAMT>10<FITID>B</STRAY>
<stmttrn><trntype>CREDIT<dtposted>20261123<trnamt>-0.01<memo>PIX E99999999202611231200ABCDEFGHIJK</stmttrn>`)
	st, err := ParseWithOptions(strings.NewReader(doc), Options{FixDebitSign: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Entries) != 3 {
		t.Fatalf("entries %d: %+v", len(st.Entries), st.Entries)
	}
	if e := st.Entries[0]; e.AmountCents != -2500 || e.Counterparty != "Loja <X>" {
		t.Errorf("fix debit sign / entity / duplicate leaf: %+v", e)
	}
	if e := st.Entries[1]; e.AmountCents != 1000 || e.ID != "B" {
		t.Errorf("unclosed STMTTRN: %+v", e)
	}
	if e := st.Entries[2]; e.ID != "ofx-3" || e.Reference != "E99999999202611231200ABCDEFGHIJK" || e.AmountCents != -1 {
		t.Errorf("generated id / e2e: %+v", e)
	}
	// Without FixDebitSign the sign is trusted.
	st, _ = ParseOFX(strings.NewReader(doc))
	if st.Entries[0].AmountCents != 2500 {
		t.Errorf("sign changed without option: %d", st.Entries[0].AmountCents)
	}
}

func TestOFXErrors(t *testing.T) {
	tests := []struct {
		name string
		doc  string
		want error
	}{
		{"no body", "OFXHEADER:100\nDATA:OFXSGML\n", ErrSyntax},
		{"no OFX element", "OFXHEADER:100\n\n<FOO>bar", ErrSyntax},
		{"missing amount", ofx1("<STMTTRN><DTPOSTED>20261121<FITID>1</STMTTRN>"), ErrMissing},
		{"bad amount", ofx1("<STMTTRN><DTPOSTED>20261121<TRNAMT>1,2,3.4.5<FITID>1</STMTTRN>"), ErrAmount},
		{"missing date", ofx1("<STMTTRN><TRNAMT>1<FITID>1</STMTTRN>"), ErrMissing},
		{"bad date", ofx1("<STMTTRN><DTPOSTED>2026-11-21<TRNAMT>1<FITID>1</STMTTRN>"), ErrDate},
		{"bad DTSTART", strings.Replace(ofx1(""), "<DTSTART>20261101", "<DTSTART>yesterday", 1), ErrDate},
		{"bad balance", ofx1("") + "", nil},
	}
	tests[len(tests)-1].doc = strings.Replace(ofx1(""), "</BANKTRANLIST>", "</BANKTRANLIST><LEDGERBAL><BALAMT>x<DTASOF>20261130</LEDGERBAL>", 1)
	tests[len(tests)-1].want = ErrAmount
	for _, tt := range tests {
		_, err := ParseOFX(strings.NewReader(tt.doc))
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: err = %v, want %v", tt.name, err, tt.want)
		}
		var pe *ParseError
		if err != nil && !errors.As(err, &pe) {
			t.Errorf("%s: not a *ParseError: %T", tt.name, err)
		}
	}
	_, err := ParseOFX(strings.NewReader(ofx1("<STMTTRN><DTPOSTED>20261121<TRNAMT>abc<FITID>1</STMTTRN>")))
	if err == nil || err.Error() != `statement: ofx1 entry 1: TRNAMT "abc": invalid amount: invalid amount` {
		t.Errorf("message: %v", err)
	}
}

func TestOFXUnterminated(t *testing.T) {
	for _, doc := range []string{
		ofx1Header + "<OFX><STMTTRN><DTPOSTED>20261121<TRNAMT>1<FITID>1",
		ofx1Header + "<OFX><!-- never closed",
		ofx1Header + "<OFX><STMTTRN",
		ofx1Header + "<OFX><><STMTTRN><DTPOSTED>20261121<TRNAMT>1</>",
	} {
		if _, err := ParseOFX(strings.NewReader(doc)); err != nil {
			t.Errorf("%q: %v", doc, err)
		}
	}
	if got := xmlDeclEncoding([]byte(`<?xml version="1.0" encoding='windows-1252'?>`)); got != "windows-1252" {
		t.Errorf("xmlDeclEncoding = %q", got)
	}
	for _, d := range []string{`<?xml version="1.0"?>`, `<?xml encoding=x?>`, `<?xml encoding="x`, `<OFX>`, `<?xml encoding="x"`} {
		if got := xmlDeclEncoding([]byte(d)); got != "" {
			t.Errorf("xmlDeclEncoding(%q) = %q", d, got)
		}
	}
}
