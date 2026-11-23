package statement_test

import (
	"fmt"
	"strings"

	"github.com/victorcondackdev/reconcile-go/statement"
)

func ExampleParse() {
	ofx := `OFXHEADER:100
DATA:OFXSGML
VERSION:102
ENCODING:USASCII
CHARSET:1252

<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS><CURDEF>BRL
<BANKTRANLIST>
<STMTTRN><TRNTYPE>CREDIT<DTPOSTED>20261121120000[-3:BRT]<TRNAMT>150.00<FITID>9001
<MEMO>PIX RECEBIDO MARIA SOUZA E99999999202611211200ABCDEFGHIJK
<STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20261121<TRNAMT>-12,90<FITID>9002<MEMO>TARIFA
</BANKTRANLIST></STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`
	st, err := statement.Parse(strings.NewReader(ofx))
	if err != nil {
		panic(err)
	}
	fmt.Println(st.Format, len(st.Entries), "entries")
	for _, e := range st.Entries {
		fmt.Println(e.ID, e.BookingDate.Format("2006-01-02"), e.AmountCents, e.Reference)
	}
	// Output:
	// ofx1 2 entries
	// 9001 2026-11-21 15000 E99999999202611211200ABCDEFGHIJK
	// 9002 2026-11-21 -1290
}

func ExampleParseCSV() {
	csv := "\xef\xbb\xbfData;Histórico;Valor\n" +
		"21/11/2026;PIX RECEBIDO JOÃO;1.234,56\n" +
		"21/11/2026;SALDO DO DIA;9.999,99\n" +
		"22/11/2026;TARIFA PACOTE;-39,90\n"
	m := statement.CSVMapping{
		DecimalSeparator: ",", ThousandsSeparator: ".", DateFormat: "dd/mm/yyyy",
		Columns:          statement.CSVColumns{BookingDate: "Data", Description: "Histórico", Amount: "Valor"},
		SkipDescriptions: []string{"saldo do dia"},
	}
	st, err := statement.ParseCSV(strings.NewReader(csv), m)
	if err != nil {
		panic(err)
	}
	for _, e := range st.Entries {
		fmt.Printf("%s %s %d %q\n", e.ID, e.BookingDate.Format("2006-01-02"), e.AmountCents, e.Description)
	}
	// Output:
	// row-2 2026-11-21 123456 "PIX RECEBIDO JOÃO"
	// row-4 2026-11-22 -3990 "TARIFA PACOTE"
}

func ExampleParseOFXDate() {
	t, _ := statement.ParseOFXDate("20261121233000[-3:BRT]")
	fmt.Println(t.Format("2006-01-02 15:04 -07:00"), "| UTC:", t.UTC().Format("2006-01-02 15:04"))
	// Output: 2026-11-21 23:30 -03:00 | UTC: 2026-11-22 02:30
}

func ExampleFindEndToEndID() {
	fmt.Println(statement.FindEndToEndID("PIX RECEBIDO E2E:E99999999202611211200ABCDEFGHIJK MARIA"))
	// Output: E99999999202611211200ABCDEFGHIJK
}
