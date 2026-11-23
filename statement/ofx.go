package statement

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/victorcondackdev/reconcile-go/internal/charset"
	"github.com/victorcondackdev/reconcile-go/internal/decimal"
)

// ParseOFX reads an OFX 1.x (SGML) or 2.x (XML) statement. Bank and credit
// card statements (STMTRS, CCSTMTRS) are supported; every STMTTRN becomes
// an Entry.
//
// The parser is a tolerant tag scanner rather than an SGML or XML parser:
// OFX 1.x leaves leaf elements unclosed, and real exports frequently break
// the rules in small ways (stray closing tags, missing </STMTTRN>, header
// declaring CHARSET:1252 for a UTF-8 body). A tag followed by text is a
// leaf; any other tag opens an aggregate that ends at its closing tag, at
// the closing tag of an enclosing aggregate, or at the next opening tag of
// the same name.
func ParseOFX(r io.Reader) (*Statement, error) {
	return ParseWithOptions(r, Options{Format: "ofx"})
}

type ofxToken struct {
	kind byte // 'o' open, 'c' close, 't' text
	name string
	text string
}

func parseOFX(data []byte, opts Options) (*Statement, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	st := &Statement{Format: FormatOFX1}
	start := bytes.IndexByte(data, '<')
	if start < 0 {
		return nil, &ParseError{Format: FormatOFX1, Err: fmt.Errorf("%w: no OFX body", ErrSyntax)}
	}
	declared := charset.UTF8
	header := data[:start]
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("<?")) || bytes.Contains(bytes.ToUpper(data[:min(len(data), 4096)]), []byte("OFXHEADER=")) {
		st.Format = FormatOFX2
		if enc := xmlDeclEncoding(data); enc != "" {
			if c, err := charset.Lookup(enc); err == nil {
				declared = c
			}
		}
	} else {
		declared = ofx1Charset(header)
	}
	body := charset.Decode(data[start:], charset.Sniff(data[start:], declared))

	toks := tokenizeOFX(body)
	p := &ofxParser{st: st, opts: opts}
	if err := p.run(toks); err != nil {
		return nil, err
	}
	if !p.sawOFX {
		return nil, &ParseError{Format: st.Format, Err: fmt.Errorf("%w: missing <OFX> element", ErrSyntax)}
	}
	return st, nil
}

// ofx1Charset reads ENCODING and CHARSET from an OFX 1.x header.
func ofx1Charset(header []byte) charset.Charset {
	enc, cs := "", ""
	for _, line := range strings.Split(string(header), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(k)) {
		case "ENCODING":
			enc = strings.TrimSpace(v)
		case "CHARSET":
			cs = strings.TrimSpace(v)
		}
	}
	if strings.EqualFold(enc, "UTF-8") || strings.EqualFold(enc, "UTF8") {
		return charset.UTF8
	}
	if c, err := charset.Lookup(cs); err == nil {
		return c
	}
	return charset.Windows1252
}

// xmlDeclEncoding returns the encoding pseudo-attribute of an XML
// declaration at the start of data.
func xmlDeclEncoding(data []byte) string {
	d := bytes.TrimSpace(data)
	if !bytes.HasPrefix(d, []byte("<?xml")) {
		return ""
	}
	end := bytes.Index(d, []byte("?>"))
	if end < 0 {
		return ""
	}
	decl := string(d[:end])
	i := strings.Index(decl, "encoding")
	if i < 0 {
		return ""
	}
	rest := strings.TrimLeft(decl[i+len("encoding"):], " \t\r\n=")
	if rest == "" || (rest[0] != '"' && rest[0] != '\'') {
		return ""
	}
	q := rest[0]
	rest = rest[1:]
	if j := strings.IndexByte(rest, q); j >= 0 {
		return rest[:j]
	}
	return ""
}

