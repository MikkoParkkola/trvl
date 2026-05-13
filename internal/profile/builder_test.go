package profile

import (
	"testing"
)

func TestBuildProfileEmpty(t *testing.T) {
	p := BuildProfile(nil)
	if p == nil {
		t.Fatal("should not be nil")
	}
	if p.TotalTrips != 0 {
		t.Errorf("TotalTrips = %d, want 0", p.TotalTrips)
	}
	if p.GeneratedAt == "" {
		t.Error("GeneratedAt should be set")
	}
}

func TestBuildProfileFlights(t *testing.T) {
	bookings := []Booking{
		{Type: "flight", Provider: "KLM", From: "HEL", To: "AMS", Price: 189, Currency: "EUR", TravelDate: "2026-03-15", Date: "2026-02-15"},
		{Type: "flight", Provider: "KLM", From: "AMS", To: "HEL", Price: 210, Currency: "EUR", TravelDate: "2026-03-20", Date: "2026-02-15"},
		{Type: "flight", Provider: "Finnair", From: "HEL", To: "BCN", Price: 250, Currency: "EUR", TravelDate: "2026-06-01", Date: "2026-05-01"},
		{Type: "flight", Provider: "Finnair", From: "BCN", To: "HEL", Price: 220, Currency: "EUR", TravelDate: "2026-06-08", Date: "2026-05-01"},
	}

	p := BuildProfile(bookings)

	if p.TotalFlights != 4 {
		t.Errorf("TotalFlights = %d, want 4", p.TotalFlights)
	}

	// Average flight price.
	wantAvg := (189.0 + 210 + 250 + 220) / 4
	if p.AvgFlightPrice != wantAvg {
		t.Errorf("AvgFlightPrice = %v, want %v", p.AvgFlightPrice, wantAvg)
	}

	// Top airlines.
	if len(p.TopAirlines) == 0 {
		t.Fatal("TopAirlines is empty")
	}
	// KL and AY should both appear.
	foundKL, foundAY := false, false
	for _, a := range p.TopAirlines {
		if a.Code == "KL" {
			foundKL = true
			if a.Flights != 2 {
				t.Errorf("KL flights = %d, want 2", a.Flights)
			}
		}
		if a.Code == "AY" {
			foundAY = true
			if a.Flights != 2 {
				t.Errorf("AY flights = %d, want 2", a.Flights)
			}
		}
	}
	if !foundKL {
		t.Error("expected KL in TopAirlines")
	}
	if !foundAY {
		t.Error("expected AY in TopAirlines")
	}

	// Alliance: KL is SkyTeam, AY is Oneworld — equal, so either is fine.
	if p.PreferredAlliance == "" {
		t.Error("PreferredAlliance should be set")
	}

	// Home detected should be HEL (most departed from).
	if len(p.HomeDetected) == 0 || p.HomeDetected[0] != "HEL" {
		t.Errorf("HomeDetected = %v, want [HEL ...]", p.HomeDetected)
	}

	// Top destinations.
	if len(p.TopDestinations) == 0 {
		t.Error("TopDestinations is empty")
	}

	// Top routes.
	if len(p.TopRoutes) == 0 {
		t.Error("TopRoutes is empty")
	}

	// Booking lead days.
	if p.AvgBookingLead <= 0 {
		t.Errorf("AvgBookingLead = %d, want > 0", p.AvgBookingLead)
	}

	// Seasonal pattern.
	if p.SeasonalPattern == nil {
		t.Error("SeasonalPattern should not be nil")
	}

	// Preferred days.
	if len(p.PreferredDays) == 0 {
		t.Error("PreferredDays should not be empty")
	}
}

