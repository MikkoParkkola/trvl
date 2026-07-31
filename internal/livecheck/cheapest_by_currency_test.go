package livecheck

import "testing"

type priced struct {
	price    float64
	currency string
}

func priceOf(p priced) float64   { return p.price }
func currencyOf(p priced) string { return p.currency }

// TestCheapestByCurrencyPrefersPreferredCurrency proves round 22's fix (#7):
// when an item exists in the watch's own (preferred) currency, it always
// wins, even if a numerically smaller price exists in a different currency.
// The old cheapest() would have picked the JPY item purely on magnitude.
func TestCheapestByCurrencyPrefersPreferredCurrency(t *testing.T) {
	items := []priced{
		{price: 30000, currency: "JPY"}, // numerically smaller, wrong currency.
		{price: 450, currency: "EUR"},   // preferred currency, must win.
		{price: 200, currency: "USD"},
	}
	got := cheapestByCurrency(items, priceOf, currencyOf, "EUR")
	if got.currency != "EUR" || got.price != 450 {
		t.Fatalf("got %+v, want the EUR item (450 EUR), not a cross-currency magnitude pick", got)
	}
}

// TestCheapestByCurrencySingleKnownCurrencyGroupFallback proves the fallback
// tier: when no item matches the preferred currency but exactly one other
// currency is present, pick the cheapest within that group.
func TestCheapestByCurrencySingleKnownCurrencyGroupFallback(t *testing.T) {
	items := []priced{
		{price: 500, currency: "USD"},
		{price: 300, currency: "USD"},
	}
	got := cheapestByCurrency(items, priceOf, currencyOf, "EUR")
	if got.currency != "USD" || got.price != 300 {
		t.Fatalf("got %+v, want cheapest USD item (300 USD)", got)
	}
}

// TestCheapestByCurrencyMultiCurrencyTieBreaksLexSmallest proves the
// multi-currency-group tier never compares magnitudes across currencies: with
// two equally-sized groups, the lexicographically smallest currency code
// wins, deterministically, regardless of provider order.
func TestCheapestByCurrencyMultiCurrencyTieBreaksLexSmallest(t *testing.T) {
	items := []priced{
		{price: 10000, currency: "JPY"}, // numerically smallest price overall.
		{price: 500, currency: "USD"},
		{price: 9999, currency: "JPY"},
		{price: 400, currency: "USD"},
	}
	// Two groups (JPY, USD), each size 2 -> tie -> lex-smallest code "JPY" wins
	// the GROUP selection, then the cheapest WITHIN that group is chosen.
	got := cheapestByCurrency(items, priceOf, currencyOf, "")
	if got.currency != "JPY" {
		t.Fatalf("got currency %q, want JPY (lexicographically smallest of the tied-size groups), never a raw-magnitude cross-currency pick", got.currency)
	}
	if got.price != 9999 {
		t.Fatalf("got price %v, want 9999 (cheapest within the chosen JPY group)", got.price)
	}
}

// TestCheapestByCurrencyAllCurrencylessFallback proves the last tier: when no
// item carries any currency, the cheapest currencyless item is returned.
func TestCheapestByCurrencyAllCurrencylessFallback(t *testing.T) {
	items := []priced{
		{price: 500, currency: ""},
		{price: 300, currency: ""},
	}
	got := cheapestByCurrency(items, priceOf, currencyOf, "EUR")
	if got.price != 300 {
		t.Fatalf("got %+v, want cheapest currencyless item (300)", got)
	}
}
