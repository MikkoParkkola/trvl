package flights

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
)

// TestSearchEasyjet_MapsFlight proves the opt-in adapter parses the documented
// availability JSON shape into canonical FlightResults when an operator points
// EASYJET_API_BASE at a reachable endpoint. Date-scoping drops the off-date
// flight; both price encodings (fare.amount and lowestFare) are handled.
func TestSearchEasyjet_MapsFlight(t *testing.T) {
	fixture := loadFixture(t, "easyjet_availability.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if got := r.URL.Query().Get("DepartureIata"); got != "AMS" {
			t.Errorf("DepartureIata = %q, want AMS", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	t.Setenv("EASYJET_API_BASE", srv.URL)

	out, err := SearchEasyjet(context.Background(), "AMS", "BCN", "2026-07-07", "EUR", SearchOptions{Adults: 1})
	if err != nil {
		t.Fatalf("SearchEasyjet error: %v", err)
	}
	// Two 2026-07-07 flights match; the 07-08 flight is date-scoped out.
	if len(out) != 2 {
		t.Fatalf("want 2 date-scoped results, got %d", len(out))
	}

	first := out[0]
	if first.Provider != "easyjet" || first.Currency != "EUR" || first.Stops != 0 {
		t.Errorf("bad first result: %+v", first)
	}
	if first.Price != 38.99 {
		t.Errorf("first price = %v, want 38.99", first.Price)
	}
	if len(first.Legs) != 1 {
		t.Fatalf("want 1 leg, got %d", len(first.Legs))
	}
	leg := first.Legs[0]
	if leg.AirlineCode != "U2" || leg.Airline != "easyJet" {
		t.Errorf("bad leg airline: %+v", leg)
	}
	// Bare numeric flight number is prefixed with the U2 carrier code.
	if leg.FlightNumber != "U28431" {
		t.Errorf("flight number = %q, want U28431", leg.FlightNumber)
	}
	if leg.DepartureAirport.Code != "AMS" || leg.ArrivalAirport.Code != "BCN" {
		t.Errorf("bad leg airports: %+v", leg)
	}
	if leg.DepartureTime != "2026-07-07T06:40" {
		t.Errorf("departure time = %q, want 2026-07-07T06:40", leg.DepartureTime)
	}
	if leg.Duration != 135 {
		t.Errorf("duration = %d, want 135", leg.Duration)
	}
	if first.BookingURL == "" {
		t.Error("booking URL not set")
	}

	// Second flight uses the lowestFare encoding and an already-prefixed number.
	second := out[1]
	if second.Price != 52.49 {
		t.Errorf("second price = %v, want 52.49 (lowestFare encoding)", second.Price)
	}
	if second.Legs[0].FlightNumber != "U28433" {
		t.Errorf("second flight number = %q, want U28433 (already-prefixed, not doubled)", second.Legs[0].FlightNumber)
	}
}

// TestSearchEasyjet_NoopWhenUnconfigured proves the adapter is a silent no-op
// (nil, nil) when no opt-in endpoint is configured — never a crash, never a
// fabricated empty result. This is the honest default given the Akamai block.
func TestSearchEasyjet_NoopWhenUnconfigured(t *testing.T) {
	t.Setenv("EASYJET_API_BASE", "")
	out, err := SearchEasyjet(context.Background(), "AMS", "BCN", "2026-07-07", "EUR", SearchOptions{})
	if err != nil {
		t.Fatalf("unconfigured easyJet should be a no-op, got error: %v", err)
	}
	if out != nil {
		t.Errorf("unconfigured easyJet should return nil results, got %d", len(out))
	}
}

// TestSearchEasyjet_ForbiddenIsTypedError proves a 403 (Akamai challenge served
// to a configured-but-still-blocked base) yields an honest typed error, not a
// silent empty — the AKAMAI_BLOCK reality surfaced as a failure, never a lie.
func TestSearchEasyjet_ForbiddenIsTypedError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<html>Access Denied</html>"))
	}))
	defer srv.Close()
	t.Setenv("EASYJET_API_BASE", srv.URL)

	_, err := SearchEasyjet(context.Background(), "AMS", "BCN", "2026-07-07", "EUR", SearchOptions{})
	if err == nil {
		t.Fatal("403 from easyJet should surface a typed error, got nil")
	}
}

