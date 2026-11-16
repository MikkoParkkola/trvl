package mcp

import (
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

// TestAccommodationEvidenceCreditsTheHeadlineProvider: evidence names the
// provider whose quote is the headline, not the cheaper lead-in teaser; the
// room lookup still keys on the Google place ID.
func TestAccommodationEvidenceCreditsTheHeadlineProvider(t *testing.T) {
	h := models.HotelResult{HotelID: "/g/11c0", HeadlineProvider: "agoda", CheapestSource: "google_hotels",
		Sources: []models.PriceSource{{Provider: "google_hotels"}, {Provider: "agoda"}}}
	if got := accommodationHotelProvider(h); got != "agoda" {
		t.Errorf("accommodationHotelProvider = %q, want agoda", got)
	}
	if got := accommodationEvidenceProvider(h, models.AccommodationOffer{}); got != "agoda" {
		t.Errorf("accommodationEvidenceProvider = %q, want agoda", got)
	}
	if !accommodationHotelIDSupportsRoomLookup(h) {
		t.Error("a Google place ID lost its room lookup because the headline came from agoda")
	}
}

// TestHotelSummaryCountsFartherHotels: the summary says how many hotels the
// distance cap left out and how to bring them back.
func TestHotelSummaryCountsFartherHotels(t *testing.T) {
	for name, res := range map[string]*models.HotelSearchResult{
		"some shown": {Success: true, Count: 1, Hotels: []models.HotelResult{{Name: "A", Price: 80, Currency: "EUR"}}, FartherOmitted: 3, MaxDistanceKm: 25},
		"none shown": {Success: true, Count: 0, FartherOmitted: 3, MaxDistanceKm: 25},
	} {
		got := hotelSummary(res, "Vienna")
		for _, want := range []string{"3 hotels", "25 km", "max_distance"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: summary %q lacks %q", name, got, want)
			}
		}
	}
}

// TestHotelListToolsDefaultTo25Km: the list tools (search_hotels,
// search_accommodations, search_hotels_with_details share this builder) cap
// distance at 25 km unless max_distance is given; 0 lifts the cap.
func TestHotelListToolsDefaultTo25Km(t *testing.T) {
	base := map[string]any{"location": "Vienna", "check_in": "2027-04-10", "check_out": "2027-04-12"}
	for _, c := range []struct {
		arg  any
		want float64
	}{{nil, 25}, {30.0, 30}, {0.0, 0}} {
		args := map[string]any{}
		for k, v := range base {
			args[k] = v
		}
		if c.arg != nil {
			args["max_distance"] = c.arg
		}
		req, err := buildHotelSearchRequest(args)
		if err != nil {
			t.Fatal(err)
		}
		if req.Options.MaxDistanceKm != c.want {
			t.Errorf("max_distance %v: cap = %v, want %v", c.arg, req.Options.MaxDistanceKm, c.want)
		}
	}
}

// TestHotelListToolsReportFartherHotels: every list tool that gets the 25 km
// default says what it left out, and declares the new fields in its schema.
func TestHotelListToolsReportFartherHotels(t *testing.T) {
	details := hotelDetailsSummary(hotelDetailsSearchResponse{Success: true, Count: 1, TotalAvailable: 1, MaxDistanceKm: 25, FartherOmitted: 3}, "Vienna")
	accom := accommodationSearchSummary(accommodationSearchResponse{Success: true, TotalAvailable: 1, CandidateCount: 1, MaxDistanceKm: 25, FartherOmitted: 3,
		Need: models.AccommodationNeed{Location: "Vienna"}})
	for name, got := range map[string]string{"search_hotels_with_details": details, "search_accommodations": accom} {
		for _, want := range []string{"3 hotels", "25 km", "max_distance"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s summary %q lacks %q", name, got, want)
			}
		}
	}

	props := func(schema interface{}) map[string]interface{} {
		return schema.(map[string]interface{})["properties"].(map[string]interface{})
	}
	hotelItem := func(schema interface{}) map[string]interface{} {
		return props(schema)["hotels"].(map[string]interface{})["items"].(map[string]interface{})["properties"].(map[string]interface{})
	}
	for name, schema := range map[string]interface{}{
		"search_hotels": hotelSearchOutputSchema(), "search_hotels_with_details": hotelDetailsSearchOutputSchema(), "search_accommodations": accommodationSearchOutputSchema(),
	} {
		for _, k := range []string{"max_distance_km", "farther_omitted"} {
			if _, ok := props(schema)[k]; !ok {
				t.Errorf("%s output schema lacks %q", name, k)
			}
		}
		if name == "search_accommodations" {
			continue
		}
		for _, k := range []string{"headline_provider", "price_provenance"} {
			if _, ok := hotelItem(schema)[k]; !ok {
				t.Errorf("%s hotel schema lacks %q", name, k)
			}
		}
	}
}
