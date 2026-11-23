package textnorm

import (
	"reflect"
	"testing"
)

func TestFold(t *testing.T) {
	if got := Fold("JOÃO Conceição ÀÉÎÕÜ ñ ºª"); got != "joao conceicao aeiou n oa" {
		t.Errorf("Fold = %q", got)
	}
	if got := Key("Data Lançamento (R$)"); got != "datalancamentor" {
		t.Errorf("Key = %q", got)
	}
}

func TestTokens(t *testing.T) {
	got := Tokens("PIX RECEBIDO - João da Silva/ME 123")
	want := []string{"pix", "recebido", "joao", "da", "silva", "me", "123"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tokens = %q", got)
	}
	if got := NameTokens("PIX RECEBIDO - João da Silva ME"); !reflect.DeepEqual(got, []string{"joao", "silva"}) {
		t.Errorf("NameTokens = %q", got)
	}
	if got := Normalize("TED  Recebida: Padaria São José LTDA"); got != "padaria sao jose" {
		t.Errorf("Normalize = %q", got)
	}
	if !IsStopWord("pix") || IsStopWord("maria") {
		t.Error("IsStopWord")
	}
}

func TestRefKey(t *testing.T) {
	for in, want := range map[string]string{
		"00012345678": "12345678", "000.123.456-7": "1234567", "e2e-abc": "E2EABC", "0000": "", "": "", "NF 2026/0815": "NF20260815",
	} {
		if got := RefKey(in); got != want {
			t.Errorf("RefKey(%q) = %q, want %q", in, got, want)
		}
	}
}