func TestEasyjetEligibleOptions(t *testing.T) {
	if !easyjetEligibleOptions(SearchOptions{}) {
		t.Error("plain one-way economy should be eligible")
	}
	if easyjetEligibleOptions(SearchOptions{ReturnDate: "2026-07-10"}) {
		t.Error("round-trip should be ineligible")
	}
	if easyjetEligibleOptions(SearchOptions{Alliances: []string{"STAR_ALLIANCE"}}) {
		t.Error("alliance filter should be ineligible (easyJet non-aligned)")
	}
	if easyjetEligibleOptions(SearchOptions{Airlines: []string{"BA"}}) {
		t.Error("non-U2 airline filter should be ineligible")
	}
	if !easyjetEligibleOptions(SearchOptions{Airlines: []string{"U2"}}) {
		t.Error("U2 airline filter should be eligible")
	}
}

func TestEasyjetSearchEligible_RequiresSharedClientAndConfig(t *testing.T) {
	// Injected (non-shared) client: never eligible, regardless of config.
	injected := batchexec.NewTestClient("http" + "://" + "127.0.0.1:0")
	t.Setenv("EASYJET_API_BASE", "http"+"://"+"example.invalid")
	if easyjetSearchEligible(injected, SearchOptions{}) {
		t.Error("injected client must never be eligible (shared-client guard)")
	}

	// Shared client but unconfigured: opt-in gate keeps it skipped.
	t.Setenv("EASYJET_API_BASE", "")
	if easyjetSearchEligible(batchexec.SharedClient(), SearchOptions{}) {
		t.Error("unconfigured easyJet must be ineligible even on the shared client")
	}
}

// TestSearchFlightsCore_EasyjetHonestStatusWhenUnconfigured is the surfacing
// proof: a default-merge search (injected Google client, so the LCC providers
// auto-skip) still emits an HONEST typed easyJet ProviderStatus carrying the
// AKAMAI_BLOCK root cause and a fix hint — never a fabricated empty result and
// never a crash. This is the Case-C deliverable: the opt-in path is wired and
// reports its unavailability truthfully.
func TestSearchFlightsCore_EasyjetHonestStatusWhenUnconfigured(t *testing.T) {
	resetFlightBreakerForTest(t, "easyjet")
	t.Setenv("EASYJET_API_BASE", "")
	body := makeFlightResponseBody(t)
	ts := flightsTestServer(t, 200, body)
	defer ts.Close()

	client := batchexec.NewTestClient(ts.URL)
	result, err := SearchFlightsWithClient(t.Context(), client, "AMS", "BCN", "2026-07-07", SearchOptions{})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}

	var ej *struct {
		status, code, fixHint string
	}
	for _, s := range result.ProviderStatuses {
		if s.ID == "easyjet" {
			ej = &struct{ status, code, fixHint string }{s.Status, s.FixHintCode, s.FixHint}
			break
		}
	}
	if ej == nil {
		t.Fatal("easyJet provider status not present in default merge — opt-in path not wired")
	}
	if ej.status != "skipped" {
		t.Errorf("easyJet status = %q, want skipped (honest opt-in skip)", ej.status)
	}
	if ej.code != "AKAMAI_BLOCK" {
		t.Errorf("easyJet fix_hint_code = %q, want AKAMAI_BLOCK", ej.code)
	}
	if ej.fixHint == "" {
		t.Error("easyJet skip must carry an actionable fix hint (set EASYJET_API_BASE)")
	}
}
