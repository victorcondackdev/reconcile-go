package charset

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"unicode/utf8"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		name string
		want Charset
		err  bool
	}{
		{"", UTF8, false}, {"UTF-8", UTF8, false}, {"USASCII", UTF8, false}, {"NONE", UTF8, false},
		{"1252", Windows1252, false}, {"windows-1252", Windows1252, false}, {"CP1252", Windows1252, false},
		{"ISO-8859-1", ISO88591, false}, {"iso_8859_1", ISO88591, false}, {"8859-1", ISO88591, false}, {"latin1", ISO88591, false},
		{"ISO-8859-15", ISO885915, false}, {"Latin9", ISO885915, false},
		{"EBCDIC", UTF8, true}, {"UTF-16", UTF8, true},
	}
	for _, tt := range tests {
		got, err := Lookup(tt.name)
		if (err != nil) != tt.err || got != tt.want {
			t.Errorf("Lookup(%q) = %v, %v; want %v, err=%v", tt.name, got, err, tt.want, tt.err)
		}
		if tt.err && !errors.Is(err, ErrUnsupported) {
			t.Errorf("Lookup(%q) error %v is not ErrUnsupported", tt.name, err)
		}
	}
}

func TestString(t *testing.T) {
	for c, want := range map[Charset]string{UTF8: "utf-8", Windows1252: "windows-1252", ISO88591: "iso-8859-1", ISO885915: "iso-8859-15"} {
		if c.String() != want {
			t.Errorf("%d.String() = %q, want %q", c, c.String(), want)
		}
	}
}

func TestDecode(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		cs   Charset
		want string
	}{
		{"ascii passthrough", []byte("PIX RECEBIDO"), Windows1252, "PIX RECEBIDO"},
		{"1252 accents", []byte("JO\xc3O CONCEI\xc7\xc3O"), Windows1252, "JOÃO CONCEIÇÃO"},
		{"1252 C1 range", []byte("\x80 \x96 \x93x\x94 \x85 \x9f"), Windows1252, "€ – “x” … Ÿ"},
		{"1252 undefined maps to C1", []byte{0x81, 0x8d, 0x8f, 0x90, 0x9d}, Windows1252, "\u0081\u008d\u008f\u0090\u009d"},
		{"latin1 C1 controls", []byte{0x80, 0xe9}, ISO88591, "\u0080é"},
		{"latin1 accents", []byte("Servi\xe7os Banc\xe1rios"), ISO88591, "Serviços Bancários"},
		{"latin9 euro", []byte{0xa4, 0xa6, 0xbd, 0xe3}, ISO885915, "€Šœã"},
		{"utf8 valid", []byte("São Paulo"), UTF8, "São Paulo"},
		{"utf8 invalid replaced", []byte("S\xe3o"), UTF8, "S�o"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(Decode(tt.in, tt.cs))
			if got != tt.want {
				t.Errorf("Decode = %q, want %q", got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("Decode output is not valid UTF-8")
			}
		})
	}
}

func TestDecodeAllBytes(t *testing.T) {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	for _, c := range []Charset{Windows1252, ISO88591, ISO885915} {
		out := Decode(all, c)
		if n := utf8.RuneCount(out); n != 256 {
			t.Errorf("%v: %d runes, want 256", c, n)
		}
		if bytes.ContainsRune(out, utf8.RuneError) {
			t.Errorf("%v: output contains U+FFFD", c)
		}
	}
	if UTF8.DecodeRune(0xe3) != utf8.RuneError || UTF8.DecodeRune('a') != 'a' {
		t.Error("UTF8.DecodeRune")
	}
}

func TestSniff(t *testing.T) {
	tests := []struct {
		name     string
		in       []byte
		declared Charset
		want     Charset
	}{
		{"ascii", []byte("abc"), Windows1252, UTF8},
		{"declared 1252, really utf-8", []byte("JOÃO"), Windows1252, UTF8},
		{"declared 1252, is 1252", []byte("JO\xc3O"), Windows1252, Windows1252},
		{"declared utf-8, really 1252", []byte("JO\xc3O"), UTF8, Windows1252},
		{"declared latin1, is latin1", []byte("caf\xe9"), ISO88591, ISO88591},
	}
	for _, tt := range tests {
		if got := Sniff(tt.in, tt.declared); got != tt.want {
			t.Errorf("%s: Sniff = %v, want %v", tt.name, got, tt.want)
		}
	}
	if HasNonASCII([]byte("abc")) || !HasNonASCII([]byte{0x80}) {
		t.Error("HasNonASCII")
	}
}

func TestNewReader(t *testing.T) {
	r, err := NewReader("ISO-8859-1", bytes.NewReader([]byte("S\xe3o")))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	if string(b) != "São" {
		t.Errorf("got %q", b)
	}
	r, err = NewReader("utf-8", bytes.NewReader([]byte("ok")))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(r); string(b) != "ok" {
		t.Errorf("got %q", b)
	}
	if _, err := NewReader("klingon", bytes.NewReader(nil)); err == nil {
		t.Error("expected error for unknown charset")
	}
	if _, err := NewReader("latin1", errReader{}); err == nil {
		t.Error("expected read error")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }
