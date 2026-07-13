package hacks

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// nearbyAirports lists airports within ~500 km of a given origin, together
// with an estimated ground-transit cost (EUR) and travel time (minutes).
// Only airports where the positioning benefit is plausibly significant are listed.
var nearbyAirports = map[string][]nearbyEntry{
	"HEL": {
		{"TLL", "Tallinn", 30, 90, "Ferry HEL→TLL (~2.5h) + bus to airport"},
		{"RIX", "Riga", 55, 210, "Bus/ferry HEL→TLL→RIX (~4h total)"},
		{"VNO", "Vilnius", 70, 360, "Bus HEL→TLL→RIX→VNO (~7h total)"},
	},
	"AMS": {
		{"EIN", "Eindhoven", 20, 75, "Train AMS Centraal→Eindhoven (1h15)"},
		{"BRU", "Brussels", 25, 90, "Train AMS→BRU (1h45)"},
		{"DUS", "Dusseldorf", 20, 120, "Train AMS→DUS (2h)"},
		{"ANR", "Antwerp", 15, 60, "Train AMS→ANR (1h)"},
		{"MST", "Maastricht", 15, 90, "Bus AMS→MST (1h30)"},
	},
	"LHR": {
		{"STN", "London Stansted", 10, 60, "National Express bus LHR→STN (1h)"},
		{"LGW", "London Gatwick", 10, 60, "Bus/train LHR→LGW (1h)"},
		{"LTN", "London Luton", 10, 60, "Bus LHR→LTN (1h)"},
		{"SEN", "Southend", 10, 90, "Bus LHR→SEN (1h30)"},
		{"BRS", "Bristol", 25, 120, "Coach LHR→BRS (2h)"},
	},
	"CDG": {
		{"ORY", "Paris Orly", 10, 60, "RER B + Orlyval CDG→ORY (1h)"},
		{"BVA", "Beauvais", 20, 90, "Shuttle bus CDG→BVA (1h30)"},
	},
	"BCN": {
		{"GRO", "Girona", 15, 90, "Bus BCN→Girona (1h30)"},
		{"REU", "Reus", 15, 90, "Bus BCN→Reus (1h30)"},
	},
	"MAD": {
		{"VLL", "Valladolid", 20, 90, "Train/bus MAD→VLL (1h30)"},
	},
	"FCO": {
		{"CIA", "Rome Ciampino", 0, 40, "Bus FCO→CIA (40 min)"},
		{"NAP", "Naples", 25, 120, "Train FCO→NAP (2h)"},
	},
	"MUC": {
		{"NUE", "Nuremberg", 20, 90, "Train MUC→NUE (1h15)"},
		{"FMM", "Memmingen", 15, 75, "Bus MUC→FMM (1h15)"},
	},
	"CPH": {
		{"MMX", "Malmo", 10, 40, "Train CPH→MMX (35 min; cross the Oresund)"},
		{"GOT", "Gothenburg", 20, 180, "Train CPH→GOT (3h)"},
		{"ARN", "Stockholm", 30, 300, "Train CPH→ARN (5h)"},
	},
	"ARN": {
		{"CPH", "Copenhagen", 30, 300, "Train ARN→CPH (5h)"},
		{"GOT", "Gothenburg", 20, 180, "Train ARN→GOT (3h)"},
	},
	"OSL": {
		{"TRF", "Sandefjord (Torp)", 15, 90, "Bus OSL→TRF (1h30)"},
	},
}

type nearbyEntry struct {
	Code        string
	City        string
	GroundCost  float64 // EUR
	GroundMins  int
	Description string
}

// cheapestFlightPriceIn returns the cheapest positive flight price after
// converting every flight's price into target, together with an ok flag.
// Flights are skipped when non-positive or when their price cannot be
// converted into target (destinations.ConvertCurrency returns a currency
// string != target). Returns (0, false) when nothing is convertible. Shared
// by the currency-honesty fix across positioning.go, open_jaw.go,
// back_to_back.go, and flight_combo.go.
func cheapestFlightPriceIn(ctx context.Context, r *models.FlightSearchResult, target string) (float64, bool) {
	if r == nil || !r.Success {
		return 0, false
	}
	min := math.MaxFloat64
	found := false
	for _, f := range r.Flights {
		if f.Price <= 0 {
			continue
		}
		conv, cur := destinations.ConvertCurrency(ctx, f.Price, f.Currency, target)
		if cur != target {
			continue
		}
		if conv < min {
			min = conv
			found = true
		}
	}
	if !found {
		return 0, false
	}
	return min, found
}

