package profile

import (
	"testing"
)

func TestParseFlightConfirmation(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		body    string
		want    *Booking
	}{
		{
			name:    "KLM confirmation",
			subject: "Booking confirmation - KLM",
			body:    "Your flight has been confirmed.\nFlight: HEL → AMS\nDeparture: 15 Jun 2026\nTotal: EUR 189.00\nBooking reference: ABC123",
			want: &Booking{
				Type: "flight", Provider: "KLM", From: "HEL", To: "AMS",
				Price: 189, Currency: "EUR", TravelDate: "2026-06-15",
				Reference: "ABC123", Source: "email",
			},
		},
		{
			name:    "Finnair e-ticket",
			subject: "E-ticket receipt - Finnair",
			body:    "E-ticket receipt for your Finnair flight\nRoute: HEL - BCN\nDate: 22 July 2026\nFare: EUR 234.00\nConfirmation: XYZ789",
			want: &Booking{
				Type: "flight", Provider: "Finnair", From: "HEL", To: "BCN",
				Price: 234, Currency: "EUR", TravelDate: "2026-07-22",
				Reference: "XYZ789", Source: "email",
			},
		},
		{
			name:    "Ryanair booking",
			subject: "Your Ryanair booking is confirmed",
			body:    "Flight booking confirmed\nFrom: STN to BCN\nDeparting: 1 Mar 2026\nTotal: EUR 45.99\nBooking No: RYNR42",
			want: &Booking{
				Type: "flight", Provider: "Ryanair", From: "STN", To: "BCN",
				Price: 45.99, Currency: "EUR", TravelDate: "2026-03-01",
				Reference: "RYNR42", Source: "email",
			},
		},
		{
			name:    "easyJet itinerary",
			subject: "Your easyJet itinerary",
			body:    "Your flight details:\nLGW -> CDG\nDate: 2026-05-10\nPrice: GBP 67.50",
			want: &Booking{
				Type: "flight", Provider: "easyJet", From: "LGW", To: "CDG",
				Price: 67.50, Currency: "GBP", TravelDate: "2026-05-10",
				Source: "email",
			},
		},
		{
			name:    "not a flight email",
			subject: "Your newsletter subscription",
			body:    "Thank you for subscribing to our travel newsletter.",
			want:    nil,
		},
		{
			name:    "hotel only email excluded",
			subject: "Reservation confirmed",
			body:    "Your stay at Hilton is confirmed.\nCheck-in: 15 Jun 2026\nCheck-out: 18 Jun 2026\nRoom rate: EUR 120.00",
			want:    nil,
		},
		{
			name:    "SAS flight with ISO date",
			subject: "SAS Scandinavian Airlines - Booking confirmation",
			body:    "Flight HEL to ARN\nDeparture: 2026-09-15\nTotal fare: SEK 1250.00\nPNR: SAS567",
			want: &Booking{
				Type: "flight", Provider: "SAS", From: "HEL", To: "ARN",
				Price: 1250, Currency: "SEK", TravelDate: "2026-09-15",
				Reference: "SAS567", Source: "email",
			},
		},
		{
			name:    "Norwegian flight",
			subject: "Flight confirmation from Norwegian",
			body:    "Your Norwegian flight is confirmed!\nOSL -> BCN\nDeparture: 3 August 2026\nTotal: NOK 899.00",
			want: &Booking{
				Type: "flight", Provider: "Norwegian", From: "OSL", To: "BCN",
				Price: 899, Currency: "NOK", TravelDate: "2026-08-03",
				Source: "email",
			},
		},
		{
			name:    "price with euro symbol",
			subject: "Your flight booking",
			body:    "Flight confirmed\nHEL - LHR\nDeparture: 20 Apr 2026\n€ 145.00",
			want: &Booking{
				Type: "flight", From: "HEL", To: "LHR",
				Price: 145, Currency: "EUR", TravelDate: "2026-04-20",
				Source: "email",
			},
		},
		{
			name:    "DD/MM/YYYY date format",
			subject: "Booking confirmation - Vueling",
			body:    "Vueling flight confirmed\nFlight: BCN to AMS\nDate: 25/12/2026\nTotal: EUR 99.00",
			want: &Booking{
				Type: "flight", Provider: "Vueling", From: "BCN", To: "AMS",
				Price: 99, Currency: "EUR", TravelDate: "2026-12-25",
				Source: "email",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseFlightConfirmation(tt.subject, tt.body)

			if tt.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil booking, got nil")
			}

			if got.Type != tt.want.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.want.Type)
			}
			if got.Provider != tt.want.Provider && tt.want.Provider != "" {
				t.Errorf("Provider = %q, want %q", got.Provider, tt.want.Provider)
			}
			if got.From != tt.want.From && tt.want.From != "" {
				t.Errorf("From = %q, want %q", got.From, tt.want.From)
			}
			if got.To != tt.want.To && tt.want.To != "" {
				t.Errorf("To = %q, want %q", got.To, tt.want.To)
			}
			if tt.want.Price > 0 && got.Price != tt.want.Price {
				t.Errorf("Price = %v, want %v", got.Price, tt.want.Price)
			}
			if tt.want.Currency != "" && got.Currency != tt.want.Currency {
				t.Errorf("Currency = %q, want %q", got.Currency, tt.want.Currency)
			}
			if tt.want.TravelDate != "" && got.TravelDate != tt.want.TravelDate {
				t.Errorf("TravelDate = %q, want %q", got.TravelDate, tt.want.TravelDate)
			}
			if tt.want.Reference != "" && got.Reference != tt.want.Reference {
				t.Errorf("Reference = %q, want %q", got.Reference, tt.want.Reference)
			}
			if got.Source != "email" {
				t.Errorf("Source = %q, want %q", got.Source, "email")
			}
		})
	}
}