func TestBuildProfileHotels(t *testing.T) {
	bookings := []Booking{
		{Type: "hotel", Provider: "Marriott", Price: 450, Nights: 3, Stars: 4, TravelDate: "2026-03-15"},
		{Type: "hotel", Provider: "Hilton", Price: 600, Nights: 3, Stars: 5, TravelDate: "2026-06-01"},
		{Type: "hotel", Provider: "Marriott", Price: 300, Nights: 2, Stars: 4, TravelDate: "2026-09-10"},
	}

	p := BuildProfile(bookings)

	if p.TotalHotelNights != 8 {
		t.Errorf("TotalHotelNights = %d, want 8", p.TotalHotelNights)
	}

	// Average star rating.
	wantStars := (4.0 + 5 + 4) / 3
	if p.AvgStarRating != wantStars {
		t.Errorf("AvgStarRating = %v, want %v", p.AvgStarRating, wantStars)
	}

	// Average nightly rate: (450/3 + 600/3 + 300/2) / 3 = (150 + 200 + 150) / 3 = 166.67
	if p.AvgNightlyRate < 160 || p.AvgNightlyRate > 170 {
		t.Errorf("AvgNightlyRate = %v, want ~166.67", p.AvgNightlyRate)
	}

	// Top hotel chains.
	if len(p.TopHotelChains) == 0 {
		t.Fatal("TopHotelChains is empty")
	}
	if p.TopHotelChains[0].Name != "Marriott" {
		t.Errorf("TopHotelChains[0] = %q, want Marriott", p.TopHotelChains[0].Name)
	}
	if p.TopHotelChains[0].Nights != 5 {
		t.Errorf("Marriott nights = %d, want 5", p.TopHotelChains[0].Nights)
	}

	// Preferred type.
	if p.PreferredType != "hotel" {
		t.Errorf("PreferredType = %q, want hotel", p.PreferredType)
	}
}

func TestBuildProfileMixed(t *testing.T) {
	bookings := []Booking{
		{Type: "flight", Provider: "AY", From: "HEL", To: "BCN", Price: 200, TravelDate: "2026-06-01"},
		{Type: "hotel", Provider: "Marriott", Price: 450, Nights: 3, Stars: 4, TravelDate: "2026-06-01"},
		{Type: "ground", Provider: "FlixBus", Price: 19, TravelDate: "2026-06-04"},
		{Type: "airbnb", Provider: "Airbnb", Price: 300, Nights: 4, TravelDate: "2026-07-15"},
		{Type: "flight", Provider: "AY", From: "BCN", To: "HEL", Price: 180, TravelDate: "2026-06-08"},
	}

	p := BuildProfile(bookings)

	if p.TotalFlights != 2 {
		t.Errorf("TotalFlights = %d, want 2", p.TotalFlights)
	}
	if p.TotalHotelNights != 7 { // 3 hotel + 4 airbnb
		t.Errorf("TotalHotelNights = %d, want 7", p.TotalHotelNights)
	}

	// Ground modes.
	if len(p.TopGroundModes) == 0 {
		t.Error("TopGroundModes is empty")
	}
	if p.TopGroundModes[0].Mode != "bus" {
		t.Errorf("TopGroundModes[0] = %q, want bus", p.TopGroundModes[0].Mode)
	}

	// Budget tier.
	if p.BudgetTier == "" {
		t.Error("BudgetTier should be set")
	}

	// Trip estimation.
	if p.TotalTrips < 1 {
		t.Errorf("TotalTrips = %d, want >= 1", p.TotalTrips)
	}

	// Avg trip length.
	if p.AvgTripLength <= 0 {
		t.Errorf("AvgTripLength = %v, want > 0", p.AvgTripLength)
	}
}

func TestBuildProfileGround(t *testing.T) {
	bookings := []Booking{
		{Type: "ground", Provider: "FlixBus", Price: 19, TravelDate: "2026-03-01"},
		{Type: "ground", Provider: "Eurostar", Price: 89, TravelDate: "2026-04-15"},
		{Type: "ground", Provider: "FlixBus", Price: 25, TravelDate: "2026-05-20"},
		{Type: "ground", Provider: "Tallink", Price: 45, TravelDate: "2026-06-10"},
	}

	p := BuildProfile(bookings)

	if len(p.TopGroundModes) == 0 {
		t.Fatal("TopGroundModes is empty")
	}

	// Bus should be most frequent.
	foundBus := false
	for _, m := range p.TopGroundModes {
		if m.Mode == "bus" {
			foundBus = true
			if m.Count != 2 {
				t.Errorf("bus count = %d, want 2", m.Count)
			}
		}
	}
	if !foundBus {
		t.Error("expected bus in TopGroundModes")
	}
}

func TestParseAirline(t *testing.T) {
	tests := []struct {
		provider string
		wantCode string
		wantName string
	}{
		{"KLM", "KL", "KLM"},
		{"Finnair", "AY", "Finnair"},
		{"Ryanair", "FR", "Ryanair"},
		{"AY", "AY", ""},
		{"KL", "KL", ""},
		{"AY (Finnair)", "AY", "Finnair"},
		{"Lufthansa", "LH", "Lufthansa"},
		{"British Airways", "BA", "British Airways"},
		{"", "", ""},
		{"Unknown Carrier", "", "Unknown Carrier"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			code, name := parseAirline(tt.provider)
			if code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
		})
	}
}

