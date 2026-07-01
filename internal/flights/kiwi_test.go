package flights

import "testing"

func TestParseKiwiRPCResponse_SSE(t *testing.T) {
	body := []byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"content\":[{\"type\":\"text\",\"text\":\"[]\"}]}}\n\n")

	rpcResp, err := parseKiwiRPCResponse(body)
	if err != nil {
		t.Fatalf("parseKiwiRPCResponse: %v", err)
	}

	content, err := extractKiwiContent(rpcResp)
	if err != nil {
		t.Fatalf("extractKiwiContent: %v", err)
	}
	if string(content) != "[]" {
		t.Fatalf("content = %s, want []", content)
	}
}

func TestMapKiwiItineraryMarksSelfConnect(t *testing.T) {
	itinerary := kiwiItinerary{
		Price:                121,
		TotalDurationSeconds: 21600,
		BookingURL:           "https://on.kiwi.com/test",
		Outbound: &kiwiLeg{
			From:          "HEL",
			To:            "DBV",
			DepartureTime: "2026-07-01T15:00:00",
			ArrivalTime:   "2026-07-01T20:00:00",
			Stops:         1,
			Segments: []kiwiSegment{
				{
					From:            "HEL",
					To:              "ARN",
					FromCity:        "Helsinki",
					ToCity:          "Stockholm",
					DepartureTime:   "2026-07-01T15:00:00",
					ArrivalTime:     "2026-07-01T16:00:00",
					DurationSeconds: 3600,
				},
				{
					From:            "ARN",
					To:              "DBV",
					FromCity:        "Stockholm",
					ToCity:          "Dubrovnik",
					DepartureTime:   "2026-07-01T16:00:00",
					ArrivalTime:     "2026-07-01T20:00:00",
					DurationSeconds: 14400,
				},
			},
		},
	}

	flight := mapKiwiItinerary(itinerary, "EUR")

	if flight.Provider != "kiwi" {
		t.Fatalf("Provider = %q, want kiwi", flight.Provider)
	}
	if !flight.SelfConnect {
		t.Fatal("expected SelfConnect=true")
	}
	if len(flight.Warnings) == 0 {
		t.Fatal("expected a self-connect warning")
	}
	if flight.Stops != 1 {
		t.Fatalf("Stops = %d, want 1", flight.Stops)
	}
	if len(flight.Legs) != 2 {
		t.Fatalf("Legs = %d, want 2", len(flight.Legs))
	}
	if flight.Legs[0].DepartureAirport.Code != "HEL" || flight.Legs[1].ArrivalAirport.Code != "DBV" {
		t.Fatalf("unexpected route: %#v", flight.Legs)
	}
	if flight.Legs[0].Duration != 60 {
		t.Fatalf("first leg duration = %d, want 60", flight.Legs[0].Duration)
	}
	if flight.Legs[1].Duration != 240 {
		t.Fatalf("second leg duration = %d, want 240", flight.Legs[1].Duration)
	}
}

func TestKiwiEligibleOptions(t *testing.T) {
	tests := []struct {
		name string
		opts SearchOptions
		want bool
	}{
		{"basic one-way", SearchOptions{}, true},
		{"round trip", SearchOptions{ReturnDate: "2026-07-08"}, false},
		{"airline filter", SearchOptions{Airlines: []string{"AY"}}, false},
		{"alliance filter", SearchOptions{Alliances: []string{"ONEWORLD"}}, false},
		{"carry on", SearchOptions{CarryOnBags: 1}, false},
		{"checked bags", SearchOptions{CheckedBags: 1}, false},
		{"require checked bag", SearchOptions{RequireCheckedBag: true}, false},
		{"exclude basic", SearchOptions{ExcludeBasic: true}, false},
		{"less emissions", SearchOptions{LessEmissions: true}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := kiwiEligibleOptions(tt.opts); got != tt.want {
				t.Fatalf("kiwiEligibleOptions(%+v) = %v, want %v", tt.opts, got, tt.want)
			}
		})
	}
}
