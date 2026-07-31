package mcp

import (
	"context"
	"os"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/watch"
)

func newWatchStore(t *testing.T) *watch.Store {
	t.Helper()
	dir := t.TempDir()
	s := watch.NewStore(dir)
	if err := s.Load(); err != nil {
		t.Fatalf("watch store load: %v", err)
	}
	return s
}

func TestHandleWatchPrice_InvalidType(t *testing.T) {
	t.Parallel()
	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "train",
		"target_price": 100.0,
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestHandleWatchPrice_ZeroTargetPrice(t *testing.T) {
	t.Parallel()
	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "flight",
		"target_price": 0.0,
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for zero target price")
	}
}

func TestHandleWatchPrice_FlightMissingOriginDest(t *testing.T) {
	t.Parallel()
	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "flight",
		"target_price": 200.0,
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing origin/dest")
	}
}

func TestHandleWatchPrice_FlightMissingDate(t *testing.T) {
	t.Parallel()
	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "flight",
		"target_price": 200.0,
		"origin":       "HEL",
		"destination":  "BCN",
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing date")
	}
}

func TestHandleWatchPrice_FlightSuccess(t *testing.T) {
	// Create a temp dir and override the home so DefaultStore uses it.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	content, structured, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "flight",
		"target_price": 300.0,
		"origin":       "hel",
		"destination":  "bcn",
		"date":         "2099-07-01",
		"return_date":  "2099-07-08",
		"currency":     "EUR",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
	// Text should mention the airports in upper case.
	if !containsString(content[0].Text, "HEL") {
		t.Errorf("expected HEL in response, got: %s", content[0].Text)
	}
	if !containsString(content[0].Text, "BCN") {
		t.Errorf("expected BCN in response, got: %s", content[0].Text)
	}
	// Return date should appear in summary.
	if !containsString(content[0].Text, "2099-07-08") {
		t.Errorf("expected return date in response, got: %s", content[0].Text)
	}
}

func TestHandleWatchPrice_FlightViaDepart_date(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "flight",
		"target_price": 200.0,
		"origin":       "HEL",
		"destination":  "NRT",
		"depart_date":  "2099-08-01",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleWatchPrice_HotelMissingLocation(t *testing.T) {
	t.Parallel()
	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "hotel",
		"target_price": 150.0,
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing hotel location")
	}
}

func TestHandleWatchPrice_HotelMissingCheckOut(t *testing.T) {
	t.Parallel()
	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "hotel",
		"target_price": 150.0,
		"location":     "Barcelona",
		"check_in":     "2099-09-01",
	}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing check_out")
	}
}

func TestHandleWatchPrice_HotelSuccess(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	content, structured, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "hotel",
		"target_price": 150.0,
		"location":     "Barcelona",
		"check_in":     "2099-09-01",
		"check_out":    "2099-09-05",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
	if !containsString(content[0].Text, "Barcelona") {
		t.Errorf("expected location in response, got: %s", content[0].Text)
	}
}

func TestHandleWatchPrice_HotelViaDestinationFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// No "location" field, use "destination" fallback.
	content, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "hotel",
		"target_price": 200.0,
		"destination":  "Paris",
		"check_in":     "2099-10-01",
		"check_out":    "2099-10-05",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
}

func TestHandleWatchPrice_HotelViaDateFallback(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// check_in falls back to "date".
	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "hotel",
		"target_price": 100.0,
		"location":     "Rome",
		"date":         "2099-11-01",
		"check_out":    "2099-11-05",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error using date fallback: %v", err)
	}
}

func TestHandleWatchPrice_DefaultCurrency(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	content, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "flight",
		"target_price": 400.0,
		"origin":       "JFK",
		"destination":  "LHR",
		"date":         "2099-06-15",
		// no currency — round 24: leaves it empty (provider-derived on first
		// quote) instead of defaulting to "EUR", which previously made the
		// first non-EUR quote look like a currency mismatch and clear the
		// threshold it had just been set to. Found by GPT second-opinion
		// review, 2026-07-31 (round 24).
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if containsString(content[0].Text, "EUR") {
		t.Errorf("expected no fabricated EUR currency for an omitted currency, got: %s", content[0].Text)
	}
}

// ============================================================
// handleListWatches — empty and with entries
// ============================================================

