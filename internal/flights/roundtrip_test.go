package flights

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/flights/afklm"
	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/time/rate"
)

func owFlight(provider, currency string, price float64, dep, arr string) models.FlightResult {
	return models.FlightResult{
		Price:    price,
		Currency: currency,
		Duration: 120,
		Stops:    0,
		Provider: provider,
		Legs: []models.FlightLeg{{
			DepartureAirport: models.AirportInfo{Code: dep},
			ArrivalAirport:   models.AirportInfo{Code: arr},
			DepartureTime:    "2026-07-01T08:00",
			ArrivalTime:      "2026-07-01T10:00",
		}},
	}
}

func TestTagGoogleNativeRoundTrip(t *testing.T) {
	src := []models.FlightResult{owFlight("Google Flights", "EUR", 314, "HEL", "BCN")}

	got := tagGoogleNativeRoundTrip(src, "HEL", "BCN", "2026-07-01", "2026-07-08", "EUR")
	if len(got) != 1 {
		t.Fatalf("count: got %d, want 1", len(got))
	}
	f := got[0]

	// Outbound-only native shell (return at booking) is deliberately NOT tagged
	// FareRoundTrip — only results with a real paired return leg get the tag
	// (after enrich or when provider returned both). This prevents misleading
	// callers. Price is still the full RT total.
	if f.FareType != "" {
		t.Errorf("FareType: got %q, want empty (outbound-only native shell not tagged round_trip)", f.FareType)
	}
	// Price is the full round-trip total (never summed legs) — carried verbatim.
	if f.Price != 314 {
		t.Errorf("Price: got %.2f, want 314 (round-trip total preserved)", f.Price)
	}
	// Stage 1 returns the outbound itinerary; the return leg is chosen at booking.
	if len(f.Legs) != 1 || f.Legs[0].Direction != "outbound" {
		t.Errorf("legs: got %+v, want one outbound-tagged leg", f.Legs)
	}
	// A warning must make the booking-time return selection explicit.
	if len(f.Warnings) == 0 || !strings.Contains(f.Warnings[0], "round-trip") {
		t.Errorf("Warnings: got %v, want a round-trip clarification first", f.Warnings)
	}
	// Booking URL must encode the return date so the user lands on the round-trip.
	if !strings.Contains(f.BookingURL, "2026-07-08") {
		t.Errorf("BookingURL: got %q, want it to include the return date", f.BookingURL)
	}

	// The source flight's legs must stay untagged — Google one-way responses are
	// cached/shared, and a leaked round-trip tag would corrupt a later one-way.
	if src[0].Legs[0].Direction != "" {
		t.Errorf("source leg mutated: Direction=%q", src[0].Legs[0].Direction)
	}
	if src[0].FareType != "" {
		t.Errorf("source FareType mutated: %q", src[0].FareType)
	}
}

func TestTagGoogleNativeRoundTrip_Empty(t *testing.T) {
	if got := tagGoogleNativeRoundTrip(nil, "HEL", "BCN", "2026-07-01", "2026-07-08", "EUR"); got != nil {
		t.Errorf("empty input should yield nil, got %v", got)
	}
}

func TestTagNativeRoundTrip_KeepsProviderDeepLink(t *testing.T) {
	src := owFlight("kiwi", "EUR", 296, "HEL", "BCN")
	src.BookingURL = "https://kiwi.com/deep/link?return=2026-07-08"

	got := tagNativeRoundTrip([]models.FlightResult{src}, kiwiNativeRoundTripWarning, "", "2026-07-01", "2026-07-08")
	if len(got) != 1 {
		t.Fatalf("count: got %d, want 1", len(got))
	}
	f := got[0]
	// Outbound-only: no FareRoundTrip tag (see correctness fix in tagNativeRoundTrip).
	if f.FareType != "" {
		t.Errorf("FareType: got %q, want empty (outbound-only native shell not tagged round_trip)", f.FareType)
	}
	// Empty bookingURL must preserve the provider's own deep link (it encodes the return).
	if f.BookingURL != "https://kiwi.com/deep/link?return=2026-07-08" {
		t.Errorf("BookingURL overwritten: got %q, want the Kiwi deep link kept", f.BookingURL)
	}
	if f.Legs[0].Direction != "outbound" {
		t.Errorf("leg Direction: got %q, want outbound", f.Legs[0].Direction)
	}
	if len(f.Warnings) == 0 || f.Warnings[0] != kiwiNativeRoundTripWarning {
		t.Errorf("Warnings: got %v, want kiwi round-trip warning first", f.Warnings)
	}
	// Source must stay untouched (cached/shared upstream).
	if src.Legs[0].Direction != "" || src.FareType != "" {
		t.Errorf("source mutated: dir=%q fare=%q", src.Legs[0].Direction, src.FareType)
	}
}

