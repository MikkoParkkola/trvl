package main

import (
	"context"
	"strings"
	"testing"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/trip"
	"github.com/MikkoParkkola/trvl/internal/trips"
	"github.com/MikkoParkkola/trvl/internal/tripwindow"
)

func TestPrintWhenTable_WithPreferredAndHotelV14(t *testing.T) {
	candidates := []tripwindow.Candidate{
		{
			Start:             "2026-07-01",
			End:               "2026-07-08",
			Nights:            7,
			FlightCost:        199,
			HotelCost:         350,
			EstimatedCost:     549,
			Currency:          "EUR",
			OverlapsPreferred: true,
			HotelName:         "Hotel Barcelona",
		},
	}
	err := printWhenTable(candidates, "HEL", "BCN")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintSuggestTable_WithInsightsV14(t *testing.T) {
	result := &trip.SmartDateResult{
		Success:      true,
		Origin:       "HEL",
		Destination:  "BCN",
		AveragePrice: 250,
		Currency:     "EUR",
		CheapestDates: []trip.CheapDate{
			{Date: "2026-07-01", DayOfWeek: "Wednesday", Price: 199, Currency: "EUR"},
		},
		Insights: []trip.DateInsight{
			{Type: "saving", Description: "Wednesday is 20% cheaper than average"},
		},
	}
	ctx := context.Background()
	err := printSuggestTable(ctx, "", result)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestTripsAlertsCmd_JSONFormatEmptyV14(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)
	oldFormat := format
	format = "json"
	defer func() { format = oldFormat }()
	cmd := tripsCmd()
	cmd.SetArgs([]string{"alerts"})
	_ = cmd.Execute()
}

func TestRunProfileShow_WithBookingsTable(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := profileAddCmd()
	addCmd.SetArgs([]string{
		"--type", "flight",
		"--provider", "Finnair",
		"--from", "HEL",
		"--to", "NRT",
		"--price", "799",
		"--currency", "EUR",
		"--travel-date", "2026-06-15",
	})
	_ = addCmd.Execute()

	cmd := profileCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Errorf("profile show: %v", err)
	}
}

func TestRunProfileShow_WithBookingsJSON(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := profileAddCmd()
	addCmd.SetArgs([]string{
		"--type", "flight",
		"--provider", "KLM",
		"--from", "HEL",
		"--to", "AMS",
		"--price", "189",
		"--currency", "EUR",
	})
	_ = addCmd.Execute()

	oldFormat := format
	format = "json"
	defer func() { format = oldFormat }()

	cmd := profileCmd()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Errorf("profile show json: %v", err)
	}
}

func TestRunProfileSummary_WithBookings(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	addCmd := profileAddCmd()
	addCmd.SetArgs([]string{
		"--type", "flight",
		"--provider", "Finnair",
		"--from", "HEL",
		"--to", "NRT",
		"--price", "799",
		"--currency", "EUR",
		"--travel-date", "2026-06-15",
	})
	_ = addCmd.Execute()

	cmd := profileCmd()
	cmd.SetArgs([]string{"summary"})
	if err := cmd.Execute(); err != nil {
		t.Errorf("profile summary: %v", err)
	}
}

func TestPrintSuggestTable_FailureBranchV21(t *testing.T) {
	result := &trip.SmartDateResult{
		Success: false,
		Error:   "no dates found",
	}
	ctx := context.Background()
	err := printSuggestTable(ctx, "", result)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestShareCmd_LastFormatLinkV21(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	ls := &LastSearch{
		Command:        "flights",
		Origin:         "HEL",
		Destination:    "NRT",
		DepartDate:     "2026-07-01",
		FlightPrice:    799,
		FlightCurrency: "EUR",
		FlightAirline:  "Finnair",
	}
	saveLastSearch(ls)

	cmd := shareCmd()
	cmd.SetArgs([]string{"--last", "--format", "link"})

	// "link" was the gist-upload format and is retired (#527). Until this
	// commit the assertion-free `_ = cmd.Execute()` here published a real
	// public gist on every run. It must now be refused outright: a caller
	// expecting a URL should be told the format is gone, not handed an
	// itinerary on stdout.
	t.Setenv("PATH", t.TempDir())
	err := cmd.Execute()
	if err == nil {
		t.Fatal("share --format link returned nil; the retired upload format must error")
	}
	if !strings.Contains(err.Error(), "removed") {
		t.Errorf("share --format link error = %v, want it to say the format was removed", err)
	}
}

func TestTripsShowCmd_JSONFormatV21(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "JSON Show Trip"})
	if err := createCmd.Execute(); err != nil {
		t.Fatalf("create trip: %v", err)
	}

	store, err := loadTripStore()
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	list := store.List()
	if len(list) == 0 {
		t.Skip("no trips in store")
	}
	tripID := list[0].ID

	oldFormat := format
	format = "json"
	defer func() { format = oldFormat }()

	cmd := tripsCmd()
	cmd.SetArgs([]string{"show", tripID})
	if err := cmd.Execute(); err != nil {
		t.Errorf("trips show json: %v", err)
	}
}

