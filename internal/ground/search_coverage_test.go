package ground

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// ============================================================
// filterGroundRoutes — tests for dedup, MaxPrice, Type filter
// ============================================================

func TestFilterGroundRoutes_MaxPrice(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Price: 10, Currency: "EUR", Departure: models.GroundStop{Time: "2026-07-01T08:00"}, Arrival: models.GroundStop{Time: "2026-07-01T12:00"}},
		{Provider: "regiojet", Price: 50, Currency: "EUR", Departure: models.GroundStop{Time: "2026-07-01T09:00"}, Arrival: models.GroundStop{Time: "2026-07-01T13:00"}},
		{Provider: "flixbus", Price: 25, Currency: "EUR", Departure: models.GroundStop{Time: "2026-07-01T10:00"}, Arrival: models.GroundStop{Time: "2026-07-01T14:00"}},
	}
	opts := SearchOptions{MaxPrice: 30}
	filtered := filterGroundRoutes(routes, opts)
	for _, r := range filtered {
		if r.Price > 30 {
			t.Errorf("route with price %.2f should have been filtered (max 30)", r.Price)
		}
	}
	if len(filtered) != 2 {
		t.Errorf("expected 2 routes after MaxPrice filter, got %d", len(filtered))
	}
}

func TestFilterGroundRoutes_TypeFilter(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Type: "bus", Price: 10, Currency: "EUR", Departure: models.GroundStop{Time: "2026-07-01T08:00"}, Arrival: models.GroundStop{Time: "2026-07-01T12:00"}},
		{Provider: "db", Type: "train", Price: 50, Currency: "EUR", Departure: models.GroundStop{Time: "2026-07-01T09:00"}, Arrival: models.GroundStop{Time: "2026-07-01T13:00"}},
		{Provider: "regiojet", Type: "bus", Price: 25, Currency: "EUR", Departure: models.GroundStop{Time: "2026-07-01T10:00"}, Arrival: models.GroundStop{Time: "2026-07-01T14:00"}},
	}
	opts := SearchOptions{Type: "train"}
	filtered := filterGroundRoutes(routes, opts)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 train route, got %d", len(filtered))
	}
	if filtered[0].Type != "train" {
		t.Errorf("expected train, got %q", filtered[0].Type)
	}
}

func TestFilterGroundRoutes_Dedup(t *testing.T) {
	// Two routes from the same provider at the same time and price should dedup.
	routes := []models.GroundRoute{
		{Provider: "flixbus", Price: 19.99, Currency: "EUR", Departure: models.GroundStop{Time: "2026-07-01T08:00"}, Arrival: models.GroundStop{Time: "2026-07-01T12:00"}},
		{Provider: "flixbus", Price: 19.99, Currency: "EUR", Departure: models.GroundStop{Time: "2026-07-01T08:00"}, Arrival: models.GroundStop{Time: "2026-07-01T12:00"}},
	}
	filtered := filterGroundRoutes(routes, SearchOptions{})
	if len(filtered) != 1 {
		t.Errorf("expected 1 route after dedup, got %d", len(filtered))
	}
}

func TestFilterGroundRoutes_DedupDifferentProviders(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Price: 19.99, Currency: "EUR", Departure: models.GroundStop{Time: "2026-07-01T08:00"}, Arrival: models.GroundStop{Time: "2026-07-01T12:00"}},
		{Provider: "regiojet", Price: 19.99, Currency: "EUR", Departure: models.GroundStop{Time: "2026-07-01T08:00"}, Arrival: models.GroundStop{Time: "2026-07-01T12:00"}},
	}
	filtered := filterGroundRoutes(routes, SearchOptions{})
	if len(filtered) != 2 {
		t.Errorf("different providers should not dedup, got %d", len(filtered))
	}
}

func TestFilterGroundRoutes_CombinedMaxPriceAndType(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Type: "bus", Price: 10, Currency: "EUR", Departure: models.GroundStop{Time: "T08:00"}, Arrival: models.GroundStop{Time: "T12:00"}},
		{Provider: "db", Type: "train", Price: 50, Currency: "EUR", Departure: models.GroundStop{Time: "T09:00"}, Arrival: models.GroundStop{Time: "T13:00"}},
		{Provider: "db", Type: "train", Price: 20, Currency: "EUR", Departure: models.GroundStop{Time: "T10:00"}, Arrival: models.GroundStop{Time: "T14:00"}},
	}
	opts := SearchOptions{MaxPrice: 30, Type: "train"}
	filtered := filterGroundRoutes(routes, opts)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 cheap train, got %d", len(filtered))
	}
	if filtered[0].Price != 20 {
		t.Errorf("expected price 20, got %.2f", filtered[0].Price)
	}
}