func TestTagNativeRoundTrip_TagsInboundByDate(t *testing.T) {
	// A native round-trip fare whose payload carries BOTH halves: outbound on
	// the departure date, the return leg on returnDate. Verified live: Google
	// returns inbound legs for some itineraries (HEL->LON 2026-07-15/07-22), so
	// they must be tagged "inbound" and the booking-time warning suppressed.
	src := models.FlightResult{
		Price: 280, Currency: "EUR", Provider: "Google Flights",
		Legs: []models.FlightLeg{
			{DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "LON"}, DepartureTime: "2026-07-15T08:05"},
			{DepartureAirport: models.AirportInfo{Code: "LON"}, ArrivalAirport: models.AirportInfo{Code: "HEL"}, DepartureTime: "2026-07-22T18:40"},
		},
	}
	got := tagNativeRoundTrip([]models.FlightResult{src}, googleNativeRoundTripWarning, "https://book", "2026-07-15", "2026-07-22")
	if len(got) != 1 {
		t.Fatalf("count: got %d, want 1", len(got))
	}
	f := got[0]
	if f.Legs[0].Direction != "outbound" {
		t.Errorf("leg 0 direction: got %q, want outbound", f.Legs[0].Direction)
	}
	if f.Legs[1].Direction != "inbound" {
		t.Errorf("leg 1 (07-22) direction: got %q, want inbound", f.Legs[1].Direction)
	}
	// Inbound legs present -> the "return selected at booking" warning is false and must NOT appear.
	for _, w := range f.Warnings {
		if w == googleNativeRoundTripWarning {
			t.Errorf("booking-time warning must be suppressed when inbound legs are present: %v", f.Warnings)
		}
	}
	// Source must stay untouched.
	if src.Legs[1].Direction != "" {
		t.Errorf("source leg mutated: %q", src.Legs[1].Direction)
	}
}

func TestDirectionTaggedLegs_SameDayReturn(t *testing.T) {
	// returnDate == departDate cannot be split by date: every leg stays outbound
	// and hasInbound is false (we never fabricate a return half).
	legs := []models.FlightLeg{
		{DepartureTime: "2026-07-15T08:05"},
		{DepartureTime: "2026-07-15T20:40"},
	}
	tagged, hasInbound := directionTaggedLegs(legs, "2026-07-15", "2026-07-15")
	if hasInbound {
		t.Errorf("same-day return must not produce an inbound leg")
	}
	for i, leg := range tagged {
		if leg.Direction != "outbound" {
			t.Errorf("leg %d: got %q, want outbound", i, leg.Direction)
		}
	}
}

func TestDirectionTaggedLegs_InvertedDates(t *testing.T) {
	// returnDate before departDate is invalid/inverted input. The split must be
	// suppressed so a leg on the departure date is NOT mislabeled inbound just
	// because its date string sorts after the (earlier) returnDate.
	legs := []models.FlightLeg{
		{DepartureTime: "2026-07-22T08:05"}, // departure-date leg
		{DepartureTime: "2026-07-22T20:40"},
	}
	tagged, hasInbound := directionTaggedLegs(legs, "2026-07-22", "2026-07-15")
	if hasInbound {
		t.Errorf("inverted dates must not produce an inbound leg")
	}
	for i, leg := range tagged {
		if leg.Direction != "outbound" {
			t.Errorf("leg %d: got %q, want outbound (inverted dates -> safe default)", i, leg.Direction)
		}
	}
}

