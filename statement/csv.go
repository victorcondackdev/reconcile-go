package statement

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/victorcondackdev/reconcile-go/internal/decimal"
	"github.com/victorcondackdev/reconcile-go/internal/tabular"
	"github.com/victorcondackdev/reconcile-go/internal/textnorm"
)

// CSVMapping declares how to read a delimited statement. It is usually
// loaded from JSON:
//
//	{
//	  "delimiter": ";",
//	  "decimal_separator": ",",
//	  "thousands_separator": ".",
//	  "date_format": "dd/mm/yyyy",
//	  "columns": {"booking_date": "Data", "description": "Histórico",
//	              "id": "Documento", "amount": "Valor"},
//	  "skip_descriptions": ["SALDO ANTERIOR", "SALDO DO DIA"]
//	}
//
// Column names are matched ignoring case, accents and punctuation; "#3"
// selects the third column by position.
type CSVMapping struct {
	// Delimiter is ";", ",", "|", "tab" or empty to sniff from the header.
	Delimiter string `json:"delimiter,omitempty"`
	// Encoding is "utf-8", "windows-1252", "iso-8859-1" or empty to accept
	// UTF-8 and fall back to Windows-1252 for invalid input. A UTF-8 BOM is
	// always skipped.
	Encoding string `json:"encoding,omitempty"`
	// SkipRows drops banner lines before the header.
	SkipRows int `json:"skip_rows,omitempty"`
	// NoHeader means the first row is data; columns must then use "#n".
	NoHeader bool `json:"no_header,omitempty"`
	// DecimalSeparator and ThousandsSeparator default to auto-detection
	// per value. Set them ("," and ".") for pt-BR files to reject
	// ambiguous values like "1.234".
	DecimalSeparator   string `json:"decimal_separator,omitempty"`
	ThousandsSeparator string `json:"thousands_separator,omitempty"`
	// DateFormat uses yyyy, yy, mm, dd, HH, MM, SS tokens or a Go layout.
	// Empty tries common layouts, day-first.
	DateFormat string `json:"date_format,omitempty"`
	// Currency applies when there is no currency column. Default "BRL".
	Currency string     `json:"currency,omitempty"`
	Columns  CSVColumns `json:"columns"`
	// DebitValues lists values of Columns.Direction that mark a debit, e.g.
	// ["D", "DEBITO"]. Default: D, DB, DEBITO, DEBIT, SAIDA.
	DebitValues []string `json:"debit_values,omitempty"`
	// Negate flips every sign (credit card exports list purchases as
	// positive amounts).
	Negate bool `json:"negate,omitempty"`
	// SkipDescriptions drops rows whose description contains one of these
	// (folded) strings: balance lines such as "SALDO ANTERIOR".
	SkipDescriptions []string `json:"skip_descriptions,omitempty"`
}

// CSVColumns names the source column of each Entry field. Either Amount, or
// Credit and/or Debit, must be set; BookingDate is required.
type CSVColumns struct {
	ID           string `json:"id,omitempty"`
	BookingDate  string `json:"booking_date,omitempty"`
	ValueDate    string `json:"value_date,omitempty"`
	Amount       string `json:"amount,omitempty"`
	Credit       string `json:"credit,omitempty"`
	Debit        string `json:"debit,omitempty"`
	Direction    string `json:"direction,omitempty"`
	Currency     string `json:"currency,omitempty"`
	Description  string `json:"description,omitempty"`
	Counterparty string `json:"counterparty,omitempty"`
	Reference    string `json:"reference,omitempty"`
}

// LoadCSVMapping decodes a CSVMapping from JSON, rejecting unknown fields.
func LoadCSVMapping(r io.Reader) (*CSVMapping, error) {
	var m CSVMapping
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("statement: csv mapping: %w", err)
	}
	return &m, nil
}

// ParseCSV reads a delimited statement described by m. Dates are
// interpreted in UTC.
func ParseCSV(r io.Reader, m CSVMapping) (*Statement, error) {
	return ParseWithOptions(r, Options{Format: FormatCSV, CSV: &m})
}

