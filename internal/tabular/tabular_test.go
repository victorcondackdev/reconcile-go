package tabular

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestRead(t *testing.T) {
	in := "\xef\xbb\xbfBanner line\nData;Valor;Hist\xf3rico\n\n01/11/2026;1.234,56;\"PIX; RECEBIDO\"\n02/11/2026;-5,00;TARIFA\n"
	tb, err := Read(strings.NewReader(in), "", "", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tb.Header, "|"); got != "Data|Valor|Histórico" {
		t.Errorf("header %q", got)
	}
	if len(tb.Rows) != 2 || tb.Rows[0][2] != "PIX; RECEBIDO" || tb.Lines[0] != 4 || tb.Lines[1] != 5 {
		t.Errorf("rows %q lines %v", tb.Rows, tb.Lines)
	}
	tb, err = Read(strings.NewReader("a,b\n1,2\n"), ",", "latin1", 0, false)
	if err != nil || len(tb.Rows) != 2 || tb.Header != nil {
		t.Errorf("no header: %v %v", tb, err)
	}
	tb, err = Read(strings.NewReader("only\n"), "", "", 5, true)
	if err != nil || len(tb.Rows) != 0 {
		t.Errorf("skip past end: %v %v", tb, err)
	}
	if _, err := Read(strings.NewReader("a"), "", "ebcdic", 0, true); err == nil {
		t.Error("expected encoding error")
	}
	if _, err := Read(strings.NewReader("a"), "ab", "", 0, true); err == nil {
		t.Error("expected delimiter error")
	}
	if _, err := Read(strings.NewReader("a,\"b\n"), ",", "", 0, true); err != nil {
		t.Errorf("lazy quotes: %v", err)
	}
	if _, err := Read(errReader{}, "", "", 0, true); err == nil {
		t.Error("expected read error")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestDelimiter(t *testing.T) {
	tests := []struct {
		spec, data string
		want       rune
	}{
		{"", "a;b;c\n1;2;3", ';'}, {"", "a,b,c", ','}, {"", "a\tb", '\t'}, {"", "a|b|c", '|'},
		{"", "\"x;y\",b,c", ','}, {"", "nothing", ','}, {"tab", "", '\t'}, {";", "", ';'}, {"\\t", "", '\t'},
	}
	for _, tt := range tests {
		got, err := Delimiter(tt.spec, []byte(tt.data))
		if err != nil || got != tt.want {
			t.Errorf("Delimiter(%q, %q) = %q, %v", tt.spec, tt.data, got, err)
		}
	}
	for _, bad := range []string{"\"", "\n", ";;"} {
		if _, err := Delimiter(bad, nil); err == nil {
			t.Errorf("Delimiter(%q) should fail", bad)
		}
	}
}

func TestColumn(t *testing.T) {
	h := []string{"Data", "Histórico", "Valor (R$)"}
	for spec, want := range map[string]int{"data": 0, "HISTORICO": 1, "valor r$": 2, "#3": 2, "": -1} {
		got, err := Column(h, spec)
		if err != nil || got != want {
			t.Errorf("Column(%q) = %d, %v", spec, got, err)
		}
	}
	if _, err := Column(h, "saldo"); !errors.Is(err, ErrNoColumn) {
		t.Errorf("missing column err = %v", err)
	}
	for _, bad := range []string{"#0", "#x"} {
		if _, err := Column(h, bad); err == nil {
			t.Errorf("Column(%q) should fail", bad)
		}
	}
	if Guess(h, "descricao", "historico") != 1 || Guess(h, "nope") != -1 {
		t.Error("Guess")
	}
	if Field([]string{"a"}, 3) != "" || Field([]string{"a"}, -1) != "" || Field([]string{"a"}, 0) != "a" {
		t.Error("Field")
	}
}

func TestDates(t *testing.T) {
	if got := Layout("dd/mm/yyyy HH:MM:SS"); got != "02/01/2006 15:04:05" {
		t.Errorf("Layout = %q", got)
	}
	if got := Layout("2006-01-02"); got != "2006-01-02" {
		t.Errorf("Layout passthrough = %q", got)
	}
	if got := Layout("dd.mm.yy"); got != "02.01.06" {
		t.Errorf("Layout yy = %q", got)
	}
	tests := []struct {
		in, layout string
		want       string
	}{
		{"21/11/2026", "", "2026-11-21"}, {"2026-11-21", "", "2026-11-21"}, {"21/11/2026 10:30", "", "2026-11-21"},
		{"21-11-2026", "", "2026-11-21"}, {"21.11.2026", "", "2026-11-21"}, {"20261121", "", "2026-11-21"},
		{"21/11/26", "", "2026-11-21"}, {"2026-11-21T10:00:00-03:00", "", "2026-11-21"},
		{"11/21/2026", "mm/dd/yyyy", "2026-11-21"}, {" 01/02/2026 ", "dd/mm/yyyy", "2026-02-01"},
	}
	for _, tt := range tests {
		got, err := ParseDate(tt.in, tt.layout, nil)
		if err != nil || got.Format("2006-01-02") != tt.want {
			t.Errorf("ParseDate(%q, %q) = %v, %v", tt.in, tt.layout, got, err)
		}
	}
	for _, bad := range []string{"31/02/2026", "2026-13-01", "ontem", ""} {
		if _, err := ParseDate(bad, "", time.UTC); err == nil {
			t.Errorf("ParseDate(%q) should fail", bad)
		}
	}
	if _, err := ParseDate("21/11/2026", "yyyy-mm-dd", time.UTC); err == nil {
		t.Error("layout mismatch should fail")
	}
}
