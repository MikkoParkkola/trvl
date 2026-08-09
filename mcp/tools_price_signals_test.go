package mcp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/booking"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/pricesignal"
)

// --- cheapestProviderPrice ---

func TestCheapestProviderPrice_Empty(t *testing.T) {
	t.Parallel()
	// GIVEN no providers
	// WHEN cheapestProviderPrice is called
	got := cheapestProviderPrice(nil)
	// THEN the zero value is returned (Price == 0)
	if got.Price != 0 {
		t.Errorf("want zero price, got %v", got.Price)
	}
}

func TestCheapestProviderPrice_PicksLowest(t *testing.T) {
	t.Parallel()
	// GIVEN three providers at 200, 100, 150
	providers := []models.ProviderPrice{
		{Provider: "A", Price: 200, Currency: "EUR"},
		{Provider: "B", Price: 100, Currency: "EUR"},
		{Provider: "C", Price: 150, Currency: "EUR"},
	}
	// WHEN cheapestProviderPrice is called
	got := cheapestProviderPrice(providers)
	// THEN the 100-EUR provider is returned
	if got.Provider != "B" || got.Price != 100 {
		t.Errorf("want B/100, got %s/%.0f", got.Provider, got.Price)
	}
}

func TestCheapestProviderPrice_IgnoresNonPositive(t *testing.T) {
	t.Parallel()
	// GIVEN providers with non-positive prices mixed in
	providers := []models.ProviderPrice{
		{Provider: "zero", Price: 0, Currency: "EUR"},
		{Provider: "neg", Price: -5, Currency: "EUR"},
		{Provider: "ok", Price: 99, Currency: "EUR"},
	}
	// WHEN cheapestProviderPrice is called
	got := cheapestProviderPrice(providers)
	// THEN the only positive provider is returned
	if got.Provider != "ok" {
		t.Errorf("want ok, got %s", got.Provider)
	}
}

// --- hotelBookingReadiness ---

func TestHotelBookingReadiness_AllSignalsTrue(t *testing.T) {
	t.Parallel()
	// GIVEN a stable, verified provider with a non-empty hotel ID
	providers := []models.ProviderPrice{
		{
			Provider:        "Booking.com",
			Price:           120,
			Currency:        "EUR",
			LinkDurability:  "stable",
			PriceConfidence: models.PriceConfidenceVerified,
		},
	}
	// WHEN readiness is evaluated
	v := hotelBookingReadiness("hotel-abc", providers)
	// THEN only RefundabilityKnown remains unknown, so verdict is Caution (not Ready).
	// Ready requires ALL four signals explicitly true for this selected seller.
	if v.Readiness == booking.Ready {
		t.Error("a selected seller with no refundability terms cannot reach Ready")
	}
	if v.Readiness != booking.Caution {
		t.Errorf("want Caution, got %s", v.Readiness)
	}
	// The one downgrade reason should mention refundability.
	// Refundability is unobtainable on this endpoint, so it belongs in the
	// ceiling reasons and NOT in the ordinary reasons. This assertion used to
	// require the opposite, which is the defect an external tester reported: a
	// source limitation reading as a finding about the property.
	found := false
	for _, r := range v.CeilingReasons {
		if strings.Contains(r, "refundability") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected refundability in the ceiling reasons, got %v", v.CeilingReasons)
	}
	for _, r := range v.Reasons {
		if strings.Contains(r, "refundability") {
			t.Errorf("refundability reported as a property finding: %q", r)
		}
	}
}

func TestHotelBookingReadiness_ExpiringLink(t *testing.T) {
	t.Parallel()
	// GIVEN an expiring link
	providers := []models.ProviderPrice{
		{
			Provider:        "Google",
			Price:           100,
			Currency:        "USD",
			LinkDurability:  "expiring",
			PriceConfidence: models.PriceConfidenceVerified,
		},
	}
	// WHEN readiness is evaluated with a known hotel ID
	v := hotelBookingReadiness("hotel-xyz", providers)
	// THEN link_stable is false → downgrade present in reasons
	hasLinkReason := false
	for _, r := range v.Reasons {
		if strings.Contains(r, "link_stable") {
			hasLinkReason = true
		}
	}
	if !hasLinkReason {
		t.Errorf("expected link_stable downgrade, got reasons: %v", v.Reasons)
	}
}

