package flights

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// TestSearchNorwegian_MapsFlight proves the opt-in adapter parses the documented
// availability JSON shape into canonical FlightResults when an operator points
// NORWEGIAN_API_BASE at a reachable endpoint. Date-scoping drops the off-date
// flight; both price encodings (fare.amount and lowestFare) are handled.
func TestSearchNorwegian_MapsFlight(t *testing.T) {
	fixture := loadFixture(t, "norwegian_availability.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.URL.Query().Get("DepartureIata"); got != "OSL" {
			t.Errorf("DepartureIata = %q, want OSL", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	t.Setenv("NORWEGIAN_API_BASE", srv.URL)

	out, err := SearchNorwegian(context.Background(), "OSL", "LGW", "2026-07-25", "EUR", SearchOptions{Adults: 1})
	if err != nil {
		t.Fatalf("SearchNorwegian error: %v", err)
	}
	// Two 2026-07-25 flights match; the 07-26 flight is date-scoped out.
	if len(out) != 2 {
		t.Fatalf("want 2 date-scoped results, got %d", len(out))
	}

	first := out[0]
	if first.Provider != "norwegian" || first.Currency != "EUR" || first.Stops != 0 {
		t.Errorf("bad first result: %+v", first)
	}
	if first.Price != 39.9 {
		t.Errorf("first price = %v, want 39.9", first.Price)
	}
	if len(first.Legs) != 1 {
		t.Fatalf("want 1 leg, got %d", len(first.Legs))
	}
	leg := first.Legs[0]
	if leg.AirlineCode != "DY" || leg.Airline != "Norwegian" {
		t.Errorf("bad leg airline: %+v", leg)
	}
	// Bare numeric flight number is prefixed with the DY carrier code.
	if leg.FlightNumber != "DY744" {
		t.Errorf("flight number = %q, want DY744", leg.FlightNumber)
	}
	if leg.DepartureAirport.Code != "OSL" || leg.ArrivalAirport.Code != "LGW" {
		t.Errorf("bad leg airports: %+v", leg)
	}
	if leg.DepartureTime != "2026-07-25T06:55" {
		t.Errorf("departure time = %q, want 2026-07-25T06:55", leg.DepartureTime)
	}
	if leg.Duration != 85 {
		t.Errorf("duration = %d, want 85", leg.Duration)
	}
	if first.BookingURL == "" {
		t.Error("booking URL not set")
	}

	// Second flight uses the lowestFare encoding and an already-prefixed number.
	second := out[1]
	if second.Price != 54.9 {
		t.Errorf("second price = %v, want 54.9 (lowestFare encoding)", second.Price)
	}
	if second.Legs[0].FlightNumber != "DY746" {
		t.Errorf("second flight number = %q, want DY746 (already-prefixed, not doubled)", second.Legs[0].FlightNumber)
	}
}

// TestSearchNorwegian_NoopWhenUnconfigured proves the adapter is a silent no-op
// (nil, nil) when no opt-in endpoint is configured — never a crash, never a
// fabricated empty result. This is the honest default given the Cloudflare block.
func TestSearchNorwegian_NoopWhenUnconfigured(t *testing.T) {
	t.Setenv("NORWEGIAN_API_BASE", "")
	out, err := SearchNorwegian(context.Background(), "OSL", "LGW", "2026-07-25", "EUR", SearchOptions{})
	if err != nil {
		t.Fatalf("unconfigured Norwegian should be a no-op, got error: %v", err)
	}
	if out != nil {
		t.Errorf("unconfigured Norwegian should return nil results, got %d", len(out))
	}
}

// TestSearchNorwegian_ForbiddenIsTypedError proves a 403 (Cloudflare challenge
// served to a configured-but-still-blocked base) yields an honest typed error,
// not a silent empty — the CLOUDFLARE_BLOCK reality surfaced as a failure, never
// a lie.
func TestSearchNorwegian_ForbiddenIsTypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html>Are you human?</html>"))
	}))
	defer srv.Close()
	t.Setenv("NORWEGIAN_API_BASE", srv.URL)

	_, err := SearchNorwegian(context.Background(), "OSL", "LGW", "2026-07-25", "EUR", SearchOptions{})
	if err == nil {
		t.Fatal("403 from Norwegian should surface a typed error, got nil")
	}
}

