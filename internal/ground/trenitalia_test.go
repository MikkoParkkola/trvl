package ground

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// loadTrenitaliaFixture reads the named file from the testdata/ directory.
func loadTrenitaliaFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("loadTrenitaliaFixture: %v", err)
	}
	return data
}

// TestTrenitaliaDurationParser verifies the duration string parser.
func TestTrenitaliaDurationParser(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"3h 10min", 190},
		{"3h 05min", 185},
		{"45min", 45},
		{"2h", 120},
		{"1h 0min", 60},
		{"", 0},
	}
	for _, tc := range cases {
		got := parseTrenitaliaDuration(tc.input)
		if got != tc.want {
			t.Errorf("parseTrenitaliaDuration(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

// TestHasTrenitaliaRoute checks the static city set membership.
func TestHasTrenitaliaRoute(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"Rome", "Milan", true},
		{"rome", "milan", true},
		{"Florence", "Venice", true},
		{"Naples", "Turin", true},
		{"London", "Paris", false},
		{"Milan", "Paris", false}, // Paris is not Italian
		{"Madrid", "Barcelona", false},
	}
	for _, tc := range cases {
		got := HasTrenitaliaRoute(tc.from, tc.to)
		if got != tc.want {
			t.Errorf("HasTrenitaliaRoute(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

// TestSearchTrenitaliaOffline verifies that a fixture response is parsed into
// the expected GroundRoute values — no network required.
func TestSearchTrenitaliaOffline(t *testing.T) {
	solutionsFixture := loadTrenitaliaFixture(t, "trenitalia_solutions.json")
	locationsFixture := loadTrenitaliaFixture(t, "trenitalia_locations.json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "locations/search") {
			_, _ = w.Write(locationsFixture)
			return
		}
		if strings.Contains(r.URL.Path, "ticket/solutions") {
			_, _ = w.Write(solutionsFixture)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	// Override the host to point at the test server.
	origHost := trenitaliaHost
	trenitaliaHost = srv.URL
	defer func() { trenitaliaHost = origHost }()

	// Override the limiter to not wait in tests.
	origLimiter := trenitaliaLimiter
	trenitaliaLimiter = newProviderLimiter(0)
	defer func() { trenitaliaLimiter = origLimiter }()

	routes, err := SearchTrenitalia(context.Background(), "Milan", "Rome", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchTrenitalia: %v", err)
	}

	// The fixture has 2 solutions: one SALEABLE at €68.90, one NOT_SALEABLE at €0.
	// Only the first should be returned.
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]
	if r.Provider != "trenitalia" {
		t.Errorf("Provider = %q, want %q", r.Provider, "trenitalia")
	}
	if r.Type != "train" {
		t.Errorf("Type = %q, want %q", r.Type, "train")
	}
	if r.Currency != "EUR" {
		t.Errorf("Currency = %q, want %q", r.Currency, "EUR")
	}
	if r.Price != 68.90 {
		t.Errorf("Price = %.2f, want 68.90", r.Price)
	}
	// "3h 10min" → 190 minutes
	if r.Duration != 190 {
		t.Errorf("Duration = %d, want 190", r.Duration)
	}
	// Single train → 0 transfers
	if r.Transfers != 0 {
		t.Errorf("Transfers = %d, want 0", r.Transfers)
	}
	if r.Departure.City != "Milan" {
		t.Errorf("Departure.City = %q, want %q", r.Departure.City, "Milan")
	}
	if r.Arrival.City != "Rome" {
		t.Errorf("Arrival.City = %q, want %q", r.Arrival.City, "Rome")
	}
	// Departure time should be normalised (no milliseconds or offset).
	if strings.Contains(r.Departure.Time, ".") || strings.Contains(r.Departure.Time, "+") {
		t.Errorf("Departure.Time not normalised: %q", r.Departure.Time)
	}
	if r.BookingURL == "" {
		t.Error("BookingURL must not be empty")
	}
	// The fixture station name for Milano should come through.
	if r.Departure.Station == "" {
		t.Error("Departure.Station must not be empty")
	}
}

// TestSearchTrenitaliaLive is an integration test that fires real HTTP requests.
// Run with TRVL_TEST_LIVE_INTEGRATIONS=1.
func TestSearchTrenitaliaLive(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("set TRVL_TEST_LIVE_INTEGRATIONS=1 to run live Trenitalia tests")
	}
	routes, err := SearchTrenitalia(context.Background(), "Milan", "Rome", "2026-08-01", "EUR")
	if err != nil {
		t.Fatalf("SearchTrenitalia live: %v", err)
	}
	if len(routes) == 0 {
		t.Error("expected at least one live route")
	}
	for _, r := range routes {
		if r.Price <= 0 {
			t.Errorf("route has non-positive price: %+v", r)
		}
	}
}

// TestCanonicalItalianCity guards the English->Italian alias map that prevents
// the resolver mis-matching (e.g. "Rome" -> "Rometta Messinese" without it).
func TestCanonicalItalianCity(t *testing.T) {
	cases := map[string]string{
		"Rome":    "Roma",
		"rome":    "Roma",
		"Turin":   "Torino",
		"Naples":  "Napoli",
		"Milan":   "Milano",
		"Roma":    "Roma",    // already Italian, unchanged
		"Bologna": "Bologna", // no alias, passthrough
	}
	for in, want := range cases {
		if got := canonicalItalianCity(in); got != want {
			t.Errorf("canonicalItalianCity(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestHasTrenitaliaRouteEmptyInput guards against blank city names matching via
// the reverse-substring check (empty string is a substring of everything).
func TestHasTrenitaliaRouteEmptyInput(t *testing.T) {
	if HasTrenitaliaRoute("", "Rome") {
		t.Error(`HasTrenitaliaRoute("", "Rome") = true, want false (blank must not match)`)
	}
	if HasTrenitaliaRoute("Milan", "") {
		t.Error(`HasTrenitaliaRoute("Milan", "") = true, want false`)
	}
	if HasTrenitaliaRoute("ro", "Rome") {
		t.Error(`2-char fragment "ro" must not match`)
	}
}

// TestSearchTrenitaliaLivePalermoCatania is a regression for the resolver
// picking a peripheral station (e.g. "Palermo Aeroporto") that returns HTTP 400.
// The city-level centroid must yield valid Palermo->Catania fares.
func TestSearchTrenitaliaLivePalermoCatania(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("set TRVL_TEST_LIVE_INTEGRATIONS=1 to run live Trenitalia tests")
	}
	date := time.Now().AddDate(0, 0, 14).Format("2006-01-02")
	routes, err := SearchTrenitalia(context.Background(), "Palermo", "Catania", date, "EUR")
	if err != nil {
		t.Fatalf("Palermo->Catania should resolve via the city centroid, got: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("expected at least one Palermo->Catania route")
	}
}