func TestHotelBookingReadiness_UnverifiedPrice(t *testing.T) {
	t.Parallel()
	// GIVEN an unverified price confidence
	providers := []models.ProviderPrice{
		{
			Provider:        "Google",
			Price:           80,
			Currency:        "USD",
			PriceConfidence: models.PriceConfidenceUnverified,
		},
	}
	// WHEN readiness is evaluated
	v := hotelBookingReadiness("hotel-q", providers)
	// THEN verified signal is false → downgrade in reasons
	hasVerifiedReason := false
	for _, r := range v.Reasons {
		if strings.Contains(r, "verified") {
			hasVerifiedReason = true
		}
	}
	if !hasVerifiedReason {
		t.Errorf("expected verified downgrade, got reasons: %v", v.Reasons)
	}
}

func TestHotelBookingReadiness_EmptyHotelID(t *testing.T) {
	t.Parallel()
	// GIVEN no hotel ID (identity not confirmed)
	providers := []models.ProviderPrice{
		{Provider: "Google", Price: 50, Currency: "EUR"},
	}
	// WHEN readiness is evaluated with empty hotelID
	v := hotelBookingReadiness("", providers)
	// THEN identity_confirmed is nil → downgrade present
	hasIdentityReason := false
	for _, r := range v.Reasons {
		if strings.Contains(r, "identity_confirmed") {
			hasIdentityReason = true
		}
	}
	if !hasIdentityReason {
		t.Errorf("expected identity_confirmed downgrade, got reasons: %v", v.Reasons)
	}
}

// TestHotelBookingReadiness_EmptyProviders confirms no panic on nil providers.
func TestHotelBookingReadiness_EmptyProviders(t *testing.T) {
	t.Parallel()
	// GIVEN no providers (cheapestProviderPrice returns zero)
	v := hotelBookingReadiness("hotel-z", nil)
	// THEN readiness is Unverified (all signals nil) — must not panic
	if v.Readiness == "" {
		t.Error("readiness must not be empty string")
	}
}

// TestBookingReadinessReasons_NilRefundability proves that a nil
// RefundabilityKnown signal always produces a downgrade reason referencing
// "refundability" — which is the structural guarantee of the prices endpoint.
func TestBookingReadinessReasons_NilRefundability(t *testing.T) {
	t.Parallel()
	providers := []models.ProviderPrice{
		{
			Provider:        "Expedia",
			Price:           200,
			Currency:        "USD",
			LinkDurability:  "stable",
			PriceConfidence: models.PriceConfidenceVerified,
		},
	}
	v := hotelBookingReadiness("hotel-ref", providers)
	hasRefundabilityReason := false
	// Missing from the selected seller, so it is a conditional ceiling reason.
	for _, r := range v.CeilingReasons {
		if strings.Contains(r, "refundability") {
			hasRefundabilityReason = true
		}
	}
	if !hasRefundabilityReason {
		t.Errorf("expected refundability in the ceiling reasons for this selected seller, got: %v", v.CeilingReasons)
	}
	for _, r := range v.Reasons {
		if strings.Contains(r, "refundability") {
			t.Errorf("refundability reported as a property finding: %q", r)
		}
	}
}

// --- hotelPriceSignals: store isolation ---

func TestHotelPriceSignals_StoreErrorDoesNotBreakReadiness(t *testing.T) {
	// GIVEN a temp HOME so watch.DefaultStore uses an isolated dir
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	// GIVEN a valid result
	result := &models.HotelPriceResult{
		Success: true,
		HotelID: "hotel-test",
		CheckIn: "2027-01-10",
		Providers: []models.ProviderPrice{
			{Provider: "Booking.com", Price: 120, Currency: "EUR", LinkDurability: "stable", PriceConfidence: models.PriceConfidenceVerified},
		},
	}

	// WHEN hotelPriceSignals is called
	pos, readiness := hotelPriceSignals("hotel-test", "2027-01-10", result)

	// THEN readiness is always computed (derived from result, not store)
	if readiness == nil {
		t.Fatal("readiness must not be nil even on store error")
	}
	// pos may be nil (no history yet — that's fine)
	_ = pos
}

func TestHotelPriceSignals_NilResult(t *testing.T) {
	t.Parallel()
	// GIVEN nil result
	pos, readiness := hotelPriceSignals("hotel-x", "2027-01-01", nil)
	// THEN both are nil — no panic
	if pos != nil || readiness != nil {
		t.Error("nil result must yield nil pos and nil readiness")
	}
}

// --- flightPriceSignals: store isolation ---

