package hacks

import (
	"fmt"
	"sort"
	"strings"
)

// surchargeLevel describes an airline's carrier-imposed surcharge (YQ/YR)
// behaviour on long-haul routes.
type surchargeLevel struct {
	Airline     string
	AirlineName string
	Level       string  // "none", "low", "medium", "high"
	TypicalEUR  float64 // typical YQ per long-haul segment
}

// airlineSurcharges is a static lookup of known YQ/YR surcharge levels.
var airlineSurcharges = map[string]surchargeLevel{
	"TK": {Airline: "TK", AirlineName: "Turkish Airlines", Level: "none", TypicalEUR: 0},
	"ET": {Airline: "ET", AirlineName: "Ethiopian Airlines", Level: "none", TypicalEUR: 0},
	"UA": {Airline: "UA", AirlineName: "United Airlines", Level: "none", TypicalEUR: 0},
	"SQ": {Airline: "SQ", AirlineName: "Singapore Airlines", Level: "none", TypicalEUR: 0},
	"BR": {Airline: "BR", AirlineName: "EVA Air", Level: "none", TypicalEUR: 0},
	"SK": {Airline: "SK", AirlineName: "SAS", Level: "low", TypicalEUR: 30},
	"MS": {Airline: "MS", AirlineName: "EgyptAir", Level: "none", TypicalEUR: 0},
	"CA": {Airline: "CA", AirlineName: "Air China", Level: "none", TypicalEUR: 0},
	"BA": {Airline: "BA", AirlineName: "British Airways", Level: "high", TypicalEUR: 350},
	"LH": {Airline: "LH", AirlineName: "Lufthansa", Level: "high", TypicalEUR: 250},
	"AF": {Airline: "AF", AirlineName: "Air France", Level: "high", TypicalEUR: 250},
	"KL": {Airline: "KL", AirlineName: "KLM", Level: "high", TypicalEUR: 250},
	"NH": {Airline: "NH", AirlineName: "ANA", Level: "high", TypicalEUR: 200},
	"AY": {Airline: "AY", AirlineName: "Finnair", Level: "medium", TypicalEUR: 150},
	"OS": {Airline: "OS", AirlineName: "Austrian Airlines", Level: "high", TypicalEUR: 200},
	"LX": {Airline: "LX", AirlineName: "Swiss", Level: "high", TypicalEUR: 200},
}

// longHaulThresholdKm is the minimum great-circle distance to consider a route
// long-haul for fuel surcharge purposes.
const longHaulThresholdKm = 3000

// hubRegions maps zero/low-YQ airline codes to the hub airports they route
// through, for suggesting alternatives.
var hubRegions = map[string]string{
	"TK": "IST",
	"ET": "ADD",
	"UA": "EWR",
	"SQ": "SIN",
	"BR": "TPE",
	"SK": "CPH",
	"MS": "CAI",
	"CA": "PEK",
}

// regionServed maps route region pairs to the zero-YQ airlines that serve them.
// Regions: "europe-asia", "europe-americas", "europe-africa", "europe-middle_east".
// Keys are alphabetically sorted region pairs (matching classifyRegion output).
var regionServed = map[string][]string{
	"asia-europe":        {"TK", "SQ", "BR", "CA"},
	"americas-europe":    {"TK", "UA"},
	"africa-europe":      {"TK", "ET", "MS"},
	"europe-middle_east": {"TK", "MS"},
	"europe-oceania":     {"SQ"},
}

// DetectFuelSurcharge checks whether any airlines in the provided list charge
// high YQ/YR fuel surcharges on a long-haul route and suggests zero-surcharge
// alternatives. This is a pure advisory detector — zero API calls.
func DetectFuelSurcharge(origin, destination string, airlines []string) []Hack {
	if origin == "" || destination == "" || len(airlines) == 0 {
		return nil
	}

	// Check route distance — only relevant for long-haul.
	dist := airportDistanceKm(origin, destination)
	if dist > 0 && dist < longHaulThresholdKm {
		return nil
	}
	// If dist == 0 both airports are unknown; we can't determine distance,
	// so we proceed optimistically (the user likely knows it's long-haul).

	// Find high-YQ airlines in the results.
	var highYQ []surchargeLevel
	for _, code := range airlines {
		code = strings.TrimSpace(strings.ToUpper(code))
		if s, ok := airlineSurcharges[code]; ok && s.Level == "high" {
			highYQ = append(highYQ, s)
		}
	}
	if len(highYQ) == 0 {
		return nil
	}

	// Determine route region for alternative suggestions.
	region := classifyRegion(origin, destination)
	alternatives := regionServed[region]

	// Filter out airlines that are already in the user's results.
	airlineSet := make(map[string]bool, len(airlines))
	for _, a := range airlines {
		airlineSet[strings.TrimSpace(strings.ToUpper(a))] = true
	}
	var suggestions []surchargeLevel
	for _, code := range alternatives {
		if !airlineSet[code] {
			if s, ok := airlineSurcharges[code]; ok {
				suggestions = append(suggestions, s)
			}
		}
	}

	// Build the hack(s) — one per high-YQ airline found.
	var hacks []Hack
	for _, hq := range highYQ {
		hack := buildFuelSurchargeHack(origin, destination, hq, suggestions)
		hacks = append(hacks, hack)
	}

	return deduplicateFuelHacks(hacks)
}

