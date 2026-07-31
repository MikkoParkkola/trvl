package watch

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestCheckOneFlagsLastMinuteHotelDeal(t *testing.T) {
	dir := t.TempDir()
	store := NewStore(dir)
	checkIn := time.Now().Add(24 * time.Hour).Format("2006-01-02")
	checkOut := time.Now().Add(48 * time.Hour).Format("2006-01-02")
	w := Watch{
		Type:              "hotel",
		Destination:       "Prague",
		DepartDate:        checkIn,
		ReturnDate:        checkOut,
		LastPrice:         200,
		LowestPrice:       200,
		Currency:          "EUR",
		LastMinuteMode:    true,
		LastMinuteDropPct: 25,
	}
	id, _, err := store.Add(w)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	w.ID = id

	result := checkOne(context.Background(), store, &stubPriceChecker{price: 145, currency: "EUR"}, w)

	if result.Error != nil {
		t.Fatalf("checkOne: %v", result.Error)
	}
	if !result.LastMinuteDeal {
		t.Fatalf("LastMinuteDeal = false, want true: %+v", result)
	}
	if result.LastMinuteDiscountPercent < 27.4 || result.LastMinuteDiscountPercent > 27.6 {
		t.Fatalf("LastMinuteDiscountPercent = %.2f, want about 27.5", result.LastMinuteDiscountPercent)
	}
	updated, ok := store.Get(id)
	if !ok {
		t.Fatal("updated watch not found")
	}
	if updated.LastPrice != 145 {
		t.Fatalf("LastPrice = %.0f, want 145", updated.LastPrice)
	}
}

func TestNotifierPrintsLastMinuteHotelDeal(t *testing.T) {
	var out bytes.Buffer
	notifier := &Notifier{Out: &out, UseColor: false}

	notifier.Notify(CheckResult{
		Watch: Watch{
			Type:              "hotel",
			Destination:       "Prague",
			LastPrice:         200,
			LastMinuteMode:    true,
			LastMinuteDropPct: 25,
		},
		NewPrice:                  145,
		PrevPrice:                 200,
		PriceDrop:                 -55,
		Currency:                  "EUR",
		LastMinuteDeal:            true,
		LastMinuteDiscountPercent: 27.5,
	})

	got := out.String()
	for _, want := range []string{"LAST-MINUTE", "Prague", "145 EUR", "27.5%"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output %q missing %q", got, want)
		}
	}
}