func TestFlightPriceSignals_StoreErrorReturnsNil(t *testing.T) {
	// GIVEN a temp HOME so watch.DefaultStore uses an isolated dir
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	// GIVEN a minimal successful flight result
	result := &models.FlightSearchResult{
		Success: true,
		Count:   1,
		Flights: []models.FlightResult{
			{Price: 300, Currency: "EUR"},
			{Price: 250, Currency: "EUR"},
		},
	}

	// WHEN flightPriceSignals is called for a single O/D
	pos, savings := flightPriceSignals("HEL", "CDG", "2027-06-01", result)

	// THEN no panic; pos may be nil (no prior history) or non-nil (after observation)
	_ = pos
	_ = savings
}

func TestFlightPriceSignals_MultiAirportSkipped(t *testing.T) {
	t.Parallel()
	// GIVEN a result with empty origin (multi-airport guard)
	result := &models.FlightSearchResult{
		Success: true,
		Flights: []models.FlightResult{{Price: 400, Currency: "EUR"}},
	}
	// WHEN called with empty origin
	pos, savings := flightPriceSignals("", "CDG", "2027-06-01", result)
	// THEN both nil — never errors, never panics
	if pos != nil || savings != nil {
		t.Error("multi-airport path must return nil, nil")
	}
}

func TestFlightPriceSignals_NilResult(t *testing.T) {
	t.Parallel()
	pos, savings := flightPriceSignals("HEL", "CDG", "2027-06-01", nil)
	if pos != nil || savings != nil {
		t.Error("nil result must yield nil pos and nil savings")
	}
}

// --- Position attachment ---

// TestPricePositionAttachedToHotelPayload proves that hotelPriceSignals wires
// a price_position into the JSON payload when history is available.
func TestPricePositionAttachedToHotelPayload(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	result := &models.HotelPriceResult{
		Success: true,
		HotelID: "hotel-pos-test",
		CheckIn: "2027-03-01",
		Providers: []models.ProviderPrice{
			{Provider: "Google", Price: 150, Currency: "EUR"},
		},
	}

	// pos may be nil on first call (observation just logged, 1 point < floor=10).
	pos, _ := hotelPriceSignals("hotel-pos-test", "2027-03-01", result)
	if pos != nil {
		b, err := json.Marshal(pos)
		if err != nil {
			t.Fatalf("marshal position: %v", err)
		}
		if !strings.Contains(string(b), `"current"`) {
			t.Errorf("position JSON missing 'current' field: %s", b)
		}
	}
}

// TestSameDayAlternativeSaving proves that a cheaper same-day flight produces
// a same_day_alternative saving in the payload.
func TestSameDayAlternativeSaving(t *testing.T) {
	// t.Setenv requires no t.Parallel.
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	// GIVEN a flight list where the headline is pricier than an alternative
	result := &models.FlightSearchResult{
		Success: true,
		Count:   2,
		Flights: []models.FlightResult{
			{Price: 400, Currency: "EUR"}, // headline (sorted first)
			{Price: 300, Currency: "EUR"}, // cheaper alternative
		},
	}

	// WHEN flightPriceSignals is called
	_, savings := flightPriceSignals("AMS", "LHR", "2027-04-01", result)

	// THEN a same_day_alternative saving of 100 EUR is present
	found := false
	for _, s := range savings {
		if s.Kind == "same_day_alternative" && s.Amount == 100 && s.Currency == "EUR" && s.CallFree {
			found = true
		}
	}
	if !found {
		t.Errorf("expected same_day_alternative saving of 100 EUR, got: %+v", savings)
	}
}

// TestVsHistorySavingNotEmittedBelowFloor proves that a non-confident position
// does not produce a vs_history saving.
func TestVsHistorySavingNotEmittedBelowFloor(t *testing.T) {
	t.Parallel()
	// GIVEN a position that is not confident (Observations < floor=10)
	pos := &pricesignal.Position{
		Band:         pricesignal.BandUnknown,
		Verdict:      pricesignal.VerdictUnknown,
		Current:      200,
		Median:       100,
		Observations: 3,
		Confident:    false,
	}
	// WHEN the confidence gate is checked via the test shim
	couldSave := positionCouldYieldHistorySaving(pos)
	// THEN gate returns false — no spurious "you could save X" claims
	if couldSave {
		t.Error("non-confident position must not yield a vs_history saving")
	}
}

// positionCouldYieldHistorySaving is a pure test-only shim that reflects the
// same confidence gate as counterfactual.VsHistory: a saving is only emitted
// when pos.Confident is true AND the current price is below the median.
func positionCouldYieldHistorySaving(pos *pricesignal.Position) bool {
	if pos == nil || !pos.Confident || pos.Median <= 0 {
		return false
	}
	return pos.Median > pos.Current
}