func TestComposeRoundTrips_SumsAndConcatenates(t *testing.T) {
	out := []models.FlightResult{owFlight("Google Flights", "EUR", 100, "HEL", "BCN")}
	in := []models.FlightResult{owFlight("Ryanair", "EUR", 60, "BCN", "HEL")}

	composed, truncated := composeRoundTrips(out, in, SearchOptions{})
	if truncated {
		t.Fatalf("did not expect truncation for 1x1 pairing")
	}
	if len(composed) != 1 {
		t.Fatalf("composed count: got %d, want 1", len(composed))
	}
	rt := composed[0]
	if rt.Price != 160 {
		t.Errorf("price: got %v, want 160 (100+60)", rt.Price)
	}
	if rt.Duration != 240 {
		t.Errorf("duration: got %d, want 240", rt.Duration)
	}
	if len(rt.Legs) != 2 {
		t.Fatalf("legs: got %d, want 2 (outbound + inbound)", len(rt.Legs))
	}
	if rt.Legs[0].DepartureAirport.Code != "HEL" || rt.Legs[1].DepartureAirport.Code != "BCN" {
		t.Errorf("leg order wrong: %q then %q", rt.Legs[0].DepartureAirport.Code, rt.Legs[1].DepartureAirport.Code)
	}
	if !strings.Contains(rt.Provider, "Google Flights") || !strings.Contains(rt.Provider, "Ryanair") {
		t.Errorf("provider label missing source providers: %q", rt.Provider)
	}
	if len(rt.Warnings) == 0 || !strings.Contains(rt.Warnings[0], "two separate one-way tickets") {
		t.Errorf("expected separate-tickets warning, got %v", rt.Warnings)
	}
}

func TestComposeRoundTrips_CheapestTotalFirst(t *testing.T) {
	out := []models.FlightResult{
		owFlight("A", "EUR", 100, "HEL", "BCN"),
		owFlight("B", "EUR", 200, "HEL", "BCN"),
	}
	in := []models.FlightResult{
		owFlight("C", "EUR", 50, "BCN", "HEL"),
		owFlight("D", "EUR", 70, "BCN", "HEL"),
	}
	composed, _ := composeRoundTrips(out, in, SearchOptions{})
	if len(composed) != 4 {
		t.Fatalf("composed count: got %d, want 4 (2x2)", len(composed))
	}
	if composed[0].Price != 150 {
		t.Errorf("cheapest total: got %v, want 150 (100+50)", composed[0].Price)
	}
	// Verify ascending order.
	for i := 1; i < len(composed); i++ {
		if composed[i].Price < composed[i-1].Price {
			t.Errorf("not sorted ascending at %d: %v < %v", i, composed[i].Price, composed[i-1].Price)
		}
	}
}

func TestComposeRoundTrips_ExcludesUnpriced(t *testing.T) {
	out := []models.FlightResult{
		owFlight("A", "EUR", 100, "HEL", "BCN"),
		owFlight("B", "", 0, "HEL", "BCN"), // unpriced — must be dropped
	}
	in := []models.FlightResult{owFlight("C", "EUR", 50, "BCN", "HEL")}
	composed, _ := composeRoundTrips(out, in, SearchOptions{})
	if len(composed) != 1 {
		t.Fatalf("composed count: got %d, want 1 (unpriced outbound dropped)", len(composed))
	}
	if composed[0].Price != 150 {
		t.Errorf("price: got %v, want 150", composed[0].Price)
	}
}

func TestNativeRoundTripCurrency_Priority(t *testing.T) {
	eur := []models.FlightResult{{Currency: "EUR"}}
	usd := []models.FlightResult{{Currency: "USD"}}

	// Explicit opts.Currency wins over everything.
	if got := nativeRoundTripCurrency(SearchOptions{Currency: "GBP"}, eur, usd); got != "GBP" {
		t.Errorf("explicit currency: got %q want GBP", got)
	}
	// Explicit opts.Currency holds even with no leg/composed signal yet — the
	// contract searchRoundTripComposed relies on when it queries the native Google
	// round-trip BEFORE the legs run (MIK-6612), so currency is not lost by reorder.
	if got := nativeRoundTripCurrency(SearchOptions{Currency: "GBP"}, nil, nil); got != "GBP" {
		t.Errorf("explicit currency with nil signals: got %q want GBP", got)
	}
	// Else fall back to the composed pairs' currency.
	if got := nativeRoundTripCurrency(SearchOptions{}, eur, usd); got != "EUR" {
		t.Errorf("composed currency: got %q want EUR", got)
	}
	// Else fall back to the outbound legs' currency.
	if got := nativeRoundTripCurrency(SearchOptions{}, nil, usd); got != "USD" {
		t.Errorf("outbound currency: got %q want USD", got)
	}
	// No signal at all -> empty (normalisation no-ops).
	if got := nativeRoundTripCurrency(SearchOptions{}, nil, nil); got != "" {
		t.Errorf("no signal: got %q want empty", got)
	}
}