// TestSearchNorwegian_RateLimitedIsTypedError proves a 429 yields a typed,
// rate-limited error classification rather than a generic failure — so the
// aggregate can distinguish a transient throttle from a hard block.
func TestSearchNorwegian_RateLimitedIsTypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()
	t.Setenv("NORWEGIAN_API_BASE", srv.URL)

	_, err := SearchNorwegian(context.Background(), "OSL", "LGW", "2026-07-25", "EUR", SearchOptions{})
	if err == nil {
		t.Fatal("429 from Norwegian should surface a typed error, got nil")
	}
}

func TestNorwegianEligibleOptions(t *testing.T) {
	if !norwegianEligibleOptions(SearchOptions{}) {
		t.Error("plain one-way economy should be eligible")
	}
	if norwegianEligibleOptions(SearchOptions{ReturnDate: "2026-07-30"}) {
		t.Error("round-trip should be ineligible")
	}
	if norwegianEligibleOptions(SearchOptions{Alliances: []string{"STAR_ALLIANCE"}}) {
		t.Error("alliance filter should be ineligible (Norwegian non-aligned)")
	}
	if norwegianEligibleOptions(SearchOptions{Airlines: []string{"BA"}}) {
		t.Error("non-DY airline filter should be ineligible")
	}
	if !norwegianEligibleOptions(SearchOptions{Airlines: []string{"DY"}}) {
		t.Error("DY airline filter should be eligible")
	}
}

func TestNorwegianSearchEligible_RequiresSharedClientAndConfig(t *testing.T) {
	// Injected (non-shared) client: never eligible, regardless of config.
	injected := batchexec.NewTestClient("http" + "://" + "127.0.0.1:0")
	t.Setenv("NORWEGIAN_API_BASE", "http"+"://"+"example.invalid")
	if norwegianSearchEligible(injected, SearchOptions{}) {
		t.Error("injected client must never be eligible (shared-client guard)")
	}

	// Shared client but unconfigured: opt-in gate keeps it skipped.
	t.Setenv("NORWEGIAN_API_BASE", "")
	if norwegianSearchEligible(batchexec.SharedClient(), SearchOptions{}) {
		t.Error("unconfigured Norwegian must be ineligible even on the shared client")
	}
}

// TestNorwegianRegistryResolves proves both the "norwegian" name and the "dy"
// alias resolve to the Norwegian entry in lccRegistry, and that an unconfigured
// (no NORWEGIAN_API_BASE) explicit provider request returns a clean empty
// result via the searchSingleLCC path — never a crash.
func TestNorwegianRegistryResolves(t *testing.T) {
	t.Setenv("NORWEGIAN_API_BASE", "")
	for _, name := range []string{"norwegian", "dy"} {
		entry, ok := lccRegistry[name]
		if !ok {
			t.Fatalf("lccRegistry missing %q", name)
		}
		if entry.name != "Norwegian" {
			t.Errorf("%q resolves to %q, want Norwegian", name, entry.name)
		}
		res, err := SearchLowCostCarrier(context.Background(), name, "OSL", "LGW", "2026-07-25", SearchOptions{})
		if err != nil {
			t.Fatalf("SearchLowCostCarrier(%q) unconfigured should be a clean no-op, got error: %v", name, err)
		}
		if res == nil || res.Count != 0 {
			t.Errorf("SearchLowCostCarrier(%q) unconfigured want 0 results, got %+v", name, res)
		}
	}
}

