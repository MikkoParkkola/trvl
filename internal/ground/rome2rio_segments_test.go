package ground

import "testing"

// Fixtures are verbatim anchor texts captured live from Rome2Rio SSR
// (Helsinki→Tallinn and Helsinki→London), so the parser is tested against the
// real grammar, not an invented one.
func TestParseRome2RioSegments(t *testing.T) {
	t.Run("single bus leg keeps city + full station", func(t *testing.T) {
		legs := parseRome2RioSegments("Take the bus from Helsinki, Harbour Terminal 2 to Tallinn, Harbour Terminal D bus bus 1203")
		if len(legs) != 1 {
			t.Fatalf("got %d legs, want 1: %+v", len(legs), legs)
		}
		l := legs[0]
		if l.Type != "bus" || l.Departure.City != "Helsinki" || l.Arrival.City != "Tallinn" {
			t.Errorf("leg = %+v", l)
		}
		if l.Departure.Station != "Helsinki, Harbour Terminal 2" || l.Arrival.Station != "Tallinn, Harbour Terminal D" {
			t.Errorf("stations not preserved: %+v", l)
		}
	})

	t.Run("ferry+fly+train resolves modes, IATA, and stops in order", func(t *testing.T) {
		text := "Ferry to Lennart Meri International Airport, fly to Luton Airport, train " +
			"Take the ferry from Helsinki to Tallinn ferry ferry " +
			"Fly from Lennart Meri International Airport (TLL) to Luton Airport (LTN) plane plane TLL - LTN " +
			"Take the train from Luton Airport Parkway to London St Pancras Intl train train"
		legs := parseRome2RioSegments(text)
		if len(legs) != 3 {
			t.Fatalf("got %d legs, want 3: %+v", len(legs), legs)
		}
		want := []struct{ mode, from, to string }{
			{"ferry", "Helsinki", "Tallinn"},
			{"fly", "TLL", "LTN"},
			{"train", "Luton Airport Parkway", "London St Pancras Intl"},
		}
		for i, w := range want {
			if legs[i].Type != w.mode || legs[i].Departure.City != w.from || legs[i].Arrival.City != w.to {
				t.Errorf("leg %d = {%s %s→%s}, want {%s %s→%s}",
					i, legs[i].Type, legs[i].Departure.City, legs[i].Arrival.City, w.mode, w.from, w.to)
			}
		}
	})

	t.Run("drive + car ferry chain normalizes car ferry to ferry", func(t *testing.T) {
		text := "Drive, car ferry Drive from Helsinki to Gothenburg car car " +
			"Take the car ferry from Gothenburg to Port of Frederikshavn carferry car ferry " +
			"Drive from Port Of Frederikshavn to Calais car car " +
			"Take the car ferry from Calais to Port of Dover carferry car ferry " +
			"Drive from Port of Dover to London car car"
		legs := parseRome2RioSegments(text)
		if len(legs) != 5 {
			t.Fatalf("got %d legs, want 5: %+v", len(legs), legs)
		}
		wantModes := []string{"drive", "ferry", "drive", "ferry", "drive"}
		for i, m := range wantModes {
			if legs[i].Type != m {
				t.Errorf("leg %d mode = %s, want %s", i, legs[i].Type, m)
			}
		}
		if legs[1].Departure.City != "Gothenburg" || legs[1].Arrival.City != "Port of Frederikshavn" {
			t.Errorf("car-ferry leg endpoints = %s→%s", legs[1].Departure.City, legs[1].Arrival.City)
		}
	})

	t.Run("no segments yields nil for fallback", func(t *testing.T) {
		if legs := parseRome2RioSegments("See schedules"); legs != nil {
			t.Errorf("expected nil, got %+v", legs)
		}
	})
}
