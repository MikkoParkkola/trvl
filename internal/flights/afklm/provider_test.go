package afklm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestProviderOriginMapping verifies that placeType maps IATA codes correctly.
func TestProviderOriginMapping_Airport(t *testing.T) {
	pt := placeType("AMS")
	if pt != "AIRPORT" {
		t.Errorf("AMS: want AIRPORT, got %s", pt)
	}
}

func TestProviderOriginMapping_RailwayStation(t *testing.T) {
	for code := range RailwayStations {
		pt := placeType(code)
		if pt != "RAILWAY_STATION" {
			t.Errorf("%s: want RAILWAY_STATION, got %s", code, pt)
		}
	}
}

func TestProviderRoundTrip(t *testing.T) {
	fixture, err := os.ReadFile("testdata/available_offers_ams_prg.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	var requestBodies [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body [4096]byte
		n, _ := r.Body.Read(body[:])
		requestBodies = append(requestBodies, body[:n])
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(200)
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	client := newTestClient(t, srv, func() time.Time { return now })
	p := NewProviderWithClient(client)

	opts := models.FlightSearchOptions{
		ReturnDate: "2026-05-22",
		CabinClass: models.Economy,
		Adults:     1,
		Currency:   "EUR",
	}
	result, err := p.SearchFlights(context.Background(), "AMS", "PRG", "2026-05-15", opts)
	if err != nil {
		t.Fatalf("SearchFlights: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success, got error: %s", result.Error)
	}
	if result.TripType != "round-trip" {
		t.Errorf("expected round-trip, got %q", result.TripType)
	}
	if len(result.Flights) == 0 {
		t.Error("expected at least one flight result")
	}
	for _, f := range result.Flights {
		if f.Provider != "afklm" {
			t.Errorf("expected provider=afklm, got %q", f.Provider)
		}
	}
}

func TestProviderQuotaExhausted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Should never be called — quota is exhausted before any HTTP call.
		t.Error("server should not be called when quota is exhausted")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"recommendations":[]}`))
	}))
	defer srv.Close()

	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	c, err := NewCache(dir, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	// Pre-fill quota to the limit.
	for i := 0; i < quotaHardLimit; i++ {
		if err := c.IncQuota(now); err != nil {
			t.Fatalf("IncQuota: %v", err)
		}
	}

	client := &Client{
		baseURL:    srv.URL,
		host:       "KL",
		key:        "test-key",
		httpClient: srv.Client(),
		limiter:    newTestLimiter(),
		cache:      c,
		now:        func() time.Time { return now },
	}
	p := NewProviderWithClient(client)

	result, err := p.SearchFlights(context.Background(), "AMS", "PRG", "2026-05-15", models.FlightSearchOptions{})
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}
	if result.Success {
		t.Error("expected success=false on quota exhaustion")
	}
	if result.Error == "" {
		t.Error("expected non-empty result.Error on quota exhaustion")
	}
}

func TestProviderCabinMapping(t *testing.T) {
	cases := []struct {
		cabin models.CabinClass
		want  string
	}{
		{models.Economy, "ECONOMY"},
		{models.Business, "BUSINESS"},
		{models.PremiumEconomy, "ALL"},
		{models.First, "ALL"},
	}
	for _, tc := range cases {
		codes := cabinCodes(tc.cabin)
		if len(codes) == 0 {
			t.Errorf("%v: empty cabin codes", tc.cabin)
			continue
		}
		if codes[0] != tc.want {
			t.Errorf("%v: want %s, got %s", tc.cabin, tc.want, codes[0])
		}
	}
}

// TestBoundDirection locks the round-trip leg tagging: bound 0 is the outbound,
// bound 1 the inbound/return, and any further bounds (open-jaw / multi-city)
// stay untagged. This is the native return-ticket leg mapping for AF-KLM, so a
// regression here would mislabel return flights.
func TestBoundDirection(t *testing.T) {
	cases := []struct {
		idx  int
		want string
	}{
		{0, "outbound"},
		{1, "inbound"},
		{2, ""},  // open-jaw third bound: untagged
		{5, ""},  // multi-city
		{-1, ""}, // defensive: never expected, must not panic or mislabel
	}
	for _, tc := range cases {
		if got := boundDirection(tc.idx); got != tc.want {
			t.Errorf("boundDirection(%d): got %q, want %q", tc.idx, got, tc.want)
		}
	}
}

// TestParseLayover covers the success path and every failure branch: empty
// endpoints and unparseable datetimes must report ok=false so the caller skips
// the layover diff rather than computing garbage from zero-value times.
func TestParseLayover(t *testing.T) {
	prev, cur, ok := parseLayover("2026-05-15T10:30:00", "2026-05-15T12:45:00")
	if !ok {
		t.Fatal("parseLayover(valid pair): ok=false, want true")
	}
	if got := cur.Sub(prev); got != 2*time.Hour+15*time.Minute {
		t.Errorf("layover gap: got %v, want 2h15m", got)
	}

	failCases := []struct {
		name             string
		prevArr, nextDep string
	}{
		{"empty_prev", "", "2026-05-15T12:45:00"},
		{"empty_next", "2026-05-15T10:30:00", ""},
		{"both_empty", "", ""},
		{"bad_prev", "garbage", "2026-05-15T12:45:00"},
		{"bad_next", "2026-05-15T10:30:00", "not-a-time"},
	}
	for _, tc := range failCases {
		if _, _, ok := parseLayover(tc.prevArr, tc.nextDep); ok {
			t.Errorf("parseLayover(%s): ok=true, want false", tc.name)
		}
	}
}
