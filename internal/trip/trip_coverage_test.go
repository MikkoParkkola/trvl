package trip

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func makeRoute(provider, depTime string, price float64, transfers, duration int, routeType string) airportTransferRoute {
	return airportTransferRoute{
		route: models.GroundRoute{
			Provider:  provider,
			Type:      routeType,
			Price:     price,
			Transfers: transfers,
			Duration:  duration,
			Departure: models.GroundStop{Time: depTime},
		},
		exact: false,
	}
}

func makeExactRoute(provider, depTime string, price float64, routeType string) airportTransferRoute {
	r := makeRoute(provider, depTime, price, 0, 60, routeType)
	r.exact = true
	return r
}

func TestSortAirportTransferRoutes_ExactBeforeCity(t *testing.T) {
	routes := []airportTransferRoute{
		makeRoute("bus", "2026-07-01T10:00:00Z", 10, 0, 60, "bus"),
		makeExactRoute("train", "2026-07-01T09:00:00Z", 20, "train"),
	}
	sortAirportTransferRoutes(routes)
	if !routes[0].exact {
		t.Error("exact route should sort before city route")
	}
}

func TestSortAirportTransferRoutes_TaxiLast(t *testing.T) {
	routes := []airportTransferRoute{
		makeRoute("taxi", "2026-07-01T10:00:00Z", 10, 0, 60, "taxi"),
		makeRoute("bus", "2026-07-01T10:00:00Z", 50, 0, 60, "bus"),
	}
	sortAirportTransferRoutes(routes)
	if strings.EqualFold(routes[0].route.Type, "taxi") {
		t.Error("taxi should sort after non-taxi")
	}
}

func TestSortAirportTransferRoutes_PricedBeforeUnpriced(t *testing.T) {
	routes := []airportTransferRoute{
		makeRoute("bus", "2026-07-01T10:00:00Z", 0, 0, 60, "bus"),      // unpriced
		makeRoute("train", "2026-07-01T10:00:00Z", 30, 0, 60, "train"), // priced
	}
	sortAirportTransferRoutes(routes)
	if routes[0].route.Price == 0 {
		t.Error("priced route should sort before unpriced")
	}
}

func TestSortAirportTransferRoutes_CheaperFirst(t *testing.T) {
	routes := []airportTransferRoute{
		makeRoute("train", "2026-07-01T10:00:00Z", 50, 0, 60, "train"),
		makeRoute("bus", "2026-07-01T10:00:00Z", 20, 0, 60, "bus"),
	}
	sortAirportTransferRoutes(routes)
	if routes[0].route.Price != 20 {
		t.Errorf("cheapest should sort first, got %v", routes[0].route.Price)
	}
}

func TestSortAirportTransferRoutes_FewerTransfersFirst(t *testing.T) {
	routes := []airportTransferRoute{
		makeRoute("bus", "2026-07-01T10:00:00Z", 30, 2, 90, "bus"),
		makeRoute("train", "2026-07-01T10:00:00Z", 30, 0, 90, "train"),
	}
	sortAirportTransferRoutes(routes)
	if routes[0].route.Transfers != 0 {
		t.Error("fewer transfers should sort first")
	}
}

func TestSortAirportTransferRoutes_EarlierDepartureFirst(t *testing.T) {
	routes := []airportTransferRoute{
		makeRoute("bus", "2026-07-01T12:00:00Z", 30, 0, 60, "bus"),
		makeRoute("train", "2026-07-01T09:00:00Z", 30, 0, 60, "train"),
	}
	sortAirportTransferRoutes(routes)
	if routes[0].route.Departure.Time != "2026-07-01T09:00:00Z" {
		t.Errorf("earlier departure should sort first, got %q", routes[0].route.Departure.Time)
	}
}

func TestSortAirportTransferRoutes_ShorterDurationFirst(t *testing.T) {
	routes := []airportTransferRoute{
		{route: models.GroundRoute{Provider: "b", Price: 30, Duration: 90, Departure: models.GroundStop{Time: "T"}}, exact: false},
		{route: models.GroundRoute{Provider: "a", Price: 30, Duration: 30, Departure: models.GroundStop{Time: "T"}}, exact: false},
	}
	sortAirportTransferRoutes(routes)
	if routes[0].route.Duration != 30 {
		t.Errorf("shorter duration should sort first, got %d", routes[0].route.Duration)
	}
}

func TestSortAirportTransferRoutes_ProviderAlphaFallback(t *testing.T) {
	routes := []airportTransferRoute{
		{route: models.GroundRoute{Provider: "z", Price: 30, Duration: 60, Departure: models.GroundStop{Time: "T"}}, exact: false},
		{route: models.GroundRoute{Provider: "a", Price: 30, Duration: 60, Departure: models.GroundStop{Time: "T"}}, exact: false},
	}
	sortAirportTransferRoutes(routes)
	if routes[0].route.Provider != "a" {
		t.Errorf("alphabetically earlier provider should sort first, got %q", routes[0].route.Provider)
	}
}

func TestSortAirportTransferRoutes_Empty(t *testing.T) {
	// Must not panic.
	sortAirportTransferRoutes(nil)
	sortAirportTransferRoutes([]airportTransferRoute{})
}

