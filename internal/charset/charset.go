// Package charset converts the single-byte character sets that Brazilian
// banks still use when exporting statements (Windows-1252, ISO-8859-1 and
// ISO-8859-15) to UTF-8, using tables embedded in the source.
package charset

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// Charset identifies a supported character set.
type Charset int

// Supported character sets.
const (
	UTF8 Charset = iota
	Windows1252
	ISO88591
	ISO885915
)

// ErrUnsupported is returned for character set names that are not known.
var ErrUnsupported = errors.New("charset: unsupported character set")

// String returns the canonical name of the character set.
func (c Charset) String() string {
	switch c {
	case Windows1252:
		return "windows-1252"
	case ISO88591:
		return "iso-8859-1"
	case ISO885915:
		return "iso-8859-15"
	default:
		return "utf-8"
	}
}

// Lookup resolves a character set name as found in OFX headers, XML
// declarations and configuration files. Matching ignores case, spaces,
// dashes and underscores, so "ISO-8859-1", "iso_8859_1", "8859-1" and
// "latin1" are equivalent. OFX 1.x "CHARSET:1252" resolves to Windows-1252
// and "CHARSET:NONE" (meaning US-ASCII) to UTF-8, a superset of ASCII.
func Lookup(name string) (Charset, error) {
	n := strings.Map(func(r rune) rune {
		switch r {
		case '-', '_', ' ', '.':
			return -1
		}
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, name)
	switch n {
	case "", "utf8", "usascii", "ascii", "none", "unicode":
		return UTF8, nil
	case "windows1252", "cp1252", "1252", "win1252", "xcp1252", "ansi":
		return Windows1252, nil
	case "iso88591", "88591", "latin1", "l1", "iso885911987", "isolatin1":
		return ISO88591, nil
	case "iso885915", "885915", "latin9", "l9":
		return ISO885915, nil
	}
	return UTF8, ErrUnsupported
}

// win1252 maps bytes 0x80-0x9F. Positions undefined in Windows-1252
// (0x81, 0x8D, 0x8F, 0x90, 0x9D) map to the C1 control of the same value,
// which is what the WHATWG Encoding Standard does.
var win1252 = [32]rune{
	0x20AC, 0x0081, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021,
	0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x008D, 0x017D, 0x008F,
	0x0090, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014,
	0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x009D, 0x017E, 0x0178,
}

// latin9 lists the eight code points where ISO-8859-15 differs from
// ISO-8859-1.
var latin9 = map[byte]rune{
	0xA4: 0x20AC, 0xA6: 0x0160, 0xA8: 0x0161, 0xB4: 0x017D,
	0xB8: 0x017E, 0xBC: 0x0152, 0xBD: 0x0153, 0xBE: 0x0178,
}

// DecodeRune maps one byte of c to its Unicode code point. For UTF8 it is
// the identity for ASCII and returns utf8.RuneError otherwise.
func (c Charset) DecodeRune(b byte) rune {
	if b < 0x80 {
		return rune(b)
	}
	switch c {
	case Windows1252:
		if b < 0xA0 {
			return win1252[b-0x80]
		}
	case ISO885915:
		if r, ok := latin9[b]; ok {
			return r
		}
	case UTF8:
		return utf8.RuneError
	}
	return rune(b)
}

// Decode converts b from c to UTF-8. For UTF8 the input is returned as is
// when valid; invalid sequences are replaced by U+FFFD.
func Decode(b []byte, c Charset) []byte {
	if c == UTF8 {
		if utf8.Valid(b) {
			return b
		}
		return bytes.ToValidUTF8(b, []byte("�"))
	}
	ascii := true
	for _, x := range b {
		if x >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return b
	}
	out := make([]byte, 0, len(b)+len(b)/8)
	for _, x := range b {
		if x < 0x80 {
			out = append(out, x)
			continue
		}
		out = utf8.AppendRune(out, c.DecodeRune(x))
	}
	return out
}

// HasNonASCII reports whether b contains any byte >= 0x80.
func HasNonASCII(b []byte) bool {
	for _, x := range b {
		if x >= 0x80 {
			return true
		}
	}
	return false
}

// Sniff decides how to decode b given the declared character set. Files
// declared as single-byte that are in fact valid UTF-8 with multi-byte
// sequences are treated as UTF-8 (a frequent mislabelling in bank exports),
// and files declared as UTF-8 that are not valid UTF-8 are treated as
// Windows-1252. Pure ASCII input is returned unchanged.
func Sniff(b []byte, declared Charset) Charset {
	if !HasNonASCII(b) {
		return UTF8
	}
	if utf8.Valid(b) {
		return UTF8
	}
	if declared == UTF8 {
		return Windows1252
	}
	return declared
}

// NewReader returns a reader that converts r from the named character set
// to UTF-8. It is suitable as encoding/xml's Decoder.CharsetReader.
func NewReader(name string, r io.Reader) (io.Reader, error) {
	c, err := Lookup(name)
	if err != nil {
		return nil, err
	}
	if c == UTF8 {
		return r, nil
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(Decode(b, c)), nil
}
