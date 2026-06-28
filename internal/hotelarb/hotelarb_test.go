package hotelarb

import (
	"path/filepath"
	"testing"
	"time"
)

func TestEvaluateRebookRecommendsManualRebookWhenRefundableStayDrops(t *testing.T) {
	hold := Hold{
		ID:            "hold_1",
		HotelName:     "Canal House",
		OriginalPrice: 420,
		Currency:      "EUR",
		Refundable:    true,
		Provider:      "Booking",
	}
	quote := PriceQuote{Price: 330, Currency: "EUR", Provider: "Google Hotels"}

	decision := EvaluateRebook(hold, quote, RebookOptions{MinSavings: 25})

	if decision.Action != ActionRebookLowerPrice {
		t.Fatalf("Action = %q, want %q", decision.Action, ActionRebookLowerPrice)
	}
	if decision.Savings != 90 {
		t.Fatalf("Savings = %.0f, want 90", decision.Savings)
	}
	if decision.SavingsPercent < 21.4 || decision.SavingsPercent > 21.5 {
		t.Fatalf("SavingsPercent = %.2f, want about 21.43", decision.SavingsPercent)
	}
	if !decision.ManualConfirmRequired {
		t.Fatal("ManualConfirmRequired should be true before any re-book action")
	}
	if decision.Reason == "" {
		t.Fatal("expected human-readable decision reason")
	}
}

func TestEvaluateRebookHoldsCurrentWhenSavingsDoNotClearFloor(t *testing.T) {
	hold := Hold{ID: "hold_2", HotelName: "Central Hotel", OriginalPrice: 200, Currency: "EUR", Refundable: true}
	quote := PriceQuote{Price: 190, Currency: "EUR", Provider: "Direct"}

	decision := EvaluateRebook(hold, quote, RebookOptions{MinSavings: 25})

	if decision.Action != ActionHoldCurrent {
		t.Fatalf("Action = %q, want %q", decision.Action, ActionHoldCurrent)
	}
	if decision.Savings != 10 {
		t.Fatalf("Savings = %.0f, want 10", decision.Savings)
	}
	if decision.ManualConfirmRequired {
		t.Fatal("hold-current decision should not require manual re-book confirmation")
	}
}

func TestDetectLastMinuteDealRequiresSub48HoursAnd25PercentDrop(t *testing.T) {
	now := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)
	checkIn := now.Add(36 * time.Hour)

	signal := DetectLastMinuteDeal(now, checkIn, 200, 145, LastMinuteOptions{})

	if !signal.Triggered {
		t.Fatalf("Triggered = false, want true: %+v", signal)
	}
	if signal.DiscountPercent < 27.4 || signal.DiscountPercent > 27.6 {
		t.Fatalf("DiscountPercent = %.2f, want about 27.5", signal.DiscountPercent)
	}
	if signal.WindowHours < 35.9 || signal.WindowHours > 36.1 {
		t.Fatalf("WindowHours = %.2f, want about 36", signal.WindowHours)
	}
}

func TestDetectLastMinuteDealRejectsOutside48Hours(t *testing.T) {
	now := time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC)
	checkIn := now.Add(72 * time.Hour)

	signal := DetectLastMinuteDeal(now, checkIn, 200, 100, LastMinuteOptions{})

	if signal.Triggered {
		t.Fatalf("Triggered = true outside the 48h window: %+v", signal)
	}
}

func TestComparePointsArbitrageChoosesHotelPointsWhenOpportunityCostBeatsCash(t *testing.T) {
	result, err := ComparePointsArbitrage(PointsArbitrageInput{
		CashPrice: 300,
		Currency:  "USD",
		Offers: []PointsOffer{
			{Program: "world-of-hyatt", PointsRequired: 12000},
			{Program: "hilton-honors", PointsRequired: 80000},
		},
	})
	if err != nil {
		t.Fatalf("ComparePointsArbitrage: %v", err)
	}

	if result.Recommendation != RecommendUsePoints {
		t.Fatalf("Recommendation = %q, want %q", result.Recommendation, RecommendUsePoints)
	}
	if result.BestOffer.ProgramSlug != "world-of-hyatt" {
		t.Fatalf("BestOffer.ProgramSlug = %q, want world-of-hyatt", result.BestOffer.ProgramSlug)
	}
	if result.BestOffer.CentsPerPoint < 2.49 || result.BestOffer.CentsPerPoint > 2.51 {
		t.Fatalf("CentsPerPoint = %.2f, want 2.50", result.BestOffer.CentsPerPoint)
	}
}