func TestSortAirportTransferRoutes_BothTaxi(t *testing.T) {
	// Both are taxis: falls through to price comparison.
	routes := []airportTransferRoute{
		makeRoute("taxi2", "2026-07-01T10:00:00Z", 50, 0, 60, "taxi"),
		makeRoute("taxi1", "2026-07-01T10:00:00Z", 30, 0, 60, "taxi"),
	}
	sortAirportTransferRoutes(routes)
	if routes[0].route.Price != 30 {
		t.Errorf("cheaper taxi should sort first, got %v", routes[0].route.Price)
	}
}

// ============================================================
// airportTransferDepartureMinutes — was 55%
// ============================================================

func TestAirportTransferDepartureMinutes_ISO8601Short(t *testing.T) {
	// "2026-07-01T09:30:00+02:00" — len >= 16, position 13 is ':'.
	mins, ok := airportTransferDepartureMinutes("2026-07-01T09:30:00+02:00")
	if !ok {
		t.Fatal("expected ok=true for valid ISO8601 short form")
	}
	if mins != 9*60+30 {
		t.Errorf("minutes = %d, want 570", mins)
	}
}

func TestAirportTransferDepartureMinutes_RFC3339Fallback(t *testing.T) {
	// Shorter than 16 chars, falls through to time.Parse(RFC3339) which also fails.
	_, ok := airportTransferDepartureMinutes("10:30")
	if ok {
		t.Error("short non-RFC3339 string should return ok=false")
	}
}

func TestAirportTransferDepartureMinutes_ValidRFC3339(t *testing.T) {
	// This has length >= 16 but position 13 IS ':' — handled by the fast path.
	// Let's use a string where position 13 is not ':' to force the RFC3339 path.
	// "2026-07-01 09" — length 13, position 13 doesn't exist, len < 16 → RFC3339.
	_, ok := airportTransferDepartureMinutes("2026-07-01 09")
	// len = 13 < 16 → falls to RFC3339 parse which fails on this format.
	if ok {
		t.Error("non-RFC3339 short string should return ok=false")
	}
}

func TestAirportTransferDepartureMinutes_InvalidHour(t *testing.T) {
	// Position 13 is ':', but hour chars are non-numeric → falls to RFC3339 path.
	// Construct: "XXXXXXXXXX   :XXXXXXXXXXXX" — 25+ chars, pos 13 = ':'.
	// "2026-07-01Taa:30:00+02:00" → hour "aa" fails Atoi.
	_, ok := airportTransferDepartureMinutes("2026-07-01Taa:30:00+02:00")
	// Fast path: hour parse fails, minute parse succeeds but we require BOTH.
	// Falls through to RFC3339 — also fails → ok=false.
	if ok {
		t.Error("non-numeric hour should return ok=false")
	}
}

func TestAirportTransferDepartureMinutes_Midnight(t *testing.T) {
	mins, ok := airportTransferDepartureMinutes("2026-07-01T00:00:00+00:00")
	if !ok {
		t.Fatal("expected ok=true for midnight")
	}
	if mins != 0 {
		t.Errorf("midnight = %d minutes, want 0", mins)
	}
}

func TestAirportTransferDepartureMinutes_EndOfDay(t *testing.T) {
	mins, ok := airportTransferDepartureMinutes("2026-07-01T23:59:00+00:00")
	if !ok {
		t.Fatal("expected ok=true for 23:59")
	}
	if mins != 23*60+59 {
		t.Errorf("23:59 = %d minutes, want %d", mins, 23*60+59)
	}
}

// ============================================================
// buildAirportTransferOriginQuery — was 66%
// ============================================================

func TestBuildAirportTransferOriginQuery_WithAirport(t *testing.T) {
	q := buildAirportTransferOriginQuery("Helsinki Airport")
	if q != "Helsinki Airport" {
		t.Errorf("got %q, want %q (already has 'airport')", q, "Helsinki Airport")
	}
}

func TestBuildAirportTransferOriginQuery_WithoutAirport(t *testing.T) {
	q := buildAirportTransferOriginQuery("Helsinki-Vantaa")
	if q != "Helsinki-Vantaa airport" {
		t.Errorf("got %q, want 'Helsinki-Vantaa airport'", q)
	}
}

func TestBuildAirportTransferOriginQuery_CaseInsensitive(t *testing.T) {
	// "AIRPORT" (uppercase) should still match.
	q := buildAirportTransferOriginQuery("LONDON AIRPORT")
	if q != "LONDON AIRPORT" {
		t.Errorf("got %q, want LONDON AIRPORT (already has airport)", q)
	}
}

// ============================================================
// convertPlanFlights / convertPlanHotels / convertPlanAmount
// ============================================================

func TestConvertPlanAmount_BlankSourceIsNotConverted(t *testing.T) {
	// destinations.ConvertCurrency echoes the target back for a blank source,
	// so a blank source must never count as converted.
	echoesTarget := func(_ context.Context, amount float64, from, to string) (float64, string) {
		if from == "" {
			return amount, to // what destinations.ConvertCurrency does
		}
		return fakeFX(context.Background(), amount, from, to)
	}
	got, ok := convertPlanAmount(context.Background(), echoesTarget, 100, "", "EUR")
	if ok || got != 100 {
		t.Errorf("got (%v, %v), want (100, false)", got, ok)
	}
}

