package explore

import (
	"net/url"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestEncodeExplorePayload_CabinClass asserts the cabin class is encoded into
// the payload's class slot (business=3) instead of the hardcoded economy.
func TestEncodeExplorePayload_CabinClass(t *testing.T) {
	business := EncodeExplorePayload("HEL", ExploreOptions{
		DepartureDate: "2026-06-15",
		CabinClass:    models.Business, // 3
	})
	economy := EncodeExplorePayload("HEL", ExploreOptions{
		DepartureDate: "2026-06-15", // CabinClass unset -> economy (1)
	})
	if business == economy {
		t.Fatal("business-class payload should differ from economy payload")
	}
	// Decode the URL-escaped payload; the class slot sits between "[]," and the
	// traveller tuple "[1,0,0,0]".
	bizRaw, err := url.QueryUnescape(business)
	if err != nil {
		t.Fatalf("unescape business: %v", err)
	}
	ecoRaw, err := url.QueryUnescape(economy)
	if err != nil {
		t.Fatalf("unescape economy: %v", err)
	}
	if !strings.Contains(bizRaw, `[],3,[1,0,0,0]`) {
		t.Errorf("business payload should encode class 3 in the class slot")
	}
	if !strings.Contains(ecoRaw, `[],1,[1,0,0,0]`) {
		t.Errorf("economy payload should encode class 1 by default")
	}
}

// TestFilterByBudget asserts the client-side budget cap is authoritative:
// over-budget and zero-price destinations are dropped, the cap is inclusive,
// and a zero cap is a no-op.
func TestFilterByBudget(t *testing.T) {
	dests := []models.ExploreDestination{
		{CityName: "Lisbon", Price: 120},
		{CityName: "Rome", Price: 200}, // exactly at cap -> kept
		{CityName: "Tokyo", Price: 900},
		{CityName: "Nameless", Price: 0}, // no price -> dropped under a cap
	}

	capped := filterByBudget(dests, 200)
	if len(capped) != 2 {
		t.Fatalf("expected 2 destinations <= 200, got %d", len(capped))
	}
	for _, d := range capped {
		if d.Price <= 0 || d.Price > 200 {
			t.Errorf("kept out-of-range destination %s @ %.0f", d.CityName, d.Price)
		}
	}

	// No cap -> no-op.
	all := []models.ExploreDestination{{Price: 50}, {Price: 5000}}
	if got := filterByBudget(all, 0); len(got) != 2 {
		t.Errorf("zero cap should be a no-op, got %d of 2", len(got))
	}
}