func TestTripsListCmd_JSONFormatV21(t *testing.T) {
	tmp := t.TempDir()
	setTestHome(t, tmp)

	createCmd := tripsCmd()
	createCmd.SetArgs([]string{"create", "JSON List Trip"})
	_ = createCmd.Execute()

	oldFormat := format
	format = "json"
	defer func() { format = oldFormat }()

	cmd := tripsCmd()
	cmd.SetArgs([]string{"list"})
	_ = cmd.Execute()
}

func TestPrintMultiCityTable_FailureBranchV23(t *testing.T) {
	result := &trip.MultiCityResult{
		Success: false,
		Error:   "no routes found",
	}
	err := printMultiCityTable(context.Background(), "", result)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintMultiCityTable_WithSavingsV23(t *testing.T) {
	result := &trip.MultiCityResult{
		Success:      true,
		HomeAirport:  "HEL",
		OptimalOrder: []string{"BCN", "ROM"},
		Permutations: 2,
		Currency:     "EUR",
		TotalCost:    600,
		Savings:      150,
		Segments: []trip.Segment{
			{From: "HEL", To: "BCN", Price: 200, Currency: "EUR"},
			{From: "BCN", To: "ROM", Price: 150, Currency: "EUR"},
			{From: "ROM", To: "HEL", Price: 250, Currency: "EUR"},
		},
	}
	err := printMultiCityTable(context.Background(), "", result)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPrintMultiCityTable_NoSavingsV23(t *testing.T) {
	result := &trip.MultiCityResult{
		Success:      true,
		HomeAirport:  "HEL",
		OptimalOrder: []string{"BCN"},
		Permutations: 1,
		Currency:     "EUR",
		TotalCost:    400,
		Savings:      0,
		Segments: []trip.Segment{
			{From: "HEL", To: "BCN", Price: 200, Currency: "EUR"},
			{From: "BCN", To: "HEL", Price: 200, Currency: "EUR"},
		},
	}
	err := printMultiCityTable(context.Background(), "", result)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPointsValueCmd_WithJSONFormatV23(t *testing.T) {
	cmd := pointsValueCmd()
	cmd.SetArgs([]string{"--format", "json"})
	_ = cmd.Execute()
}

func TestFormatEventsCard_EmptyV24(t *testing.T) {
	err := formatEventsCard(nil, "Barcelona", "2026-07-01", "2026-07-08")
	if err != nil {
		t.Errorf("formatEventsCard empty: %v", err)
	}
}

func TestFormatEventsCard_WithEventsV24(t *testing.T) {
	events := []models.Event{
		{
			Name:       "Test Concert",
			Date:       "2026-07-03",
			Time:       "20:00",
			Venue:      "Palau Sant Jordi",
			Type:       "Music",
			PriceRange: "€30-€80",
		},
		{
			Name:       "FC Barcelona Match",
			Date:       "2026-07-05",
			Time:       "18:00",
			Venue:      "Spotify Camp Nou",
			Type:       "Sports",
			PriceRange: "€50-€200",
		},
	}
	err := formatEventsCard(events, "Barcelona", "2026-07-01", "2026-07-08")
	if err != nil {
		t.Errorf("formatEventsCard with events: %v", err)
	}
}

func TestFormatNearbyCard_EmptyV24(t *testing.T) {
	result := &destinations.NearbyResult{}
	if err := formatNearbyCard(result); err != nil {
		t.Errorf("formatNearbyCard empty: %v", err)
	}
}

func TestFormatNearbyCard_WithPOIsV24(t *testing.T) {
	result := &destinations.NearbyResult{
		POIs: []models.NearbyPOI{
			{Name: "La Boqueria", Type: "market", Distance: 120, Cuisine: "market", Hours: "9:00-20:00"},
			{Name: "Bar El Xampanyet", Type: "bar", Distance: 250, Cuisine: "tapas"},
		},
		RatedPlaces: []models.RatedPlace{
			{Name: "Tickets", Rating: 9.5, Category: "restaurant", PriceLevel: 3, Distance: 400},
		},
		Attractions: []models.Attraction{
			{Name: "Sagrada Familia", Kind: "church", Distance: 1500},
		},
	}
	if err := formatNearbyCard(result); err != nil {
		t.Errorf("formatNearbyCard with POIs: %v", err)
	}
}

func TestTruncate_V24(t *testing.T) {
	cases := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "he..."},
		{"hello", 5, "hello"},
		{"ab", 2, "ab"},
		{"abc", 1, "a"},
	}
	for _, tc := range cases {
		got := truncate(tc.input, tc.maxLen)
		if got != tc.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.maxLen, got, tc.want)
		}
	}
}