func TestComparePointsArbitrageChoosesCashWhenPointsAreBelowFloor(t *testing.T) {
	result, err := ComparePointsArbitrage(PointsArbitrageInput{
		CashPrice: 100,
		Currency:  "USD",
		Offers: []PointsOffer{
			{Program: "hilton-honors", PointsRequired: 50000},
		},
	})
	if err != nil {
		t.Fatalf("ComparePointsArbitrage: %v", err)
	}

	if result.Recommendation != RecommendPayCash {
		t.Fatalf("Recommendation = %q, want %q", result.Recommendation, RecommendPayCash)
	}
	if result.BestOffer.OpportunityCost != 200 {
		t.Fatalf("OpportunityCost = %.0f, want 200", result.BestOffer.OpportunityCost)
	}
}

func TestComparePointsArbitrageSupportsWyndhamRewards(t *testing.T) {
	result, err := ComparePointsArbitrage(PointsArbitrageInput{
		CashPrice: 180,
		Currency:  "USD",
		Offers: []PointsOffer{
			{Program: "wyndham-rewards", PointsRequired: 15000},
		},
	})
	if err != nil {
		t.Fatalf("ComparePointsArbitrage: %v", err)
	}

	if result.BestOffer.ProgramName != "Wyndham Rewards" {
		t.Fatalf("ProgramName = %q, want Wyndham Rewards", result.BestOffer.ProgramName)
	}
}

func TestHoldStoreRoundTripsActiveHoldsJSON(t *testing.T) {
	dir := t.TempDir()
	store := NewHoldStore(dir)

	id, err := store.Add(Hold{
		HotelName:     "Prague Stay",
		HotelID:       "/g/hotel",
		Location:      "Prague",
		CheckIn:       "2026-06-01",
		CheckOut:      "2026-06-04",
		OriginalPrice: 360,
		Currency:      "EUR",
		Refundable:    true,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if _, err := store.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	holds := store.List()
	if len(holds) != 1 {
		t.Fatalf("List length = %d, want 1", len(holds))
	}
	if holds[0].ID != id {
		t.Fatalf("stored ID = %q, want %q", holds[0].ID, id)
	}
	if got := store.Path(); got != filepath.Join(dir, "active_holds.json") {
		t.Fatalf("Path = %q, want active_holds.json under store dir", got)
	}
}

func TestHoldStoreGetUpdateAndRemove(t *testing.T) {
	store := NewHoldStore(t.TempDir())
	id, err := store.Add(Hold{
		HotelName:     "Editable Stay",
		CheckIn:       "2026-06-01",
		CheckOut:      "2026-06-03",
		OriginalPrice: 240,
		Currency:      "EUR",
		Refundable:    true,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	hold, ok := store.Get(id)
	if !ok {
		t.Fatalf("Get(%q) returned false", id)
	}
	hold.LastSeenPrice = 210
	if err := store.Update(hold); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, ok := store.Get(id)
	if !ok || updated.LastSeenPrice != 210 {
		t.Fatalf("updated hold = %#v, ok=%v", updated, ok)
	}

	removed, err := store.Remove(id)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !removed {
		t.Fatal("Remove returned false for existing hold")
	}
	if _, ok := store.Get(id); ok {
		t.Fatal("hold still present after Remove")
	}
	removed, err = store.Remove(id)
	if err != nil {
		t.Fatalf("Remove missing: %v", err)
	}
	if removed {
		t.Fatal("Remove returned true for missing hold")
	}
}

func TestDefaultHoldStoreUsesHomeTrvlDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	store, err := DefaultHoldStore()
	if err != nil {
		t.Fatalf("DefaultHoldStore: %v", err)
	}
	want := filepath.Join(home, ".trvl", "active_holds.json")
	if got := store.Path(); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}
