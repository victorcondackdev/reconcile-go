// Package decimal parses monetary amounts written as decimal strings into
// signed integer cents without going through floating point.
package decimal

import (
	"errors"
	"strconv"
	"strings"
)

// Errors returned by Parse.
var (
	ErrSyntax    = errors.New("invalid amount")
	ErrPrecision = errors.New("amount has non-zero digits beyond cents")
	ErrRange     = errors.New("amount out of range")
)

// maxIntegerDigits keeps cents well inside int64 (about 9.2e18).
const maxIntegerDigits = 15

// Options controls Parse.
type Options struct {
	// Decimal is the decimal separator. Zero means auto-detect: when both
	// '.' and ',' occur the last one is the decimal separator; when only
	// one of them occurs once it is the decimal separator; when it occurs
	// more than once it is a thousands separator.
	Decimal byte
	// Thousands is the grouping separator removed before parsing. Ignored
	// when Decimal is zero.
	Thousands byte
	// Lenient accepts the decorations found in spreadsheets and bank
	// exports: a currency symbol ("R$", "BRL", "$"), surrounding spaces
	// (including NBSP), parentheses for negatives, a trailing '-' and a
	// trailing C (credit) or D (debit) marker.
	Lenient bool
}

// Parse converts s to cents according to opts.
func Parse(s string, opts Options) (int64, error) {
	neg := false
	if opts.Lenient {
		s = strings.ReplaceAll(s, " ", " ")
		s = strings.TrimSpace(s)
		if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
			neg = true
			s = strings.TrimSpace(s[1 : len(s)-1])
		}
		if n := len(s); n > 0 {
			switch s[n-1] {
			case 'D', 'd':
				neg = !neg
				s = strings.TrimSpace(s[:n-1])
			case 'C', 'c':
				s = strings.TrimSpace(s[:n-1])
			case '-':
				neg = !neg
				s = strings.TrimSpace(s[:n-1])
			}
		}
		if strings.HasPrefix(s, "-") {
			neg = !neg
			s = strings.TrimSpace(s[1:])
		} else if strings.HasPrefix(s, "+") {
			s = strings.TrimSpace(s[1:])
		}
		for _, sym := range []string{"R$", "BRL", "US$", "USD", "EUR", "$", "€"} {
			if strings.HasPrefix(s, sym) {
				s = strings.TrimSpace(s[len(sym):])
				break
			}
		}
		s = strings.ReplaceAll(s, " ", "")
	} else {
		s = strings.TrimSpace(s)
	}
	if strings.HasPrefix(s, "-") {
		neg = !neg
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	if s == "" {
		return 0, ErrSyntax
	}
	dec, th := opts.Decimal, opts.Thousands
	if dec == 0 {
		dec, th = detect(s)
	}
	intPart, frac := s, ""
	if i := strings.LastIndexByte(s, dec); i >= 0 {
		intPart, frac = s[:i], s[i+1:]
	}
	if th != 0 {
		intPart = strings.ReplaceAll(intPart, string(th), "")
	}
	if intPart == "" && frac == "" {
		return 0, ErrSyntax
	}
	if !digits(intPart) || !digits(frac) {
		return 0, ErrSyntax
	}
	intPart = strings.TrimLeft(intPart, "0")
	if len(intPart) > maxIntegerDigits {
		return 0, ErrRange
	}
	if len(frac) > 2 {
		if strings.Trim(frac[2:], "0") != "" {
			return 0, ErrPrecision
		}
		frac = frac[:2]
	}
	for len(frac) < 2 {
		frac += "0"
	}
	var whole int64
	if intPart != "" {
		w, err := strconv.ParseInt(intPart, 10, 64)
		if err != nil {
			return 0, ErrRange
		}
		whole = w
	}
	f, _ := strconv.ParseInt(frac, 10, 64)
	c := whole*100 + f
	if neg {
		c = -c
	}
	return c, nil
}

func detect(s string) (dec, th byte) {
	dot, comma := strings.Count(s, "."), strings.Count(s, ",")
	switch {
	case dot > 0 && comma > 0:
		if strings.LastIndexByte(s, '.') > strings.LastIndexByte(s, ',') {
			return '.', ','
		}
		return ',', '.'
	case comma == 1:
		return ',', 0
	case comma > 1:
		return 0xff, ','
	case dot > 1:
		return 0xff, '.'
	default:
		return '.', 0
	}
}

func digits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Format renders cents as a plain decimal string with a '.' separator and
// no grouping, e.g. -123456 becomes "-1234.56".
func Format(c int64) string {
	neg := c < 0
	u := uint64(c)
	if neg {
		u = uint64(-c)
	}
	s := strconv.FormatUint(u/100, 10) + "." + pad2(u%100)
	if neg {
		return "-" + s
	}
	return s
}

// FormatGrouped renders cents with thousands grouping using the given
// separators, e.g. FormatGrouped(123456, ',', '.') is "1.234,56".
func FormatGrouped(c int64, dec, th byte) string {
	neg := c < 0
	u := uint64(c)
	if neg {
		u = uint64(-c)
	}
	w := strconv.FormatUint(u/100, 10)
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i := 0; i < len(w); i++ {
		if i > 0 && (len(w)-i)%3 == 0 {
			b.WriteByte(th)
		}
		b.WriteByte(w[i])
	}
	b.WriteByte(dec)
	b.WriteString(pad2(u % 100))
	return b.String()
}

func pad2(n uint64) string {
	if n < 10 {
		return "0" + strconv.FormatUint(n, 10)
	}
	return strconv.FormatUint(n, 10)
}