func TestNormalizeChain(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"Marriott Hotels", "Marriott"},
		{"hilton garden inn", "Hilton"},
		{"ihg hotel", "IHG"},
		{"Random Inn", "Random Inn"},
		{"", ""},
		{"Airbnb stay", "Airbnb"},
		{"Booking.com property", "Booking.com"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := normalizeChain(tt.provider)
			if got != tt.want {
				t.Errorf("normalizeChain(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestNormalizeGroundMode(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"flixbus", "bus"},
		{"eurostar", "train"},
		{"tallink", "ferry"},
		{"uber", "rideshare"},
		{"bus", "bus"},
		{"train", "train"},
		{"ferry", "ferry"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := normalizeGroundMode(tt.provider)
			if got != tt.want {
				t.Errorf("normalizeGroundMode(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestDetectAlliance(t *testing.T) {
	tests := []struct {
		name     string
		airlines []AirlineStats
		want     string
	}{
		{
			name:     "SkyTeam dominant",
			airlines: []AirlineStats{{Code: "KL", Flights: 5}, {Code: "AF", Flights: 3}, {Code: "AY", Flights: 2}},
			want:     "SkyTeam",
		},
		{
			name:     "Oneworld dominant",
			airlines: []AirlineStats{{Code: "AY", Flights: 10}, {Code: "BA", Flights: 5}},
			want:     "Oneworld",
		},
		{
			name:     "Star Alliance dominant",
			airlines: []AirlineStats{{Code: "LH", Flights: 7}, {Code: "SK", Flights: 3}},
			want:     "Star Alliance",
		},
		{
			name:     "no alliance (LCC only)",
			airlines: []AirlineStats{{Code: "FR", Flights: 5}, {Code: "U2", Flights: 3}},
			want:     "",
		},
		{
			name:     "empty",
			airlines: nil,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectAlliance(tt.airlines)
			if got != tt.want {
				t.Errorf("detectAlliance = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClassifyBudget(t *testing.T) {
	tests := []struct {
		avgTrip  float64
		avgNight float64
		want     string
	}{
		{200, 40, "budget"},
		{500, 100, "mid-range"},
		{2000, 250, "premium"},
		{0, 0, ""},
		{100, 0, "budget"},   // fallback to trip cost
		{1500, 0, "premium"}, // fallback to trip cost
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := classifyBudget(tt.avgTrip, tt.avgNight)
			if got != tt.want {
				t.Errorf("classifyBudget(%v, %v) = %q, want %q", tt.avgTrip, tt.avgNight, got, tt.want)
			}
		})
	}
}

func TestEstimateTripCount(t *testing.T) {
	// Bookings on same dates = 1 trip.
	bookings := []Booking{
		{TravelDate: "2026-03-15"},
		{TravelDate: "2026-03-15"},
		{TravelDate: "2026-03-16"},
	}
	count := estimateTripCount(bookings)
	if count != 1 {
		t.Errorf("same-date group: got %d, want 1", count)
	}

	// Bookings far apart = separate trips.
	bookings2 := []Booking{
		{TravelDate: "2026-03-15"},
		{TravelDate: "2026-06-01"},
		{TravelDate: "2026-09-10"},
	}
	count2 := estimateTripCount(bookings2)
	if count2 != 3 {
		t.Errorf("separate trips: got %d, want 3", count2)
	}

	// No dates = fallback to booking count.
	bookings3 := []Booking{
		{Provider: "KLM"},
		{Provider: "Finnair"},
	}
	count3 := estimateTripCount(bookings3)
	if count3 != 2 {
		t.Errorf("no dates: got %d, want 2", count3)
	}
}

func TestTopKeys(t *testing.T) {
	counts := map[string]int{
		"HEL": 10,
		"AMS": 5,
		"BCN": 3,
		"LHR": 1,
	}

	top := topKeys(counts, 2)
	if len(top) != 2 {
		t.Fatalf("len = %d, want 2", len(top))
	}
	if top[0] != "HEL" {
		t.Errorf("top[0] = %q, want HEL", top[0])
	}
	if top[1] != "AMS" {
		t.Errorf("top[1] = %q, want AMS", top[1])
	}
}

func TestIsAlpha(t *testing.T) {
	if !isAlpha("ABC") {
		t.Error("ABC should be alpha")
	}
	if !isAlpha("abc") {
		t.Error("abc should be alpha")
	}
	if isAlpha("AB1") {
		t.Error("AB1 should not be alpha")
	}
	if isAlpha("") {
		t.Error("empty should not be alpha")
	}
}
