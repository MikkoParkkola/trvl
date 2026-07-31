package watch

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestValidateRejectsMalformedCurrency proves round 22's watch.Validate fix
// (#5): a user-supplied Currency that fails IsValidCurrencyFormat is now
// rejected at Add-time instead of being silently persisted and only caught
// later (or not at all) by check.go/store.go's own format guards. Before the
// fix, Validate never looked at Currency at all.
func TestValidateRejectsMalformedCurrency(t *testing.T) {
	w := Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "US1", // digits are not a valid ISO-4217-shaped code.
	}
	if err := w.Validate(); err == nil {
		t.Fatal("Validate() = nil, want error for malformed currency \"US1\"")
	}
}

// TestValidateAcceptsWellFormedCurrency is the paired positive case: a
// well-formed currency must not be rejected by the new check.
func TestValidateAcceptsWellFormedCurrency(t *testing.T) {
	w := Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "usd", // lower-case; Validate/normalization upstream uppercases.
	}
	if err := w.Validate(); err != nil {
		t.Fatalf("Validate() = %v, want nil for well-formed currency", err)
	}
}

// TestRecordPriceRejectsMismatchWithZeroPriorObservations proves round 22's
// store.go fix (#6, first gap): a watch created with an explicit Currency but
// zero prior observations (LastPrice==0, LowestPrice==0) must still reject a
// differently-currencied RecordPrice call. Before the fix, rejection was
// gated on hasPriorObservation, so this exact case fell through and silently
// adopted the mismatched currency on the "first" observation.
func TestRecordPriceRejectsMismatchWithZeroPriorObservations(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "USD",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := store.RecordPrice(id, 400, "EUR"); err == nil {
		t.Fatal("RecordPrice = nil, want error: currency EUR mismatches watch's established USD even with zero prior observations")
	}

	got, ok := store.Get(id)
	if !ok {
		t.Fatalf("watch not found after rejected RecordPrice")
	}
	if got.Currency != "USD" || got.LastPrice != 0 {
		t.Errorf("watch mutated by rejected RecordPrice: Currency=%q LastPrice=%v, want unchanged USD/0", got.Currency, got.LastPrice)
	}
}

// TestRecordPriceRejectsEmptyCurrencyOnEstablishedWatch proves round 22's
// store.go fix (#6, second gap): an empty-currency observation must be
// rejected once the watch already has an established (non-empty) Currency,
// rather than being silently written into a series the rest of the package
// assumes is single-currency.
func TestRecordPriceRejectsEmptyCurrencyOnEstablishedWatch(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	id, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "USD",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := store.RecordPrice(id, 400, "USD"); err != nil {
		t.Fatalf("RecordPrice (establishing baseline): %v", err)
	}

	if err := store.RecordPrice(id, 410, ""); err == nil {
		t.Fatal("RecordPrice = nil, want error: empty currency onto a watch already established in USD")
	}
}

// TestCheckOneSetsAlertsClearedByCurrencyChangeAndNotifies proves round 22's
// fixes #2 and #3 together: when a currency mismatch clears a live
// BelowPrice threshold, CheckResult.AlertsClearedByCurrencyChange must be set
// (not just the threshold silently zeroed), and Notifier.Notify must surface
// a "CURRENCY CHANGED" line for it. Before the fix neither happened: the
// user's alert threshold vanished with no signal anywhere.
func TestCheckOneSetsAlertsClearedByCurrencyChangeAndNotifies(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "USD",
		BelowPrice:  300,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{
		{price: 450, currency: "EUR"}, // currency mismatch clears BelowPrice.
	}}

	results := CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("CheckAll: results=%+v", results)
	}
	if !results[0].AlertsClearedByCurrencyChange {
		t.Fatal("AlertsClearedByCurrencyChange = false, want true (BelowPrice was live and got cleared)")
	}

	var buf bytes.Buffer
	n := &Notifier{Out: &buf}
	n.Notify(results[0])

	out := buf.String()
	if !strings.Contains(out, "CURRENCY CHANGED") {
		t.Errorf("Notify output = %q, want it to contain \"CURRENCY CHANGED\"", out)
	}
	if !strings.Contains(out, "EUR") {
		t.Errorf("Notify output = %q, want it to mention the new currency EUR", out)
	}
}

// TestCheckOneNoCurrencyClearNotificationWhenNoThresholdWasSet is the
// negative case: a currency mismatch with no BelowPrice/AlertDropAbs set has
// nothing to clear, so AlertsClearedByCurrencyChange must stay false and no
// "CURRENCY CHANGED" line should print.
func TestCheckOneNoCurrencyClearNotificationWhenNoThresholdWasSet(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)

	_, _, err := store.Add(Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2026-07-01",
		Currency:    "USD",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	checker := &sequencedChecker{steps: []struct {
		price    float64
		currency string
	}{
		{price: 450, currency: "EUR"},
	}}

	results := CheckAll(context.Background(), store, checker)
	if len(results) != 1 || results[0].Error != nil {
		t.Fatalf("CheckAll: results=%+v", results)
	}
	if results[0].AlertsClearedByCurrencyChange {
		t.Fatal("AlertsClearedByCurrencyChange = true, want false: no threshold was set to clear")
	}

	var buf bytes.Buffer
	n := &Notifier{Out: &buf}
	n.Notify(results[0])
	if strings.Contains(buf.String(), "CURRENCY CHANGED") {
		t.Errorf("Notify output = %q, want no CURRENCY CHANGED line when nothing was cleared", buf.String())
	}
}