// ============================================================
// isProviderNotApplicable
// ============================================================

func TestIsProviderNotApplicable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"station for city", fmt.Errorf("no DB station for Helsinki"), true},
		{"city found for", fmt.Errorf("no FlixBus city found for X"), true},
		{"port for", fmt.Errorf("no DFDS port for X"), true},
		{"no route for", fmt.Errorf("no route for X"), true},
		{"no Tallink route", fmt.Errorf("no Tallink route"), true},
		{"no Eurostar route", fmt.Errorf("no Eurostar route"), true},
		{"no DFDS route", fmt.Errorf("no DFDS route"), true},
		{"no Stena Line route", fmt.Errorf("no Stena Line route"), true},
		{"rate limiter", fmt.Errorf("rate limiter: rate: Wait something"), true},
		{"deadline", fmt.Errorf("context deadline exceeded"), true},
		{"real error", fmt.Errorf("HTTP 500 internal server error"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isProviderNotApplicable(tt.err)
			if got != tt.want {
				t.Errorf("isProviderNotApplicable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// ============================================================
// shouldKeepGroundRoute
// ============================================================

func TestShouldKeepGroundRoute(t *testing.T) {
	tests := []struct {
		name string
		r    models.GroundRoute
		want bool
	}{
		{"positive price any provider", models.GroundRoute{Provider: "flixbus", Price: 10}, true},
		{"zero price regiojet", models.GroundRoute{Provider: "regiojet", Price: 0}, false},
		{"zero price transitous (schedule-only)", models.GroundRoute{Provider: "transitous", Price: 0}, true},
		{"zero price db (schedule-only)", models.GroundRoute{Provider: "db", Price: 0}, true},
		{"zero price ns (schedule-only)", models.GroundRoute{Provider: "ns", Price: 0}, true},
		{"zero price vr (schedule-only)", models.GroundRoute{Provider: "vr", Price: 0}, true},
		{"zero price tallink (schedule-only)", models.GroundRoute{Provider: "tallink", Price: 0}, true},
		{"zero price stenaline (schedule-only)", models.GroundRoute{Provider: "stenaline", Price: 0}, true},
		{"zero price dfds (schedule-only)", models.GroundRoute{Provider: "dfds", Price: 0}, true},
		{"zero price vikingline (schedule-only)", models.GroundRoute{Provider: "vikingline", Price: 0}, true},
		{"zero price eckeroline (schedule-only)", models.GroundRoute{Provider: "eckeroline", Price: 0}, true},
		{"zero price finnlines (schedule-only)", models.GroundRoute{Provider: "finnlines", Price: 0}, true},
		{"zero price unknown", models.GroundRoute{Provider: "unknown", Price: 0}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldKeepGroundRoute(tt.r)
			if got != tt.want {
				t.Errorf("shouldKeepGroundRoute(%s, %.2f) = %v, want %v", tt.r.Provider, tt.r.Price, got, tt.want)
			}
		})
	}
}

// ============================================================
// deduplicateGroundRoutes
// ============================================================

func TestDeduplicateGroundRoutes_VariousCases(t *testing.T) {
	routes := []models.GroundRoute{
		{Provider: "flixbus", Price: 10, Departure: models.GroundStop{Time: "08:00"}, Arrival: models.GroundStop{Time: "12:00"}},
		{Provider: "flixbus", Price: 10, Departure: models.GroundStop{Time: "08:00"}, Arrival: models.GroundStop{Time: "12:00"}},
		{Provider: "flixbus", Price: 20, Departure: models.GroundStop{Time: "09:00"}, Arrival: models.GroundStop{Time: "13:00"}},
	}
	deduped := deduplicateGroundRoutes(routes)
	if len(deduped) != 2 {
		t.Errorf("expected 2 unique routes, got %d", len(deduped))
	}
}

func TestDeduplicateGroundRoutes_Empty(t *testing.T) {
	deduped := deduplicateGroundRoutes(nil)
	if len(deduped) != 0 {
		t.Errorf("expected 0 routes from nil, got %d", len(deduped))
	}
}

// ============================================================
// roundedPriceCents
// ============================================================

func TestRoundedPriceCents(t *testing.T) {
	tests := []struct {
		price float64
		want  int64
	}{
		{19.99, 1999},
		{0, 0},
		{100.005, 10001},
		{0.01, 1},
	}
	for _, tt := range tests {
		got := roundedPriceCents(tt.price)
		if got != tt.want {
			t.Errorf("roundedPriceCents(%.3f) = %d, want %d", tt.price, got, tt.want)
		}
	}
}

// ============================================================
// browserFallbacksEnabled
// ============================================================

func TestBrowserFallbacksEnabled_Explicit(t *testing.T) {
	opts := SearchOptions{AllowBrowserFallbacks: true}
	if !browserFallbacksEnabled(opts) {
		t.Error("expected true when AllowBrowserFallbacks is true")
	}
}

func TestBrowserFallbacksEnabled_EnvTrue(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "true")
	opts := SearchOptions{}
	if !browserFallbacksEnabled(opts) {
		t.Error("expected true from env var")
	}
}

func TestBrowserFallbacksEnabled_EnvFalse(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "false")
	opts := SearchOptions{}
	if browserFallbacksEnabled(opts) {
		t.Error("expected false from env var")
	}
}

func TestBrowserFallbacksEnabled_EnvEmpty(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "")
	opts := SearchOptions{}
	if browserFallbacksEnabled(opts) {
		t.Error("expected false from empty env var")
	}
}