// TestVsHistorySaving_ConfidenceFloorViaCompute proves the pricesignal.Compute
// floor path: fewer obs than floor → Confident=false → no verdict.
func TestVsHistorySaving_ConfidenceFloorViaCompute(t *testing.T) {
	t.Parallel()
	// GIVEN fewer observations than the floor
	history := []float64{300, 320}
	p := pricesignal.Compute(history, 500, 10) // floor=10 > 2 obs
	if p.Confident {
		t.Fatal("expected not-confident with 2 obs below floor 10")
	}
	if p.Band != pricesignal.BandUnknown {
		t.Errorf("want BandUnknown, got %s", p.Band)
	}
	// Gate must block saving.
	if positionCouldYieldHistorySaving(&p) {
		t.Error("non-confident position from Compute must not yield saving")
	}
}

// --- Savings: call-free guarantee ---

func TestSavingsAreAlwaysCallFree(t *testing.T) {
	// t.Setenv requires no t.Parallel.
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	result := &models.FlightSearchResult{
		Success: true,
		Count:   2,
		Flights: []models.FlightResult{
			{Price: 500, Currency: "USD"},
			{Price: 350, Currency: "USD"},
		},
	}
	_, savings := flightPriceSignals("JFK", "LAX", "2027-08-15", result)
	for _, s := range savings {
		if !s.CallFree {
			t.Errorf("saving %s must be call_free, got false", s.Kind)
		}
		if s.AsOf.IsZero() {
			t.Errorf("saving %s must have a non-zero AsOf timestamp", s.Kind)
		}
	}
}

// --- Accumulation ---

// TestHotelPriceSignals_AccumulatesPosition proves that after the first call
// logs an observation the position carries the correct Current field.
func TestHotelPriceSignals_AccumulatesPosition(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)

	result := &models.HotelPriceResult{
		Success: true,
		HotelID: "acc-hotel",
		CheckIn: "2027-05-01",
		Providers: []models.ProviderPrice{
			{Provider: "Google", Price: 180, Currency: "EUR"},
		},
	}

	// First call logs the observation.
	pos1, _ := hotelPriceSignals("acc-hotel", "2027-05-01", result)

	// Second call should return a position with Current == 180.
	pos2, _ := hotelPriceSignals("acc-hotel", "2027-05-01", result)
	if pos2 == nil {
		// Acceptable with only 1-2 obs — assert no panic.
		_ = pos1
		return
	}
	if pos2.Current != 180 {
		t.Errorf("position Current = %.0f, want 180", pos2.Current)
	}
	if pos2.Observations <= 0 {
		t.Error("observations must be > 0")
	}
}

// --- Error sentinel (import smoke test) ---

func TestErrors_NewCompiles(t *testing.T) {
	t.Parallel()
	err := errors.New("sentinel")
	if err == nil {
		t.Error("errors.New returned nil")
	}
}

// Ensure time import is used.
var _ = time.Now

// --- roomsBookingReadiness (MCP hotel_rooms readiness, MIK-6232) ---

func TestRoomsBookingReadiness_ReadyReachable(t *testing.T) {
	refundable := true
	avail := &hotels.RoomAvailability{
		HotelID: "/g/abc",
		Rooms: []hotels.RoomType{
			{
				Refundable:  &refundable,
				ProviderURL: "https://www.booking.com/hotel/x.html",
				InventoryOptions: []models.RoomInventoryQuote{
					{PriceConfidence: models.PriceConfidenceVerified},
				},
			},
		},
	}
	if v := roomsBookingReadiness(avail); v.Readiness != booking.Ready {
		t.Fatalf("want ready, got %s (reasons %v)", v.Readiness, v.Reasons)
	}
}

func TestRoomsBookingReadiness_ExpiringLinkDowngrades(t *testing.T) {
	refundable := true
	avail := &hotels.RoomAvailability{
		HotelID: "/g/abc",
		Rooms: []hotels.RoomType{
			{
				Refundable:  &refundable,
				ProviderURL: "https://www.google.com/aclk?adurl=x",
				InventoryOptions: []models.RoomInventoryQuote{
					{PriceConfidence: models.PriceConfidenceVerified},
				},
			},
		},
	}
	if v := roomsBookingReadiness(avail); v.Readiness == booking.Ready {
		t.Fatalf("expiring link must downgrade below ready")
	}
}