func TestComposeRoundTrips_MarksSplitTicketsFareType(t *testing.T) {
	out := []models.FlightResult{owFlight("Google Flights", "EUR", 100, "HEL", "BCN")}
	in := []models.FlightResult{owFlight("Ryanair", "EUR", 60, "BCN", "HEL")}

	composed, _ := composeRoundTrips(out, in, SearchOptions{})
	if len(composed) != 1 {
		t.Fatalf("composed count: got %d, want 1", len(composed))
	}
	// A composed pair is two separate tickets — never a native round-trip fare.
	if composed[0].FareType != models.FareSplitTickets {
		t.Errorf("FareType: got %q, want %q", composed[0].FareType, models.FareSplitTickets)
	}
}

func TestComposeRoundTrips_TagsLegDirection(t *testing.T) {
	out := []models.FlightResult{owFlight("Google Flights", "EUR", 100, "HEL", "BCN")}
	in := []models.FlightResult{owFlight("Ryanair", "EUR", 60, "BCN", "HEL")}

	composed, _ := composeRoundTrips(out, in, SearchOptions{})
	if len(composed) != 1 {
		t.Fatalf("composed count: got %d, want 1", len(composed))
	}
	legs := composed[0].Legs
	if len(legs) != 2 {
		t.Fatalf("legs: got %d, want 2", len(legs))
	}
	if legs[0].Direction != "outbound" {
		t.Errorf("first leg Direction: got %q, want outbound", legs[0].Direction)
	}
	if legs[1].Direction != "inbound" {
		t.Errorf("second leg Direction: got %q, want inbound", legs[1].Direction)
	}

	// The source one-way leg slices must stay untagged — they are cached/shared
	// upstream and a leaked round-trip tag would corrupt a later one-way response.
	if out[0].Legs[0].Direction != "" {
		t.Errorf("source outbound leg was mutated: Direction=%q", out[0].Legs[0].Direction)
	}
	if in[0].Legs[0].Direction != "" {
		t.Errorf("source inbound leg was mutated: Direction=%q", in[0].Legs[0].Direction)
	}
}

func TestComposeRoundTrips_SkipsCurrencyMismatch(t *testing.T) {
	out := []models.FlightResult{owFlight("A", "USD", 100, "HEL", "BCN")}
	in := []models.FlightResult{owFlight("C", "EUR", 50, "BCN", "HEL")}
	composed, _ := composeRoundTrips(out, in, SearchOptions{})
	if len(composed) != 0 {
		t.Fatalf("expected 0 composed (currency mismatch), got %d", len(composed))
	}
}

func TestComposeRoundTrips_EmptyInputs(t *testing.T) {
	if c, _ := composeRoundTrips(nil, []models.FlightResult{owFlight("C", "EUR", 50, "BCN", "HEL")}, SearchOptions{}); len(c) != 0 {
		t.Errorf("empty outbound should yield 0 composed, got %d", len(c))
	}
	if c, _ := composeRoundTrips([]models.FlightResult{owFlight("A", "EUR", 100, "HEL", "BCN")}, nil, SearchOptions{}); len(c) != 0 {
		t.Errorf("empty inbound should yield 0 composed, got %d", len(c))
	}
}

func TestComposeRoundTrips_BoundsAndReportsTruncation(t *testing.T) {
	// roundTripLegCandidates each side -> up to candidates^2 pairings, bounded to
	// roundTripMaxResults with truncated=true.
	var out, in []models.FlightResult
	for i := 0; i < roundTripLegCandidates+4; i++ {
		out = append(out, owFlight("A", "EUR", float64(100+i), "HEL", "BCN"))
		in = append(in, owFlight("C", "EUR", float64(50+i), "BCN", "HEL"))
	}
	composed, truncated := composeRoundTrips(out, in, SearchOptions{})
	if !truncated {
		t.Errorf("expected truncated=true when pairings exceed roundTripMaxResults")
	}
	if len(composed) != roundTripMaxResults {
		t.Errorf("composed count: got %d, want %d (bounded)", len(composed), roundTripMaxResults)
	}
}

