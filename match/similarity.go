package match

import (
	"github.com/victorcondackdev/reconcile-go/internal/textnorm"
)

// JaroWinkler returns the Jaro-Winkler similarity of a and b, between 0
// (nothing in common) and 1 (identical). It compares runes as given; use
// Normalize first to ignore case, accents and banking noise words.
//
// The Winkler prefix bonus (scaling factor 0.1, prefix up to 4 runes) is
// applied only when the Jaro similarity exceeds 0.7, as in Winkler's
// original formulation.
func JaroWinkler(a, b string) float64 {
	return jaroWinkler([]rune(a), []rune(b))
}

func jaroWinkler(a, b []rune) float64 {
	j := jaro(a, b)
	if j <= 0.7 {
		return j
	}
	l := 0
	for l < 4 && l < len(a) && l < len(b) && a[l] == b[l] {
		l++
	}
	return j + float64(l)*0.1*(1-j)
}

func jaro(a, b []rune) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	window := len(b)/2 - 1
	if window < 0 {
		window = 0
	}
	// Strings in statements are short; a stack buffer avoids allocation
	// for the common case.
	var bufA, bufB [64]bool
	ma, mb := bufA[:0], bufB[:0]
	if len(a) <= 64 && len(b) <= 64 {
		ma, mb = bufA[:len(a)], bufB[:len(b)]
	} else {
		ma, mb = make([]bool, len(a)), make([]bool, len(b))
	}
	m := 0
	for i := range a {
		lo := max(0, i-window)
		hi := min(len(b), i+window+1)
		for k := lo; k < hi; k++ {
			if mb[k] || a[i] != b[k] {
				continue
			}
			ma[i], mb[k] = true, true
			m++
			break
		}
	}
	if m == 0 {
		return 0
	}
	t, k := 0, 0
	for i := range a {
		if !ma[i] {
			continue
		}
		for !mb[k] {
			k++
		}
		if a[i] != b[k] {
			t++
		}
		k++
	}
	fm := float64(m)
	return (fm/float64(len(a)) + fm/float64(len(b)) + (fm-float64(t)/2)/fm) / 3
}

// Normalize folds case and accents, splits on punctuation and drops stop
// words (banking jargon such as "PIX", "TED", "RECEBIDO", legal suffixes
// such as "LTDA", and Portuguese prepositions):
//
//	Normalize("PIX RECEBIDO - João da Silva ME") == "joao silva"
func Normalize(s string) string { return textnorm.Normalize(s) }

// TokenSimilarity compares two names or descriptions token by token: each
// token is paired with its most similar token on the other side
// (Jaro-Winkler) and the averages of both directions are averaged. Token
// order and noise words do not matter. Two empty inputs score 0.
func TokenSimilarity(a, b string) float64 {
	ta, tb := tokenize(a), tokenize(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	return (containment(ta, tb) + containment(tb, ta)) / 2
}

func tokenize(s string) [][]rune {
	toks := textnorm.NameTokens(s)
	out := make([][]rune, len(toks))
	for i, t := range toks {
		out[i] = []rune(t)
	}
	return out
}

// containment returns how well the tokens of want are found in have: the
// mean, over want's tokens, of the best Jaro-Winkler score against have.
// Tokens shorter than 3 runes must match exactly.
func containment(have, want [][]rune) float64 {
	if len(want) == 0 || len(have) == 0 {
		return 0
	}
	var sum float64
	for _, w := range want {
		best := 0.0
		for _, h := range have {
			var s float64
			if len(w) < 3 || len(h) < 3 {
				if string(w) == string(h) {
					s = 1
				}
			} else {
				s = jaroWinkler(w, h)
			}
			if s > best {
				best = s
				if best == 1 {
					break
				}
			}
		}
		sum += best
	}
	return sum / float64(len(want))
}