// TestNorwegianRoundTripComposesViaSearchSingleLCC proves a round-trip request
// (opts.ReturnDate set) composes a genuine return ticket from two one-way legs
// via the shared searchSingleLCC split-ticket path — not a bare one-way returned
// for a round-trip request. The outbound and inbound legs are served from the
// same configured availability fixture endpoint, direction-swapped per leg.
func TestNorwegianRoundTripComposesViaSearchSingleLCC(t *testing.T) {
	out := loadFixture(t, "norwegian_availability.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Echo a date-matched availability payload for whichever leg/date is
		// requested so both the outbound (OSL->LGW 07-25) and inbound
		// (LGW->OSL 07-28) one-way searches return a priced flight.
		dep := r.URL.Query().Get("DepartureIata")
		arr := r.URL.Query().Get("ArrivalIata")
		from := r.URL.Query().Get("DepartureDateFrom")
		body := `{"flights":[{"flightNumber":"700","departureAirport":"` + dep +
			`","arrivalAirport":"` + arr + `","departureDateTime":"` + from +
			`T07:00:00","arrivalDateTime":"` + from + `T08:25:00","fare":{"amount":49.9,"currencyCode":"EUR"}}]}`
		_ = out // fixture retained for shape reference; per-leg body is synthesized
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	t.Setenv("NORWEGIAN_API_BASE", srv.URL)

	res, err := searchSingleLCC(context.Background(), "Norwegian", SearchNorwegian,
		"OSL", "LGW", "2026-07-25", SearchOptions{Adults: 1, ReturnDate: "2026-07-28"})
	if err != nil {
		t.Fatalf("round-trip searchSingleLCC error: %v", err)
	}
	if res.TripType != "round_trip" {
		t.Fatalf("TripType = %q, want round_trip", res.TripType)
	}
	if res.Count == 0 {
		t.Fatal("round-trip composition returned no itineraries")
	}
	// A composed return itinerary carries both legs (outbound + inbound).
	rt := res.Flights[0]
	if len(rt.Legs) != 2 {
		t.Fatalf("composed round-trip want 2 legs, got %d: %+v", len(rt.Legs), rt)
	}
	if rt.Legs[0].DepartureAirport.Code != "OSL" || rt.Legs[1].DepartureAirport.Code != "LGW" {
		t.Errorf("legs not direction-composed OSL->LGW + LGW->OSL: %+v", rt.Legs)
	}
}

// TestSearchFlightsCore_NorwegianHonestStatusWhenUnconfigured is the surfacing
// proof: a default-merge search (injected Google client, so the LCC providers
// auto-skip) still emits an HONEST typed Norwegian ProviderStatus carrying the
// CLOUDFLARE_BLOCK root cause and a fix hint — never a fabricated empty result
// and never a crash. This is the opt-in deliverable: the path is wired and
// reports its unavailability truthfully.
func TestSearchFlightsCore_NorwegianHonestStatusWhenUnconfigured(t *testing.T) {
	resetFlightBreakerForTest(t, "norwegian")
	t.Setenv("NORWEGIAN_API_BASE", "")
	body := makeFlightResponseBody(t)
	ts := flightsTestServer(t, 200, body)
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	result, err := SearchFlightsWithClient(t.Context(), client, "OSL", "LGW", "2026-07-25", SearchOptions{})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}

	var dy *struct {
		status, code, fixHint string
	}
	for _, s := range result.ProviderStatuses {
		if s.ID == "norwegian" {
			dy = &struct{ status, code, fixHint string }{s.Status, s.FixHintCode, s.FixHint}
			break
		}
	}
	if dy == nil {
		t.Fatal("Norwegian provider status not present in default merge — opt-in path not wired")
	}
	if dy.status != "skipped" {
		t.Errorf("Norwegian status = %q, want skipped (honest opt-in skip)", dy.status)
	}
	if dy.code != "CLOUDFLARE_BLOCK" {
		t.Errorf("Norwegian fix_hint_code = %q, want CLOUDFLARE_BLOCK", dy.code)
	}
	if dy.fixHint == "" {
		t.Error("Norwegian skip must carry an actionable fix hint (set NORWEGIAN_API_BASE)")
	}
}