func TestLoungeFFCards_EmptyV24(t *testing.T) {
	cards := loungeFFCards(nil)
	if len(cards) != 0 {
		t.Errorf("expected empty cards for nil programs, got %v", cards)
	}
}

func TestLoungeFFCards_WithAlliancesV24(t *testing.T) {
	programs := []preferences.FrequentFlyerStatus{
		{Alliance: "oneworld", Tier: "sapphire", AirlineCode: "BA"},
		{Alliance: "star_alliance", Tier: "gold", AirlineCode: "LH"},
		{Alliance: "skyteam", Tier: "elite_plus", AirlineCode: "AF"},
	}
	cards := loungeFFCards(programs)
	if len(cards) == 0 {
		t.Error("expected non-empty cards for known alliances")
	}
}

func TestLoungeFFCards_UnknownAllianceV24(t *testing.T) {
	programs := []preferences.FrequentFlyerStatus{
		{Alliance: "unknown-alliance", Tier: "gold", AirlineCode: "XX"},
	}
	cards := loungeFFCards(programs)

	_ = cards
}

func TestLoungeTierDisplay_KnownAllianceV24(t *testing.T) {
	display := loungeTierDisplay("oneworld", "emerald")
	if display != "Emerald" {
		t.Errorf("expected Emerald, got %s", display)
	}
}

func TestLoungeTierDisplay_UnknownTierV24(t *testing.T) {

	display := loungeTierDisplay("oneworld", "diamond")
	if display == "" {
		t.Error("expected non-empty display for unknown tier")
	}
}

func TestLoungeTierDisplay_UnknownAllianceV24(t *testing.T) {
	display := loungeTierDisplay("unknown", "gold")
	if display == "" {
		t.Error("expected non-empty display for unknown alliance")
	}
}

func TestFormatDestinationCard_MinimalV25(t *testing.T) {
	info := &models.DestinationInfo{
		Location: "Barcelona",
	}
	if err := formatDestinationCard(info); err != nil {
		t.Errorf("formatDestinationCard minimal: %v", err)
	}
}

func TestFormatDestinationCard_FullV25(t *testing.T) {
	info := &models.DestinationInfo{
		Location: "Tokyo",
		Timezone: "Asia/Tokyo",
		Country: models.CountryInfo{
			Name:       "Japan",
			Code:       "JP",
			Capital:    "Tokyo",
			Languages:  []string{"Japanese"},
			Currencies: []string{"JPY"},
			Region:     "Asia",
		},
		Weather: models.WeatherInfo{
			Forecast: []models.WeatherDay{
				{Date: "2026-07-01", TempHigh: 28, TempLow: 22, Precipitation: 3.5, Description: "Partly cloudy"},
				{Date: "2026-07-02", TempHigh: 30, TempLow: 24, Precipitation: 0, Description: "Sunny"},
			},
		},
		Holidays: []models.Holiday{
			{Date: "2026-07-01", Name: "Test Holiday", Type: "public"},
		},
		Safety: models.SafetyInfo{
			Level:       4.5,
			Advisory:    "Exercise normal caution",
			Source:      "Travel Advisory",
			LastUpdated: "2026-01-01",
		},
		Currency: models.CurrencyInfo{
			LocalCurrency: "JPY",
			ExchangeRate:  160.5,
			BaseCurrency:  "EUR",
		},
	}
	if err := formatDestinationCard(info); err != nil {
		t.Errorf("formatDestinationCard full: %v", err)
	}
}