func tokenizeOFX(b []byte) []ofxToken {
	var toks []ofxToken
	s := string(b)
	for len(s) > 0 {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			if t := strings.TrimSpace(s); t != "" {
				toks = append(toks, ofxToken{kind: 't', text: t})
			}
			break
		}
		if t := strings.TrimSpace(s[:i]); t != "" {
			toks = append(toks, ofxToken{kind: 't', text: t})
		}
		s = s[i:]
		if strings.HasPrefix(s, "<!--") {
			j := strings.Index(s, "-->")
			if j < 0 {
				break
			}
			s = s[j+3:]
			continue
		}
		j := strings.IndexByte(s, '>')
		if j < 0 {
			break
		}
		tag := s[1:j]
		s = s[j+1:]
		if tag == "" || tag[0] == '?' || tag[0] == '!' {
			continue
		}
		selfClose := strings.HasSuffix(tag, "/")
		tag = strings.TrimSuffix(tag, "/")
		closing := strings.HasPrefix(tag, "/")
		tag = strings.TrimPrefix(tag, "/")
		if k := strings.IndexAny(tag, " \t\r\n"); k >= 0 {
			tag = tag[:k]
		}
		name := strings.ToUpper(tag)
		if name == "" {
			continue
		}
		switch {
		case closing:
			toks = append(toks, ofxToken{kind: 'c', name: name})
		case selfClose:
			toks = append(toks, ofxToken{kind: 'o', name: name}, ofxToken{kind: 'c', name: name})
		default:
			toks = append(toks, ofxToken{kind: 'o', name: name})
		}
	}
	return toks
}

type ofxParser struct {
	st     *Statement
	opts   Options
	stack  []string
	trn    map[string]string
	trnIdx int
	bal    map[string]string
	sawOFX bool
	err    error
}

func (p *ofxParser) in(name string) bool {
	for i := len(p.stack) - 1; i >= 0; i-- {
		if p.stack[i] == name {
			return true
		}
	}
	return false
}

func (p *ofxParser) parent() string {
	if len(p.stack) == 0 {
		return ""
	}
	return p.stack[len(p.stack)-1]
}

func (p *ofxParser) run(toks []ofxToken) error {
	for i := 0; i < len(toks) && p.err == nil; i++ {
		t := toks[i]
		switch t.kind {
		case 'o':
			if t.name == "OFX" {
				p.sawOFX = true
			}
			// Leaf: a tag followed by text, optionally closed (OFX 2.x).
			if i+1 < len(toks) && toks[i+1].kind == 't' {
				p.leaf(t.name, html.UnescapeString(toks[i+1].text))
				i++
				if i+1 < len(toks) && toks[i+1].kind == 'c' && toks[i+1].name == t.name {
					i++
				}
				continue
			}
			// Empty XML leaf: <MEMO></MEMO>.
			if i+1 < len(toks) && toks[i+1].kind == 'c' && toks[i+1].name == t.name && t.name != "STMTTRN" {
				p.leaf(t.name, "")
				i++
				continue
			}
			if p.in(t.name) && (t.name == "STMTTRN" || t.name == "LEDGERBAL" || t.name == "AVAILBAL") {
				p.closeTo(t.name)
			}
			p.open(t.name)
		case 'c':
			if p.in(t.name) {
				p.closeTo(t.name)
			}
		}
	}
	for len(p.stack) > 0 && p.err == nil {
		p.pop()
	}
	return p.err
}

func (p *ofxParser) open(name string) {
	p.stack = append(p.stack, name)
	switch name {
	case "STMTTRN":
		p.trnIdx++
		p.trn = map[string]string{}
	case "LEDGERBAL", "AVAILBAL":
		p.bal = map[string]string{}
	}
}

func (p *ofxParser) closeTo(name string) {
	for len(p.stack) > 0 && p.err == nil {
		top := p.pop()
		if top == name {
			return
		}
	}
}

func (p *ofxParser) pop() string {
	top := p.stack[len(p.stack)-1]
	p.stack = p.stack[:len(p.stack)-1]
	switch top {
	case "STMTTRN":
		if p.trn != nil {
			p.finishTrn()
			p.trn = nil
		}
	case "LEDGERBAL", "AVAILBAL":
		if p.bal != nil {
			p.finishBal(top)
			p.bal = nil
		}
	}
	return top
}

