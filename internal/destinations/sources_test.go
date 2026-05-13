package destinations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// --- Wikivoyage tests ---

func TestGetWikivoyageGuide(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wikivoyageResponse{}
		resp.Query.Pages = map[string]struct {
			PageID  int    `json:"pageid"`
			Title   string `json:"title"`
			Extract string `json:"extract"`
		}{
			"12345": {
				PageID: 12345,
				Title:  "Barcelona",
				Extract: `Barcelona is a city on the Mediterranean coast of Spain.

== Get in ==
Barcelona has an international airport and high-speed rail connections.

== See ==
La Sagrada Familia is the most visited monument. The Gothic Quarter has medieval streets.

== Eat ==
Try tapas at La Boqueria market. Paella is a local specialty.

== Drink ==
Barcelona has a vibrant nightlife scene.

== Stay safe ==
Watch for pickpockets on La Rambla.`,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	origURL := setTestWikivoyageURL(server.URL)
	clearAllCaches()
	defer setTestWikivoyageURL(origURL)

	ctx := context.Background()
	guide, err := GetWikivoyageGuide(ctx, "Barcelona")
	if err != nil {
		t.Fatalf("GetWikivoyageGuide: %v", err)
	}

	if guide.Location != "Barcelona" {
		t.Errorf("Location = %q, want Barcelona", guide.Location)
	}

	if guide.Summary == "" {
		t.Error("Summary should not be empty")
	}

	if _, ok := guide.Sections["See"]; !ok {
		t.Error("missing 'See' section")
	}
	if _, ok := guide.Sections["Eat"]; !ok {
		t.Error("missing 'Eat' section")
	}
	if _, ok := guide.Sections["Get in"]; !ok {
		t.Error("missing 'Get in' section")
	}

	if len(guide.Sections) != 5 {
		t.Errorf("expected 5 sections, got %d: %v", len(guide.Sections), guide.Sections)
	}

	if guide.URL == "" {
		t.Error("URL should not be empty")
	}
}

func TestGetWikivoyageGuide_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := wikivoyageResponse{}
		resp.Query.Pages = map[string]struct {
			PageID  int    `json:"pageid"`
			Title   string `json:"title"`
			Extract string `json:"extract"`
		}{
			"-1": {PageID: -1, Title: "Xyzzyplugh"},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	origURL := setTestWikivoyageURL(server.URL)
	clearAllCaches()
	defer setTestWikivoyageURL(origURL)

	ctx := context.Background()
	_, err := GetWikivoyageGuide(ctx, "Xyzzyplugh")
	if err == nil {
		t.Error("expected error for non-existent article")
	}
}

func TestGetWikivoyageGuide_Caching(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		resp := wikivoyageResponse{}
		resp.Query.Pages = map[string]struct {
			PageID  int    `json:"pageid"`
			Title   string `json:"title"`
			Extract string `json:"extract"`
		}{
			"1": {PageID: 1, Title: "Tokyo", Extract: "Tokyo is the capital of Japan."},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	origURL := setTestWikivoyageURL(server.URL)
	clearAllCaches()
	defer setTestWikivoyageURL(origURL)

	ctx := context.Background()

	// First call hits server.
	_, err := GetWikivoyageGuide(ctx, "Tokyo")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Second call should use cache.
	_, err = GetWikivoyageGuide(ctx, "Tokyo")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if callCount != 1 {
		t.Errorf("expected 1 server call (cache hit), got %d", callCount)
	}
}

// --- OSM Overpass tests ---

func TestGetNearbyPOIs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := overpassResponse{
			Elements: []overpassElement{
				{
					Type: "node",
					ID:   1,
					Lat:  41.3810,
					Lon:  2.1720,
					Tags: map[string]string{
						"name":    "Bar Celta",
						"amenity": "restaurant",
						"cuisine": "spanish",
						"phone":   "+34 123 456",
					},
				},
				{
					Type: "node",
					ID:   2,
					Lat:  41.3815,
					Lon:  2.1725,
					Tags: map[string]string{
						"name":          "Cafe Central",
						"amenity":       "cafe",
						"opening_hours": "Mo-Fr 08:00-20:00",
					},
				},
				{
					Type: "node",
					ID:   3,
					Lat:  41.3820,
					Lon:  2.1730,
					Tags: map[string]string{}, // Unnamed, should be skipped.
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	origURL := setTestOverpassURL(server.URL)
	clearAllCaches()
	defer setTestOverpassURL(origURL)

	ctx := context.Background()
	pois, err := GetNearbyPOIs(ctx, 41.3800, 2.1700, 500, "all")
	if err != nil {
		t.Fatalf("GetNearbyPOIs: %v", err)
	}

	if len(pois) != 2 {
		t.Fatalf("expected 2 POIs (unnamed skipped), got %d", len(pois))
	}

	// Should be sorted by distance.
	if pois[0].Distance > pois[1].Distance {
		t.Error("POIs should be sorted by distance ascending")
	}

	if pois[0].Cuisine != "spanish" {
		t.Errorf("first POI cuisine = %q, want spanish", pois[0].Cuisine)
	}
}

func TestGetNearbyPOIs_EmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(overpassResponse{Elements: nil})
	}))
	defer server.Close()

	origURL := setTestOverpassURL(server.URL)
	clearAllCaches()
	defer setTestOverpassURL(origURL)

	ctx := context.Background()
	pois, err := GetNearbyPOIs(ctx, 0.0, 0.0, 500, "restaurant")
	if err != nil {
		t.Fatalf("GetNearbyPOIs: %v", err)
	}

	if len(pois) != 0 {
		t.Errorf("expected 0 POIs, got %d", len(pois))
	}
}

// --- Events tests ---

func TestGetEvents_NoKey(t *testing.T) {
	// Ensure env var is not set.
	orig := os.Getenv("TICKETMASTER_API_KEY")
	_ = os.Unsetenv("TICKETMASTER_API_KEY")
	defer func() {
		if orig != "" {
			_ = os.Setenv("TICKETMASTER_API_KEY", orig)
		}
	}()

	ctx := context.Background()
	events, err := GetEvents(ctx, "Barcelona", "2026-07-01", "2026-07-08")
	if err != nil {
		t.Fatalf("GetEvents should not error without key: %v", err)
	}
	if events != nil {
		t.Error("events should be nil when key is not set")
	}
}

func TestGetEvents_WithKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ticketmasterResponse{}
		resp.Embedded.Events = []ticketmasterEvent{
			{
				Name: "Flamenco Night",
				Type: "event",
				URL:  "https://ticketmaster.com/event/123",
				Dates: struct {
					Start struct {
						LocalDate string `json:"localDate"`
						LocalTime string `json:"localTime"`
					} `json:"start"`
				}{
					Start: struct {
						LocalDate string `json:"localDate"`
						LocalTime string `json:"localTime"`
					}{LocalDate: "2026-07-03", LocalTime: "20:00"},
				},
				Embedded: struct {
					Venues []struct {
						Name string `json:"name"`
					} `json:"venues"`
				}{
					Venues: []struct {
						Name string `json:"name"`
					}{{Name: "Palau de la Musica"}},
				},
				Classifications: []struct {
					Segment struct {
						Name string `json:"name"`
					} `json:"segment"`
				}{{Segment: struct {
					Name string `json:"name"`
				}{Name: "Arts"}}},
				PriceRanges: []struct {
					Min      float64 `json:"min"`
					Max      float64 `json:"max"`
					Currency string  `json:"currency"`
				}{{Min: 25, Max: 80, Currency: "EUR"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	origURL := setTestTicketmasterURL(server.URL)
	clearAllCaches()
	defer setTestTicketmasterURL(origURL)

	// Set the API key for the test.
	origKey := os.Getenv("TICKETMASTER_API_KEY")
	_ = os.Setenv("TICKETMASTER_API_KEY", "test-key")
	defer func() {
		if origKey == "" {
			_ = os.Unsetenv("TICKETMASTER_API_KEY")
		} else {
			_ = os.Setenv("TICKETMASTER_API_KEY", origKey)
		}
	}()

	ctx := context.Background()
	events, err := GetEvents(ctx, "Barcelona", "2026-07-01", "2026-07-08")
	if err != nil {
		t.Fatalf("GetEvents: %v", err)
	}

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	e := events[0]
	if e.Name != "Flamenco Night" {
		t.Errorf("Name = %q, want Flamenco Night", e.Name)
	}
	if e.Venue != "Palau de la Musica" {
		t.Errorf("Venue = %q, want Palau de la Musica", e.Venue)
	}
	if e.Type != "arts" {
		t.Errorf("Type = %q, want arts", e.Type)
	}
	if e.PriceRange != "25-80 EUR" {
		t.Errorf("PriceRange = %q, want '25-80 EUR'", e.PriceRange)
	}
}

// --- Foursquare tests ---

func TestGetRatedPlaces_NoKey(t *testing.T) {
	orig := os.Getenv("FOURSQUARE_API_KEY")
	_ = os.Unsetenv("FOURSQUARE_API_KEY")
	defer func() {
		if orig != "" {
			_ = os.Setenv("FOURSQUARE_API_KEY", orig)
		}
	}()

	ctx := context.Background()
	places, err := GetRatedPlaces(ctx, 41.38, 2.17, "restaurant", 10)
	if err != nil {
		t.Fatalf("GetRatedPlaces should not error without key: %v", err)
	}
	if places != nil {
		t.Error("places should be nil when key is not set")
	}
}

func TestGetRatedPlaces_WithKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify Authorization header.
		if r.Header.Get("Authorization") != "test-fs-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		resp := foursquareResponse{
			Results: []foursquarePlace{
				{
					Name:     "Can Paixano",
					Rating:   8.5,
					Distance: 150,
					Location: struct {
						Address          string `json:"address"`
						FormattedAddress string `json:"formatted_address"`
					}{FormattedAddress: "Carrer de la Reina Cristina 7"},
					Categories: []struct {
						Name string `json:"name"`
					}{{Name: "Tapas Restaurant"}},
					Price: 2,
					Tips: []struct {
						Text string `json:"text"`
					}{{Text: "Great cava and sandwiches!"}},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	origURL := setTestFoursquareURL(server.URL)
	clearAllCaches()
	defer setTestFoursquareURL(origURL)

	origKey := os.Getenv("FOURSQUARE_API_KEY")
	_ = os.Setenv("FOURSQUARE_API_KEY", "test-fs-key")
	defer func() {
		if origKey == "" {
			_ = os.Unsetenv("FOURSQUARE_API_KEY")
		} else {
			_ = os.Setenv("FOURSQUARE_API_KEY", origKey)
		}
	}()

	ctx := context.Background()
	places, err := GetRatedPlaces(ctx, 41.38, 2.17, "restaurant", 10)
	if err != nil {
		t.Fatalf("GetRatedPlaces: %v", err)
	}

	if len(places) != 1 {
		t.Fatalf("expected 1 place, got %d", len(places))
	}

	p := places[0]
	if p.Name != "Can Paixano" {
		t.Errorf("Name = %q, want Can Paixano", p.Name)
	}
	if p.Rating != 8.5 {
		t.Errorf("Rating = %.1f, want 8.5", p.Rating)
	}
	if p.PriceLevel != 2 {
		t.Errorf("PriceLevel = %d, want 2", p.PriceLevel)
	}
	if p.Tip != "Great cava and sandwiches!" {
		t.Errorf("Tip = %q", p.Tip)
	}
}

// --- Geoapify tests ---

func TestGetWalkablePOIs_NoKey(t *testing.T) {
	orig := os.Getenv("GEOAPIFY_API_KEY")
	_ = os.Unsetenv("GEOAPIFY_API_KEY")
	defer func() {
		if orig != "" {
			_ = os.Setenv("GEOAPIFY_API_KEY", orig)
		}
	}()

	ctx := context.Background()
	pois, err := GetWalkablePOIs(ctx, 41.38, 2.17, 10, "restaurant")
	if err != nil {
		t.Fatalf("GetWalkablePOIs should not error without key: %v", err)
	}
	if pois != nil {
		t.Error("pois should be nil when key is not set")
	}
}

func TestGetWalkablePOIs_WithKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := geoapifyPlacesResponse{
			Features: []struct {
				Properties struct {
					Name     string  `json:"name"`
					Category string  `json:"category"`
					Lat      float64 `json:"lat"`
					Lon      float64 `json:"lon"`
					Distance int     `json:"distance"`
					Phone    string  `json:"contact:phone"`
					Website  string  `json:"website"`
					Opening  string  `json:"opening_hours"`
				} `json:"properties"`
			}{
				{Properties: struct {
					Name     string  `json:"name"`
					Category string  `json:"category"`
					Lat      float64 `json:"lat"`
					Lon      float64 `json:"lon"`
					Distance int     `json:"distance"`
					Phone    string  `json:"contact:phone"`
					Website  string  `json:"website"`
					Opening  string  `json:"opening_hours"`
				}{
					Name:    "Test Cafe",
					Lat:     41.381,
					Lon:     2.171,
					Phone:   "+34 111",
					Opening: "08:00-22:00",
				}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	origURL := setTestGeoapifyPlacesURL(server.URL)
	clearAllCaches()
	defer setTestGeoapifyPlacesURL(origURL)

	origKey := os.Getenv("GEOAPIFY_API_KEY")
	_ = os.Setenv("GEOAPIFY_API_KEY", "test-geo-key")
	defer func() {
		if origKey == "" {
			_ = os.Unsetenv("GEOAPIFY_API_KEY")
		} else {
			_ = os.Setenv("GEOAPIFY_API_KEY", origKey)
		}
	}()

	ctx := context.Background()
	pois, err := GetWalkablePOIs(ctx, 41.38, 2.17, 10, "cafe")
	if err != nil {
		t.Fatalf("GetWalkablePOIs: %v", err)
	}

	if len(pois) != 1 {
		t.Fatalf("expected 1 POI, got %d", len(pois))
	}

	if pois[0].Name != "Test Cafe" {
		t.Errorf("Name = %q, want Test Cafe", pois[0].Name)
	}
	if pois[0].Phone != "+34 111" {
		t.Errorf("Phone = %q, want +34 111", pois[0].Phone)
	}
}

// --- OpenTripMap/Attractions tests ---

func TestGetAttractions_NoKey(t *testing.T) {
	orig := os.Getenv("OPENTRIPMAP_API_KEY")
	_ = os.Unsetenv("OPENTRIPMAP_API_KEY")
	defer func() {
		if orig != "" {
			_ = os.Setenv("OPENTRIPMAP_API_KEY", orig)
		}
	}()

	ctx := context.Background()
	attractions, err := GetAttractions(ctx, 41.38, 2.17, 1000)
	if err != nil {
		t.Fatalf("GetAttractions should not error without key: %v", err)
	}
	if attractions != nil {
		t.Error("attractions should be nil when key is not set")
	}
}

func TestGetAttractions_WithKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		places := []openTripMapPlace{
			{
				XID:       "W123",
				Name:      "La Sagrada Familia",
				Kinds:     "architecture,religion",
				Dist:      350,
				Wikipedia: "https://en.wikipedia.org/wiki/Sagrada_Fam%C3%ADlia",
			},
			{
				XID:   "N456",
				Name:  "Park Guell",
				Kinds: "gardens",
				Dist:  800,
			},
			{
				XID:   "N789",
				Name:  "", // unnamed, should be skipped
				Kinds: "other",
				Dist:  100,
			},
		}
		_ = json.NewEncoder(w).Encode(places)
	}))
	defer server.Close()

	origURL := setTestOpenTripMapURL(server.URL)
	clearAllCaches()
	defer setTestOpenTripMapURL(origURL)

	origKey := os.Getenv("OPENTRIPMAP_API_KEY")
	_ = os.Setenv("OPENTRIPMAP_API_KEY", "test-otm-key")
	defer func() {
		if origKey == "" {
			_ = os.Unsetenv("OPENTRIPMAP_API_KEY")
		} else {
			_ = os.Setenv("OPENTRIPMAP_API_KEY", origKey)
		}
	}()

	ctx := context.Background()
	attractions, err := GetAttractions(ctx, 41.38, 2.17, 1000)
	if err != nil {
		t.Fatalf("GetAttractions: %v", err)
	}

	if len(attractions) != 2 {
		t.Fatalf("expected 2 attractions (unnamed skipped), got %d", len(attractions))
	}

	// Should be sorted by distance.
	if attractions[0].Distance > attractions[1].Distance {
		t.Error("attractions should be sorted by distance ascending")
	}

	if attractions[0].Name != "La Sagrada Familia" {
		t.Errorf("first attraction = %q, want La Sagrada Familia", attractions[0].Name)
	}
	if attractions[0].Kind != "architecture" {
		t.Errorf("kind = %q, want architecture", attractions[0].Kind)
	}
}

// --- Haversine tests ---

func TestHaversineMeters(t *testing.T) {
	// Barcelona to a point ~111m north (approximately 0.001 degrees lat).
	dist := haversineMeters(41.3800, 2.1700, 41.3810, 2.1700)
	if dist < 100 || dist > 120 {
		t.Errorf("haversine ~0.001 deg lat = %d m, want ~111m", dist)
	}

	// Same point.
	dist = haversineMeters(41.3800, 2.1700, 41.3800, 2.1700)
	if dist != 0 {
		t.Errorf("same point distance = %d, want 0", dist)
	}
}

// --- Deduplication tests ---

func TestDeduplicatePOIs(t *testing.T) {
	existing := []models.NearbyPOI{
		{Name: "Cafe A", Lat: 41.3800, Lon: 2.1700},
	}
	incoming := []models.NearbyPOI{
		{Name: "Cafe A (duplicate)", Lat: 41.38003, Lon: 2.17003}, // ~4m away, should be deduped
		{Name: "Cafe B", Lat: 41.3850, Lon: 2.1750},               // ~600m away, unique
	}

	merged := deduplicatePOIs(existing, incoming)
	if len(merged) != 2 {
		t.Errorf("expected 2 POIs after dedup, got %d", len(merged))
	}
}

// --- Overpass query builder tests ---

func TestBuildOverpassQuery_Category(t *testing.T) {
	q := buildOverpassQuery(41.38, 2.17, 500, "restaurant")
	if q == "" {
		t.Error("query should not be empty")
	}
	// Should contain amenity=restaurant.
	if !contains(q, "amenity=restaurant") {
		t.Error("query should contain amenity=restaurant")
	}
}

func TestBuildOverpassQuery_Tourism(t *testing.T) {
	q := buildOverpassQuery(41.38, 2.17, 500, "attraction")
	if !contains(q, "tourism=attraction") {
		t.Error("query should contain tourism=attraction for tourism categories")
	}
}

func TestBuildOverpassQuery_All(t *testing.T) {
	q := buildOverpassQuery(41.38, 2.17, 500, "all")
	if !contains(q, "amenity~") {
		t.Error("'all' query should contain amenity regex")
	}
	if !contains(q, "tourism~") {
		t.Error("'all' query should contain tourism regex")
	}
}

func TestWaitForOSMRateLimitHonorsContextCancellation(t *testing.T) {
	osmRateLimiter.Lock()
	osmRateLimiter.lastReq = time.Now()
	osmRateLimiter.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := waitForOSMRateLimit(ctx)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("waitForOSMRateLimit ignored cancellation and slept too long")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr) && searchSubstring(s, substr))
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