func TestParseHotelConfirmation(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		body    string
		want    *Booking
	}{
		{
			name:    "Booking.com reservation",
			subject: "Booking confirmation from Booking.com",
			body:    "Your reservation is confirmed.\nHotel: Grand Plaza\nCheck-in: 15 Jun 2026\nCheck-out: 18 Jun 2026\nTotal: EUR 360.00\n4 star hotel\nConfirmation: BK12345",
			want: &Booking{
				Type: "hotel", Provider: "Booking.com",
				Price: 360, Currency: "EUR", TravelDate: "2026-06-15",
				Nights: 3, Stars: 4, Reference: "BK12345",
				Source: "booking.com",
			},
		},
		{
			name:    "Airbnb confirmation",
			subject: "Reservation confirmed - Airbnb",
			body:    "You're going to Barcelona!\nYour Airbnb reservation is confirmed.\nCheck-in: 20 Jul 2026\nCheck-out: 25 Jul 2026\nTotal: EUR 450.00\nConfirmation: HMABCDE",
			want: &Booking{
				Type: "airbnb", Provider: "Airbnb", To: "Barcelona",
				Price: 450, Currency: "EUR", TravelDate: "2026-07-20",
				Nights: 5, Reference: "HMABCDE",
			},
		},
		{
			name:    "Marriott reservation",
			subject: "Your reservation at Marriott Prague",
			body:    "Marriott Hotels confirms your reservation.\nHotel: Marriott Prague\nCheck-in: 2026-03-10\nCheck-out: 2026-03-13\nRoom rate: EUR 150.00\n5 star property",
			want: &Booking{
				Type: "hotel", Provider: "Marriott",
				Price: 150, Currency: "EUR", TravelDate: "2026-03-10",
				Nights: 3, Stars: 5,
			},
		},
		{
			name:    "Hilton with ISO dates",
			subject: "Hilton - Booking Confirmation",
			body:    "Your Hilton reservation is confirmed.\nCheck-in: 2026-05-01\nCheck-out: 2026-05-04\nTotal: USD 525.00\n4 star\nReservation number: HLT789",
			want: &Booking{
				Type: "hotel", Provider: "Hilton",
				Price: 525, Currency: "USD", TravelDate: "2026-05-01",
				Nights: 3, Stars: 4, Reference: "HLT789",
			},
		},
		{
			name:    "not a hotel email",
			subject: "Welcome to our loyalty programme",
			body:    "Thank you for joining. Earn points on every stay.",
			want:    nil,
		},
		{
			name:    "hotel with DD/MM/YYYY",
			subject: "Hotel booking confirmed",
			body:    "Your stay is confirmed.\nHotel: Best Western Central\nCheck-in: 01/06/2026\nCheck-out: 04/06/2026\nTotal: GBP 240.00",
			want: &Booking{
				Type: "hotel", Provider: "Best Western",
				Price: 240, Currency: "GBP", TravelDate: "2026-06-01",
				Nights: 3,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseHotelConfirmation(tt.subject, tt.body)

			if tt.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil booking, got nil")
			}

			if got.Type != tt.want.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.want.Type)
			}
			if tt.want.Provider != "" && got.Provider != tt.want.Provider {
				t.Errorf("Provider = %q, want %q", got.Provider, tt.want.Provider)
			}
			if tt.want.To != "" && got.To != tt.want.To {
				t.Errorf("To = %q, want %q", got.To, tt.want.To)
			}
			if tt.want.Price > 0 && got.Price != tt.want.Price {
				t.Errorf("Price = %v, want %v", got.Price, tt.want.Price)
			}
			if tt.want.Currency != "" && got.Currency != tt.want.Currency {
				t.Errorf("Currency = %q, want %q", got.Currency, tt.want.Currency)
			}
			if tt.want.TravelDate != "" && got.TravelDate != tt.want.TravelDate {
				t.Errorf("TravelDate = %q, want %q", got.TravelDate, tt.want.TravelDate)
			}
			if tt.want.Nights > 0 && got.Nights != tt.want.Nights {
				t.Errorf("Nights = %d, want %d", got.Nights, tt.want.Nights)
			}
			if tt.want.Stars > 0 && got.Stars != tt.want.Stars {
				t.Errorf("Stars = %d, want %d", got.Stars, tt.want.Stars)
			}
			if tt.want.Reference != "" && got.Reference != tt.want.Reference {
				t.Errorf("Reference = %q, want %q", got.Reference, tt.want.Reference)
			}
		})
	}
}

