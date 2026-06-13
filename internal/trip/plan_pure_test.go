package trip

import (
	"testing"

	"github.com/MikkoParkkola/trvl/internal/models"
)

func TestTrimReview(t *testing.T) {
	tests := []struct {
		name  string
		input string
		n     int
		want  string
	}{
		{name: "short enough", input: "Great hotel", n: 50, want: "Great hotel"},
		{name: "exact length", input: "12345", n: 5, want: "12345"},
		{name: "truncates at word boundary", input: "This is a great place to stay", n: 15, want: "This is a great..."},
		{name: "single word too long", input: "Supercalifragilisticexpialidocious", n: 10, want: "Supercalif..."},
		{name: "whitespace trim", input: "  hello  ", n: 100, want: "hello"},
		{name: "empty", input: "", n: 10, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimReview(tt.input, tt.n)
			if got != tt.want {
				t.Errorf("trimReview(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
			}
		})
	}
}

func TestTrimGuideSection(t *testing.T) {
	tests := []struct {
		name  string
		input string
		n     int
		want  string
	}{
		{name: "short enough", input: "Visit the old town.", n: 100, want: "Visit the old town."},
		{name: "cuts at period", input: "Sentence one. Sentence two. Sentence three.", n: 28, want: "Sentence one. Sentence two."},
		{name: "empty", input: "", n: 10, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trimGuideSection(tt.input, tt.n)
			if got != tt.want {
				t.Errorf("trimGuideSection(%q, %d) = %q, want %q", tt.input, tt.n, got, tt.want)
			}
		})
	}
}