func TestBrowserFallbacksEnabled_EnvInvalid(t *testing.T) {
	t.Setenv("TRVL_ALLOW_BROWSER_FALLBACKS", "not-a-bool")
	opts := SearchOptions{}
	if browserFallbacksEnabled(opts) {
		t.Error("expected false from invalid env var")
	}
}

// ============================================================
// MarketedProviderNames / MarketedProviderCount / searchResultBufferCapacity
// ============================================================

func TestMarketedProviderNames(t *testing.T) {
	names := MarketedProviderNames()
	if len(names) == 0 {
		t.Fatal("MarketedProviderNames returned empty slice")
	}
	// Verify defensive copy.
	names[0] = "mutated"
	original := MarketedProviderNames()
	if original[0] == "mutated" {
		t.Error("MarketedProviderNames should return a copy, not the original slice")
	}
}

func TestMarketedProviderCount(t *testing.T) {
	count := MarketedProviderCount()
	if count != len(marketedProviderNames) {
		t.Errorf("count = %d, want %d", count, len(marketedProviderNames))
	}
}

func TestSearchResultBufferCapacity(t *testing.T) {
	cap := searchResultBufferCapacity()
	if cap != MarketedProviderCount()+1 {
		t.Errorf("cap = %d, want %d", cap, MarketedProviderCount()+1)
	}
}

// ============================================================
// DefaultProvider.SearchGround (just verifies delegation)
// ============================================================

// TestDefaultProvider_SearchGround_EmptyInputs filters to a provider that does
// not exist, so empty inputs run the whole search path with nothing to call.
func TestDefaultProvider_SearchGround_EmptyInputs(t *testing.T) {
	p := &DefaultProvider{}
	result, err := p.SearchGround(t.Context(), "", "", "2026-07-01", models.GroundSearchOptions{Providers: []string{"no-such-provider"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || len(result.Routes) != 0 {
		t.Fatalf("result = %+v, want a non-nil result with no routes", result)
	}
}

// TestDefaultProvider_SearchGround_CancelledContext pins that a caller whose
// context is already cancelled gets context.Canceled back before any provider
// is contacted (doGroundSearchSingleflight checks ctx before the fan-out).
func TestDefaultProvider_SearchGround_CancelledContext(t *testing.T) {
	p := &DefaultProvider{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := p.SearchGround(ctx, "", "", "2026-07-01", models.GroundSearchOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if result != nil {
		t.Errorf("result = %+v, want nil", result)
	}
}
