package watch

import (
	"context"
	"testing"
)

// A watch must not throw away its accumulated history on the strength of ONE
// poll in a different currency.
//
// Providers intermittently return no price in the requested currency at all.
// Treating that poll as a currency change wipes LastPrice, LowestPrice,
// CheapestDate, BaselinePrice and LastAlertedPrice and clears BelowPrice and
// AlertDropAbs. If the provider recovers next poll, the same reset fires in
// reverse -- so a flapping provider destroys that history repeatedly
// (trvl#546, trvl#550).
//
// The wait is cheap by construction: a currency-mismatched poll already skips
// the threshold comparison, so deferring adoption forfeits alerts the poll was
// never going to produce.

// TRVL.CURRENCY.CONFIRM.1 -- one odd poll changes nothing.
func TestSingleForeignCurrencyPollDoesNotResetTheWatch(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "NRT",
		Currency: "JPY", BelowPrice: 20000, AlertDropAbs: 2000,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.RecordPrice(id, 25000, "JPY"); err != nil {
		t.Fatalf("seed history: %v", err)
	}
	if _, err := s.Mutate(id, func(w *Watch) {
		w.LowestPrice = 21000
		w.BaselinePrice = 26000
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	w, _ := s.Get(id)
	res := checkOne(context.Background(), s, &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{{price: 180, currency: "EUR"}}}, w)
	if res.Error != nil {
		t.Fatalf("check: %v", res.Error)
	}

	got, _ := s.Get(id)
	if got.Currency != "JPY" {
		t.Errorf("Currency = %q after ONE foreign poll, want JPY -- a single poll is not evidence "+
			"of a currency change", got.Currency)
	}
	if got.BelowPrice != 20000 {
		t.Errorf("BelowPrice = %v, want 20000 -- the user's target was cleared on one odd poll",
			got.BelowPrice)
	}
	if got.AlertDropAbs != 2000 {
		t.Errorf("AlertDropAbs = %v, want 2000", got.AlertDropAbs)
	}
	if got.LowestPrice != 21000 || got.BaselinePrice != 26000 {
		t.Errorf("accumulated state was wiped: lowest=%v baseline=%v, want 21000/26000",
			got.LowestPrice, got.BaselinePrice)
	}
	if len(s.History(id)) != 1 {
		t.Errorf("price history was purged: %d points, want the seeded 1", len(s.History(id)))
	}
	if res.BelowGoal {
		t.Error("BelowGoal fired on a 180 EUR quote against a 20000 JPY target")
	}
}

// TRVL.CURRENCY.CONFIRM.2 -- a genuine switch still lands, on the second poll.
// Without this the fix would be indistinguishable from never adopting at all.
func TestSecondConsecutiveForeignPollAdoptsTheCurrency(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "NRT",
		Currency: "JPY", BelowPrice: 20000,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.RecordPrice(id, 25000, "JPY"); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{{price: 180, currency: "EUR"}, {price: 175, currency: "EUR"}}}

	for i := 0; i < 2; i++ {
		w, _ := s.Get(id)
		if res := checkOne(context.Background(), s, checker, w); res.Error != nil {
			t.Fatalf("check %d: %v", i+1, res.Error)
		}
	}

	got, _ := s.Get(id)
	if got.Currency != "EUR" {
		t.Errorf("Currency = %q after TWO consecutive EUR polls, want EUR -- a confirmed change "+
			"must still take effect", got.Currency)
	}
	if got.BelowPrice != 0 {
		t.Errorf("BelowPrice = %v, want 0 -- a JPY target cannot survive into EUR", got.BelowPrice)
	}
	jpy := 0
	for _, p := range s.History(id) {
		if p.Currency == "JPY" {
			jpy++
		}
	}
	if jpy != 0 {
		t.Errorf("%d JPY point(s) survived a confirmed currency change", jpy)
	}
}

// TRVL.CURRENCY.CONFIRM.3 -- a provider that flaps back does NOT accumulate
// dissent. The counter tracks CONSECUTIVE disagreement; one matching poll
// resets it, or a watch could bank one dissent per month and eventually flip on
// unrelated blips months apart.
func TestFlappingProviderNeverAdoptsTheForeignCurrency(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "NRT",
		Currency: "JPY", BelowPrice: 20000,
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := s.RecordPrice(id, 25000, "JPY"); err != nil {
		t.Fatalf("seed history: %v", err)
	}

	// EUR, JPY, EUR, JPY, EUR -- never twice in a row.
	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{
		{price: 180, currency: "EUR"},
		{price: 24000, currency: "JPY"},
		{price: 178, currency: "EUR"},
		{price: 23800, currency: "JPY"},
		{price: 176, currency: "EUR"},
	}}

	for i := 0; i < 5; i++ {
		w, _ := s.Get(id)
		if res := checkOne(context.Background(), s, checker, w); res.Error != nil {
			t.Fatalf("check %d: %v", i+1, res.Error)
		}
	}

	got, _ := s.Get(id)
	if got.Currency != "JPY" {
		t.Errorf("Currency = %q after a flapping provider, want JPY -- alternating polls are not "+
			"two CONSECUTIVE ones", got.Currency)
	}
	if got.BelowPrice != 20000 {
		t.Errorf("BelowPrice = %v, want 20000 -- the target survived five flaps or it did not",
			got.BelowPrice)
	}
}

// TRVL.CURRENCY.CONFIRM.4 -- a brand-new watch adopts its FIRST quote
// immediately. There is nothing to protect, and refusing would leave it unable
// to establish a currency at all -- which is why an earlier attempt to fix this
// at the provider-selection layer was reverted.
func TestNewWatchAdoptsItsFirstQuoteImmediately(t *testing.T) {
	s := NewStore(t.TempDir())
	id, _, err := s.Add(Watch{
		Type: "flight", Origin: "HEL", Destination: "NRT",
		Currency: "JPY",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	w, _ := s.Get(id)
	if res := checkOne(context.Background(), s, &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{{price: 180, currency: "EUR"}}}, w); res.Error != nil {
		t.Fatalf("check: %v", res.Error)
	}

	got, _ := s.Get(id)
	if got.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR -- a watch with no prior observation must adopt its "+
			"first quote or it can never establish one", got.Currency)
	}
	if got.LastPrice != 180 {
		t.Errorf("LastPrice = %v, want 180", got.LastPrice)
	}
}
