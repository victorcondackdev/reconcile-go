package statement

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		in   string
		want Format
	}{
		{"OFXHEADER:100\nDATA:OFXSGML\n\n<OFX>", FormatOFX1},
		{"\xef\xbb\xbf<OFX><SIGNONMSGSRSV1>", FormatOFX1},
		{`<?xml version="1.0"?><?OFX OFXHEADER="200"?><OFX>`, FormatOFX2},
		{`<?xml version="1.0"?><OFX>`, FormatOFX2},
		{`<Document><BkToCstmrStmt>`, FormatCAMT053},
		{`<Document><BkToCstmrDbtCdtNtfctn>`, FormatCAMT054},
		{`<Document><BkToCstmrAcctRpt>`, FormatCAMT052},
		{`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.053.001.08">`, FormatCAMT053},
		{`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.054.001.08">`, FormatCAMT054},
		{`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:camt.052.001.08">`, FormatCAMT052},
		{"Data;Valor\n01/11/2026;1,00", FormatCSV},
		{"Extrato\nConta 1\nData,Valor\n", FormatCSV},
		{"<html>", FormatUnknown},
		{"", FormatUnknown},
		{"just words", FormatUnknown},
		{"a;b\x00", FormatUnknown},
	}
	for _, tt := range tests {
		if got := Detect([]byte(tt.in)); got != tt.want {
			t.Errorf("Detect(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if _, err := Parse(strings.NewReader("<html></html>")); !errors.Is(err, ErrUnknownFormat) {
		t.Errorf("unknown: %v", err)
	}
	long := strings.Repeat("x", 9000) + "<OFX>"
	if Detect([]byte(long)) != FormatUnknown {
		t.Error("detection should only look at the head")
	}
}

func TestParseReadErrors(t *testing.T) {
	if _, err := Parse(failingReader{}); err == nil || err.Error() != "boom" {
		t.Errorf("read error: %v", err)
	}
	defer func(n int64) { maxInput = n }(maxInput)
	maxInput = 1024
	if _, err := Parse(io.LimitReader(zeros{}, 2048)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	return len(p), nil
}

func TestFindEndToEndID(t *testing.T) {
	tests := map[string]string{
		"PIX E99999999202611211200ABCDEFGHIJK FULANO":   "E99999999202611211200ABCDEFGHIJK",
		"E2E:D12345678202601011200abcdefghijk":          "D12345678202601011200abcdefghijk",
		"XE99999999202611211200ABCDEFGHIJK":             "",
		"E99999999202611211200ABCDEFGHIJKX":             "",
		"E99999999202613211200ABCDEFGHIJK":              "",
		"E99999999202611001200ABCDEFGHIJK":              "",
		"E9999999920261121120-ABCDEFGHIJK":              "",
		"E99999999202611211200ABCDEFGHIJ-":              "",
		"short E123":                                    "",
		"E12 then E99999999202611211200ABCDEFGHIJK end": "E99999999202611211200ABCDEFGHIJK",
	}
	for in, want := range tests {
		if got := FindEndToEndID(in); got != want {
			t.Errorf("FindEndToEndID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseErrorMessage(t *testing.T) {
	e := &ParseError{Format: FormatCSV, Index: 7, Field: "amount", Value: strings.Repeat("9", 50), Err: ErrAmount}
	want := `statement: csv line 7: amount "` + strings.Repeat("9", 40) + `...": invalid amount`
	if e.Error() != want || !errors.Is(e, ErrAmount) {
		t.Errorf("got %q", e.Error())
	}
	e = &ParseError{Format: FormatCAMT053, Err: ErrSyntax}
	if e.Error() != "statement: camt.053: syntax error" {
		t.Errorf("got %q", e.Error())
	}
	if !(Entry{AmountCents: 1}).IsCredit() || (Entry{AmountCents: -1}).IsCredit() {
		t.Error("IsCredit")
	}
}

func TestForcedFormat(t *testing.T) {
	doc := ofx1("<STMTTRN><TRNTYPE>CREDIT<DTPOSTED>20261121<TRNAMT>1<FITID>1</STMTTRN>")
	for _, f := range []Format{"ofx", FormatOFX1, FormatOFX2} {
		if _, err := ParseWithOptions(strings.NewReader(doc), Options{Format: f}); err != nil {
			t.Errorf("forced %s: %v", f, err)
		}
	}
	if _, err := ParseWithOptions(strings.NewReader(doc), Options{Format: "xls"}); !errors.Is(err, ErrUnknownFormat) {
		t.Errorf("forced unknown: %v", err)
	}
	if _, err := ParseWithOptions(strings.NewReader(doc), Options{Format: "camt"}); !errors.Is(err, ErrSyntax) {
		t.Errorf("forced camt on ofx: %v", err)
	}
	// A configured CSV mapping makes undetectable text parse as CSV.
	m := &CSVMapping{NoHeader: true, Delimiter: "|", Columns: CSVColumns{BookingDate: "#1", Amount: "#2"}}
	st, err := ParseWithOptions(strings.NewReader("banner\n"), Options{CSV: m})
	if err == nil {
		t.Errorf("banner as data should fail, got %+v", st)
	}
	if _, err := ParseWithOptions(strings.NewReader("nothing here"), Options{CSV: &CSVMapping{SkipRows: 1, Columns: CSVColumns{BookingDate: "#1", Amount: "#2"}}}); err != nil {
		t.Errorf("empty after skip: %v", err)
	}
	if _, err := ParseWithOptions(strings.NewReader("a;\"b\n"), Options{Format: FormatCSV}); err == nil {
		t.Error("guess on bad header should fail")
	}
}