// GuessCSVMapping builds a mapping from common Portuguese and English
// header names (Data, Histórico, Valor, Crédito, Débito, Documento, ...).
// It reports false when no date or amount column can be identified.
func GuessCSVMapping(header []string) (CSVMapping, bool) {
	name := func(aliases ...string) string {
		if i := tabular.Guess(header, aliases...); i >= 0 {
			return header[i]
		}
		return ""
	}
	c := CSVColumns{
		ID:           name("id", "fitid", "documento", "nr documento", "numero documento", "n documento", "identificador", "transaction id", "id transacao"),
		BookingDate:  name("data", "data lancamento", "data do lancamento", "data movimento", "dt lancamento", "booking date", "date", "dt"),
		ValueDate:    name("data valor", "data credito", "value date"),
		Amount:       name("valor", "valor r$", "valor (r$)", "amount", "value", "montante", "valor lancamento"),
		Credit:       name("credito", "creditos", "entrada", "entradas", "credit"),
		Debit:        name("debito", "debitos", "saida", "saidas", "debit"),
		Direction:    name("d/c", "c/d", "dc", "natureza", "tipo lancamento", "credit/debit"),
		Currency:     name("moeda", "currency"),
		Description:  name("historico", "descricao", "description", "lancamento", "memo", "detalhe", "detalhes"),
		Counterparty: name("favorecido", "pagador", "contraparte", "counterparty", "nome", "beneficiario", "remetente", "payee"),
		Reference:    name("referencia", "reference", "e2e", "endtoendid", "end to end id", "id pix", "nosso numero"),
	}
	m := CSVMapping{Columns: c}
	ok := c.BookingDate != "" && (c.Amount != "" || c.Credit != "" || c.Debit != "")
	return m, ok
}

func guessFromData(data []byte) (*CSVMapping, error) {
	t, err := tabular.Read(bytes.NewReader(data), "", "", 0, true)
	if err != nil {
		return nil, &ParseError{Format: FormatCSV, Err: fmt.Errorf("%w: %v", ErrSyntax, err)}
	}
	m, ok := GuessCSVMapping(t.Header)
	if !ok {
		return nil, &ParseError{Format: FormatCSV, Err: fmt.Errorf("%w: cannot infer date and amount columns from header %q; provide a CSVMapping", ErrMissing, t.Header)}
	}
	return &m, nil
}

var defaultDebitValues = []string{"D", "DB", "DEBITO", "DEBIT", "SAIDA", "-"}

func sepByte(s string) (byte, error) {
	switch len(s) {
	case 0:
		return 0, nil
	case 1:
		return s[0], nil
	}
	return 0, fmt.Errorf("separator %q must be a single character", s)
}

