// Package statement parses bank and PSP statements into a common,
// normalized representation.
//
// Supported inputs are OFX 1.x (SGML) and 2.x (XML), ISO 20022 camt.052,
// camt.053 and camt.054, and delimited text files described by a
// declarative CSVMapping. Parse sniffs the format from the content, so
// callers do not have to trust file extensions.
//
// Amounts are signed int64 cents: credits to the account holder are
// positive, debits negative. Dates keep the offset found in the file; the
// matching engine compares them by calendar day in that offset.
package statement

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Format identifies a statement format.
type Format string

// Formats recognized by Detect.
const (
	FormatUnknown Format = ""
	FormatOFX1    Format = "ofx1"
	FormatOFX2    Format = "ofx2"
	FormatCSV     Format = "csv"
	FormatCAMT052 Format = "camt.052"
	FormatCAMT053 Format = "camt.053"
	FormatCAMT054 Format = "camt.054"
)

// MaxInputSize bounds the bytes read by Parse.
const MaxInputSize = 256 << 20

// maxInput is MaxInputSize, lowered by tests.
var maxInput int64 = MaxInputSize

// Sentinel errors. Parse errors wrap one of them and can be tested with
// errors.Is.
var (
	ErrUnknownFormat = errors.New("statement: unknown format")
	ErrTooLarge      = errors.New("statement: input too large")
	ErrSyntax        = errors.New("syntax error")
	ErrAmount        = errors.New("invalid amount")
	ErrDate          = errors.New("invalid date")
	ErrMissing       = errors.New("missing required field")
)

// Entry is one booked movement on a statement.
type Entry struct {
	// ID identifies the entry within its source: OFX FITID, camt
	// AcctSvcrRef/NtryRef, or a CSV column. Generated ("ofx-3", "row-12")
	// when the source has none.
	ID string `json:"id"`
	// BookingDate is when the bank booked the movement.
	BookingDate time.Time `json:"booking_date"`
	// ValueDate is when the funds became available; equal to BookingDate
	// when the source does not distinguish them.
	ValueDate time.Time `json:"value_date"`
	// AmountCents is signed: positive credits, negative debits.
	AmountCents int64  `json:"amount_cents"`
	Currency    string `json:"currency"`
	// Description is the free text the bank attached (OFX NAME/MEMO, camt
	// AddtlNtryInf/Ustrd, CSV "histórico").
	Description string `json:"description,omitempty"`
	// Counterparty is the payer of a credit or the payee of a debit, when
	// the source states it separately.
	Counterparty string `json:"counterparty,omitempty"`
	// Reference is the strongest identifier available for matching: the
	// Pix EndToEndId, an ISO 20022 EndToEndId or creditor reference, a
	// boleto "nosso número" or check number.
	Reference string `json:"reference,omitempty"`
	// Type is the source's transaction type (OFX TRNTYPE, camt bank
	// transaction code), informational only.
	Type string `json:"type,omitempty"`
	// Raw holds the source fields of the entry, keyed by tag or column.
	Raw map[string]string `json:"raw,omitempty"`
}

// IsCredit reports whether the entry increases the account balance.
func (e Entry) IsCredit() bool { return e.AmountCents > 0 }

// Balance is a balance reported by the statement (OFX LEDGERBAL/AVAILBAL,
// camt Bal).
type Balance struct {
	Kind        string    `json:"kind"`
	AmountCents int64     `json:"amount_cents"`
	Currency    string    `json:"currency,omitempty"`
	Date        time.Time `json:"date"`
}

