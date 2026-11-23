package match

import (
	"math"
	"testing"
)

func TestJaroWinkler(t *testing.T) {
	// Reference values from Winkler (1990) and the usual test suites.
	tests := []struct {
		a, b string
		want float64
	}{
		{"MARTHA", "MARHTA", 0.9611},
		{"DWAYNE", "DUANE", 0.8400},
		{"DIXON", "DICKSONX", 0.8133},
		{"JELLYFISH", "SMELLYFISH", 0.8963},
		{"", "", 1},
		{"abc", "", 0},
		{"abc", "xyz", 0},
		{"same", "same", 1},
		{"a", "a", 1},
		{"JOÃO", "JOAO", 0.8667},
	}
	for _, tt := range tests {
		got := JaroWinkler(tt.a, tt.b)
		if math.Abs(got-tt.want) > 0.0001 {
			t.Errorf("JaroWinkler(%q, %q) = %.4f, want %.4f", tt.a, tt.b, got, tt.want)
		}
		if rev := JaroWinkler(tt.b, tt.a); math.Abs(rev-got) > 1e-12 {
			t.Errorf("not symmetric for %q/%q: %v vs %v", tt.a, tt.b, got, rev)
		}
	}
	long1 := "transportadora serra azul logistica integrada do brasil e mercosul ltda"
	long2 := "transportadora serra azul logistica integrada do brasil e mercosul s.a."
	if s := JaroWinkler(long1, long2); s < 0.95 || s > 1 {
		t.Errorf("long strings: %v", s)
	}
}

func TestTokenSimilarity(t *testing.T) {
	tests := []struct {
		a, b   string
		lo, hi float64
	}{
		{"PIX RECEBIDO MARIA APARECIDA SOUZA", "Maria Aparecida Souza", 1, 1},
		{"JOAO DA CONCEICAO", "João da Conceição", 1, 1},
		{"SOUZA MARIA", "Maria Souza", 1, 1},
		{"MARIA APARECIDA SOUSA", "Maria Aparecida Souza", 0.95, 0.999},
		{"FERNANDA GONCALVES", "Ricardo Almeida", 0, 0.6},
		{"", "Maria", 0, 0},
		{"PIX TED", "Maria", 0, 0},
	}
	for _, tt := range tests {
		got := TokenSimilarity(tt.a, tt.b)
		if got < tt.lo || got > tt.hi {
			t.Errorf("TokenSimilarity(%q, %q) = %.3f, want [%.2f, %.2f]", tt.a, tt.b, got, tt.lo, tt.hi)
		}
	}
	if Normalize("PIX RECEBIDO - João da Silva ME") != "joao silva" {
		t.Errorf("Normalize = %q", Normalize("PIX RECEBIDO - João da Silva ME"))
	}
	// Tokens shorter than three runes need an exact match.
	if c := containment(tokenize("ab cd"), tokenize("ab ce")); c != 0.5 {
		t.Errorf("short tokens: %v", c)
	}
	if containment(nil, tokenize("x")) != 0 {
		t.Error("empty have")
	}
}

func BenchmarkJaroWinkler(b *testing.B) {
	for i := 0; i < b.N; i++ {
		JaroWinkler("MARIA APARECIDA DOS SANTOS", "MARIA APARECIDA DOS SANTOS OLIVEIRA")
	}
}