func TestParseGroundConfirmation(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		body    string
		want    *Booking
	}{
		{
			name:    "FlixBus ticket",
			subject: "Your FlixBus ticket confirmation",
			body:    "FlixBus booking confirmed!\nFrom: Prague to Vienna\nDeparture: 10 May 2026\nTotal: EUR 19.00\nBooking No: FLX123",
			want: &Booking{
				Type: "ground", Provider: "FlixBus",
				Price: 19, Currency: "EUR", TravelDate: "2026-05-10",
				Reference: "FLX123",
			},
		},
		{
			name:    "Eurostar booking",
			subject: "Eurostar booking confirmation",
			body:    "Your Eurostar journey\nLondon to Paris\nDate: 2026-06-20\nFare: GBP 89.00",
			want: &Booking{
				Type: "ground", Provider: "Eurostar",
				Price: 89, Currency: "GBP", TravelDate: "2026-06-20",
			},
		},
		{
			name:    "not a ground transport email",
			subject: "Your subscription renewal",
			body:    "Your annual subscription has been renewed.",
			want:    nil,
		},
		{
			name:    "train ticket generic",
			subject: "Your train ticket is confirmed",
			body:    "Train ticket confirmed\nRegioJet booking\nFrom: Prague to Brno\nDeparture: 5 Apr 2026\nTotal: CZK 199.00",
			want: &Booking{
				Type: "ground", Provider: "Regiojet",
				Price: 199, Currency: "CZK", TravelDate: "2026-04-05",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseGroundConfirmation(tt.subject, tt.body)

			if tt.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected non-nil booking, got nil")
			}

			if got.Type != tt.want.Type {
				t.Errorf("Type = %q, want %q", got.Type, tt.want.Type)
			}
			if tt.want.Provider != "" && !caseInsensitiveContains(got.Provider, tt.want.Provider) {
				t.Errorf("Provider = %q, want to contain %q", got.Provider, tt.want.Provider)
			}
			if tt.want.Price > 0 && got.Price != tt.want.Price {
				t.Errorf("Price = %v, want %v", got.Price, tt.want.Price)
			}
			if tt.want.Currency != "" && got.Currency != tt.want.Currency {
				t.Errorf("Currency = %q, want %q", got.Currency, tt.want.Currency)
			}
			if tt.want.TravelDate != "" && got.TravelDate != tt.want.TravelDate {
				t.Errorf("TravelDate = %q, want %q", got.TravelDate, tt.want.TravelDate)
			}
		})
	}
}