func TestFirstSectionByKey(t *testing.T) {
	sections := map[string]string{
		"See":   "Visit the castle",
		"Do":    "Go hiking",
		"Sleep": "",
		"Eat":   "Try local food",
	}

	tests := []struct {
		name       string
		candidates []string
		want       string
		wantOk     bool
	}{
		{name: "exact match", candidates: []string{"See"}, want: "Visit the castle", wantOk: true},
		{name: "case insensitive", candidates: []string{"see"}, want: "Visit the castle", wantOk: true},
		{name: "first candidate wins", candidates: []string{"Do", "See"}, want: "Go hiking", wantOk: true},
		{name: "empty value skipped", candidates: []string{"Sleep", "Eat"}, want: "Try local food", wantOk: true},
		{name: "not found", candidates: []string{"Buy"}, want: "", wantOk: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := firstSectionByKey(sections, tt.candidates...)
			if ok != tt.wantOk {
				t.Errorf("firstSectionByKey() ok = %v, want %v", ok, tt.wantOk)
			}
			if got != tt.want {
				t.Errorf("firstSectionByKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestJoinRoute(t *testing.T) {
	tests := []struct {
		name  string
		parts []string
		want  string
	}{
		{name: "empty", parts: nil, want: ""},
		{name: "single", parts: []string{"HEL"}, want: "HEL"},
		{name: "two", parts: []string{"HEL", "BCN"}, want: "HEL -> BCN"},
		{name: "three", parts: []string{"HEL", "AMS", "BCN"}, want: "HEL -> AMS -> BCN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := joinRoute(tt.parts)
			if got != tt.want {
				t.Errorf("joinRoute(%v) = %q, want %q", tt.parts, got, tt.want)
			}
		})
	}
}

func TestJoinAmenities(t *testing.T) {
	tests := []struct {
		name      string
		amenities []string
		want      string
	}{
		{name: "empty", amenities: nil, want: ""},
		{name: "one", amenities: []string{"wifi"}, want: "wifi"},
		{name: "three", amenities: []string{"wifi", "pool", "gym"}, want: "wifi, pool, gym"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := joinAmenities(tt.amenities)
			if got != tt.want {
				t.Errorf("joinAmenities(%v) = %q, want %q", tt.amenities, got, tt.want)
			}
		})
	}
}

func TestExtractTopHotels(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Expensive", Price: 200, Currency: "EUR", Rating: 4.5, HotelID: "h1"},
		{Name: "Budget", Price: 50, Currency: "EUR", Rating: 3.8, HotelID: "h2"},
		{Name: "Mid", Price: 100, Currency: "EUR", Rating: 4.2, HotelID: "h3", Amenities: []string{"wifi", "pool", "gym", "spa"}},
	}

	got := extractTopHotels(hotels, 3, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 hotels, got %d", len(got))
	}
	// Should be sorted by price: Budget (50), Mid (100).
	if got[0].Name != "Budget" {
		t.Errorf("first should be Budget, got %q", got[0].Name)
	}
	if got[0].Total != 150 { // 50 * 3 nights
		t.Errorf("Budget total = %v, want 150", got[0].Total)
	}
	if got[1].Name != "Mid" {
		t.Errorf("second should be Mid, got %q", got[1].Name)
	}
	// Mid has 4 amenities, should truncate to "wifi, pool, gym +1 more".
	if got[1].Amenities == "" {
		t.Error("Mid amenities should be non-empty")
	}
}

func TestExtractTopHotels_ZeroPriceFiltered(t *testing.T) {
	hotels := []models.HotelResult{
		{Name: "Free", Price: 0, Currency: "EUR"},
		{Name: "Paid", Price: 80, Currency: "EUR"},
	}
	got := extractTopHotels(hotels, 2, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 hotel (zero price filtered), got %d", len(got))
	}
	if got[0].Name != "Paid" {
		t.Errorf("expected Paid, got %q", got[0].Name)
	}
}

func TestExtractTopHotels_SkipsLeadInPricesForFinalTotals(t *testing.T) {
	hotels := []models.HotelResult{
		{
			Name:            "Cheap Lead-In",
			Price:           40,
			Currency:        "EUR",
			PriceBasis:      models.PriceBasisLeadIn,
			PriceConfidence: models.PriceConfidenceUnverified,
		},
		{
			Name:            "Matched Room",
			Price:           90,
			Currency:        "EUR",
			PriceBasis:      models.PriceBasisRoomTotal,
			PriceConfidence: models.PriceConfidenceRoomLevel,
		},
	}

	got := extractTopHotels(hotels, 2, 5)
	if len(got) != 1 {
		t.Fatalf("expected only the matched room to be eligible, got %d hotels: %#v", len(got), got)
	}
	if got[0].Name != "Matched Room" {
		t.Fatalf("selected hotel = %q, want Matched Room", got[0].Name)
	}
}

func TestExtractTopHotels_Empty(t *testing.T) {
	got := extractTopHotels(nil, 3, 5)
	if len(got) != 0 {
		t.Errorf("expected 0 hotels for nil input, got %d", len(got))
	}
}

func TestChoosePlanSummaryCurrency(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		result    *PlanResult
		want      string
	}{
		{
			name:      "requested takes priority",
			requested: "GBP",
			result:    &PlanResult{},
			want:      "GBP",
		},
		{
			name:      "fallback to outbound flight",
			requested: "",
			result: &PlanResult{
				OutboundFlights: []PlanFlight{{Currency: "SEK"}},
			},
			want: "SEK",
		},
		{
			name:      "fallback to return flight",
			requested: "",
			result: &PlanResult{
				ReturnFlights: []PlanFlight{{Currency: "NOK"}},
			},
			want: "NOK",
		},
		{
			name:      "fallback to hotel",
			requested: "",
			result: &PlanResult{
				Hotels: []PlanHotel{{Currency: "CZK"}},
			},
			want: "CZK",
		},
		{
			name:      "final fallback EUR",
			requested: "",
			result:    &PlanResult{},
			want:      "EUR",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := choosePlanSummaryCurrency(tt.requested, tt.result)
			if got != tt.want {
				t.Errorf("choosePlanSummaryCurrency(%q) = %q, want %q", tt.requested, got, tt.want)
			}
		})
	}
}