// buildFuelSurchargeHack constructs a Hack for a single high-YQ airline.
func buildFuelSurchargeHack(origin, destination string, hq surchargeLevel, alternatives []surchargeLevel) Hack {
	savings := hq.TypicalEUR // per segment, round-trip = 2x
	rtSavings := savings * 2

	desc := fmt.Sprintf(
		"%s (%s) adds approximately EUR %.0f in carrier-imposed surcharges (YQ/YR) per long-haul segment "+
			"(EUR %.0f round-trip). These surcharges are baked into the ticket price.",
		hq.AirlineName, hq.Airline, savings, rtSavings,
	)

	steps := []string{
		fmt.Sprintf("Your %s→%s search includes %s which charges high fuel surcharges",
			origin, destination, hq.AirlineName),
	}

	if len(alternatives) > 0 {
		// Sort alternatives by name for stable output.
		sort.Slice(alternatives, func(i, j int) bool {
			return alternatives[i].Airline < alternatives[j].Airline
		})
		names := make([]string, len(alternatives))
		for i, a := range alternatives {
			hub := hubRegions[a.Airline]
			if hub != "" {
				names[i] = fmt.Sprintf("%s (%s, via %s)", a.AirlineName, a.Airline, hub)
			} else {
				names[i] = fmt.Sprintf("%s (%s)", a.AirlineName, a.Airline)
			}
		}
		steps = append(steps,
			fmt.Sprintf("Consider zero/low-surcharge alternatives: %s", strings.Join(names, ", ")),
			"Search these airlines on the same route for potentially lower total fares",
		)
	} else {
		steps = append(steps, "Check if any competing airlines serve this route without fuel surcharges")
	}

	steps = append(steps, "Compare total ticket prices — surcharges are included in the displayed fare")

	return Hack{
		Type:        "fuel_surcharge",
		Title:       fmt.Sprintf("High fuel surcharge on %s — save up to EUR %.0f RT", hq.AirlineName, rtSavings),
		Currency:    "EUR",
		Savings:     roundSavings(rtSavings),
		Description: desc,
		Risks: []string{
			"Surcharge amounts are approximate and vary by route and fare class",
			"Alternative airlines may have different schedules, connections, or service quality",
			"Total ticket price matters more than surcharge alone — compare all-in fares",
		},
		Steps: steps,
	}
}

// classifyRegion determines the broad route region from origin and destination
// airport coordinates. Returns a key like "europe-asia".
func classifyRegion(origin, destination string) string {
	originRegion := airportRegion(origin)
	destRegion := airportRegion(destination)

	if originRegion == destRegion {
		return "" // intra-regional, no cross-region alternatives
	}

	// Normalise to alphabetical order for consistent lookup.
	a, b := originRegion, destRegion
	if a > b {
		a, b = b, a
	}
	return a + "-" + b
}

// airportRegion returns a broad geographic region for an airport based on
// its coordinates. Regions: "europe", "asia", "americas", "africa",
// "middle_east", "oceania".
func airportRegion(code string) string {
	coords, ok := airportCoords[code]
	if !ok {
		return "unknown"
	}
	lat, lon := coords[0], coords[1]

	// Middle East: lat 12-42, lon 25-60
	if lat >= 12 && lat <= 42 && lon >= 25 && lon <= 60 {
		return "middle_east"
	}
	// Europe: lat 35-72, lon -25 to 40
	if lat >= 35 && lat <= 72 && lon >= -25 && lon <= 40 {
		return "europe"
	}
	// Asia: lat -10 to 55, lon 60-150
	if lat >= -10 && lat <= 55 && lon >= 60 && lon <= 150 {
		return "asia"
	}
	// Africa: lat -35 to 37, lon -20 to 52
	if lat >= -35 && lat <= 37 && lon >= -20 && lon <= 52 {
		return "africa"
	}
	// Oceania: lat -50 to -10, lon 110-180
	if lat >= -50 && lat <= -10 && lon >= 110 && lon <= 180 {
		return "oceania"
	}
	// Americas: lon -170 to -30
	if lon >= -170 && lon <= -30 {
		return "americas"
	}

	return "unknown"
}

// deduplicateFuelHacks removes duplicate fuel surcharge hacks for the same
// airline (in case the user passes the same code twice).
func deduplicateFuelHacks(hacks []Hack) []Hack {
	seen := make(map[string]bool)
	var result []Hack
	for _, h := range hacks {
		// Use the title as dedup key since it contains the airline name.
		if !seen[h.Title] {
			seen[h.Title] = true
			result = append(result, h)
		}
	}
	return result
}
