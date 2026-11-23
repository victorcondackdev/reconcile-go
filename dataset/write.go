package dataset

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/victorcondackdev/reconcile-go/internal/decimal"
	"github.com/victorcondackdev/reconcile-go/match"
	"github.com/victorcondackdev/reconcile-go/statement"
)

// StatementMapping describes the CSV written by WriteStatementCSV.
func StatementMapping() statement.CSVMapping {
	return statement.CSVMapping{
		Delimiter: ";", DecimalSeparator: ",", ThousandsSeparator: ".", DateFormat: "dd/mm/yyyy",
		Columns: statement.CSVColumns{ID: "Documento", BookingDate: "Data", Description: "Histórico", Amount: "Valor"},
	}
}

// ExpectedMapping describes the CSV written by WriteExpectedCSV.
func ExpectedMapping() match.ExpectedMapping {
	return match.ExpectedMapping{
		Delimiter: ";", DecimalSeparator: ",", ThousandsSeparator: ".", DateFormat: "dd/mm/yyyy",
		Columns: match.ExpectedColumns{
			ID: "pedido", Amount: "valor", Date: "data", Reference: "referencia",
			Counterparty: "cliente", Description: "descricao", Tags: "tags",
		},
	}
}

// Mapping is the mapping file layout understood by the reconcile CLI.
type Mapping struct {
	Statement *statement.CSVMapping  `json:"statement,omitempty"`
	Expected  *match.ExpectedMapping `json:"expected,omitempty"`
}

// WriteMapping writes the CLI mapping file for the generated CSVs.
func WriteMapping(w io.Writer) error {
	s, x := StatementMapping(), ExpectedMapping()
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Mapping{Statement: &s, Expected: &x})
}

// WriteStatementCSV writes the entries as a pt-BR bank export: UTF-8 with
// BOM, ';' separated, dd/mm/yyyy dates and 1.234,56 amounts.
func (d *Dataset) WriteStatementCSV(w io.Writer) error {
	bw := bufio.NewWriter(w)
	bw.WriteString("\xef\xbb\xbf")
	cw := csv.NewWriter(bw)
	cw.Comma = ';'
	cw.Write([]string{"Data", "Documento", "Histórico", "Valor"})
	for _, e := range d.Entries {
		cw.Write([]string{e.BookingDate.Format("02/01/2006"), e.ID, e.Description, decimal.FormatGrouped(e.AmountCents, ',', '.')})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	return bw.Flush()
}

// WriteExpectedCSV writes the expected records in the layout described by
// ExpectedMapping.
func (d *Dataset) WriteExpectedCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.Write([]string{"pedido", "data", "valor", "referencia", "cliente", "descricao", "tags"})
	for _, x := range d.Expected {
		cw.Write([]string{x.ID, x.Date.Format("02/01/2006"), decimal.FormatGrouped(x.AmountCents, ',', '.'),
			x.Reference, x.Counterparty, x.Description, strings.Join(x.Tags, "|")})
	}
	cw.Flush()
	return cw.Error()
}

// WriteStatementOFX writes the entries as an OFX 1.0.2 SGML bank statement
// declared as CHARSET:1252.
func (d *Dataset) WriteStatementOFX(w io.Writer) error {
	bw := bufio.NewWriter(w)
	p := func(format string, args ...any) { fmt.Fprintf(bw, format, args...) }
	p("OFXHEADER:100\r\nDATA:OFXSGML\r\nVERSION:102\r\nSECURITY:NONE\r\nENCODING:USASCII\r\nCHARSET:1252\r\nCOMPRESSION:NONE\r\nOLDFILEUID:NONE\r\nNEWFILEUID:NONE\r\n\r\n")
	p("<OFX>\r\n<SIGNONMSGSRSV1><SONRS><STATUS><CODE>0<SEVERITY>INFO</STATUS><DTSERVER>20261130120000[-3:BRT]<LANGUAGE>POR</SONRS></SIGNONMSGSRSV1>\r\n")
	p("<BANKMSGSRSV1><STMTTRNRS><TRNUID>1<STATUS><CODE>0<SEVERITY>INFO</STATUS><STMTRS><CURDEF>BRL\r\n")
	p("<BANKACCTFROM><BANKID>0999<BRANCHID>0001<ACCTID>12345-6<ACCTTYPE>CHECKING</BANKACCTFROM>\r\n<BANKTRANLIST>\r\n")
	for _, e := range d.Entries {
		typ := "CREDIT"
		if e.AmountCents < 0 {
			typ = "DEBIT"
		}
		p("<STMTTRN>\r\n<TRNTYPE>%s\r\n<DTPOSTED>%s[-3:BRT]\r\n<TRNAMT>%s\r\n<FITID>%s\r\n<MEMO>%s\r\n</STMTTRN>\r\n",
			typ, e.BookingDate.Format("20060102150405"), decimal.Format(e.AmountCents), e.ID, sgmlEscape(e.Description))
	}
	p("</BANKTRANLIST>\r\n</STMTRS></STMTTRNRS></BANKMSGSRSV1>\r\n</OFX>\r\n")
	return bw.Flush()
}

func sgmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// WriteTruth writes the ground truth and duplicate aliases as JSON.
func (d *Dataset) WriteTruth(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(struct {
		Truth   []Link            `json:"truth"`
		Aliases map[string]string `json:"aliases"`
	}{d.Truth, d.Aliases})
}
