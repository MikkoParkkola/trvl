package models

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSearchResultDestinationJSONParity guards the text/JSON parity contract:
// every default-search result type that prints a destination footer on the
// text path must also serialize a `destination` field on the JSON path. The
// gap this guards against is adding the human-readable footer while forgetting
// the JSON field, so machine consumers silently lose the enrichment.
func TestSearchResultDestinationJSONParity(t *testing.T) {
	info := &DestinationInfo{Location: "Lisbon"}

	cases := []struct {
		name string
		val  any
	}{
		{"hotels", &HotelSearchResult{Destination: info}},
		{"flights", &FlightSearchResult{Destination: info}},
		{"ground", &GroundSearchResult{Destination: info}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(c.val)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if !strings.Contains(string(b), `"destination"`) {
				t.Errorf("%s JSON missing destination field: %s", c.name, b)
			}
			if !strings.Contains(string(b), `"Lisbon"`) {
				t.Errorf("%s JSON missing destination payload: %s", c.name, b)
			}
		})
	}
}

// TestSearchResultDestinationOmittedWhenNil confirms the field is absent (not
// a null) when enrichment degrades, so the additive contract holds: a failed
// lookup leaves no trace in the output.
func TestSearchResultDestinationOmittedWhenNil(t *testing.T) {
	cases := []struct {
		name string
		val  any
	}{
		{"hotels", &HotelSearchResult{}},
		{"flights", &FlightSearchResult{}},
		{"ground", &GroundSearchResult{}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(c.val)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(b), `"destination"`) {
				t.Errorf("%s JSON should omit destination when nil: %s", c.name, b)
			}
		})
	}
}
