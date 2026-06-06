package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/testutil"
)

func TestResourcesList(t *testing.T) {
	t.Parallel()
	s := NewServer()
	resp := sendRequest(t, s, "resources/list", 1, nil)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result ResourcesListResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	if len(result.Resources) < 5 {
		t.Fatalf("expected at least 5 resources, got %d", len(result.Resources))
	}

	expected := map[string]bool{
		"trvl://airports/popular": false,
		"trvl://help/flights":     false,
		"trvl://help/hotels":      false,
		"trvl://trip/summary":     false,
		"trvl://watches":          false,
		"trvl://trips":            false,
		"trvl://trips/upcoming":   false,
		"trvl://trips/alerts":     false,
		"trvl://onboarding":       false,
	}
	for _, r := range result.Resources {
		if _, ok := expected[r.URI]; !ok {
			// Dynamic watch and trip resources are allowed.
			if !strings.HasPrefix(r.URI, "trvl://watch/") && !strings.HasPrefix(r.URI, "trvl://trips/trip_") {
				t.Errorf("unexpected resource: %s", r.URI)
			}
		}
		expected[r.URI] = true
		if r.Name == "" {
			t.Errorf("resource %s has empty name", r.URI)
		}
	}
	for uri, found := range expected {
		if !found {
			t.Errorf("missing resource: %s", uri)
		}
	}
}

func TestResourcesRead_PopularAirports(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := ResourcesReadParams{URI: "trvl://airports/popular"}
	resp := sendRequest(t, s, "resources/read", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result ResourcesReadResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	text := result.Contents[0].Text
	if !strings.Contains(text, "HEL") {
		t.Error("airports should contain HEL")
	}
	if !strings.Contains(text, "JFK") {
		t.Error("airports should contain JFK")
	}
	// Should have 50 airports (50 lines).
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) != 50 {
		t.Errorf("expected 50 airports, got %d", len(lines))
	}
}

