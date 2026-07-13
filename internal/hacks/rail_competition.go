package hacks

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/destinations"
)

// railCorridor describes a route with multiple competing rail operators,
// driving prices down through competition.
type railCorridor struct {
	From       string   // origin IATA or city code
	To         string   // destination IATA or city code
	Operators  []string // competing operators on this route
	MinFareEUR float64  // lowest advance purchase fare
	Country    string   // country/countries
	Notes      string   // booking advice
}

// competitiveCorridors lists European rail routes with significant operator
// competition. Prices reflect advance purchase minimums as of 2025-2026.
var competitiveCorridors = []railCorridor{
	// Spain — 4 operators on key routes
	{From: "MAD", To: "BCN", Operators: []string{"AVE", "AVLO", "Ouigo", "Iryo"},
		MinFareEUR: 7, Country: "ES", Notes: "4 operators; book 30+ days ahead for EUR 7-18"},
	{From: "MAD", To: "VLC", Operators: []string{"AVE", "AVLO", "Ouigo", "Iryo"},
		MinFareEUR: 7, Country: "ES", Notes: "4 operators; advance fares from EUR 7"},
	{From: "MAD", To: "SVQ", Operators: []string{"AVE", "AVLO", "Iryo"},
		MinFareEUR: 9, Country: "ES", Notes: "3 operators; advance fares from EUR 9"},
	{From: "MAD", To: "AGP", Operators: []string{"AVE", "AVLO", "Iryo"},
		MinFareEUR: 9, Country: "ES", Notes: "3 operators; advance fares from EUR 9"},
	// Italy — duopoly
	{From: "MXP", To: "FCO", Operators: []string{"Trenitalia", "Italo"},
		MinFareEUR: 10, Country: "IT", Notes: "Duopoly drives advance prices to EUR 9.90"},
	{From: "MXP", To: "NAP", Operators: []string{"Trenitalia", "Italo"},
		MinFareEUR: 10, Country: "IT", Notes: "Duopoly; advance fares from EUR 10"},
	{From: "FCO", To: "NAP", Operators: []string{"Trenitalia", "Italo"},
		MinFareEUR: 10, Country: "IT", Notes: "Duopoly; advance fares from EUR 10"},
	{From: "VCE", To: "FCO", Operators: []string{"Trenitalia", "Italo"},
		MinFareEUR: 10, Country: "IT", Notes: "Duopoly; advance fares from EUR 10"},
	// Czech Republic — RegioJet disruption
	{From: "PRG", To: "VIE", Operators: []string{"RegioJet", "OBB", "FlixBus"},
		MinFareEUR: 9, Country: "CZ/AT", Notes: "RegioJet undercuts OBB; advance fares from EUR 9"},
	{From: "PRG", To: "BTS", Operators: []string{"RegioJet", "FlixBus"},
		MinFareEUR: 7, Country: "CZ/SK", Notes: "Budget operators; fares from EUR 7"},
	{From: "PRG", To: "BUD", Operators: []string{"RegioJet", "FlixBus"},
		MinFareEUR: 15, Country: "CZ/HU", Notes: "Competition on Prague-Budapest; fares from EUR 15"},
}

// iataToCity maps IATA airport codes to city names for rail corridor matching.
// Supplements cityFromCode with additional codes relevant to rail routes.
var railCityMap = map[string]string{
	"MAD": "Madrid", "BCN": "Barcelona", "VLC": "Valencia",
	"SVQ": "Seville", "AGP": "Malaga",
	"MXP": "Milan", "FCO": "Rome", "NAP": "Naples", "VCE": "Venice",
	"PRG": "Prague", "VIE": "Vienna", "BTS": "Bratislava", "BUD": "Budapest",
}

// detectRailCompetition fires when origin and destination match a known
// competitive rail corridor where multiple operators drive down prices.
// Purely advisory — zero API calls.
func detectRailCompetition(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() {
		return nil
	}

	origin := strings.ToUpper(in.Origin)
	dest := strings.ToUpper(in.Destination)

	target := strings.ToUpper(strings.TrimSpace(in.currency()))
	if target == "" {
		target = "EUR"
	}

	var hacks []Hack

	for _, c := range competitiveCorridors {
		// Match in either direction.
		if (c.From == origin && c.To == dest) || (c.From == dest && c.To == origin) {
			minFare, mcur := destinations.ConvertCurrency(ctx, c.MinFareEUR, "EUR", target)
			if mcur != target {
				// Can't honestly convert the fare — suppress rather than
				// mislabel it.
				break
			}

			fromCity := railCityName(c.From)
			toCity := railCityName(c.To)
			operators := strings.Join(c.Operators, ", ")

			notes := c.Notes
			if notes == "" {
				notes = fmt.Sprintf("Book advance tickets for fares from %s %.0f", target, minFare)
			}

			hack := Hack{
				Type: "rail_competition",
				Title: fmt.Sprintf("%s to %s — %d competing rail operators (from %s %.0f)",
					fromCity, toCity, len(c.Operators), target, minFare),
				Description: fmt.Sprintf(
					"%s to %s has %d competing operators: %s. "+
						"Competition keeps advance fares as low as %s %.0f. %s.",
					fromCity, toCity, len(c.Operators), operators, target, minFare, notes),
				Savings:  0, // advisory — no concrete savings without flight price comparison
				Currency: target,
				Steps: []string{
					fmt.Sprintf("Compare prices across all operators: %s", operators),
					"Book 30+ days in advance for the lowest fares",
					fmt.Sprintf("Check each operator's website — aggregators miss some (especially %s)", c.Operators[len(c.Operators)-1]),
				},
				Risks: []string{
					"Cheapest advance fares are non-refundable and non-changeable",
					"Prices rise sharply within 7 days of departure",
				},
			}

			// If we have a flight price to compare against, note the potential
			// saving. in.NaivePrice is already denominated in target (see
			// DetectorInput.NaivePrice contract) — converting it again from EUR
			// would double-convert and corrupt the comparison for every
			// non-EUR request.
			if in.NaivePrice > 0 {
				naivePrice := in.NaivePrice
				if naivePrice > minFare {
					saving := naivePrice - minFare
					hack.Savings = roundSavings(saving)
					hack.Description += fmt.Sprintf(
						" Your flight costs %s %.0f — rail from %s %.0f saves up to %s %.0f.",
						target, naivePrice, target, minFare, target, saving)
					hack.Steps = append(hack.Steps,
						fmt.Sprintf("Flight costs %s %.0f; cheapest rail is %s %.0f — consider the trade-off (rail is slower but cheaper and city-centre to city-centre)",
							target, naivePrice, target, minFare))
				}
			}

			hacks = append(hacks, hack)
			break // one match per search
		}
	}

	return hacks
}

// railCityName returns a display city name for a rail corridor IATA code.
func railCityName(code string) string {
	if city, ok := railCityMap[code]; ok {
		return city
	}
	return cityFromCode(code)
}