// --- Price extraction edge cases ---

func TestExtractPrice(t *testing.T) {
	tests := []struct {
		body     string
		wantAmt  float64
		wantCurr string
	}{
		{"Total: EUR 234.00", 234, "EUR"},
		{"Amount: 99.50 EUR", 99.50, "EUR"},
		{"Price: USD 150", 150, "USD"},
		{"Fare: GBP 67.50", 67.50, "GBP"},
		{"Total: CZK 1999", 1999, "CZK"},
		{"€ 145.00", 145, "EUR"},
		{"$99.99", 99.99, "USD"},
		{"£200", 200, "GBP"},
		{"Total fare: SEK 1250.00", 1250, "SEK"},
		{"Cost: NOK 899", 899, "NOK"},
	}

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			amt, curr := extractPrice(tt.body)
			if amt != tt.wantAmt {
				t.Errorf("amount = %v, want %v", amt, tt.wantAmt)
			}
			if curr != tt.wantCurr {
				t.Errorf("currency = %q, want %q", curr, tt.wantCurr)
			}
		})
	}
}

func TestExtractRoute(t *testing.T) {
	tests := []struct {
		body     string
		wantFrom string
		wantTo   string
	}{
		{"HEL → BCN", "HEL", "BCN"},
		{"Flight: HEL -> AMS", "HEL", "AMS"},
		{"HEL - LHR", "HEL", "LHR"},
		{"Route: STN to BCN", "STN", "BCN"},
		{"From: LGW to CDG", "LGW", "CDG"},
	}

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			from, to := extractRoute(tt.body)
			if from != tt.wantFrom {
				t.Errorf("from = %q, want %q", from, tt.wantFrom)
			}
			if to != tt.wantTo {
				t.Errorf("to = %q, want %q", to, tt.wantTo)
			}
		})
	}
}

func TestExtractDate(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{"Departure: 15 Jun 2026", "2026-06-15"},
		{"Date: 22 July 2026", "2026-07-22"},
		{"Departing: 1 Mar 2026", "2026-03-01"},
		{"Date: 2026-05-10", "2026-05-10"},
		{"Date: 25/12/2026", "2026-12-25"},
		{"Departure: 3 August 2026", "2026-08-03"},
		{"Date: 5 September 2026", "2026-09-05"},
	}

	patterns := flightDatePatterns

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			got := extractDate(tt.body, patterns)
			if got != tt.want {
				t.Errorf("extractDate(%q) = %q, want %q", tt.body, got, tt.want)
			}
		})
	}
}