func TestHandleListWatches_Empty(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("hits live external APIs; set TRVL_TEST_LIVE_INTEGRATIONS=1 to run. Tracked in #45")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	content, structured, err := handleListWatches(context.Background(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "No active price watches") {
		t.Errorf("expected empty message, got: %s", content[0].Text)
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
}

func TestHandleListWatches_WithEntries(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Pre-populate watches by calling handleWatchPrice.
	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "flight",
		"target_price": 250.0,
		"origin":       "HEL",
		"destination":  "BCN",
		"date":         "2099-05-01",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup watch: %v", err)
	}

	content, structured, err := handleListWatches(context.Background(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "active watch") {
		t.Errorf("expected watch count, got: %s", content[0].Text)
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
}

// ============================================================
// handleCheckWatches — empty watches path
// ============================================================

func TestHandleCheckWatches_Empty(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("hits live external APIs; set TRVL_TEST_LIVE_INTEGRATIONS=1 to run. Tracked in #45")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	content, structured, err := handleCheckWatches(context.Background(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "No active watches") {
		t.Errorf("expected empty message, got: %s", content[0].Text)
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
}

func TestHandleCheckWatches_WithWatches(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	// Add a flight watch.
	_, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "flight",
		"target_price": 300.0,
		"origin":       "HEL",
		"destination":  "CDG",
		"date":         "2099-06-01",
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup watch: %v", err)
	}

	content, structured, err := handleCheckWatches(context.Background(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	// Should mention checked count.
	if !containsString(content[0].Text, "Checked") {
		t.Errorf("expected 'Checked' in response, got: %s", content[0].Text)
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
}

// ============================================================
// watchRoute — all type branches
// ============================================================

func TestWatchRoute_Flight_WithDates(t *testing.T) {
	t.Parallel()
	w := watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2099-07-01",
		ReturnDate:  "2099-07-08",
	}
	route := watchRoute(w)
	if !containsString(route, "HEL") || !containsString(route, "BCN") {
		t.Errorf("watchRoute(flight) = %q, want HEL and BCN", route)
	}
	if !containsString(route, "2099-07-01") {
		t.Errorf("watchRoute(flight) = %q, want depart date", route)
	}
	if !containsString(route, "2099-07-08") {
		t.Errorf("watchRoute(flight) = %q, want return date", route)
	}
}

func TestWatchRoute_Flight_NoDate(t *testing.T) {
	t.Parallel()
	w := watch.Watch{
		Type:        "flight",
		Origin:      "JFK",
		Destination: "LHR",
	}
	route := watchRoute(w)
	if !containsString(route, "JFK") || !containsString(route, "LHR") {
		t.Errorf("watchRoute(flight no date) = %q", route)
	}
}

func TestWatchRoute_Hotel_WithDates(t *testing.T) {
	t.Parallel()
	w := watch.Watch{
		Type:        "hotel",
		Destination: "Barcelona",
		DepartFrom:  "2099-09-01",
		DepartTo:    "2099-09-05",
	}
	route := watchRoute(w)
	if !containsString(route, "Barcelona") {
		t.Errorf("watchRoute(hotel) = %q, want Barcelona", route)
	}
	if !containsString(route, "2099-09-01") {
		t.Errorf("watchRoute(hotel) = %q, want check-in date", route)
	}
}

func TestWatchRoute_Hotel_WithHotelName(t *testing.T) {
	t.Parallel()
	w := watch.Watch{
		Type:        "hotel",
		Destination: "Barcelona",
		HotelName:   "Hotel Arts",
		DepartDate:  "2099-09-01",
	}
	route := watchRoute(w)
	if !containsString(route, "Hotel Arts") {
		t.Errorf("watchRoute(hotel with name) = %q, want hotel name", route)
	}
}

func TestWatchRoute_Hotel_NoDates(t *testing.T) {
	t.Parallel()
	w := watch.Watch{
		Type:        "hotel",
		Destination: "Rome",
	}
	route := watchRoute(w)
	if route != "Rome" {
		t.Errorf("watchRoute(hotel no dates) = %q, want Rome", route)
	}
}

func TestWatchRoute_Room(t *testing.T) {
	t.Parallel()
	w := watch.Watch{
		Type:         "room",
		HotelName:    "Grand Hotel",
		RoomKeywords: []string{"suite", "ocean view"},
	}
	route := watchRoute(w)
	if !containsString(route, "Grand Hotel") {
		t.Errorf("watchRoute(room) = %q, want hotel name", route)
	}
	if !containsString(route, "suite") {
		t.Errorf("watchRoute(room) = %q, want keyword", route)
	}
}

func TestWatchRoute_Default(t *testing.T) {
	t.Parallel()
	w := watch.Watch{
		Type:        "unknown",
		Destination: "Paris",
	}
	route := watchRoute(w)
	if route != "Paris" {
		t.Errorf("watchRoute(unknown) = %q, want Paris", route)
	}
}

// ============================================================
// mcpPriceChecker.CheckPrice
// ============================================================

// TestMCPPriceChecker_DelegatesUnsupportedType verifies the MCP price checker
// delegates to the shared livecheck implementation: an unsupported watch type
// yields an error (not a fabricated zero price). This is deterministic and hits
// no network. Live re-pricing is covered by the probe-gated test in
// tools_watch_price_live_test.go.
func TestMCPPriceChecker_DelegatesUnsupportedType(t *testing.T) {
	t.Parallel()
	c := &mcpPriceChecker{}
	price, _, _, err := c.CheckPrice(context.Background(), watch.Watch{
		Type:     "ferry", // not flight/hotel: livecheck rejects it
		Currency: "EUR",
	})
	if err == nil {
		t.Fatal("expected error for unsupported watch type, got nil")
	}
	if price != 0 {
		t.Errorf("price = %f, want 0 on error", price)
	}
}

// ============================================================
// handleProviderHealth — empty log and with data
// ============================================================

func TestHandleProviderHealth_EmptyLog(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("hits live external APIs; set TRVL_TEST_LIVE_INTEGRATIONS=1 to run. Tracked in #45")
	}
	// Use a temp dir that has no health.jsonl.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	content, _, err := handleProviderHealth(context.Background(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "No health data") {
		t.Errorf("expected empty message, got: %s", content[0].Text)
	}
}