// Statement is a parsed statement.
type Statement struct {
	Format    Format    `json:"format"`
	BankID    string    `json:"bank_id,omitempty"`
	AccountID string    `json:"account_id,omitempty"`
	Currency  string    `json:"currency,omitempty"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	Balances  []Balance `json:"balances,omitempty"`
	Entries   []Entry   `json:"entries"`
}

// Totals returns the sum of credits and of debits (the latter negative).
func (s *Statement) Totals() (credits, debits int64) {
	for _, e := range s.Entries {
		if e.AmountCents > 0 {
			credits += e.AmountCents
		} else {
			debits += e.AmountCents
		}
	}
	return credits, debits
}

// ParseError describes a problem in a specific entry or line.
type ParseError struct {
	Format Format
	// Index is the 1-based entry number (OFX, camt) or line (CSV).
	Index int
	Field string
	Value string
	Err   error
}

func (e *ParseError) Error() string {
	var b strings.Builder
	b.WriteString("statement: ")
	b.WriteString(string(e.Format))
	if e.Index > 0 {
		unit := "entry"
		if e.Format == FormatCSV {
			unit = "line"
		}
		fmt.Fprintf(&b, " %s %d", unit, e.Index)
	}
	if e.Field != "" {
		fmt.Fprintf(&b, ": %s", e.Field)
	}
	if e.Value != "" {
		v := e.Value
		if len(v) > 40 {
			v = v[:40] + "..."
		}
		fmt.Fprintf(&b, " %q", v)
	}
	fmt.Fprintf(&b, ": %v", e.Err)
	return b.String()
}

func (e *ParseError) Unwrap() error { return e.Err }

// Options controls ParseWithOptions.
type Options struct {
	// Format forces a format instead of sniffing it.
	Format Format
	// CSV describes delimited input. When nil, CSV columns are guessed
	// from the header with GuessCSVMapping.
	CSV *CSVMapping
	// Location is used for CSV dates, which carry no offset. Defaults to
	// UTC; only the calendar day matters for matching.
	Location *time.Location
	// FixDebitSign negates positive OFX amounts whose TRNTYPE is a debit
	// (DEBIT, PAYMENT, FEE, ...). Some banks export unsigned TRNAMT.
	FixDebitSign bool
	// SplitBatches emits one entry per camt TxDtls when an Ntry carries
	// several transactions with their own amounts, instead of one entry
	// per Ntry.
	SplitBatches bool
}

// Parse reads a statement, detecting its format from the content.
func Parse(r io.Reader) (*Statement, error) { return ParseWithOptions(r, Options{}) }

// ParseWithOptions reads a statement with explicit options.
func ParseWithOptions(r io.Reader, opts Options) (*Statement, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxInput+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxInput {
		return nil, ErrTooLarge
	}
	f := opts.Format
	if f == FormatUnknown {
		f = Detect(data)
		if f == FormatUnknown && opts.CSV != nil {
			f = FormatCSV
		}
	}
	switch f {
	case FormatOFX1, FormatOFX2, "ofx":
		return parseOFX(data, opts)
	case FormatCAMT052, FormatCAMT053, FormatCAMT054, "camt":
		return parseCAMT(data, opts)
	case FormatCSV:
		m := opts.CSV
		if m == nil {
			g, err := guessFromData(data)
			if err != nil {
				return nil, err
			}
			m = g
		}
		return parseCSV(data, *m, opts.Location)
	}
	return nil, ErrUnknownFormat
}

// Detect sniffs the format of a statement from its first bytes.
func Detect(data []byte) Format {
	head := data
	if len(head) > 8192 {
		head = head[:8192]
	}
	head = bytes.TrimPrefix(head, []byte("\xef\xbb\xbf"))
	up := bytes.ToUpper(head)
	trimmed := bytes.TrimSpace(head)
	switch {
	case bytes.Contains(up, []byte("OFXHEADER:")), bytes.Contains(up, []byte("<OFX>")) && !bytes.HasPrefix(trimmed, []byte("<?xml")):
		if bytes.Contains(up, []byte("OFXHEADER=")) {
			return FormatOFX2
		}
		return FormatOFX1
	case bytes.Contains(up, []byte("OFXHEADER=")), bytes.Contains(up, []byte("<OFX>")):
		return FormatOFX2
	case bytes.Contains(head, []byte("BkToCstmrStmt")):
		return FormatCAMT053
	case bytes.Contains(head, []byte("BkToCstmrDbtCdtNtfctn")):
		return FormatCAMT054
	case bytes.Contains(head, []byte("BkToCstmrAcctRpt")):
		return FormatCAMT052
	case bytes.Contains(head, []byte("camt.053")):
		return FormatCAMT053
	case bytes.Contains(head, []byte("camt.054")):
		return FormatCAMT054
	case bytes.Contains(head, []byte("camt.052")):
		return FormatCAMT052
	}
	if len(trimmed) == 0 || trimmed[0] == '<' || bytes.IndexByte(head, 0) >= 0 {
		return FormatUnknown
	}
	// Delimited text: one of the first lines (banners are common) has a
	// field separator.
	for i, line := range bytes.SplitN(trimmed, []byte("\n"), 6) {
		if i < 5 && bytes.ContainsAny(line, ";,\t|") {
			return FormatCSV
		}
	}
	return FormatUnknown
}

// FindEndToEndID returns the first Pix EndToEndId found in s, or "". A Pix
// EndToEndId has 32 characters: 'E' (or 'D' for returns), the 8-digit ISPB
// of the payer's institution, a 12-digit yyyyMMddHHmm timestamp and 11
// alphanumeric characters.
func FindEndToEndID(s string) string {
	for i := 0; i+32 <= len(s); i++ {
		c := s[i]
		if c != 'E' && c != 'D' {
			continue
		}
		if i > 0 && isAlnum(s[i-1]) {
			continue
		}
		if i+32 < len(s) && isAlnum(s[i+32]) {
			continue
		}
		cand := s[i : i+32]
		if isEndToEndID(cand) {
			return cand
		}
	}
	return ""
}

func isEndToEndID(s string) bool {
	if len(s) != 32 || (s[0] != 'E' && s[0] != 'D') {
		return false
	}
	for i := 1; i < 21; i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	// month and day sanity check on the timestamp
	mm := (s[13]-'0')*10 + s[14] - '0'
	dd := (s[15]-'0')*10 + s[16] - '0'
	if mm < 1 || mm > 12 || dd < 1 || dd > 31 {
		return false
	}
	for i := 21; i < 32; i++ {
		if !isAlnum(s[i]) {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z'
}

func joinNonEmpty(sep string, parts ...string) string {
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.Join(strings.Fields(p), " ")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return strings.Join(out, sep)
}