func TestRoundTripComposerStatus_TruncationVisible(t *testing.T) {
	s := roundTripComposerStatus(8, 8, roundTripMaxResults, true)
	if s.Status != models.StatusOK {
		t.Errorf("status: got %q, want ok", s.Status)
	}
	if !strings.Contains(s.FixHint, "more pairings exist") {
		t.Errorf("truncation must be visible in fix hint, got %q", s.FixHint)
	}
}

func TestRoundTripComposerStatus_NoHit(t *testing.T) {
	s := roundTripComposerStatus(0, 0, 0, false)
	if s.Status != models.StatusCheckedNoHit {
		t.Errorf("status: got %q, want checked_no_hit", s.Status)
	}
	if s.Error == "" {
		t.Errorf("expected diagnostic error on zero pairings")
	}
}

func TestComposedProviderLabel(t *testing.T) {
	if got := composedProviderLabel("Google Flights", "Ryanair"); got != "composed (Google Flights + Ryanair)" {
		t.Errorf("label: got %q", got)
	}
	if got := composedProviderLabel("", ""); got != "composed (unknown + unknown)" {
		t.Errorf("empty label: got %q", got)
	}
}

// --- AFKLM opportunistic in default RT merge (credential = enable) + no-cred silent + tag correctness ---

// (var afklmTestFlights lives in roundtrip.go as package-level seam)

func TestSearchRoundTrip_DefaultMerge_IncludesAFKLMWhenCredential(t *testing.T) {
	sample := models.FlightResult{
		Price: 453.98, Currency: "EUR", Provider: "afklm", FareType: models.FareRoundTrip,
		Legs: []models.FlightLeg{
			{DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "PRG"}, DepartureTime: "2026-05-15T06:40", Direction: "outbound"},
			{DepartureAirport: models.AirportInfo{Code: "PRG"}, ArrivalAirport: models.AirportInfo{Code: "AMS"}, DepartureTime: "2026-05-22T20:55", Direction: "inbound"},
		},
	}
	origF := afklmTestFlights
	afklmTestFlights = []models.FlightResult{sample}
	defer func() { afklmTestFlights = origF }()

	// stub NewProvider too (seam takes precedence inside search func)
	origNew := afklmNewProvider
	afklmNewProvider = func() (*afklm.AFKLMProvider, error) { return nil, nil }
	defer func() { afklmNewProvider = origNew }()

	body := makeTestFlightBody(t)
	ts := makeTestFlightServer(t, 200, body)
	defer ts.Close()
	bx := batchexec.NewTestClient(ts.URL)

	res, err := SearchFlightsWithClient(context.Background(), bx, "AMS", "PRG", "2026-05-15", SearchOptions{ReturnDate: "2026-05-22"})
	if err != nil {
		t.Fatalf("default RT must succeed: %v", err)
	}
	has := false
	for _, f := range res.Flights {
		if f.Provider == "afklm" && f.FareType == models.FareRoundTrip {
			has = true
			break
		}
	}
	if !has {
		t.Errorf("afklm RT must appear in default merge when 'credential' present via seam")
	}
}

func TestSearchRoundTrip_DefaultMerge_SilentSkipNoCredential(t *testing.T) {
	origNew := afklmNewProvider
	afklmNewProvider = func() (*afklm.AFKLMProvider, error) { return nil, afklm.ErrNoCredential }
	defer func() { afklmNewProvider = origNew }()

	origF := afklmTestFlights
	afklmTestFlights = nil
	defer func() { afklmTestFlights = origF }()

	body := makeTestFlightBody(t)
	ts := makeTestFlightServer(t, 200, body)
	defer ts.Close()
	bx := batchexec.NewTestClient(ts.URL)

	res, err := SearchFlightsWithClient(context.Background(), bx, "HEL", "NRT", "2026-06-15", SearchOptions{ReturnDate: "2026-06-22"})
	if err != nil {
		t.Fatalf("no-cred must not fail the merge: %v", err)
	}
	for _, f := range res.Flights {
		if f.Provider == "afklm" {
			t.Errorf("afklm must be absent with no credential")
		}
	}
}

