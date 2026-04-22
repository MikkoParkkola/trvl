package flights

// coverage.go -- airport lounge coverage map for the lounge filter (Task D).
//
// This is a lightweight lookup table seeded from the verbose card names already
// present in internal/lounges/static_data.go. The map is keyed by IATA airport
// code and maps to the set of verbose card names with confirmed lounge coverage.
//
// Adding an airport: add an entry to loungeAirportCoverage below.
// Seed covers the EU airports listed in the spec plus AMS and HEL.

// loungeAirportCoverage maps IATA airport code -> set of card names that have
// confirmed lounge coverage at that airport. Card name strings must match the
// values used in internal/lounges/static_data.go (and therefore in
// loungeCardExpansion in filter_mikko.go).
var loungeAirportCoverage = map[string]map[string]bool{
	// Netherlands
	"AMS": ppFBOWCoverage(),

	// Finland
	"HEL": merge3(ppCoverage(), owCoverage(), map[string]bool{
		"Finnair Plus Gold":     true,
		"Finnair Plus Platinum": true,
	}),

	// France
	"CDG": ppFBOWCoverage(),

	// UK
	"LHR": ppFBOWCoverage(),

	// Germany
	"FRA": ppFBOWCoverage(),
	"MUC": ppFBOWCoverage(),

	// Spain
	"MAD": ppFBOWCoverage(),
	"BCN": ppCoverage(), // PP only; no OW/FB-branded lounge confirmed

	// Denmark
	"CPH": ppFBOWCoverage(),

	// Sweden
	"ARN": ppFBOWCoverage(),

	// Norway
	"OSL": ppCoverage(),

	// Austria
	"VIE": ppFBOWCoverage(),

	// Switzerland
	"ZRH": ppFBOWCoverage(),

	// Turkey
	"IST": ppFBOWCoverage(),

	// Poland
	"WAW": ppCoverage(),

	// Czech Republic
	"PRG": ppCoverage(),

	// Poland (Krakow)
	"KRK": ppCoverage(),

	// Hungary
	"BUD": ppCoverage(),

	// Portugal
	"LIS": ppCoverage(),

	// Ireland
	"DUB": ppCoverage(),
}

// ppCoverage returns coverage for Priority Pass family cards only.
func ppCoverage() map[string]bool {
	return map[string]bool{
		"Priority Pass": true,
		"LoungeKey":     true,
		"Dragon Pass":   true,
		"Diners Club":   true,
	}
}

// owCoverage returns coverage for Oneworld tier cards.
func owCoverage() map[string]bool {
	return map[string]bool{
		"Oneworld Sapphire": true,
		"Oneworld Emerald":  true,
	}
}

// fbCoverage returns coverage for Flying Blue / SkyTeam cards.
func fbCoverage() map[string]bool {
	return map[string]bool{
		"Flying Blue Gold":     true,
		"Flying Blue Platinum": true,
		"SkyTeam Elite Plus":   true,
	}
}

// ppFBOWCoverage returns coverage for all three card families: PP, FB, and OW.
func ppFBOWCoverage() map[string]bool {
	return merge3(ppCoverage(), fbCoverage(), owCoverage())
}

// merge3 merges up to three bool maps into a single new map.
func merge3(maps ...map[string]bool) map[string]bool {
	out := make(map[string]bool)
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}