func TestResourcesRead_FlightGuide(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := ResourcesReadParams{URI: "trvl://help/flights"}
	resp := sendRequest(t, s, "resources/read", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result ResourcesReadResult
	_ = json.Unmarshal(resultJSON, &result)

	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	if !strings.Contains(result.Contents[0].Text, "search_flights") {
		t.Error("flight guide should mention search_flights")
	}
	if result.Contents[0].MimeType != "text/markdown" {
		t.Errorf("mime type = %q, want text/markdown", result.Contents[0].MimeType)
	}
}

func TestResourcesRead_HotelGuide(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := ResourcesReadParams{URI: "trvl://help/hotels"}
	resp := sendRequest(t, s, "resources/read", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result ResourcesReadResult
	_ = json.Unmarshal(resultJSON, &result)

	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	if !strings.Contains(result.Contents[0].Text, "search_hotels") {
		t.Error("hotel guide should mention search_hotels")
	}
}

func TestResourcesRead_NotFound(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := ResourcesReadParams{URI: "trvl://nonexistent"}
	resp := sendRequest(t, s, "resources/read", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown resource")
	}
}

// --- Innovation 1: Resource Links in Tool Results ---

func TestResourceLinkInFlightResults(t *testing.T) {
	t.Parallel()
	testutil.RequireLiveIntegration(t)
	s := NewServer()
	params := ToolCallParams{
		Name: "search_flights",
		Arguments: map[string]any{
			"origin":         "HEL",
			"destination":    "NRT",
			"departure_date": "2026-05-15",
		},
	}
	resp := sendRequest(t, s, "tools/call", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Skipf("skipping: live API returned error (expected on some CI runners): %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result ToolCallResult
	if err := json.Unmarshal(resultJSON, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(result.Content) == 0 {
		t.Skip("skipping: live API returned no content (network issue on CI)")
	}

	// Look for a resource_link content block.
	var foundLink bool
	for _, cb := range result.Content {
		if cb.Type == "resource_link" {
			foundLink = true
			if !strings.Contains(cb.URI, "trvl://watch/HEL-NRT-2026-05-15") {
				t.Errorf("resource_link URI = %q, want trvl://watch/HEL-NRT-2026-05-15", cb.URI)
			}
			if cb.Name == "" {
				t.Error("resource_link should have a name")
			}
			if cb.Description == "" {
				t.Error("resource_link should have a description")
			}
		}
	}
	if !foundLink {
		t.Error("expected a resource_link content block in flight search results")
	}
}

func TestResourceLinkInHotelResults(t *testing.T) {
	t.Parallel()
	testutil.RequireLiveIntegration(t)
	s := NewServer()
	params := ToolCallParams{
		Name: "search_hotels",
		Arguments: map[string]any{
			"location":  "Helsinki",
			"check_in":  "2026-05-15",
			"check_out": "2026-05-18",
		},
	}
	resp := sendRequest(t, s, "tools/call", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Skipf("skipping: live API returned error (expected on some CI runners): %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result ToolCallResult
	_ = json.Unmarshal(resultJSON, &result)

	if len(result.Content) == 0 {
		t.Skip("skipping: live API returned no content (network issue on CI)")
	}

	var foundLink bool
	for _, cb := range result.Content {
		if cb.Type == "resource_link" {
			foundLink = true
			if !strings.Contains(cb.URI, "trvl://search/hotels/Helsinki-2026-05-15-2026-05-18") {
				t.Errorf("resource_link URI = %q", cb.URI)
			}
		}
	}
	if !foundLink {
		t.Error("expected a resource_link content block in hotel search results")
	}
}

// --- Innovation 2: Watch Resources ---

func TestWatchResourceURIParsing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		query   string
		wantURI string
	}{
		{"HEL->BCN 2026-07-01", "trvl://watch/HEL-BCN-2026-07-01"},
		{"JFK->LHR 2026-08-15 (round-trip return 2026-08-22)", "trvl://watch/JFK-LHR-2026-08-15"},
		{"", ""},
		{"NOLOCATION", ""},
	}

	for _, tt := range tests {
		got := watchURIFromQuery(tt.query)
		if got != tt.wantURI {
			t.Errorf("watchURIFromQuery(%q) = %q, want %q", tt.query, got, tt.wantURI)
		}
	}
}

func TestWatchResourceInvalidURI(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := ResourcesReadParams{URI: "trvl://watch/invalid"}
	resp := sendRequest(t, s, "resources/read", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error == nil {
		t.Fatal("expected error for invalid watch URI")
	}
}

func TestDynamicResourcesListAfterSearch(t *testing.T) {
	t.Parallel()
	s := NewServer()

	// Before any search: only static resources.
	resources := s.listResources()
	initialCount := len(resources)

	// Record a flight search to simulate tool call.
	s.recordSearch("flight", "HEL->BCN 2026-07-01", 113, "EUR")

	// After search: should include a watch resource.
	resources = s.listResources()
	if len(resources) != initialCount+1 {
		t.Errorf("expected %d resources after search, got %d", initialCount+1, len(resources))
	}

	found := false
	for _, r := range resources {
		if r.URI == "trvl://watch/HEL-BCN-2026-07-01" {
			found = true
			if r.Name == "" {
				t.Error("watch resource should have a name")
			}
		}
	}
	if !found {
		t.Error("expected trvl://watch/HEL-BCN-2026-07-01 in resources list")
	}
}

func TestPriceCacheGetSet(t *testing.T) {
	t.Parallel()
	c := newPriceCache()

	// Initially empty.
	_, ok := c.get("HEL-BCN-2026-07-01")
	if ok {
		t.Error("expected cache miss for new key")
	}

	c.set("HEL-BCN-2026-07-01", 113)
	v, ok := c.get("HEL-BCN-2026-07-01")
	if !ok {
		t.Fatal("expected cache hit after set")
	}
	if v != 113 {
		t.Errorf("cached price = %f, want 113", v)
	}
}

// --- Innovation 3: Trip Summary Resource ---

func TestTripSummaryEmpty(t *testing.T) {
	t.Parallel()
	s := NewServer()
	params := ResourcesReadParams{URI: "trvl://trip/summary"}
	resp := sendRequest(t, s, "resources/read", 1, params)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("error: %+v", resp.Error)
	}

	resultJSON, _ := json.Marshal(resp.Result)
	var result ResourcesReadResult
	_ = json.Unmarshal(resultJSON, &result)

	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	text := result.Contents[0].Text
	if !strings.Contains(text, "No searches yet") {
		t.Errorf("empty summary = %q, want 'No searches yet'", text)
	}
}

func TestTripSummaryAfterSearches(t *testing.T) {
	t.Parallel()
	s := NewServer()

	// Record some searches directly.
	s.recordSearch("flight", "HEL->BCN 2026-07-01", 113, "EUR")
	s.recordSearch("flight", "BCN->HEL 2026-07-08", 89, "EUR")
	s.recordSearch("hotel", "Barcelona 2026-07-01 to 2026-07-08", 85, "EUR")
	s.recordSearch("destination", "Barcelona", 0, "")

	result, err := s.readTripSummary()
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if len(result.Contents) == 0 {
		t.Fatal("expected contents")
	}
	text := result.Contents[0].Text

	// Should mention counts.
	if !strings.Contains(text, "2 flight(s)") {
		t.Errorf("summary missing flight count: %s", text)
	}
	if !strings.Contains(text, "1 hotel(s)") {
		t.Errorf("summary missing hotel count: %s", text)
	}
	if !strings.Contains(text, "1 destination(s)") {
		t.Errorf("summary missing destination count: %s", text)
	}

	// Should contain search details.
	if !strings.Contains(text, "HEL->BCN") {
		t.Errorf("summary missing flight route: %s", text)
	}
	if !strings.Contains(text, "113") {
		t.Errorf("summary missing flight price: %s", text)
	}
	if !strings.Contains(text, "Barcelona") {
		t.Errorf("summary missing hotel location: %s", text)
	}

	// Should have an estimated total.
	if !strings.Contains(text, "Estimated total") {
		t.Errorf("summary missing estimated total: %s", text)
	}
}

func TestRecordSearch(t *testing.T) {
	t.Parallel()
	s := NewServer()

	s.recordSearch("flight", "HEL->BCN 2026-07-01", 113, "EUR")

	s.tripState.mu.Lock()
	defer s.tripState.mu.Unlock()
	if len(s.tripState.Searches) != 1 {
		t.Fatalf("expected 1 search, got %d", len(s.tripState.Searches))
	}
	sr := s.tripState.Searches[0]
	if sr.Type != "flight" {
		t.Errorf("type = %q, want flight", sr.Type)
	}
	if sr.Query != "HEL->BCN 2026-07-01" {
		t.Errorf("query = %q", sr.Query)
	}
	if sr.BestPrice != 113 {
		t.Errorf("best_price = %f, want 113", sr.BestPrice)
	}
	if sr.Currency != "EUR" {
		t.Errorf("currency = %q, want EUR", sr.Currency)
	}
	if sr.Time.IsZero() {
		t.Error("time should be set")
	}
}

func TestExtractBestFlightPrice(t *testing.T) {
	t.Parallel()
	m := map[string]interface{}{
		"flights": []interface{}{
			map[string]interface{}{"price": 500.0, "currency": "EUR"},
			map[string]interface{}{"price": 300.0, "currency": "EUR"},
			map[string]interface{}{"price": 700.0, "currency": "EUR"},
		},
	}
	price, currency := extractBestFlightPrice(m)
	if price != 300 {
		t.Errorf("price = %f, want 300", price)
	}
	if currency != "EUR" {
		t.Errorf("currency = %q, want EUR", currency)
	}
}

func TestExtractBestFlightPrice_NoFlights(t *testing.T) {
	t.Parallel()
	m := map[string]interface{}{}
	price, _ := extractBestFlightPrice(m)
	if price != 0 {
		t.Errorf("price = %f, want 0", price)
	}
}

func TestExtractBestHotelPrice(t *testing.T) {
	t.Parallel()
	m := map[string]interface{}{
		"hotels": []interface{}{
			map[string]interface{}{"price": 200.0, "currency": "EUR"},
			map[string]interface{}{"price": 85.0, "currency": "EUR"},
		},
	}
	price, currency := extractBestHotelPrice(m)
	if price != 85 {
		t.Errorf("price = %f, want 85", price)
	}
	if currency != "EUR" {
		t.Errorf("currency = %q, want EUR", currency)
	}
}

func TestExtractBestHotelPrice_NoHotels(t *testing.T) {
	t.Parallel()
	m := map[string]interface{}{}
	price, _ := extractBestHotelPrice(m)
	if price != 0 {
		t.Errorf("price = %f, want 0", price)
	}
}

func TestAddResourceLinks_FlightArgs(t *testing.T) {
	t.Parallel()
	s := NewServer()
	args := map[string]any{
		"origin":         "HEL",
		"destination":    "BCN",
		"departure_date": "2026-07-01",
	}
	content := s.addResourceLinks(nil, args)
	if len(content) == 0 {
		t.Fatal("expected resource_link content block")
	}
	if content[0].Type != "resource_link" {
		t.Errorf("type = %q, want resource_link", content[0].Type)
	}
	if content[0].URI != "trvl://watch/HEL-BCN-2026-07-01" {
		t.Errorf("URI = %q", content[0].URI)
	}
}

func TestAddResourceLinks_HotelArgs(t *testing.T) {
	t.Parallel()
	s := NewServer()
	args := map[string]any{
		"location":  "Helsinki",
		"check_in":  "2026-05-15",
		"check_out": "2026-05-18",
	}
	content := s.addResourceLinks(nil, args)
	if len(content) == 0 {
		t.Fatal("expected resource_link content block")
	}
	if content[0].Type != "resource_link" {
		t.Errorf("type = %q, want resource_link", content[0].Type)
	}
	if !strings.Contains(content[0].URI, "Helsinki") {
		t.Errorf("URI = %q, should contain Helsinki", content[0].URI)
	}
}

func TestAddResourceLinks_NoArgs(t *testing.T) {
	t.Parallel()
	s := NewServer()
	content := s.addResourceLinks(nil, map[string]any{})
	if len(content) != 0 {
		t.Errorf("expected no resource links for empty args, got %d", len(content))
	}
}

func TestTripSummaryResourceInList(t *testing.T) {
	t.Parallel()
	s := NewServer()
	resources := s.listResources()

	found := false
	for _, r := range resources {
		if r.URI == "trvl://trip/summary" {
			found = true
			if r.Name != "Trip Planning Summary" {
				t.Errorf("name = %q", r.Name)
			}
		}
	}
	if !found {
		t.Error("trvl://trip/summary should be in resources list")
	}
}
