package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/providers"
	"github.com/MikkoParkkola/trvl/internal/watch"
)

// ============================================================
// handleWatchPrice — all branches
// ============================================================

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

	content, _, err := handleWatchPrice(context.Background(), map[string]any{
		"type":         "flight",
		"target_price": 400.0,
		"origin":       "JFK",
		"destination":  "LHR",
		"date":         "2099-06-15",
		// no currency — should default to EUR
	}, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsString(content[0].Text, "EUR") {
		t.Errorf("expected default EUR currency, got: %s", content[0].Text)
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

func TestMCPPriceChecker_ReturnsZero(t *testing.T) {
	t.Parallel()
	c := &mcpPriceChecker{}
	price, currency, date, err := c.CheckPrice(context.Background(), watch.Watch{
		Type:     "flight",
		Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if price != 0 {
		t.Errorf("price = %f, want 0", price)
	}
	if currency != "EUR" {
		t.Errorf("currency = %q, want EUR", currency)
	}
	if date != "" {
		t.Errorf("date = %q, want empty", date)
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

func TestHandleProviderHealth_WithData(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("hits live external APIs; set TRVL_TEST_LIVE_INTEGRATIONS=1 to run. Tracked in #45")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Write health log entries directly to temp dir.
	healthPath := filepath.Join(tmp, ".trvl", "health.jsonl")
	if err := os.MkdirAll(filepath.Dir(healthPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	entries := []providers.HealthEntry{
		{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Provider:  "test-provider",
			Operation: "search",
			Status:    "ok",
			LatencyMs: 200,
			Results:   5,
		},
		{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Provider:  "test-provider",
			Operation: "search",
			Status:    "error",
			LatencyMs: 500,
			Error:     "connection refused",
		},
		{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Provider:  "other-provider",
			Operation: "search",
			Status:    "timeout",
			LatencyMs: 15000,
			Error:     "deadline exceeded",
		},
	}

	f, err := os.Create(healthPath)
	if err != nil {
		t.Fatalf("create health log: %v", err)
	}
	for _, e := range entries {
		line, _ := json.Marshal(e)
		f.Write(append(line, '\n'))
	}
	f.Close()

	content, structured, err := handleProviderHealth(context.Background(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "test-provider") {
		t.Errorf("expected provider name in response, got: %s", content[0].Text)
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
	// Structured should be a map with "providers" key.
	data, _ := json.Marshal(structured)
	var parsed struct {
		Providers []struct {
			Provider   string `json:"provider"`
			TotalCalls int    `json:"total_calls"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal structured: %v", err)
	}
	if len(parsed.Providers) < 2 {
		t.Errorf("expected at least 2 providers, got %d", len(parsed.Providers))
	}
}

func TestHandleProviderHealth_WithErrorsAndTimeouts(t *testing.T) {
	if os.Getenv("TRVL_TEST_LIVE_INTEGRATIONS") != "1" {
		t.Skip("hits live external APIs; set TRVL_TEST_LIVE_INTEGRATIONS=1 to run. Tracked in #45")
	}
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	healthPath := filepath.Join(tmp, ".trvl", "health.jsonl")
	if err := os.MkdirAll(filepath.Dir(healthPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	entry := providers.HealthEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Provider:  "flaky-provider",
		Operation: "search",
		Status:    "error",
		LatencyMs: 100,
		Error:     "timeout",
	}
	line, _ := json.Marshal(entry)
	os.WriteFile(healthPath, append(line, '\n'), 0o600)

	content, _, err := handleProviderHealth(context.Background(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !containsString(content[0].Text, "flaky-provider") {
		t.Errorf("expected provider name, got: %s", content[0].Text)
	}
	// Error count should show in text.
	if !containsString(content[0].Text, "errors") {
		t.Errorf("expected error count in response, got: %s", content[0].Text)
	}
}

// ============================================================
// readTripsUpcoming — empty and with trip data
// ============================================================

func TestReadTripsUpcoming_Empty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	s := NewServer()
	result, err := s.readTripsUpcoming()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	if !containsString(result.Contents[0].Text, "No upcoming trips") {
		t.Errorf("expected empty message, got: %s", result.Contents[0].Text)
	}
}

// ============================================================
// readTripsList / readTripsAlerts — empty home dir
// ============================================================

func TestReadTripsList_Empty(t *testing.T) {
	tmp := t.TempDir()
	orig := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", orig)

	s := NewServer()
	result, err := s.readTripsList()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	if result.Contents[0].MimeType != "application/json" {
		t.Errorf("expected JSON mime type, got: %s", result.Contents[0].MimeType)
	}
}

func TestReadTripsAlerts_Empty(t *testing.T) {
	tmp := t.TempDir()
	orig := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", orig)

	s := NewServer()
	result, err := s.readTripsAlerts()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	if result.Contents[0].MimeType != "application/json" {
		t.Errorf("expected JSON mime type, got: %s", result.Contents[0].MimeType)
	}
}

// ============================================================
// readWatchResource — ID-based lookup via watch store
// ============================================================

func TestReadWatchResource_InvalidLegacyURI(t *testing.T) {
	t.Parallel()
	s := NewServer()
	s.watchStore = newWatchStore(t)

	// Incomplete legacy format → error.
	_, err := s.readWatchResource("trvl://watch/only-one-part")
	if err == nil {
		t.Fatal("expected error for malformed watch URI")
	}
}

func TestReadWatchResource_IDLookup(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	s := NewServer()
	store := watch.NewStore(tmp)
	if err := store.Load(); err != nil {
		t.Fatalf("store load: %v", err)
	}
	s.watchStore = store

	// Add a watch and get its ID.
	id, err := store.Add(watch.Watch{
		Type:        "flight",
		Origin:      "HEL",
		Destination: "BCN",
		DepartDate:  "2099-07-01",
		BelowPrice:  300,
		Currency:    "EUR",
	})
	if err != nil {
		t.Fatalf("add watch: %v", err)
	}

	// readWatchResource should resolve the ID via the store.
	result, err := s.readWatchResource("trvl://watch/" + id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	if !containsString(result.Contents[0].Text, "HEL") {
		t.Errorf("expected origin in result, got: %s", result.Contents[0].Text)
	}
}

// ============================================================
// handleToolsCall — extra path coverage (tool not found via RPC)
// ============================================================

func TestHandleToolsCall_MissingNameParam(t *testing.T) {
	t.Parallel()
	s := NewServer()
	// Params with no "name" field → should return an error.
	params := map[string]any{
		"arguments": map[string]any{},
	}
	resp := sendRequest(t, s, "tools/call", 99, params)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error == nil {
		t.Fatal("expected error response for missing tool name")
	}
}

// ============================================================
// handleBuildProfile — path coverage via testable core
// ============================================================

func TestHandleBuildProfileWithPath_NoBookings(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")

	content, structured, err := handleBuildProfileWithPath(map[string]any{}, path, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
}

func TestHandleBuildProfileWithPath_EmailSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")

	content, structured, err := handleBuildProfileWithPath(map[string]any{"source": "email"}, path, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "Gmail") {
		t.Errorf("expected Gmail instruction, got: %s", content[0].Text)
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
}

func TestHandleBuildProfileWithPath_InvalidSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")

	_, _, err := handleBuildProfileWithPath(map[string]any{"source": "bad-source"}, path, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid source")
	}
}

// ============================================================
// handleAddBookingWithPath — coverage
// ============================================================

func TestHandleAddBookingWithPath_MissingType(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	_, _, err := handleAddBookingWithPath(map[string]any{
		"provider": "Finnair",
	}, path, nil)
	if err == nil {
		t.Fatal("expected error for missing type")
	}
}

func TestHandleAddBookingWithPath_MissingProvider(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	_, _, err := handleAddBookingWithPath(map[string]any{
		"type": "flight",
	}, path, nil)
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestHandleAddBookingWithPath_FlightSuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "profile.json")
	content, structured, err := handleAddBookingWithPath(map[string]any{
		"type":     "flight",
		"provider": "Finnair",
		"from":     "HEL",
		"to":       "BCN",
		"price":    350.0,
		"currency": "EUR",
		"date":     "2026-05-01",
	}, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "Finnair") {
		t.Errorf("expected provider in response, got: %s", content[0].Text)
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
}

// ============================================================
// handleInterviewTripWithPath — coverage
// ============================================================

func TestHandleInterviewTripWithPath_Empty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	profilePath := filepath.Join(dir, "profile.json")
	prefsPath := filepath.Join(dir, "prefs.json")

	content, structured, err := handleInterviewTripWithPath(map[string]any{}, profilePath, prefsPath, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if structured == nil {
		t.Fatal("expected structured output")
	}
}

// ============================================================
// Tool definition completeness — watch price tools
// ============================================================

func TestWatchPriceTool_Definition(t *testing.T) {
	t.Parallel()
	tool := watchPriceTool()
	if tool.Name != "watch_price" {
		t.Errorf("Name = %q, want watch_price", tool.Name)
	}
	if len(tool.InputSchema.Required) < 2 {
		t.Errorf("Required = %v, want at least 2", tool.InputSchema.Required)
	}
	if tool.Annotations == nil {
		t.Fatal("annotations should be set")
	}
	if tool.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint should be false for write tool")
	}
}

func TestListWatchesTool_Definition(t *testing.T) {
	t.Parallel()
	tool := listWatchesTool()
	if tool.Name != "list_watches" {
		t.Errorf("Name = %q, want list_watches", tool.Name)
	}
	if !tool.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint should be true")
	}
}

func TestCheckWatchesTool_Definition(t *testing.T) {
	t.Parallel()
	tool := checkWatchesTool()
	if tool.Name != "check_watches" {
		t.Errorf("Name = %q, want check_watches", tool.Name)
	}
	if !tool.Annotations.OpenWorldHint {
		t.Error("OpenWorldHint should be true (makes live requests)")
	}
}

func TestProviderHealthTool_Definition(t *testing.T) {
	t.Parallel()
	tool := providerHealthTool()
	if tool.Name != "provider_health" {
		t.Errorf("Name = %q, want provider_health", tool.Name)
	}
	if !tool.Annotations.ReadOnlyHint {
		t.Error("ReadOnlyHint should be true")
	}
}

// ============================================================
// handleConfigureProvider — elicit timeout path
// ============================================================

func TestHandleConfigureProvider_ElicitTimeout(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	args := map[string]any{
		"id":           "timeout-test",
		"name":         "Timeout Provider",
		"category":     "hotels",
		"endpoint":     "https://api.example.com/search",
		"results_path": "$.results",
		"field_mapping": map[string]any{
			"name": "$.hotel_name",
		},
	}

	elicit := func(message string, schema map[string]interface{}) (map[string]interface{}, error) {
		return nil, fmt.Errorf("deadline exceeded waiting for user response")
	}

	content, _, err := handleConfigureProvider(context.Background(), args, elicit, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	if !containsString(content[0].Text, "timed out") {
		t.Errorf("expected timeout message, got: %s", content[0].Text)
	}
}

// ============================================================
// handleSuggestProviders — category filter
// ============================================================

func TestHandleSuggestProviders_CategoryFilter(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	content, structured, err := handleSuggestProviders(context.Background(), map[string]any{
		"category": "hotels",
	}, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected content blocks")
	}
	// All results should be hotels.
	suggestions, ok := structured.([]providerSuggestion)
	if !ok {
		t.Fatalf("structured type = %T, want []providerSuggestion", structured)
	}
	for _, s := range suggestions {
		if s.Category != "hotels" {
			t.Errorf("provider %q has category %q, want hotels", s.ID, s.Category)
		}
	}
}

func TestHandleSuggestProviders_EmptyCategory(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)
	_, _, err := handleSuggestProviders(context.Background(), map[string]any{
		"category": "nonexistent_category",
	}, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestHandleSuggestProviders_ConfiguredMarked(t *testing.T) {
	t.Parallel()
	reg := testRegistry(t)

	// Use the first actual catalog provider ID: "booking".
	config := &providers.ProviderConfig{
		ID:       "booking",
		Name:     "Booking.com",
		Category: "hotels",
		Endpoint: "https://www.booking.com/dml/graphql",
		Method:   "POST",
		ResponseMapping: providers.ResponseMapping{
			ResultsPath: "$.data.searchQueries.search.results",
			Fields:      map[string]string{"name": "$.basicPropertyData.name"},
		},
	}
	_ = reg.Save(config)

	_, structured, err := handleSuggestProviders(context.Background(), map[string]any{}, nil, nil, nil, reg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	suggestions, ok := structured.([]providerSuggestion)
	if !ok {
		t.Fatalf("structured type = %T", structured)
	}

	// At least one provider must appear. Verify "booking" is marked configured.
	if len(suggestions) == 0 {
		t.Fatal("expected at least one suggestion")
	}
	found := false
	for _, s := range suggestions {
		if s.ID == "booking" {
			found = true
			if !s.Configured {
				t.Error("booking should be marked as configured")
			}
			break
		}
	}
	if !found {
		t.Errorf("booking not found in %d suggestions", len(suggestions))
	}
}

// ============================================================
// buildAnnotatedContentBlocks — error path (non-marshallable)
// ============================================================

func TestBuildAnnotatedContentBlocks_NonMarshalable(t *testing.T) {
	t.Parallel()
	// channels cannot be marshalled → should return error.
	_, err := buildAnnotatedContentBlocks("summary", make(chan int))
	if err == nil {
		t.Fatal("expected error for non-marshallable structured data")
	}
}

func TestBuildAnnotatedContentBlocks_Nil(t *testing.T) {
	t.Parallel()
	// nil structured → should succeed with just the text block.
	blocks, err := buildAnnotatedContentBlocks("hello", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) == 0 {
		t.Fatal("expected at least one content block")
	}
	if !strings.Contains(blocks[0].Text, "hello") {
		t.Errorf("expected 'hello' in content, got: %s", blocks[0].Text)
	}
}
