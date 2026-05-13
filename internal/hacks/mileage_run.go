package hacks

import (
	"context"
	"fmt"
	"strings"
)

// mileageRunRoute describes a cheap route for earning qualifying miles/segments.
type mileageRunRoute struct {
	From        string  // IATA origin
	To          string  // IATA destination
	Airline     string  // Display name with IATA code
	Alliance    string  // "star_alliance", "skyteam", "oneworld"
	CostEUR     float64 // typical low fare (one-way)
	MilesEarned int     // approximate qualifying miles
	CostPerMile float64 // EUR per qualifying mile
}

// cheapMileageRuns is a curated list of the cheapest mileage-earning routes
// in Europe, sorted by cost-per-mile within each alliance.
var cheapMileageRuns = []mileageRunRoute{
	// Star Alliance
	{From: "IST", To: "AYT", Airline: "Turkish (TK)", Alliance: "star_alliance", CostEUR: 30, MilesEarned: 400, CostPerMile: 0.08},
	{From: "ATH", To: "SKG", Airline: "Aegean (A3)", Alliance: "star_alliance", CostEUR: 40, MilesEarned: 300, CostPerMile: 0.13},
	{From: "LIS", To: "OPO", Airline: "TAP (TP)", Alliance: "star_alliance", CostEUR: 30, MilesEarned: 200, CostPerMile: 0.15},
	// SkyTeam
	{From: "FCO", To: "MXP", Airline: "ITA (AZ)", Alliance: "skyteam", CostEUR: 35, MilesEarned: 350, CostPerMile: 0.10},
	{From: "CDG", To: "LYS", Airline: "Air France (AF)", Alliance: "skyteam", CostEUR: 40, MilesEarned: 300, CostPerMile: 0.13},
	{From: "AMS", To: "BRU", Airline: "KLM (KL)", Alliance: "skyteam", CostEUR: 60, MilesEarned: 130, CostPerMile: 0.46},
	// Oneworld
	{From: "MAD", To: "BCN", Airline: "Iberia (IB)", Alliance: "oneworld", CostEUR: 40, MilesEarned: 390, CostPerMile: 0.10},
	{From: "LHR", To: "EDI", Airline: "BA (BA)", Alliance: "oneworld", CostEUR: 50, MilesEarned: 330, CostPerMile: 0.15},
	{From: "HEL", To: "ARN", Airline: "Finnair (AY)", Alliance: "oneworld", CostEUR: 60, MilesEarned: 400, CostPerMile: 0.15},
}

// allianceDisplayNames maps internal alliance IDs to human-readable names.
var allianceDisplayNames = map[string]string{
	"star_alliance": "Star Alliance",
	"skyteam":       "SkyTeam",
	"oneworld":      "Oneworld",
}

// detectMileageRun suggests the cheapest mileage-earning routes near the
// user's origin airport. Useful for travellers close to airline status
// qualification. Purely advisory — zero API calls.
func detectMileageRun(_ context.Context, in DetectorInput) []Hack {
	if in.Origin == "" {
		return nil
	}

	origin := strings.ToUpper(in.Origin)

	// Find runs reachable from the user's origin (either end of the route).
	type match struct {
		route    mileageRunRoute
		fromUser string // which end the user would depart from
		toUser   string // which end the user would fly to
	}

	var matches []match
	for _, r := range cheapMileageRuns {
		if r.From == origin {
			matches = append(matches, match{route: r, fromUser: r.From, toUser: r.To})
		} else if r.To == origin {
			matches = append(matches, match{route: r, fromUser: r.To, toUser: r.From})
		}
	}

	if len(matches) == 0 {
		return nil
	}

	// Return top 3 cheapest by cost-per-mile.
	if len(matches) > 3 {
		matches = matches[:3]
	}

	var hacks []Hack
	for _, m := range matches {
		allianceName := allianceDisplayNames[m.route.Alliance]
		if allianceName == "" {
			allianceName = m.route.Alliance
		}

		hacks = append(hacks, Hack{
			Type: "mileage_run",
			Title: fmt.Sprintf("Mileage run: %s→%s on %s (EUR %.2f/mile)",
				m.fromUser, m.toUser, m.route.Airline, m.route.CostPerMile),
			Description: fmt.Sprintf(
				"If you're chasing %s status, %s→%s on %s earns ~%d qualifying miles "+
					"for ~EUR %.0f (EUR %.2f per qualifying mile). "+
					"Book a day-return for maximum segment credit.",
				allianceName, m.fromUser, m.toUser, m.route.Airline,
				m.route.MilesEarned, m.route.CostEUR, m.route.CostPerMile),
			Savings:  0, // advisory — status value is personal
			Currency: "EUR",
			Steps: []string{
				fmt.Sprintf("Search %s→%s on %s website for lowest fare", m.fromUser, m.toUser, m.route.Airline),
				"Book the cheapest fare class that earns qualifying miles (avoid Basic Economy)",
				"Day-return maximises segments earned per trip",
				fmt.Sprintf("~%d qualifying miles at ~EUR %.0f = EUR %.2f per mile",
					m.route.MilesEarned, m.route.CostEUR, m.route.CostPerMile),
			},
			Risks: []string{
				"Basic Economy fares may earn 0 qualifying miles — check fare class rules",
				"Airline loyalty programs change qualification rules frequently",
				"Cost-per-mile varies with booking date and demand",
			},
		})
	}

	return hacks
}