func parseCSV(data []byte, m CSVMapping, loc *time.Location) (*Statement, error) {
	fail := func(line int, field, value string, err error) error {
		return &ParseError{Format: FormatCSV, Index: line, Field: field, Value: value, Err: err}
	}
	dec, err := sepByte(m.DecimalSeparator)
	if err != nil {
		return nil, fail(0, "decimal_separator", "", err)
	}
	th, err := sepByte(m.ThousandsSeparator)
	if err != nil {
		return nil, fail(0, "thousands_separator", "", err)
	}
	t, err := tabular.Read(bytes.NewReader(data), m.Delimiter, m.Encoding, m.SkipRows, !m.NoHeader)
	if err != nil {
		return nil, fail(0, "", "", fmt.Errorf("%w: %v", ErrSyntax, err))
	}
	col := func(field, spec string) (int, error) {
		i, err := tabular.Column(t.Header, spec)
		if err != nil {
			return -1, fail(0, "columns."+field, spec, fmt.Errorf("%w: %v", ErrMissing, err))
		}
		return i, nil
	}
	c := m.Columns
	var idx [11]int
	specs := [11][2]string{
		{"id", c.ID}, {"booking_date", c.BookingDate}, {"value_date", c.ValueDate},
		{"amount", c.Amount}, {"credit", c.Credit}, {"debit", c.Debit},
		{"direction", c.Direction}, {"currency", c.Currency},
		{"description", c.Description}, {"counterparty", c.Counterparty},
		{"reference", c.Reference},
	}
	for i, s := range specs {
		if idx[i], err = col(s[0], s[1]); err != nil {
			return nil, err
		}
	}
	iID, iDate, iVal, iAmt, iCr, iDb, iDir, iCur, iDesc, iCp, iRef := idx[0], idx[1], idx[2], idx[3], idx[4], idx[5], idx[6], idx[7], idx[8], idx[9], idx[10]
	if iDate < 0 {
		return nil, fail(0, "columns.booking_date", "", ErrMissing)
	}
	if iAmt < 0 && iCr < 0 && iDb < 0 {
		return nil, fail(0, "columns.amount", "", ErrMissing)
	}
	debitVals := m.DebitValues
	if len(debitVals) == 0 {
		debitVals = defaultDebitValues
	}
	isDebit := map[string]bool{}
	for _, v := range debitVals {
		if k := textnorm.Key(v); k != "" {
			isDebit[k] = true
		}
		if v == "-" {
			isDebit["-"] = true
		}
	}
	var skip []string
	for _, s := range m.SkipDescriptions {
		if k := textnorm.Fold(strings.TrimSpace(s)); k != "" {
			skip = append(skip, k)
		}
	}
	cur := strings.ToUpper(m.Currency)
	if cur == "" {
		cur = "BRL"
	}
	opts := decimal.Options{Decimal: dec, Thousands: th, Lenient: true}
	st := &Statement{Format: FormatCSV, Currency: cur}
	for ri, rec := range t.Rows {
		line := t.Lines[ri]
		get := func(i int) string { return tabular.Field(rec, i) }
		desc := get(iDesc)
		if len(skip) > 0 {
			fd := textnorm.Fold(desc)
			skipped := false
			for _, s := range skip {
				if strings.Contains(fd, s) {
					skipped = true
					break
				}
			}
			if skipped {
				continue
			}
		}
		dateS := get(iDate)
		var amt int64
		switch {
		case iAmt >= 0:
			s := get(iAmt)
			if s == "" {
				return nil, fail(line, "amount", "", ErrMissing)
			}
			if amt, err = decimal.Parse(s, opts); err != nil {
				return nil, fail(line, "amount", s, fmt.Errorf("%w: %v", ErrAmount, err))
			}
			if iDir >= 0 {
				d := get(iDir)
				if isDebit[textnorm.Key(d)] || (d == "-" && isDebit["-"]) {
					amt = -abs(amt)
				}
			}
		default:
			cs, ds := get(iCr), get(iDb)
			if cs == "" && ds == "" {
				return nil, fail(line, "credit/debit", "", ErrMissing)
			}
			if cs != "" {
				v, err := decimal.Parse(cs, opts)
				if err != nil {
					return nil, fail(line, "credit", cs, fmt.Errorf("%w: %v", ErrAmount, err))
				}
				amt += abs(v)
			}
			if ds != "" {
				v, err := decimal.Parse(ds, opts)
				if err != nil {
					return nil, fail(line, "debit", ds, fmt.Errorf("%w: %v", ErrAmount, err))
				}
				amt -= abs(v)
			}
		}
		if m.Negate {
			amt = -amt
		}
		if dateS == "" {
			return nil, fail(line, "booking_date", "", ErrMissing)
		}
		bd, err := tabular.ParseDate(dateS, m.DateFormat, loc)
		if err != nil {
			return nil, fail(line, "booking_date", dateS, fmt.Errorf("%w: %v", ErrDate, err))
		}
		vd := bd
		if s := get(iVal); s != "" {
			if vd, err = tabular.ParseDate(s, m.DateFormat, loc); err != nil {
				return nil, fail(line, "value_date", s, fmt.Errorf("%w: %v", ErrDate, err))
			}
		}
		id := get(iID)
		if id == "" {
			id = "row-" + strconv.Itoa(line)
		}
		ecur := cur
		if s := get(iCur); s != "" {
			ecur = strings.ToUpper(s)
		}
		cp := get(iCp)
		ref := get(iRef)
		if ref == "" {
			ref = FindEndToEndID(desc + " " + cp)
		}
		raw := make(map[string]string, len(rec))
		for i, v := range rec {
			k := "#" + strconv.Itoa(i+1)
			if i < len(t.Header) && t.Header[i] != "" {
				k = t.Header[i]
			}
			raw[k] = v
		}
		st.Entries = append(st.Entries, Entry{
			ID: id, BookingDate: bd, ValueDate: vd, AmountCents: amt,
			Currency: ecur, Description: strings.Join(strings.Fields(desc), " "),
			Counterparty: cp, Reference: ref, Raw: raw,
		})
		if st.Start.IsZero() || bd.Before(st.Start) {
			st.Start = bd
		}
		if bd.After(st.End) {
			st.End = bd
		}
	}
	return st, nil
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
