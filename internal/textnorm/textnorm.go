// Package textnorm normalizes free text from bank statements and ledgers:
// case and accent folding, tokenization and stop-word removal.
package textnorm

import (
	"strings"
	"unicode"
)

// fold maps Latin letters with diacritics to their base letter. It covers
// Portuguese, Spanish, French and German letters, which is what appears in
// Brazilian statements.
var fold = map[rune]rune{
	'á': 'a', 'à': 'a', 'â': 'a', 'ã': 'a', 'ä': 'a', 'å': 'a',
	'é': 'e', 'è': 'e', 'ê': 'e', 'ë': 'e',
	'í': 'i', 'ì': 'i', 'î': 'i', 'ï': 'i',
	'ó': 'o', 'ò': 'o', 'ô': 'o', 'õ': 'o', 'ö': 'o',
	'ú': 'u', 'ù': 'u', 'û': 'u', 'ü': 'u',
	'ç': 'c', 'ñ': 'n', 'ý': 'y', 'ÿ': 'y',
	'ª': 'a', 'º': 'o',
}

// Fold lower-cases s and removes diacritics.
func Fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		r = unicode.ToLower(r)
		if f, ok := fold[r]; ok {
			r = f
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Key folds s and drops everything that is not a letter or digit, so
// "Data Lançamento" and "data_lancamento" both become "datalancamento".
func Key(s string) string {
	var b strings.Builder
	for _, r := range Fold(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Tokens folds s and splits it on every rune that is not a letter or digit.
func Tokens(s string) []string {
	return strings.FieldsFunc(Fold(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// stop lists tokens that carry no identifying information in statement
// descriptions and party names: banking jargon, legal-entity suffixes and
// Portuguese prepositions.
var stop = map[string]bool{
	"de": true, "da": true, "do": true, "das": true, "dos": true, "e": true,
	"a": true, "o": true, "em": true, "para": true, "por": true,
	"ltda": true, "me": true, "sa": true, "s": true, "epp": true,
	"eireli": true, "mei": true, "cia": true,
	"pix": true, "ted": true, "doc": true, "tef": true, "transf": true,
	"transferencia": true, "recebido": true, "recebida": true,
	"enviado": true, "enviada": true, "pagamento": true, "pgto": true,
	"pag": true, "credito": true, "debito": true, "cred": true, "deb": true,
	"liq": true, "liquidacao": true, "rem": true, "remetente": true,
}

// IsStopWord reports whether t (already folded) is a stop word.
func IsStopWord(t string) bool { return stop[t] }

// NameTokens returns the tokens of s without stop words and without
// one-character tokens. It is used to compare party names.
func NameTokens(s string) []string {
	toks := Tokens(s)
	out := toks[:0]
	for _, t := range toks {
		if len(t) > 1 && !stop[t] {
			out = append(out, t)
		}
	}
	return out
}

// Normalize returns the name tokens of s joined by single spaces.
func Normalize(s string) string { return strings.Join(NameTokens(s), " ") }

// RefKey normalizes an identifier for exact comparison: it keeps letters
// and digits, upper-cases them and strips leading zeros, because banks pad
// "nosso número" and document numbers differently.
func RefKey(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		}
	}
	k := strings.TrimLeft(b.String(), "0")
	return k
}
