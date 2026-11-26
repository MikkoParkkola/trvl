package models

import "testing"

// Ryanair's UK bases resolve to GB, so a rule keyed on a UK departure (the
// GBP currency note, #815) does not fail closed for them, and they have a
// name, as every airportCountries entry does.
func TestAirportCountry_RyanairUKBases(t *testing.T) {
	for _, code := range []string{"LPL", "GLA", "NCL", "BFS", "EMA"} {
		if got := AirportCountry(code); got != "GB" {
			t.Errorf("AirportCountry(%q) = %q, want GB", code, got)
		}
		if LookupAirportName(code) == code {
			t.Errorf("AirportNames has no entry for %s", code)
		}
	}
}
