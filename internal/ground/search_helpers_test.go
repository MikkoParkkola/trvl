package ground

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestBrowserFallbacksEnabled(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "")
	if browserFallbacksEnabled(SearchOptions{}) {
		t.Fatal("expected browser fallbacks to be disabled by default")
	}

	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "true")
	if !browserFallbacksEnabled(SearchOptions{}) {
		t.Fatal("expected environment opt-in to enable browser fallbacks")
	}

	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "definitely-not-bool")
	if browserFallbacksEnabled(SearchOptions{}) {
		t.Fatal("expected invalid environment value to keep browser fallbacks disabled")
	}

	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "")
	if !browserFallbacksEnabled(SearchOptions{AllowBrowserFallbacks: true}) {
		t.Fatal("expected explicit option to enable browser fallbacks")
	}
}

func TestDeduplicateGroundRoutes(t *testing.T) {
	routes := []models.GroundRoute{
		{
			Provider: "trainline",
			Price:    49,
			Departure: models.GroundStop{
				Time: "2026-07-01T08:00:00",
			},
			Arrival: models.GroundStop{
				Time: "2026-07-01T10:00:00",
			},
		},
		{
			Provider: "trainline",
			Price:    49,
			Departure: models.GroundStop{
				Time: "2026-07-01T08:00:00",
			},
			Arrival: models.GroundStop{
				Time: "2026-07-01T10:00:00",
			},
		},
		{
			Provider: "trainline",
			Price:    55,
			Departure: models.GroundStop{
				Time: "2026-07-01T08:00:00",
			},
			Arrival: models.GroundStop{
				Time: "2026-07-01T10:00:00",
			},
		},
	}

	deduped := deduplicateGroundRoutes(routes)
	if len(deduped) != 2 {
		t.Fatalf("expected 2 unique routes, got %d", len(deduped))
	}
	if deduped[0].Price != 49 || deduped[1].Price != 55 {
		t.Fatalf("unexpected deduplicated prices: %#v", deduped)
	}
}

func TestFilterUnavailableGroundRoutes(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Price: 0},
		{Provider: "transitous", Price: 0},
		{Provider: "db", Price: 0},
		{Provider: "trainline", Price: 39},
	}

	filtered := filterUnavailableGroundRoutes(routes)
	if len(filtered) != 3 {
		t.Fatalf("expected 3 routes after filtering, got %d", len(filtered))
	}
	if filtered[0].Provider != "transitous" {
		t.Fatalf("expected schedule-only transitous route to be kept, got %q", filtered[0].Provider)
	}
	if filtered[1].Provider != "db" {
		t.Fatalf("expected schedule-only db route to be kept, got %q", filtered[1].Provider)
	}
	if filtered[2].Provider != "trainline" {
		t.Fatalf("expected priced route to be kept, got %q", filtered[2].Provider)
	}
}

func TestFilterGroundRoutes(t *testing.T) {
	routes := []models.GroundRoute{
		{
			Provider: "flixbus",
			Type:     "bus",
			Price:    0,
		},
		{
			Provider: "trainline",
			Type:     "bus",
			Price:    19,
			Departure: models.GroundStop{
				Time: "2026-07-01T08:00:00",
			},
			Arrival: models.GroundStop{
				Time: "2026-07-01T10:00:00",
			},
		},
		{
			Provider: "trainline",
			Type:     "bus",
			Price:    19,
			Departure: models.GroundStop{
				Time: "2026-07-01T08:00:00",
			},
			Arrival: models.GroundStop{
				Time: "2026-07-01T10:00:00",
			},
		},
		{
			Provider: "trainline",
			Type:     "train",
			Price:    49,
			Departure: models.GroundStop{
				Time: "2026-07-01T09:00:00",
			},
			Arrival: models.GroundStop{
				Time: "2026-07-01T11:00:00",
			},
		},
		{
			Provider: "transitous",
			Type:     "train",
			Price:    0,
			Departure: models.GroundStop{
				Time: "2026-07-01T09:30:00",
			},
			Arrival: models.GroundStop{
				Time: "2026-07-01T11:30:00",
			},
		},
	}

	filtered := filterGroundRoutes(routes, SearchOptions{
		MaxPrice: 25,
		Type:     "bus",
	})

	if len(filtered) != 1 {
		t.Fatalf("expected 1 filtered route, got %d", len(filtered))
	}
	if filtered[0].Provider != "trainline" {
		t.Fatalf("expected trainline route to survive filtering, got %q", filtered[0].Provider)
	}
	if filtered[0].Price != 19 {
		t.Fatalf("expected filtered route price 19, got %v", filtered[0].Price)
	}
}