func (p *ofxParser) leaf(name, value string) {
	value = strings.TrimSpace(value)
	if p.trn != nil && p.in("STMTTRN") {
		key := name
		switch parent := p.parent(); parent {
		case "PAYEE":
			if name == "NAME" {
				key = "PAYEE.NAME"
			}
		case "CURRENCY", "ORIGCURRENCY":
			key = parent + "." + name
		}
		if _, dup := p.trn[key]; !dup {
			p.trn[key] = value
		}
		return
	}
	if p.bal != nil && (p.parent() == "LEDGERBAL" || p.parent() == "AVAILBAL") {
		p.bal[name] = value
		return
	}
	switch name {
	case "CURDEF":
		if p.st.Currency == "" {
			p.st.Currency = strings.ToUpper(value)
		}
	case "BANKID":
		if p.st.BankID == "" {
			p.st.BankID = value
		}
	case "ACCTID":
		if p.st.AccountID == "" {
			p.st.AccountID = value
		}
	case "DTSTART", "DTEND":
		if !p.in("BANKTRANLIST") {
			return
		}
		t, err := ParseOFXDate(value)
		if err != nil {
			p.err = &ParseError{Format: p.st.Format, Field: name, Value: value, Err: err}
			return
		}
		if name == "DTSTART" && p.st.Start.IsZero() {
			p.st.Start = t
		} else if name == "DTEND" {
			p.st.End = t
		}
	}
}

var debitTypes = map[string]bool{
	"DEBIT": true, "PAYMENT": true, "CHECK": true, "FEE": true, "SRVCHG": true,
	"ATM": true, "POS": true, "CASH": true, "DIRECTDEBIT": true, "REPEATPMT": true,
}

func (p *ofxParser) finishTrn() {
	f := p.trn
	idx := p.trnIdx
	fail := func(field, value string, err error) {
		p.err = &ParseError{Format: p.st.Format, Index: idx, Field: field, Value: value, Err: err}
	}
	amtS, ok := f["TRNAMT"]
	if !ok || amtS == "" {
		fail("TRNAMT", "", ErrMissing)
		return
	}
	amt, err := decimal.Parse(amtS, decimal.Options{})
	if err != nil {
		fail("TRNAMT", amtS, fmt.Errorf("%w: %v", ErrAmount, err))
		return
	}
	typ := strings.ToUpper(f["TRNTYPE"])
	if p.opts.FixDebitSign && amt > 0 && debitTypes[typ] {
		amt = -amt
	}
	dtS := f["DTPOSTED"]
	if dtS == "" {
		fail("DTPOSTED", "", ErrMissing)
		return
	}
	posted, err := ParseOFXDate(dtS)
	if err != nil {
		fail("DTPOSTED", dtS, err)
		return
	}
	value := posted
	if s := f["DTAVAIL"]; s != "" {
		if t, err := ParseOFXDate(s); err == nil {
			value = t
		}
	}
	name := f["NAME"]
	if name == "" {
		name = f["PAYEE.NAME"]
	}
	memo := f["MEMO"]
	cur := p.st.Currency
	if c := f["CURRENCY.CURSYM"]; c != "" {
		cur = strings.ToUpper(c)
	}
	if cur == "" {
		cur = "BRL"
	}
	id := f["FITID"]
	if id == "" {
		id = "ofx-" + strconv.Itoa(idx)
	}
	ref := FindEndToEndID(memo + " " + name)
	if ref == "" {
		ref = firstNonEmpty(f["REFNUM"], f["CHECKNUM"], f["SRVRTID"])
	}
	e := Entry{
		ID:           id,
		BookingDate:  posted,
		ValueDate:    value,
		AmountCents:  amt,
		Currency:     cur,
		Description:  ofxDescription(name, memo),
		Counterparty: name,
		Reference:    ref,
		Type:         typ,
		Raw:          f,
	}
	p.st.Entries = append(p.st.Entries, e)
}

func (p *ofxParser) finishBal(kind string) {
	amtS := p.bal["BALAMT"]
	if amtS == "" {
		return
	}
	amt, err := decimal.Parse(amtS, decimal.Options{})
	if err != nil {
		p.err = &ParseError{Format: p.st.Format, Field: kind + ".BALAMT", Value: amtS, Err: fmt.Errorf("%w: %v", ErrAmount, err)}
		return
	}
	b := Balance{Kind: kind, AmountCents: amt, Currency: p.st.Currency}
	if s := p.bal["DTASOF"]; s != "" {
		if t, err := ParseOFXDate(s); err == nil {
			b.Date = t
		}
	}
	p.st.Balances = append(p.st.Balances, b)
}

