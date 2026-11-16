package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// stubNightlyAndMonthly makes the hotel search return one nightly hotel and
// one monthly rental, offline, honouring StayTerm the way the core search does
// (TestCoreSearchLeavesMonthlyRentalsOutByDefault pins the core). It returns
// the terms each call asked for.
func stubNightlyAndMonthly(t *testing.T) *[]hotels.StayTerm {
	t.Helper()
	var terms []hotels.StayTerm
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	origS, origA, origR := searchHotelsFunc, fetchHotelAmenitiesFunc, getRoomAvailabilityWithOptsFunc
	t.Cleanup(func() {
		searchHotelsFunc, fetchHotelAmenitiesFunc, getRoomAvailabilityWithOptsFunc = origS, origA, origR
	})
	searchHotelsFunc = func(_ context.Context, _ string, opts hotels.HotelSearchOptions) (*models.HotelSearchResult, error) {
		terms = append(terms, opts.StayTerm)
		res := &models.HotelSearchResult{Success: true, Count: 2, Hotels: []models.HotelResult{
			{Name: "Hotel Nightly", HotelID: "/g/nightly", Price: 90, Currency: "EUR", PropertyType: "hotel",
				PriceBasis: models.PriceBasisLeadIn, BookingURL: "https://booking.example/nightly",
				Sources: []models.PriceSource{{Provider: "booking", Price: 90, Currency: "EUR", PriceBasis: models.PriceBasisLeadIn}}},
			{Name: "Spotahome Flat", HotelID: "spot-1", Price: 1850, Currency: "EUR", PropertyType: "apartment",
				PriceBasis: models.PriceBasisMonthly, BookingURL: "https://spotahome.example/1",
				Sources: []models.PriceSource{{Provider: "spotahome", Price: 1850, Currency: "EUR", PriceBasis: models.PriceBasisMonthly}}},
		}}
		if opts.StayTerm != hotels.StayIncludeMonthly {
			// What the core search returns for a nightly term (pinned by
			// TestCoreSearchLeavesMonthlyRentalsOutByDefault).
			res.Hotels, res.Count, res.MonthlyOmitted = res.Hotels[:1], 1, 1
		}
		return res, nil
	}
	fetchHotelAmenitiesFunc = func(context.Context, string) ([]string, error) { return nil, errors.New("offline") }
	getRoomAvailabilityWithOptsFunc = func(context.Context, hotels.RoomSearchOptions) (*hotels.RoomAvailability, error) {
		return nil, errors.New("offline")
	}
	return &terms
}

var stayArgs = map[string]any{"location": "Vienna", "check_in": "2027-04-10", "check_out": "2027-04-12"}

func hotelNamesIn(v any) []string {
	var names []string
	list, _ := v.([]any)
	for _, h := range list {
		if m, ok := h.(map[string]any); ok {
			n, _ := m["name"].(string)
			names = append(names, n)
		}
	}
	return names
}

// TestNightlyHotelListsLeaveMonthlyRentalsOut: a monthly rent is not a nightly
// price, so search_hotels and search_hotels_with_details leave the rental out,
// count it, and point to search_accommodations.
func TestNightlyHotelListsLeaveMonthlyRentalsOut(t *testing.T) {
	terms := stubNightlyAndMonthly(t)
	for _, tool := range []string{"search_hotels", "search_hotels_with_details"} {
		text, structured, isErr := callTool(t, NewServer(), tool, stayArgs)
		if isErr {
			t.Fatalf("%s: isError: %s", tool, text)
		}
		names := hotelNamesIn(structured["hotels"])
		for _, n := range names {
			if n == "Spotahome Flat" {
				t.Errorf("%s lists the monthly rental: %v", tool, names)
			}
		}
		if len(names) != 1 {
			t.Errorf("%s hotels = %v, want only Hotel Nightly", tool, names)
		}
		if got, _ := structured["monthly_omitted"].(float64); got != 1 {
			t.Errorf("%s monthly_omitted = %v, want 1", tool, structured["monthly_omitted"])
		}
		for _, want := range []string{"1 monthly rental", "search_accommodations"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s summary lacks %q:\n%s", tool, want, text)
			}
		}
	}
	for _, term := range *terms {
		if term != hotels.StayNightly {
			t.Errorf("a nightly hotel tool asked for stay term %v, want StayNightly", term)
		}
	}
}

// TestAccommodationsKeepMonthlyRentalsLabelled: search_accommodations is where
// monthly rentals belong; the rental stays, labelled with its monthly basis.
func TestAccommodationsKeepMonthlyRentalsLabelled(t *testing.T) {
	terms := stubNightlyAndMonthly(t)
	text, structured, isErr := callTool(t, NewServer(), "search_accommodations", stayArgs)
	if isErr {
		t.Fatalf("isError: %s", text)
	}
	found := false
	for _, key := range []string{"candidates", "offers", "rejected_offers"} {
		list, _ := structured[key].([]any)
		for _, item := range list {
			m, _ := item.(map[string]any)
			if strings.Contains(strings.ToLower(asString(m["name"])+asString(m["property_name"])), "spotahome flat") {
				found = true
				if basis := asString(m["price_basis"]); basis != models.PriceBasisMonthly {
					t.Errorf("%s rental price_basis = %q, want monthly", key, basis)
				}
			}
		}
	}
	if !found {
		t.Errorf("search_accommodations dropped the monthly rental: %v", structured)
	}
	if len(*terms) == 0 || (*terms)[0] != hotels.StayIncludeMonthly {
		t.Errorf("search_accommodations asked for stay terms %v, want StayIncludeMonthly", *terms)
	}
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