// ============================================================
// normalizeGroundCurrencies + sortGroundRoutes cross-currency tests
// All use stub converters (never real destinations.ConvertCurrency).
// ============================================================

func makeGroundRoute(provider, curr string, price float64, depTime, arrTime string) models.GroundRoute {
	return models.GroundRoute{
		Provider: provider,
		Type:     "bus",
		Price:    price,
		Currency: curr,
		Duration: 120,
		Departure: models.GroundStop{
			City: "Origin",
			Time: depTime,
		},
		Arrival: models.GroundStop{
			City: "Dest",
			Time: arrTime,
		},
		Legs:       []models.GroundLeg{{Type: "bus", Provider: provider}},
		BookingURL: "https://example.com/" + provider,
	}
}

func TestNormalizeGroundCurrencies_MixedSuccess_Ranking(t *testing.T) {
	// 110 EUR vs 119 USD; stub makes 119 USD convert to ~109.48 EUR.
	// After norm+sort the USD-converted must rank #1 (the raw numeric trap).
	routes := []models.GroundRoute{
		makeGroundRoute("flix", "EUR", 110, "2026-07-01T08:00", "2026-07-01T10:00"),
		makeGroundRoute("regio", "USD", 119, "2026-07-01T09:00", "2026-07-01T11:00"),
	}
	conv := func(ctx context.Context, amount float64, from, to string) (float64, bool) {
		if strings.EqualFold(from, "USD") && strings.EqualFold(to, "EUR") {
			return amount * 0.92, true // 119 * 0.92 = 109.48 < 110
		}
		return 0, false
	}
	normalizeGroundCurrencies(context.Background(), routes, "EUR", conv)
	// resolve not strictly needed (distinct keys), but run for fidelity
	routes = models.ResolveGroundSources(routes)
	sortGroundRoutes(routes)

	if len(routes) != 2 {
		t.Fatalf("got %d routes", len(routes))
	}
	// first should be the (converted) cheaper USD one, now in EUR
	if routes[0].Currency != "EUR" || routes[0].ComparablePrice <= 0 {
		t.Errorf("first route should be normalized EUR with ComparablePrice set: %+v", routes[0])
	}
	if routes[0].PriceForRanking() >= routes[1].PriceForRanking() {
		t.Errorf("expected converted USD route cheaper for ranking: got %.2f then %.2f",
			routes[0].PriceForRanking(), routes[1].PriceForRanking())
	}
	// the originally 119 now shows converted price
	if routes[0].PriceForRanking() > 110 {
		t.Errorf("converted price should be <110, got %v", routes[0].PriceForRanking())
	}
}

func TestNormalizeGroundCurrencies_FXUnavailable_IncomparableTail(t *testing.T) {
	// Stub refuses all FX; target EUR routes get Comparable, others stay raw.
	// Sort must put target-curr group first, then tail grouped by curr code (alpha) then price.
	routes := []models.GroundRoute{
		makeGroundRoute("p1", "USD", 50, "2026-07-01T08:00", "2026-07-01T10:00"),
		makeGroundRoute("p2", "EUR", 120, "2026-07-01T09:00", "2026-07-01T11:00"),
		makeGroundRoute("p3", "GBP", 90, "2026-07-01T10:00", "2026-07-01T12:00"),
		makeGroundRoute("p4", "EUR", 95, "2026-07-01T07:00", "2026-07-01T09:00"),
	}
	conv := func(ctx context.Context, amount float64, from, to string) (float64, bool) {
		return 0, false // FX unavailable
	}
	normalizeGroundCurrencies(context.Background(), routes, "EUR", conv)
	routes = models.ResolveGroundSources(routes)
	sortGroundRoutes(routes)

	if len(routes) != 4 {
		t.Fatalf("got %d", len(routes))
	}
	// first two should be the EUR ones (comparable), sorted by price 95 then 120
	if routes[0].Currency != "EUR" || routes[0].ComparablePrice == 0 || routes[0].PriceForRanking() != 95 {
		t.Errorf("first should be EUR 95 comparable: %+v", routes[0])
	}
	if routes[1].Currency != "EUR" || routes[1].PriceForRanking() != 120 {
		t.Errorf("second should be EUR 120: %+v", routes[1])
	}
	// then incomparable tail: GBP (alpha before USD) then the USD
	if routes[2].Currency != "GBP" {
		t.Errorf("third should be GBP group start, got %q", routes[2].Currency)
	}
	if routes[3].Currency != "USD" {
		t.Errorf("fourth should be USD, got %q", routes[3].Currency)
	}
	// ensure no cross-curr numeric lie: the raw 50 USD must NOT be first (it is in tail)
	if routes[0].Price == 50 || routes[0].Currency == "USD" {
		t.Error("raw foreign cheap price incorrectly ranked before target-currency group")
	}
}