// ofxDescription combines NAME and MEMO, avoiding the repetition that
// banks produce when MEMO already starts with NAME.
func ofxDescription(name, memo string) string {
	n := strings.Join(strings.Fields(name), " ")
	m := strings.Join(strings.Fields(memo), " ")
	if n != "" && strings.HasPrefix(strings.ToUpper(m), strings.ToUpper(n)) {
		return m
	}
	return joinNonEmpty(" ", n, m)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ParseOFXDate parses an OFX date-time:
//
//	YYYYMMDD[HHMM[SS[.XXX]]][[+|-]h[.mm][:TZ]]
//
// for example "20261121", "20261121120000" and "20261121120000.000[-3:BRT]".
// Without an offset the time is GMT, as the OFX specification states. The
// offset may carry minutes after a dot ("+5.30"); a single digit after the
// dot is read as a fraction of an hour ("-3.5" is -03:30).
func ParseOFXDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	orig := s
	loc := time.UTC
	if i := strings.IndexByte(s, '['); i >= 0 {
		tz := s[i+1:]
		s = strings.TrimSpace(s[:i])
		tz = strings.TrimSuffix(strings.TrimSpace(tz), "]")
		l, err := ofxZone(tz)
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: %q: %v", ErrDate, orig, err)
		}
		loc = l
	}
	frac := ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		frac = s[i+1:]
		s = s[:i]
		if frac == "" || len(frac) > 9 || !allDigits(frac) {
			return time.Time{}, fmt.Errorf("%w: %q", ErrDate, orig)
		}
	}
	if !allDigits(s) || (len(s) != 8 && len(s) != 12 && len(s) != 14) {
		return time.Time{}, fmt.Errorf("%w: %q", ErrDate, orig)
	}
	n := func(a, b int) int {
		v, _ := strconv.Atoi(s[a:b])
		return v
	}
	y, mo, d := n(0, 4), n(4, 6), n(6, 8)
	h, mi, sec := 0, 0, 0
	if len(s) >= 12 {
		h, mi = n(8, 10), n(10, 12)
	}
	if len(s) == 14 {
		sec = n(12, 14)
	}
	ns := 0
	if frac != "" {
		f := frac + strings.Repeat("0", 9-len(frac))
		ns, _ = strconv.Atoi(f)
	}
	if mo < 1 || mo > 12 || d < 1 || h > 23 || mi > 59 || sec > 59 {
		return time.Time{}, fmt.Errorf("%w: %q", ErrDate, orig)
	}
	t := time.Date(y, time.Month(mo), d, h, mi, sec, ns, loc)
	if t.Day() != d {
		return time.Time{}, fmt.Errorf("%w: %q: day out of range", ErrDate, orig)
	}
	return t, nil
}

func ofxZone(tz string) (*time.Location, error) {
	off, name, _ := strings.Cut(tz, ":")
	off = strings.TrimSpace(off)
	name = strings.TrimSpace(name)
	if off == "" {
		if name == "" {
			return time.UTC, nil
		}
		return nil, fmt.Errorf("missing offset")
	}
	sign := 1
	switch off[0] {
	case '+':
		off = off[1:]
	case '-':
		sign = -1
		off = off[1:]
	}
	hs, ms, hasMin := strings.Cut(off, ".")
	if hs == "" || len(hs) > 2 || !allDigits(hs) {
		return nil, fmt.Errorf("invalid offset %q", tz)
	}
	h, _ := strconv.Atoi(hs)
	m := 0
	if hasMin {
		if ms == "" || len(ms) > 2 || !allDigits(ms) {
			return nil, fmt.Errorf("invalid offset %q", tz)
		}
		m, _ = strconv.Atoi(ms)
		if len(ms) == 1 {
			m *= 6
		}
	}
	if h > 14 || m > 59 {
		return nil, fmt.Errorf("offset out of range %q", tz)
	}
	secs := sign * (h*3600 + m*60)
	if name == "" {
		name = fmt.Sprintf("UTC%+03d:%02d", sign*h, m)
		if secs < 0 {
			name = fmt.Sprintf("UTC-%02d:%02d", h, m)
		}
	}
	return time.FixedZone(name, secs), nil
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}
