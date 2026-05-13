package afklm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestAvailableOffersHappyPath(t *testing.T) {
	fixture, err := os.ReadFile("testdata/available_offers_ams_prg.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(200)
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	client := newTestClient(t, srv, func() time.Time { return now })

	req := AvailableOffersRequest{
		BookingFlow: "LEISURE",
		Passengers:  []Passenger{{ID: 1, Type: "ADT"}},
		RequestedConnections: []RequestedConnection{{
			DepartureDate: "2026-05-15",
			Origin:        Place{Type: "AIRPORT", Code: "AMS"},
			Destination:   Place{Type: "AIRPORT", Code: "PRG"},
		}},
		Currency: "EUR",
	}

	resp, stale, err := client.AvailableOffers(context.Background(), req)
	if err != nil {
		t.Fatalf("AvailableOffers: %v", err)
	}
	if stale {
		t.Error("expected non-stale first call")
	}
	if len(resp.Recommendations) != 2 {
		t.Errorf("expected 2 recommendations, got %d", len(resp.Recommendations))
	}
	// Verify parsed price from the real RJF fixture.
	price := resp.Recommendations[0].FlightProducts[0].Connections[0].Price.DisplayPrice
	if price != 453.98 {
		t.Errorf("expected price 453.98, got %v", price)
	}
}

func TestAvailableOffersServedFromCache(t *testing.T) {
	fixture, _ := os.ReadFile("testdata/available_offers_ams_prg.json")
	var serverCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalls++
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(200)
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	client := newTestClient(t, srv, func() time.Time { return now })

	req := AvailableOffersRequest{
		BookingFlow: "LEISURE",
		Passengers:  []Passenger{{ID: 1, Type: "ADT"}},
		RequestedConnections: []RequestedConnection{{
			DepartureDate: "2026-05-15",
			Origin:        Place{Type: "AIRPORT", Code: "AMS"},
			Destination:   Place{Type: "AIRPORT", Code: "PRG"},
		}},
	}

	if _, _, err := client.AvailableOffers(context.Background(), req); err != nil {
		t.Fatalf("AvailableOffers first call: %v", err)
	}
	if _, _, err := client.AvailableOffers(context.Background(), req); err != nil {
		t.Fatalf("AvailableOffers cached call: %v", err)
	}

	if serverCalls != 1 {
		t.Errorf("expected 1 server call (2nd should be cached), got %d", serverCalls)
	}
}

// TestMapRecommendationsFixture verifies that mapRecommendations correctly
// joins top-level BoundConnections to pricing connections using the real fixture.
func TestMapRecommendationsFixture(t *testing.T) {
	fixture, err := os.ReadFile("testdata/available_offers_ams_prg.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/hal+json")
		w.WriteHeader(200)
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	now := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	client := newTestClient(t, srv, func() time.Time { return now })

	req := AvailableOffersRequest{
		BookingFlow: "LEISURE",
		Passengers:  []Passenger{{ID: 1, Type: "ADT"}},
		RequestedConnections: []RequestedConnection{{
			DepartureDate: "2026-05-15",
			Origin:        Place{Type: "AIRPORT", Code: "AMS"},
			Destination:   Place{Type: "AIRPORT", Code: "PRG"},
		}},
		Currency: "EUR",
	}

	resp, _, err := client.AvailableOffers(context.Background(), req)
	if err != nil {
		t.Fatalf("AvailableOffers: %v", err)
	}

	results := mapRecommendations(resp, "AMS", "PRG")
	if len(results) == 0 {
		t.Fatal("expected at least one FlightResult from fixture")
	}
	first := results[0]
	if first.Duration == 0 {
		t.Error("expected non-zero Duration")
	}
	if len(first.Legs) == 0 {
		t.Fatal("expected at least one leg")
	}
	leg := first.Legs[0]
	if leg.DepartureAirport.Code != "AMS" {
		t.Errorf("expected DepartureAirport.Code=AMS, got %q", leg.DepartureAirport.Code)
	}
	if leg.ArrivalAirport.Code != "PRG" {
		t.Errorf("expected ArrivalAirport.Code=PRG, got %q", leg.ArrivalAirport.Code)
	}
	if leg.AirlineCode != "KL" {
		t.Errorf("expected AirlineCode=KL, got %q", leg.AirlineCode)
	}
	if leg.FlightNumber != "KL1351" {
		t.Errorf("expected FlightNumber=KL1351, got %q", leg.FlightNumber)
	}
	if leg.DepartureTime != "2026-04-24T06:55:00" {
		t.Errorf("expected DepartureTime=2026-04-24T06:55:00, got %q", leg.DepartureTime)
	}
}

// TestMapRecommendationsSynthetic verifies the join logic with a hand-crafted
// two-bound response (outbound + return) without touching the network.
func TestMapRecommendationsSynthetic(t *testing.T) {
	resp := &AvailableOffersResponse{
		Recommendations: []Recommendation{
			{
				FlightProducts: []FlightProduct{
					{
						Price: Price{DisplayPrice: 200, Currency: "EUR"},
						Connections: []PricingConnection{
							{ConnectionID: 0, Price: Price{DisplayPrice: 100, Currency: "EUR"}},
							{ConnectionID: 0, Price: Price{DisplayPrice: 100, Currency: "EUR"}},
						},
					},
				},
			},
		},
		Connections: [][]BoundConnection{
			// Bound 0 (outbound)
			{
				{
					ID:       0,
					Duration: 90,
					Segments: []Segment{
						{
							Origin:            SegmentPlace{Code: "AMS"},
							Destination:       SegmentPlace{Code: "PRG"},
							MarketingFlight:   MarketingFlight{Number: "1351", Carrier: MarketingCarrier{Code: "KL", Name: "KLM"}},
							DepartureDateTime: "2026-06-15T06:55:00",
							ArrivalDateTime:   "2026-06-15T08:25:00",
							Duration:          90,
						},
					},
				},
			},
			// Bound 1 (return)
			{
				{
					ID:       0,
					Duration: 95,
					Segments: []Segment{
						{
							Origin:            SegmentPlace{Code: "PRG"},
							Destination:       SegmentPlace{Code: "AMS"},
							MarketingFlight:   MarketingFlight{Number: "1352", Carrier: MarketingCarrier{Code: "KL", Name: "KLM"}},
							DepartureDateTime: "2026-06-22T10:00:00",
							ArrivalDateTime:   "2026-06-22T11:35:00",
							Duration:          95,
						},
					},
				},
			},
		},
	}

	results := mapRecommendations(resp, "AMS", "PRG")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if len(r.Legs) != 2 {
		t.Fatalf("expected 2 legs (outbound + return), got %d", len(r.Legs))
	}
	if r.Legs[0].DepartureAirport.Code != "AMS" {
		t.Errorf("leg 0 departure: want AMS, got %q", r.Legs[0].DepartureAirport.Code)
	}
	if r.Legs[1].DepartureAirport.Code != "PRG" {
		t.Errorf("leg 1 departure: want PRG, got %q", r.Legs[1].DepartureAirport.Code)
	}
	if r.Duration != 185 {
		t.Errorf("expected total duration 185, got %d", r.Duration)
	}
}

// TestMapRecommendationsLayover verifies layover computation between segments
// within the same bound.
func TestMapRecommendationsLayover(t *testing.T) {
	resp := &AvailableOffersResponse{
		Recommendations: []Recommendation{
			{
				FlightProducts: []FlightProduct{
					{
						Price: Price{DisplayPrice: 300, Currency: "EUR"},
						Connections: []PricingConnection{
							{ConnectionID: 0, Price: Price{DisplayPrice: 300, Currency: "EUR"}},
						},
					},
				},
			},
		},
		Connections: [][]BoundConnection{
			{
				{
					ID:       0,
					Duration: 310,
					Segments: []Segment{
						{
							Origin:            SegmentPlace{Code: "AMS"},
							Destination:       SegmentPlace{Code: "CDG"},
							MarketingFlight:   MarketingFlight{Number: "9001", Carrier: MarketingCarrier{Code: "KL"}},
							DepartureDateTime: "2026-04-24T08:00:00",
							ArrivalDateTime:   "2026-04-24T10:00:00",
							Duration:          120,
						},
						{
							Origin:            SegmentPlace{Code: "CDG"},
							Destination:       SegmentPlace{Code: "PRG"},
							MarketingFlight:   MarketingFlight{Number: "9002", Carrier: MarketingCarrier{Code: "AF"}},
							DepartureDateTime: "2026-04-24T12:30:00",
							ArrivalDateTime:   "2026-04-24T14:10:00",
							Duration:          100,
						},
					},
				},
			},
		},
	}

	results := mapRecommendations(resp, "AMS", "PRG")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].Legs) != 2 {
		t.Fatalf("expected 2 legs, got %d", len(results[0].Legs))
	}
	// Leg 0 arrives 10:00, leg 1 departs 12:30 → 150 min layover.
	layover := results[0].Legs[1].LayoverMinutes
	if layover != 150 {
		t.Errorf("expected LayoverMinutes=150, got %d", layover)
	}
}

func TestDaysUntilDepartureTTLTable(t *testing.T) {
	now := time.Date(2026, 4, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		daysStr string
		days    int
		wantTTL time.Duration
	}{
		{"2026-06-05", 45, 72 * time.Hour}, // 45 days
		{"2026-05-11", 20, 24 * time.Hour}, // 20 days
		{"2026-05-01", 10, 12 * time.Hour}, // 10 days
		{"2026-04-26", 5, 6 * time.Hour},   // 5 days
		{"2026-04-22", 1, 2 * time.Hour},   // 1 day
	}

	for _, tc := range cases {
		days := daysFromISO(tc.daysStr, now)
		if days != tc.days {
			t.Errorf("daysFromISO(%s): got %d, want %d", tc.daysStr, days, tc.days)
		}
		ttl := DepArrTTL(days)
		if ttl != tc.wantTTL {
			t.Errorf("TTL for %s (%d days): got %v, want %v", tc.daysStr, days, ttl, tc.wantTTL)
		}
	}
}
