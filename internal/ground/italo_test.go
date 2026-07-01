package ground

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// loadItaloFixture reads the named file from the testdata/ directory.
func loadItaloFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("loadItaloFixture: %v", err)
	}
	return data
}

// TestHasItaloRoute checks the static city-code gate and its normalisation.
func TestHasItaloRoute(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"Milan", "Rome", true},
		{"milano", "roma", true},
		{"Milano Centrale", "Roma Termini", true}, // station-style names
		{"Turin", "Naples", true},                 // English aliases
		{"Venice", "Florence", true},
		{"Milan, Italy", "Rome", true}, // Italy qualifier
		{"Milan, Ohio", "Rome", false}, // foreign qualifier -> skip
		{"London", "Paris", false},     // not Italian
		{"Milan", "Lisbon", false},     // one endpoint off-network
		{"Como", "Rome", false},        // Como is Trenitalia-only, not on Italo map
	}
	for _, tc := range cases {
		if got := HasItaloRoute(tc.from, tc.to); got != tc.want {
			t.Errorf("HasItaloRoute(%q,%q) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

// TestItaloStationCode checks a few specific resolutions and codes.
func TestItaloStationCode(t *testing.T) {
	cases := []struct {
		in       string
		wantCode string
		wantOK   bool
	}{
		{"Milan", "MC_", true},
		{"Rome", "RMT", true},
		{"Firenze", "SMN", true},
		{"Torino", "OUE", true},
		{"Venezia", "VSL", true},
		{"Roma Tiburtina", "RTB", true},  // station-precise: NOT RMT
		{"Milano Rogoredo", "RG_", true}, // station-precise: NOT MC_
		{"Reggio Emilia", "AAV", true},   // previously-missing stop
		{"Roma Foo", "", false},          // unrecognised station-style -> fail safe
		{"Milan, Ohio", "", false},
		{"Nowhere", "", false},
		{"", "", false},
	}
	for _, tc := range cases {
		code, ok := italoStationCode(tc.in)
		if ok != tc.wantOK || code != tc.wantCode {
			t.Errorf("italoStationCode(%q) = (%q,%v), want (%q,%v)", tc.in, code, ok, tc.wantCode, tc.wantOK)
		}
	}
}

// TestItaloParseTime covers naive-local and RFC3339 timestamp parsing.
func TestItaloParseTime(t *testing.T) {
	cases := []struct {
		in     string
		wantOK bool
	}{
		{"2026-07-15T08:00:00", true},
		{"2026-07-15T08:00:00+02:00", true}, // RFC3339 with offset (defensive)
		{"  2026-07-15T08:00:00  ", true},   // trimmed
		{"bad", false},
		{"", false},
	}
	for _, tc := range cases {
		_, ok := italoParseTime(tc.in)
		if ok != tc.wantOK {
			t.Errorf("italoParseTime(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
		}
	}
	// Duration derives from parsed times: 08:00 -> 11:10 is 190 minutes.
	d, _ := italoParseTime("2026-07-15T08:00:00")
	a, _ := italoParseTime("2026-07-15T11:10:00")
	if got := int(a.Sub(d).Minutes()); got != 190 {
		t.Errorf("duration = %d, want 190", got)
	}
}

// TestBuildItaloBookingURL checks the deep-link date reformat and escaping.
func TestBuildItaloBookingURL(t *testing.T) {
	got := buildItaloBookingURL("MC_", "RMT", "2026-07-15")
	for _, want := range []string{"osc=MC_", "dsc=RMT", "od=15%2F07%2F2026", "startSearch=true"} {
		if !strings.Contains(got, want) {
			t.Errorf("bookingURL %q missing %q", got, want)
		}
	}
}

// TestSearchItaloOffline drives the full login -> booking -> poll -> parse flow
// against an httptest server, verifying the fixture maps to the expected route:
// the direct ITALO journey at its cheapest fare, with the TRENITALIA journey
// filtered out, the sold-out (zero-price) ITALO journey skipped, and the
// multi-segment (connecting) ITALO journey skipped (direct-only).
func TestSearchItaloOffline(t *testing.T) {
	statusFixture := loadItaloFixture(t, "italo_status.json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/login":
			http.SetCookie(w, &http.Cookie{Name: "BIGSessionToken", Value: "test-jwt"})
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"isLoggedIn":true,"isAnonymous":true}`))
		case r.URL.Path == "/api/v1/booking":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"operationId":"op-1","pollAfter":1,"isCompleted":false}`))
		case strings.HasPrefix(r.URL.Path, "/api/v1/booking/status/"):
			// Assert the Bearer token from login was carried through.
			if got := r.Header.Get("Authorization"); got != "Bearer test-jwt" {
				t.Errorf("status Authorization = %q, want %q", got, "Bearer test-jwt")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(statusFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	origLogin, origAPI, origLimiter, origPoll := italoLoginHost, italoAPIHost, italoLimiter, italoPollInterval
	italoLoginHost = srv.URL
	italoAPIHost = srv.URL
	italoLimiter = newProviderLimiter(0)
	italoPollInterval = 0
	defer func() {
		italoLoginHost, italoAPIHost, italoLimiter, italoPollInterval = origLogin, origAPI, origLimiter, origPoll
	}()

	routes, err := SearchItalo(context.Background(), "Milan", "Rome", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchItalo: %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route (ITALO only, priced), got %d", len(routes))
	}
	r := routes[0]
	if r.Provider != "italo" {
		t.Errorf("Provider = %q, want italo", r.Provider)
	}
	if r.Type != "train" {
		t.Errorf("Type = %q, want train", r.Type)
	}
	if r.Currency != "EUR" {
		t.Errorf("Currency = %q, want EUR", r.Currency)
	}
	if r.Price != 59.9 {
		t.Errorf("Price = %.2f, want 59.90 (cheapest ADT fare)", r.Price)
	}
	if r.Duration != 190 {
		t.Errorf("Duration = %d, want 190", r.Duration)
	}
	if r.Transfers != 0 {
		t.Errorf("Transfers = %d, want 0", r.Transfers)
	}
	if r.Departure.Station != "Milano Centrale" {
		t.Errorf("Departure.Station = %q, want Milano Centrale", r.Departure.Station)
	}
	if r.Arrival.Station != "Roma Termini" {
		t.Errorf("Arrival.Station = %q, want Roma Termini", r.Arrival.Station)
	}
	if !strings.Contains(r.BookingURL, "osc=MC_") || !strings.Contains(r.BookingURL, "dsc=RMT") {
		t.Errorf("BookingURL = %q, missing station codes", r.BookingURL)
	}
}

// TestSearchItaloLive hits the real Italo API. Opt in with
// TRVL_TEST_LIVE_INTEGRATIONS=1; it needs network and an unblocked IP.
func TestSearchItaloLive(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("set TRVL_TEST_LIVE_INTEGRATIONS=1 to run live Italo tests")
	}
	routes, err := SearchItalo(context.Background(), "Milan", "Rome", "2026-07-15", "EUR")
	if err != nil {
		t.Fatalf("SearchItalo live: %v", err)
	}
	if len(routes) == 0 {
		t.Fatal("live Italo search returned 0 routes for Milan->Rome")
	}
	for _, r := range routes {
		if r.Provider != "italo" || r.Type != "train" {
			t.Errorf("unexpected route shape: %+v", r)
		}
		if r.Price <= 0 {
			t.Errorf("route with non-positive price: %+v", r)
		}
	}
}