func TestSearchRoundTrip_DefaultMerge_SilentSkipDailyQuota(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	d := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("no HTTP call on AFKLM daily quota in default-merge path")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cli, err := afklm.NewClient(afklm.ClientOptions{
		Credential: "dummy",
		CacheDir:   d,
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Now:        func() time.Time { return now },
		Limiter:    rate.NewLimiter(rate.Inf, 1),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	cc := cli.Cache()
	for i := 0; i < 95; i++ {
		_ = cc.IncQuota(now)
	}

	p := afklm.NewProviderWithClient(cli)

	origNew := afklmNewProvider
	afklmNewProvider = func() (*afklm.AFKLMProvider, error) { return p, nil }
	defer func() { afklmNewProvider = origNew }()

	origF := afklmTestFlights
	afklmTestFlights = nil
	defer func() { afklmTestFlights = origF }()

	flights, statuses := searchAFKLMNativeRoundTrip(context.Background(), "AMS", "PRG", "2026-08-01", "2026-08-08", SearchOptions{})
	if flights != nil || statuses != nil {
		t.Errorf("searchAFKLMNativeRoundTrip must return nil,nil on daily quota (like ErrNoCredential); got %d/%d", len(flights), len(statuses))
	}
}

// TestSearchMultiAirport_Spread_AFKLMAtMostOnePerLogicalSearch verifies that
// when SearchMultiAirport (the spread path used by find + default merge RT)
// fans multiple origins, AFKLM default-merge path issues at most 1 seam call
// (hence at most 1 query) thanks to the primary-only + seam-suppression logic.
func TestSearchMultiAirport_Spread_AFKLMAtMostOnePerLogicalSearch(t *testing.T) {
	origNew := afklmNewProvider
	defer func() { afklmNewProvider = origNew }()

	var calls int32
	afklmNewProvider = func() (*afklm.AFKLMProvider, error) {
		atomic.AddInt32(&calls, 1)
		// Return non-NoCredential err so searchAFKLM skips without calling SearchFlights on a nil p
		// and without network.
		return nil, fmt.Errorf("afklm test: no real call")
	}

	origF := afklmTestFlights
	afklmTestFlights = nil
	defer func() { afklmTestFlights = origF }()

	// RT + multiple origins exercises the spread + primary AFKLM restriction.
	// Other providers fan normally; AFKLM must not.
	// Use short ctx so parallel sub-searches (google etc) fail fast instead of hanging on net.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	opts := SearchOptions{ReturnDate: "2026-07-10"}
	_, err := SearchMultiAirport(ctx, []string{"HEL", "AMS"}, []string{"BCN"}, "2026-07-01", opts)
	if err != nil {
		// SearchMultiAirport itself does not error on provider failures (they are silent-skipped).
		t.Fatalf("SearchMultiAirport unexpected err: %v", err)
	}
	if calls > 1 {
		t.Errorf("AFKLM seam called %d times on spread RT search; want <=1 (primary only)", calls)
	}
}

func TestOutboundOnlyNativeShell_NotTaggedRoundTrip(t *testing.T) {
	src := []models.FlightResult{owFlight("Google Flights", "EUR", 300, "HEL", "BCN")}
	got := tagGoogleNativeRoundTrip(src, "HEL", "BCN", "2026-07-01", "2026-07-08", "EUR")
	if len(got) != 1 {
		t.Fatal("expected 1")
	}
	if got[0].FareType == models.FareRoundTrip {
		t.Error("outbound-only native shell must NOT be tagged FareRoundTrip")
	}
}

// --- local test helpers ---

func makeTestFlightBody(t *testing.T) []byte {
	t.Helper()
	leg := make([]any, 23)
	leg[3] = "HEL"
	leg[6] = "NRT"
	leg[20] = []any{2026.0, 6.0, 15.0}
	leg[21] = []any{2026.0, 6.0, 16.0}
	leg[22] = []any{"AY", "79", nil, "Finnair"}
	fi := make([]any, 13)
	fi[2] = []any{leg}
	fi[9] = 350.0
	fl := []any{fi, []any{[]any{nil, 350.0}}, nil, nil, []any{}}
	inner := make([]any, 4)
	inner[2] = []any{[]any{fl}}
	ij, _ := json.Marshal(inner)
	outer := []any{[]any{nil, nil, string(ij)}}
	oj, _ := json.Marshal(outer)
	return append([]byte(")]}'\n"), oj...)
}

func makeTestFlightServer(t *testing.T, code int, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write(body)
	}))
}