// detectPositioning checks whether flying from a nearby airport is cheaper
// even after adding ground-transit costs.
func detectPositioning(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.Date == "" {
		return nil
	}

	candidates, ok := nearbyAirports[in.Origin]
	if !ok {
		return nil
	}

	// All user-visible prices here (direct flight, alt flight, and the static
	// EUR ground-cost estimate) are converted into the requested currency; a
	// candidate whose price cannot be converted is skipped rather than shown
	// unconverted or mislabelled.
	target := strings.ToUpper(strings.TrimSpace(in.currency()))
	if target == "" {
		target = "EUR"
	}

	// Baseline: direct flight from origin, converted into the requested currency.
	directResult, err := flights.SearchFlights(ctx, in.Origin, in.Destination, in.Date, flights.SearchOptions{SearchOverride: in.SearchOverride})
	if err != nil || !directResult.Success || len(directResult.Flights) == 0 {
		return nil
	}
	directPrice, ok := cheapestFlightPriceIn(ctx, directResult, target)
	if !ok {
		return nil
	}
	currency := target

	var hacks []Hack
	for _, entry := range candidates {
		altResult, err := flights.SearchFlights(ctx, entry.Code, in.Destination, in.Date, flights.SearchOptions{SearchOverride: in.SearchOverride})
		if err != nil || !altResult.Success || len(altResult.Flights) == 0 {
			continue
		}
		altPrice, ok := cheapestFlightPriceIn(ctx, altResult, target)
		if !ok {
			continue
		}

		// entry.GroundCost is an EUR-denominated static estimate; suppress this
		// candidate rather than mixing currencies when it cannot be converted.
		groundCost, gcur := destinations.ConvertCurrency(ctx, entry.GroundCost, "EUR", target)
		if gcur != target {
			continue
		}

		totalCost := altPrice + groundCost
		savings := directPrice - totalCost
		if savings < 10 { // require at least 10 units net saving
			continue
		}

		hacks = append(hacks, Hack{
			Type:     "positioning",
			Title:    "Positioning flight via " + entry.City,
			Currency: currency,
			Savings:  roundSavings(savings),
			Description: fmt.Sprintf(
				"Fly from %s (%s) instead of %s: flight %.0f + transit %.0f = %.0f total vs %.0f direct. Saves %s %.0f.",
				entry.Code, entry.City, in.Origin,
				altPrice, groundCost, totalCost,
				directPrice, currency, math.Round(savings),
			),
			Risks: []string{
				"Ground transit adds travel time — budget extra time to reach " + entry.City,
				"Ground transit schedules may not align perfectly with flight times",
				"Ground transport disruptions (strikes, traffic) may cause you to miss the flight",
			},
			Steps: []string{
				fmt.Sprintf("Travel from %s to %s: %s", in.Origin, entry.City, entry.Description),
				fmt.Sprintf("Search flights %s→%s on %s", entry.Code, in.Destination, in.Date),
				"Allow at least 2 hours at the alternative airport for check-in",
			},
			Citations: []string{
				googleFlightsURL(in.Destination, entry.Code, in.Date),
			},
		})
	}

	return hacks
}

// airportCoords stores approximate lat/lon for airports referenced by positioning.
// Exported for testing.
var AirportCoords = map[string]models.Location{
	"HEL": {Name: "Helsinki", Latitude: 60.317, Longitude: 24.963},
	"TLL": {Name: "Tallinn", Latitude: 59.413, Longitude: 24.833},
	"RIX": {Name: "Riga", Latitude: 56.924, Longitude: 23.971},
	"AMS": {Name: "Amsterdam", Latitude: 52.308, Longitude: 4.764},
	"EIN": {Name: "Eindhoven", Latitude: 51.450, Longitude: 5.374},
}