func TestExtractTopFlights(t *testing.T) {
	flights := []models.FlightResult{
		{Price: 300, Currency: "EUR", Stops: 1, Duration: 240, Legs: []models.FlightLeg{
			{Airline: "KLM", FlightNumber: "KL1234", DepartureTime: "08:00", ArrivalTime: "10:00",
				DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "AMS"}},
			{Airline: "KLM", FlightNumber: "KL5678", DepartureTime: "12:00", ArrivalTime: "14:00",
				DepartureAirport: models.AirportInfo{Code: "AMS"}, ArrivalAirport: models.AirportInfo{Code: "BCN"}},
		}},
		{Price: 100, Currency: "EUR", Stops: 0, Duration: 180, Legs: []models.FlightLeg{
			{Airline: "Vueling", FlightNumber: "VY1001", DepartureTime: "06:00", ArrivalTime: "09:00",
				DepartureAirport: models.AirportInfo{Code: "HEL"}, ArrivalAirport: models.AirportInfo{Code: "BCN"}},
		}},
	}

	got := extractTopFlights(flights, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 flights, got %d", len(got))
	}
	// Sorted by price: Vueling (100), KLM (300).
	if got[0].Price != 100 {
		t.Errorf("first flight price = %v, want 100", got[0].Price)
	}
	if got[0].Airline != "Vueling" {
		t.Errorf("first airline = %q, want Vueling", got[0].Airline)
	}
	if got[0].Route != "HEL -> BCN" {
		t.Errorf("first route = %q, want HEL -> BCN", got[0].Route)
	}
	if got[1].Route != "HEL -> AMS -> BCN" {
		t.Errorf("second route = %q, want HEL -> AMS -> BCN", got[1].Route)
	}
}

func TestExtractTopFlights_ZeroPriceFiltered(t *testing.T) {
	flights := []models.FlightResult{
		{Price: 0, Currency: "EUR"},
		{Price: 150, Currency: "EUR"},
	}
	got := extractTopFlights(flights, 5)
	if len(got) != 1 {
		t.Fatalf("expected 1 flight, got %d", len(got))
	}
}

func TestBuildDiscoverReasoning(t *testing.T) {
	tests := []struct {
		name     string
		rating   float64
		slack    float64
		currency string
		wantSub  string
	}{
		{name: "good hotel under budget", rating: 4.5, slack: 100, currency: "EUR", wantSub: "4.5"},
		{name: "no rating", rating: 0, slack: 50, currency: "EUR", wantSub: "EUR 50 under budget"},
		{name: "no slack", rating: 4.0, slack: 0, currency: "EUR", wantSub: "4.0"},
		{name: "both zero", rating: 0, slack: 0, currency: "EUR", wantSub: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildDiscoverReasoning(tt.rating, tt.slack, tt.currency)
			if tt.wantSub != "" && !contains(got, tt.wantSub) {
				t.Errorf("buildDiscoverReasoning() = %q, should contain %q", got, tt.wantSub)
			}
			if tt.wantSub == "" && got != "" {
				t.Errorf("buildDiscoverReasoning() = %q, want empty", got)
			}
		})
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && (s == sub || len(s) > 0 && containsHelper(s, sub))
}

func containsHelper(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestDiscoverOptions_ApplyDefaults(t *testing.T) {
	tests := []struct {
		name string
		in   DiscoverOptions
		min  int
		max  int
		top  int
	}{
		{name: "all zeros", in: DiscoverOptions{}, min: 2, max: 4, top: 5},
		{name: "custom values", in: DiscoverOptions{MinNights: 3, MaxNights: 7, Top: 10}, min: 3, max: 7, top: 10},
		{name: "max less than min", in: DiscoverOptions{MinNights: 5, MaxNights: 2}, min: 5, max: 5, top: 5},
		{name: "negative values", in: DiscoverOptions{MinNights: -1, MaxNights: -1, Top: -1}, min: 2, max: 4, top: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.in
			opts.applyDefaults()
			if opts.MinNights != tt.min {
				t.Errorf("MinNights = %d, want %d", opts.MinNights, tt.min)
			}
			if opts.MaxNights != tt.max {
				t.Errorf("MaxNights = %d, want %d", opts.MaxNights, tt.max)
			}
			if opts.Top != tt.top {
				t.Errorf("Top = %d, want %d", opts.Top, tt.top)
			}
		})
	}
}