func TestExtractReference(t *testing.T) {
	tests := []struct {
		body string
		want string
	}{
		{"Booking reference: ABC123", "ABC123"},
		{"Confirmation: XYZ789", "XYZ789"},
		{"Booking No: RYNR42", "RYNR42"},
		{"PNR: SAS567", "SAS567"},
		{"Reservation number: HLT789", "HLT789"},
		{"No reference here", ""},
	}

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			got := extractReference(tt.body)
			if got != tt.want {
				t.Errorf("extractReference = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractStars(t *testing.T) {
	tests := []struct {
		body string
		want int
	}{
		{"4 star hotel", 4},
		{"5★ property", 5},
		{"3 star", 3},
		{"no star info", 0},
	}

	for _, tt := range tests {
		t.Run(tt.body, func(t *testing.T) {
			got := extractStars(tt.body)
			if got != tt.want {
				t.Errorf("extractStars = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCalculateNights(t *testing.T) {
	tests := []struct {
		checkin  string
		checkout string
		want     int
	}{
		{"2026-06-15", "2026-06-18", 3},
		{"2026-03-10", "2026-03-13", 3},
		{"2026-05-01", "2026-05-04", 3},
		{"", "2026-06-18", 0},
		{"2026-06-15", "", 0},
		{"invalid", "2026-06-18", 0},
	}

	for _, tt := range tests {
		t.Run(tt.checkin+"_"+tt.checkout, func(t *testing.T) {
			got := calculateNights(tt.checkin, tt.checkout)
			if got != tt.want {
				t.Errorf("calculateNights(%q, %q) = %d, want %d", tt.checkin, tt.checkout, got, tt.want)
			}
		})
	}
}

func TestBookingExtractionPrompt(t *testing.T) {
	prompt := BookingExtractionPrompt("KLM Booking", "Flight HEL to AMS...")
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
	if !caseInsensitiveContains(prompt, "KLM Booking") {
		t.Error("prompt should contain subject")
	}
	if !caseInsensitiveContains(prompt, "Flight HEL to AMS") {
		t.Error("prompt should contain body")
	}
}

func TestProfileAnalysisPrompt(t *testing.T) {
	bookings := []Booking{
		{Type: "flight", Provider: "KLM", From: "HEL", To: "AMS", Price: 189},
	}
	prompt := ProfileAnalysisPrompt(bookings)
	if prompt == "" {
		t.Error("prompt should not be empty")
	}
	if !caseInsensitiveContains(prompt, "KLM") {
		t.Error("prompt should contain booking data")
	}
}

func TestTruncateStr(t *testing.T) {
	if got := truncateStr("hello", 10); got != "hello" {
		t.Errorf("truncateStr short = %q", got)
	}
	if got := truncateStr("hello world", 5); got != "hello" {
		t.Errorf("truncateStr long = %q", got)
	}
}

func TestContainsAny(t *testing.T) {
	if !containsAny("flight booking confirmed", []string{"confirmed", "other"}) {
		t.Error("should find 'confirmed'")
	}
	if containsAny("hello world", []string{"foo", "bar"}) {
		t.Error("should not find foo/bar")
	}
}

func TestIsNumeric(t *testing.T) {
	if !isNumeric("123.45") {
		t.Error("123.45 is numeric")
	}
	if !isNumeric("1,234") {
		t.Error("1,234 is numeric")
	}
	if isNumeric("EUR") {
		t.Error("EUR is not numeric")
	}
	if isNumeric("") {
		t.Error("empty is not numeric")
	}
}

func TestParseNumber(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"234.00", 234},
		{"234,00", 234},
		{"1,234.56", 1234.56},
		{"1.234,56", 1234.56},
		{"99", 99},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseNumber(tt.input)
			if got != tt.want {
				t.Errorf("parseNumber(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// caseInsensitiveContains is a test helper.
func caseInsensitiveContains(s, substr string) bool {
	return len(s) >= len(substr) && containsLower(s, substr)
}

func containsLower(s, sub string) bool {
	ls := toLower(s)
	lsub := toLower(sub)
	for i := 0; i <= len(ls)-len(lsub); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return true
		}
	}
	return false
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range len(s) {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}
