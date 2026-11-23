package decimal

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	br := Options{Decimal: ',', Thousands: '.', Lenient: true}
	auto := Options{}
	lenient := Options{Lenient: true}
	tests := []struct {
		in   string
		opts Options
		want int64
		err  error
	}{
		{"1234.56", auto, 123456, nil},
		{"-1234.56", auto, -123456, nil},
		{"+10", auto, 1000, nil},
		{"0.5", auto, 50, nil},
		{".5", auto, 50, nil},
		{"7.", auto, 700, nil},
		{"1234,56", auto, 123456, nil},
		{"1.234,56", auto, 123456, nil},
		{"1,234.56", auto, 123456, nil},
		{"1.234.567", auto, 123456700, nil},
		{"1,234,567", auto, 123456700, nil},
		{"100.000", auto, 10000, nil},
		{"1.005", auto, 0, ErrPrecision},
		{"1.234,56", br, 123456, nil},
		{"1.234", br, 123400, nil},
		{"R$ 1.234,56", br, 123456, nil},
		{"-R$ 1.234,56", br, -123456, nil},
		{"R$ -10,00", br, -1000, nil},
		{"(1.234,56)", br, -123456, nil},
		{"1.234,56 D", br, -123456, nil},
		{"1.234,56C", br, 123456, nil},
		{"1.234,56-", br, -123456, nil},
		{" 1.234,56 ", br, 123456, nil},
		{"BRL 5,00", br, 500, nil},
		{"$ 3.50", lenient, 350, nil},
		{"", auto, 0, ErrSyntax},
		{"-", auto, 0, ErrSyntax},
		{".", auto, 0, ErrSyntax},
		{"abc", auto, 0, ErrSyntax},
		{"1e5", auto, 0, ErrSyntax},
		{"12 34", auto, 0, ErrSyntax},
		{"1,2,3.4.5", auto, 0, ErrSyntax},
		{"1234567890123456", auto, 0, ErrRange},
		{"000000000000000000001", auto, 100, nil},
		{"999999999999999.99", auto, 99999999999999999, nil},
	}
	for _, tt := range tests {
		got, err := Parse(tt.in, tt.opts)
		if !errors.Is(err, tt.err) || (tt.err == nil && got != tt.want) {
			t.Errorf("Parse(%q, %+v) = %d, %v; want %d, %v", tt.in, tt.opts, got, err, tt.want, tt.err)
		}
	}
}

func TestFormat(t *testing.T) {
	for c, want := range map[int64]string{0: "0.00", 5: "0.05", -5: "-0.05", 123456: "1234.56", -100: "-1.00"} {
		if got := Format(c); got != want {
			t.Errorf("Format(%d) = %q, want %q", c, got, want)
		}
	}
	for c, want := range map[int64]string{0: "0,00", 123456: "1.234,56", -123456789: "-1.234.567,89", 99999: "999,99", 100000: "1.000,00"} {
		if got := FormatGrouped(c, ',', '.'); got != want {
			t.Errorf("FormatGrouped(%d) = %q, want %q", c, got, want)
		}
	}
}

func FuzzRoundTrip(f *testing.F) {
	for _, s := range []int64{0, 1, -1, 123456, -99999999} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, c int64) {
		if c > 1e17 || c < -1e17 {
			return
		}
		for _, s := range []string{Format(c), FormatGrouped(c, ',', '.'), FormatGrouped(c, '.', ',')} {
			opts := Options{}
			if s == FormatGrouped(c, ',', '.') {
				opts = Options{Decimal: ',', Thousands: '.'}
			} else if s != Format(c) {
				opts = Options{Decimal: '.', Thousands: ','}
			}
			got, err := Parse(s, opts)
			if err != nil || got != c {
				t.Fatalf("Parse(%q) = %d, %v; want %d", s, got, err, c)
			}
		}
	})
}