func TestFormatGuideCard_EmptyV25(t *testing.T) {
	guide := &models.WikivoyageGuide{
		Location: "Prague",
		URL:      "https://en.wikivoyage.org/wiki/Prague",
	}
	if err := formatGuideCard(guide); err != nil {
		t.Errorf("formatGuideCard empty: %v", err)
	}
}

func TestFormatGuideCard_WithContentV25(t *testing.T) {
	guide := &models.WikivoyageGuide{
		Location: "Barcelona",
		URL:      "https://en.wikivoyage.org/wiki/Barcelona",
		Summary:  "Barcelona is the capital of Catalonia and the second-largest city in Spain.",
		Sections: map[string]string{
			"See":       "The city boasts amazing architecture by Antoni Gaudí.",
			"Eat":       "Tapas, pintxos, and paella are local specialties.",
			"Get in":    "El Prat Airport serves many European routes.",
			"Sleep":     "Hotels range from budget to luxury in the Eixample district.",
			"Get out":   "Day trips to Montserrat and Costa Brava are popular.",
			"Stay safe": "Keep an eye on pickpockets in tourist areas.",
		},
	}
	if err := formatGuideCard(guide); err != nil {
		t.Errorf("formatGuideCard with content: %v", err)
	}
}

func TestPrintReviewsTable_EmptyV25(t *testing.T) {
	result := &models.HotelReviewResult{
		Name:    "Hotel Test",
		Summary: models.ReviewSummary{AverageRating: 4.2, TotalReviews: 100},
		Reviews: nil,
	}
	if err := printReviewsTable(result); err != nil {
		t.Errorf("printReviewsTable empty: %v", err)
	}
}

func TestPrintReviewsTable_WithReviewsV25(t *testing.T) {
	result := &models.HotelReviewResult{
		Name:    "Grand Hotel",
		Summary: models.ReviewSummary{AverageRating: 4.7, TotalReviews: 250},
		Reviews: []models.HotelReview{
			{Rating: 5.0, Text: "Excellent stay, highly recommend!", Author: "Alice", Date: "2026-04-01"},
			{Rating: 4.0, Text: "Good location but the room was a bit small for the price paid.", Author: "Bob", Date: "2026-03-28"},
			{Rating: 3.5, Text: strings.Repeat("This is a very long review text that exceeds the 80 character limit. ", 2), Author: "Charlie", Date: "2026-03-15"},
		},
	}
	if err := printReviewsTable(result); err != nil {
		t.Errorf("printReviewsTable with reviews: %v", err)
	}
}

func TestStarRating_V25(t *testing.T) {
	cases := []float64{0, 1, 2.5, 3, 4.5, 5}
	for _, r := range cases {
		s := starRating(r)
		if s == "" {
			t.Errorf("starRating(%v) returned empty string", r)
		}
	}
}

func TestPrintTripWeather_EmptyLegsV25(t *testing.T) {
	tr := &trips.Trip{
		ID:   "test-trip-weather",
		Name: "Weather Test Trip",
		Legs: nil,
	}

	printTripWeather(context.Background(), tr)
}

func TestPrintTripWeather_LegsWithEmptyToV25(t *testing.T) {
	tr := &trips.Trip{
		ID:   "test-trip-weather-2",
		Name: "Weather Test Trip 2",
		Legs: []trips.TripLeg{
			{From: "HEL", To: "", StartTime: "2026-07-01T08:00"},
			{From: "BCN", To: "HEL", StartTime: ""},
		},
	}

	printTripWeather(context.Background(), tr)
}

func TestFormatRoomsTable_NoName_UsesHotelID_V26(t *testing.T) {
	result := &hotels.RoomAvailability{
		HotelID: "/g/unknown",
		Rooms: []hotels.RoomType{
			{Name: "Deluxe", Price: 200, Currency: "EUR", MaxGuests: 2,
				Amenities: []string{"wifi", "breakfast", "pool", "spa", "gym", "parking"}},
		},
	}
	err := formatRoomsTable(result)
	if err != nil {
		t.Errorf("formatRoomsTable(no name, long amenities) error: %v", err)
	}
}

func TestFormatRoomsTable_ZeroPrice_V26(t *testing.T) {

	result := &hotels.RoomAvailability{
		Name: "Test Hotel",
		Rooms: []hotels.RoomType{
			{Name: "Free Room", Price: 0, Currency: "EUR"},
			{Name: "Paid Room", Price: 150, Currency: "EUR", MaxGuests: 2},
		},
	}
	err := formatRoomsTable(result)
	if err != nil {
		t.Errorf("formatRoomsTable(zero price) error: %v", err)
	}
}
