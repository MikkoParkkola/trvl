package models

import (
	"reflect"
	"testing"
)

func TestEditDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"helsinki", "helsinki", 0},
		{"helsinkii", "helsinki", 1},
		{"cabin_clas", "cabin_class", 1},
		{"kitten", "sitting", 3},
		{"münchen", "munchen", 1}, // by rune, not by byte
	} {
		if got := EditDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("EditDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSimilarCities(t *testing.T) {
	if got := SimilarCities("Helsinkii", 3); len(got) == 0 || got[0] != "helsinki" {
		t.Fatalf("SimilarCities(Helsinkii) = %v, want helsinki first", got)
	}
	if got := SimilarCities("Torino", 3); len(got) != 0 {
		// Turin is absent from the table; nothing within distance 2 should
		// masquerade as it.
		t.Fatalf("SimilarCities(Torino) = %v, want none", got)
	}
	if got := SimilarCities("", 3); got != nil {
		t.Fatalf("empty name must return nil, got %v", got)
	}
	if got := SimilarCities("Helsinkii", 0); got != nil {
		t.Fatalf("limit 0 must return nil, got %v", got)
	}
	a, b := SimilarCities("Pari", 5), SimilarCities("Pari", 5)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("results must be deterministic: %v vs %v", a, b)
	}
}