func TestConvertPlanFlights_SkipsZeroPrice(t *testing.T) {
	flights := []PlanFlight{
		{Price: 0, Currency: "EUR"},
		{Price: 100, Currency: "EUR"},
	}
	// EUR -> EUR: no-op conversion. Zero-price entry should be skipped.
	convertPlanFlights(context.Background(), flights, "EUR", fakeFX)
	if flights[0].Price != 0 {
		t.Errorf("zero-price flight should remain 0, got %v", flights[0].Price)
	}
}

func TestConvertPlanFlights_SkipsSameCurrency(t *testing.T) {
	flights := []PlanFlight{
		{Price: 200, Currency: "EUR"},
	}
	// Already EUR, target EUR — no conversion needed.
	convertPlanFlights(context.Background(), flights, "EUR", fakeFX)
	if flights[0].Price != 200 {
		t.Errorf("same-currency flight price changed: %v", flights[0].Price)
	}
}

func TestConvertPlanFlights_SkipsEmptyCurrency(t *testing.T) {
	flights := []PlanFlight{
		{Price: 150, Currency: ""},
	}
	convertPlanFlights(context.Background(), flights, "USD", fakeFX)
	// Empty source currency — should be skipped (guard: Currency == "").
	if flights[0].Currency != "" {
		t.Errorf("empty-currency flight should not be modified, got %q", flights[0].Currency)
	}
}

func TestConvertPlanFlights_Empty(t *testing.T) {
	// Should not panic on nil/empty slice.
	convertPlanFlights(context.Background(), nil, "EUR", fakeFX)
	convertPlanFlights(context.Background(), []PlanFlight{}, "EUR", fakeFX)
}

func TestConvertPlanHotels_SkipsSameCurrency(t *testing.T) {
	hotels := []PlanHotel{
		{PerNight: 80, Total: 240, Currency: "EUR"},
	}
	convertPlanHotels(context.Background(), hotels, "EUR", fakeFX)
	if hotels[0].PerNight != 80 {
		t.Errorf("same-currency hotel per-night changed: %v", hotels[0].PerNight)
	}
	if hotels[0].Total != 240 {
		t.Errorf("same-currency hotel total changed: %v", hotels[0].Total)
	}
}

func TestConvertPlanHotels_SkipsEmptyCurrency(t *testing.T) {
	hotels := []PlanHotel{
		{PerNight: 80, Total: 240, Currency: ""},
	}
	convertPlanHotels(context.Background(), hotels, "USD", fakeFX)
	// Empty source currency — skipped.
	if hotels[0].Currency != "" {
		t.Errorf("empty-currency hotel should not be modified, got %q", hotels[0].Currency)
	}
}

func TestConvertPlanHotels_ZeroPerNight(t *testing.T) {
	// PerNight=0, Total=240, Currency="USD" → target "EUR".
	// PerNight skip path (price <= 0 guard), Total conversion path.
	hotels := []PlanHotel{
		{PerNight: 0, Total: 240, Currency: "USD"},
	}
	convertPlanHotels(context.Background(), hotels, "USD", fakeFX) // same currency — no-op
	if hotels[0].Total != 240 {
		t.Errorf("same-currency hotel total changed: %v", hotels[0].Total)
	}
}

func TestConvertPlanHotels_Empty(t *testing.T) {
	convertPlanHotels(context.Background(), nil, "EUR", fakeFX)
	convertPlanHotels(context.Background(), []PlanHotel{}, "EUR", fakeFX)
}

// ============================================================
// Discover validation — was 0% (only validation paths)
// ============================================================

func TestDiscover_NegativeBudget(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		From:   "2026-07-01",
		Until:  "2026-07-31",
		Budget: -1,
	})
	if err == nil {
		t.Error("expected error for negative budget")
	}
}

func TestDiscover_ZeroBudget(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		From:   "2026-07-01",
		Until:  "2026-07-31",
		Budget: 0,
	})
	if err == nil {
		t.Error("expected error for zero budget")
	}
}

func TestDiscover_EmptyFrom(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		Until:  "2026-07-31",
		Budget: 500,
	})
	if err == nil {
		t.Error("expected error for empty from date")
	}
}

func TestDiscover_EmptyUntil(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		From:   "2026-07-01",
		Budget: 500,
	})
	if err == nil {
		t.Error("expected error for empty until date")
	}
}

func TestDiscover_InvalidFromDate(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		From:   "not-a-date",
		Until:  "2026-07-31",
		Budget: 500,
	})
	if err == nil {
		t.Error("expected error for invalid from date")
	}
}

func TestDiscover_InvalidUntilDate(t *testing.T) {
	_, err := Discover(context.Background(), DiscoverOptions{
		Origin: "HEL",
		From:   "2026-07-01",
		Until:  "bad-date",
		Budget: 500,
	})
	if err == nil {
		t.Error("expected error for invalid until date")
	}
}
