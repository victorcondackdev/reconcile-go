package match

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
)

// ExpectedMapping declares how to read expected records from CSV. Column
// names are matched ignoring case, accents and punctuation; "#n" selects a
// column by 1-based position.
type ExpectedMapping struct {
	Delimiter          string          `json:"delimiter,omitempty"`
	Encoding           string          `json:"encoding,omitempty"`
	SkipRows           int             `json:"skip_rows,omitempty"`
	NoHeader           bool            `json:"no_header,omitempty"`
	DecimalSeparator   string          `json:"decimal_separator,omitempty"`
	ThousandsSeparator string          `json:"thousands_separator,omitempty"`
	DateFormat         string          `json:"date_format,omitempty"`
	Columns            ExpectedColumns `json:"columns"`
	// TagSeparator splits the tags column. Default "|".
	TagSeparator string `json:"tag_separator,omitempty"`
	// Negate flips every sign, for ledgers that store payables as
	// positive amounts.
	Negate bool `json:"negate,omitempty"`
}

// ExpectedColumns names the source column of each Expected field. ID and
// Amount are required.
type ExpectedColumns struct {
	ID           string `json:"id,omitempty"`
	Amount       string `json:"amount,omitempty"`
	Date         string `json:"date,omitempty"`
	Reference    string `json:"reference,omitempty"`
	Counterparty string `json:"counterparty,omitempty"`
	Description  string `json:"description,omitempty"`
	Tags         string `json:"tags,omitempty"`
}

// GuessExpectedMapping maps common header names (id, pedido, valor,
// amount, data, vencimento, referencia, cliente, tags, ...). It reports
// false when no ID or amount column is found.
func GuessExpectedMapping(header []string) (ExpectedMapping, bool) {
	name := func(aliases ...string) string {
		if i := tabular.Guess(header, aliases...); i >= 0 {
			return header[i]
		}
		return ""
	}
	c := ExpectedColumns{
		ID:           name("id", "pedido", "order id", "order", "numero pedido", "documento", "invoice", "fatura", "titulo"),
		Amount:       name("valor", "amount", "valor r$", "total", "value", "montante"),
		Date:         name("data", "date", "vencimento", "data pedido", "data vencimento", "due date", "order date"),
		Reference:    name("referencia", "reference", "e2e", "endtoendid", "nosso numero", "txid", "ref"),
		Counterparty: name("cliente", "customer", "counterparty", "pagador", "nome", "fornecedor", "supplier", "contraparte", "sacado"),
		Description:  name("descricao", "description", "historico", "memo", "observacao"),
		Tags:         name("tags", "tag", "canal", "channel", "meio pagamento", "payment method"),
	}
	return ExpectedMapping{Columns: c}, c.ID != "" && c.Amount != ""
}

// ReadExpectedCSV reads expected records described by m. When m is nil the
// mapping is guessed from the header.
func ReadExpectedCSV(r io.Reader, m *ExpectedMapping) ([]Expected, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var mm ExpectedMapping
	if m != nil {
		mm = *m
	}
	t, err := tabular.Read(bytes.NewReader(data), mm.Delimiter, mm.Encoding, mm.SkipRows, !mm.NoHeader)
	if err != nil {
		return nil, fmt.Errorf("match: expected csv: %w", err)
	}
	if m == nil {
		g, ok := GuessExpectedMapping(t.Header)
		if !ok {
			return nil, fmt.Errorf("match: expected csv: cannot infer id and amount columns from header %q", t.Header)
		}
		mm.Columns = g.Columns
	}
	fail := func(line int, field, value string, err error) error {
		if line > 0 {
			return fmt.Errorf("match: expected csv line %d: %s %q: %w", line, field, value, err)
		}
		return fmt.Errorf("match: expected csv: %s %q: %w", field, value, err)
	}
	c := mm.Columns
	specs := []struct{ name, spec string }{
		{"id", c.ID}, {"amount", c.Amount}, {"date", c.Date}, {"reference", c.Reference},
		{"counterparty", c.Counterparty}, {"description", c.Description}, {"tags", c.Tags},
	}
	idx := make([]int, len(specs))
	for i, s := range specs {
		if idx[i], err = tabular.Column(t.Header, s.spec); err != nil {
			return nil, fail(0, "columns."+s.name, s.spec, err)
		}
	}
	iID, iAmt, iDate, iRef, iCp, iDesc, iTags := idx[0], idx[1], idx[2], idx[3], idx[4], idx[5], idx[6]
	if iID < 0 || iAmt < 0 {
		return nil, fail(0, "columns", "id/amount", fmt.Errorf("id and amount columns are required"))
	}
	var dec, th byte
	if len(mm.DecimalSeparator) == 1 {
		dec = mm.DecimalSeparator[0]
	}
	if len(mm.ThousandsSeparator) == 1 {
		th = mm.ThousandsSeparator[0]
	}
	tagSep := mm.TagSeparator
	if tagSep == "" {
		tagSep = "|"
	}
	out := make([]Expected, 0, len(t.Rows))
	for ri, rec := range t.Rows {
		line := t.Lines[ri]
		get := func(i int) string { return tabular.Field(rec, i) }
		x := Expected{ID: get(iID), Reference: get(iRef), Counterparty: get(iCp), Description: get(iDesc)}
		if x.ID == "" {
			x.ID = "line-" + strconv.Itoa(line)
		}
		as := get(iAmt)
		amt, err := decimal.Parse(as, decimal.Options{Decimal: dec, Thousands: th, Lenient: true})
		if err != nil {
			return nil, fail(line, "amount", as, err)
		}
		if mm.Negate {
			amt = -amt
		}
		x.AmountCents = amt
		if ds := get(iDate); ds != "" {
			d, err := tabular.ParseDate(ds, mm.DateFormat, time.UTC)
			if err != nil {
				return nil, fail(line, "date", ds, err)
			}
			x.Date = d
		}
		if ts := get(iTags); ts != "" {
			for _, tg := range strings.Split(ts, tagSep) {
				if tg = strings.TrimSpace(tg); tg != "" {
					x.Tags = append(x.Tags, tg)
				}
			}
		}
		out = append(out, x)
	}
	return out, nil
}

// ReadExpectedJSON reads a JSON array of Expected records.
func ReadExpectedJSON(r io.Reader) ([]Expected, error) {
	var out []Expected
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("match: expected json: %w", err)
	}
	return out, nil
}