func TestNormalizeGroundCurrencies_PermutationIndependence(t *testing.T) {
	// Different input order must yield identical final order after norm+resolve+sort.
	base := []models.GroundRoute{
		makeGroundRoute("a", "EUR", 100, "2026-07-01T08:00", "2026-07-01T10:00"),
		makeGroundRoute("b", "USD", 80, "2026-07-01T09:00", "2026-07-01T11:00"),
		makeGroundRoute("c", "EUR", 90, "2026-07-01T07:00", "2026-07-01T09:00"),
	}
	conv := func(ctx context.Context, amount float64, from, to string) (float64, bool) {
		if strings.EqualFold(from, "USD") && strings.EqualFold(to, "EUR") {
			return amount * 0.9, true // 72 EUR
		}
		return amount, strings.EqualFold(from, to)
	}

	// process original order
	r1 := append([]models.GroundRoute(nil), base...)
	normalizeGroundCurrencies(context.Background(), r1, "EUR", conv)
	r1 = models.ResolveGroundSources(r1)
	sortGroundRoutes(r1)

	// reversed input order
	r2 := append([]models.GroundRoute(nil), base[2], base[1], base[0])
	normalizeGroundCurrencies(context.Background(), r2, "EUR", conv)
	r2 = models.ResolveGroundSources(r2)
	sortGroundRoutes(r2)

	if len(r1) != len(r2) {
		t.Fatalf("len differ %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i].Provider != r2[i].Provider ||
			r1[i].PriceForRanking() != r2[i].PriceForRanking() ||
			r1[i].Currency != r2[i].Currency {
			t.Errorf("order mismatch at %d: %+v vs %+v", i, r1[i], r2[i])
		}
	}
}

func TestNormalizeGroundCurrencies_MaxPriceRespectsNormalization(t *testing.T) {
	// A route at raw 50 "USD" that converts to 125 "EUR" must be excluded by MaxPrice=100 (in target).
	// A route at 80 EUR (stays) must survive.
	routes := []models.GroundRoute{
		makeGroundRoute("cheapraw", "USD", 50, "2026-07-01T08:00", "2026-07-01T10:00"),
		makeGroundRoute("normal", "EUR", 80, "2026-07-01T09:00", "2026-07-01T11:00"),
	}
	conv := func(ctx context.Context, amount float64, from, to string) (float64, bool) {
		if strings.EqualFold(from, "USD") && strings.EqualFold(to, "EUR") {
			return 125.0, true // expensive after conversion
		}
		return amount, true
	}
	normalizeGroundCurrencies(context.Background(), routes, "EUR", conv)

	opts := SearchOptions{MaxPrice: 100, Currency: "EUR"}
	filtered := filterGroundRoutes(routes, opts)
	// note: we normalize before filter in real path; here we simulate the state after norm

	if len(filtered) != 1 || filtered[0].Provider != "normal" {
		t.Fatalf("expected only the EUR 80 to survive MaxPrice after norm, got %+v", filtered)
	}
	if filtered[0].Price > 100 {
		t.Error("surviving route should be under 100")
	}
}
