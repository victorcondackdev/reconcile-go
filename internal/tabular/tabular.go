// Package tabular holds the CSV plumbing shared by the statement and
// expected-record readers: BOM and charset handling, delimiter sniffing,
// column resolution and date layouts.
package tabular

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/victorcondackdev/reconcile-go/internal/charset"
	"github.com/victorcondackdev/reconcile-go/internal/textnorm"
)

// ErrNoColumn is returned when a configured column is not in the header.
var ErrNoColumn = errors.New("column not found")

// Table is a decoded CSV file.
type Table struct {
	Header []string
	Rows   [][]string
	// Lines holds the 1-based physical line number of each row.
	Lines []int
}

// Read decodes a CSV document. delimiter may be empty (sniffed), a single
// character, or "tab". encoding may be empty (UTF-8, falling back to
// Windows-1252 when the input is not valid UTF-8). skip drops leading lines
// before the header (bank exports often start with account banners).
func Read(r io.Reader, delimiter, encoding string, skip int, header bool) (*Table, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	cs, err := charset.Lookup(encoding)
	if err != nil {
		return nil, fmt.Errorf("encoding %q: %w", encoding, err)
	}
	if encoding == "" {
		cs = charset.Sniff(b, charset.UTF8)
	}
	b = charset.Decode(b, cs)
	line := 1
	for i := 0; i < skip; i++ {
		j := bytes.IndexByte(b, '\n')
		if j < 0 {
			b = nil
			break
		}
		b = b[j+1:]
		line++
	}
	d, err := Delimiter(delimiter, b)
	if err != nil {
		return nil, err
	}
	cr := csv.NewReader(bytes.NewReader(b))
	cr.Comma = d
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.ReuseRecord = false
	t := &Table{}
	first := true
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("csv: %w", err)
		}
		if blank(rec) {
			continue
		}
		for i := range rec {
			rec[i] = strings.TrimSpace(rec[i])
		}
		if first && header {
			t.Header = rec
			first = false
			continue
		}
		first = false
		ln, _ := cr.FieldPos(0)
		t.Rows = append(t.Rows, rec)
		t.Lines = append(t.Lines, ln+line-1)
	}
	return t, nil
}

func blank(rec []string) bool {
	for _, f := range rec {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}

// Delimiter resolves a configured delimiter or sniffs one from the first
// line of b, choosing among ';', ',', tab and '|' the one that occurs most
// often outside quotes.
func Delimiter(spec string, b []byte) (rune, error) {
	switch strings.ToLower(spec) {
	case "":
	case "tab", "\\t", "\t":
		return '\t', nil
	default:
		rs := []rune(spec)
		if len(rs) != 1 || rs[0] == '"' || rs[0] == '\r' || rs[0] == '\n' {
			return 0, fmt.Errorf("invalid delimiter %q", spec)
		}
		return rs[0], nil
	}
	end := bytes.IndexByte(b, '\n')
	if end < 0 {
		end = len(b)
	}
	counts := map[byte]int{}
	inQuote := false
	for _, c := range b[:end] {
		if c == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote {
			counts[c]++
		}
	}
	best, n := byte(','), 0
	for _, c := range []byte{';', ',', '\t', '|'} {
		if counts[c] > n {
			best, n = c, counts[c]
		}
	}
	return rune(best), nil
}

// Column resolves spec against header. spec is either a header name
// (compared after accent and case folding, ignoring punctuation) or "#n"
// for the 1-based column n. An empty spec returns -1 and no error.
func Column(header []string, spec string) (int, error) {
	if spec == "" {
		return -1, nil
	}
	if strings.HasPrefix(spec, "#") {
		n, err := strconv.Atoi(spec[1:])
		if err != nil || n < 1 {
			return -1, fmt.Errorf("invalid column index %q", spec)
		}
		return n - 1, nil
	}
	k := textnorm.Key(spec)
	for i, h := range header {
		if textnorm.Key(h) == k {
			return i, nil
		}
	}
	return -1, fmt.Errorf("%w: %q", ErrNoColumn, spec)
}

// Guess returns the index of the first header that matches one of the
// aliases (folded keys), or -1.
func Guess(header []string, aliases ...string) int {
	for _, a := range aliases {
		k := textnorm.Key(a)
		for i, h := range header {
			if textnorm.Key(h) == k {
				return i
			}
		}
	}
	return -1
}

// Field returns rec[i] or "" when i is out of range.
func Field(rec []string, i int) string {
	if i < 0 || i >= len(rec) {
		return ""
	}
	return rec[i]
}

// Layout converts a friendly date pattern to a Go time layout. Supported
// tokens: yyyy, yy, mm (month), dd, HH, MM (minutes), SS. A pattern that
// already contains "2006" is returned unchanged.
func Layout(p string) string {
	if p == "" || strings.Contains(p, "2006") {
		return p
	}
	r := strings.NewReplacer("yyyy", "2006", "yy", "06", "mm", "01", "dd", "02", "HH", "15", "MM", "04", "SS", "05")
	return r.Replace(p)
}

// defaultLayouts are tried in order when no date format is configured.
var defaultLayouts = []string{
	"02/01/2006", "2006-01-02", "02/01/2006 15:04:05", "02/01/2006 15:04",
	"2006-01-02 15:04:05", "2006-01-02T15:04:05", time.RFC3339,
	"02-01-2006", "02.01.2006", "2006/01/02", "02/01/06", "20060102",
}

// ParseDate parses s with layout (see Layout) in loc, or with a list of
// common layouts when layout is empty. Day-first layouts are tried before
// month-first ones, matching Brazilian usage.
func ParseDate(s, layout string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if loc == nil {
		loc = time.UTC
	}
	if layout != "" {
		return time.ParseInLocation(Layout(layout), s, loc)
	}
	for _, l := range defaultLayouts {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized date %q", s)
}
