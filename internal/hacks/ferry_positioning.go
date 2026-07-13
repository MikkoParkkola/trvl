package hacks

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// ferryRoute describes a known ferry connection useful for positioning.
type ferryRoute struct {
	FerryFrom string  // city name (for ground search)
	FerryTo   string  // city name (for ground search)
	AirportTo string  // IATA code of the arrival-side airport to fly from
	FerryEUR  float64 // approximate minimum ferry fare (EUR)
	Overnight bool    // true if this is typically an overnight ferry
	Notes     string  // e.g. provider name, duration
}

// ferryPositioningRoutes maps an IATA origin airport code to viable ferry
// positioning routes. Each route: take ferry, then fly from AirportTo.
var ferryPositioningRoutes = map[string][]ferryRoute{
	"HEL": {
		{
			FerryFrom: "Helsinki", FerryTo: "Tallinn",
			AirportTo: "TLL", FerryEUR: 19, Overnight: false,
			Notes: "Eckerö Line or Tallink (~2.5h); airport 10 min from port",
		},
		{
			FerryFrom: "Helsinki", FerryTo: "Stockholm",
			AirportTo: "ARN", FerryEUR: 35, Overnight: true,
			Notes: "Tallink/Viking Line (~16h overnight); Arlanda 40 min from Värtahamnen",
		},
		{
			FerryFrom: "Helsinki", FerryTo: "Riga",
			AirportTo: "RIX", FerryEUR: 45, Overnight: true,
			Notes: "Tallink (~27h, 2 nights); check schedule before booking",
		},
	},
	"TLL": {
		{
			FerryFrom: "Tallinn", FerryTo: "Helsinki",
			AirportTo: "HEL", FerryEUR: 19, Overnight: false,
			Notes: "Eckerö/Tallink (~2.5h)",
		},
		{
			FerryFrom: "Tallinn", FerryTo: "Stockholm",
			AirportTo: "ARN", FerryEUR: 30, Overnight: true,
			Notes: "Tallink (~18h overnight)",
		},
	},
	"ARN": {
		{
			FerryFrom: "Stockholm", FerryTo: "Tallinn",
			AirportTo: "TLL", FerryEUR: 30, Overnight: true,
			Notes: "Tallink (~18h overnight); TLL often cheaper for European LCCs",
		},
		{
			FerryFrom: "Stockholm", FerryTo: "Helsinki",
			AirportTo: "HEL", FerryEUR: 35, Overnight: true,
			Notes: "Viking Line/Tallink (~16h overnight)",
		},
		{
			FerryFrom: "Stockholm", FerryTo: "Riga",
			AirportTo: "RIX", FerryEUR: 40, Overnight: true,
			Notes: "Tallink (~17h overnight); Riga has Ryanair/Wizz coverage",
		},
	},
	"CPH": {
		{
			FerryFrom: "Copenhagen", FerryTo: "Oslo",
			AirportTo: "OSL", FerryEUR: 45, Overnight: true,
			Notes: "DFDS overnight (~16h)",
		},
	},
	"OSL": {
		{
			FerryFrom: "Oslo", FerryTo: "Copenhagen",
			AirportTo: "CPH", FerryEUR: 45, Overnight: true,
			Notes: "DFDS overnight (~16h); CPH often cheaper for intercontinental",
		},
	},
}

// detectFerryPositioning checks whether taking a ferry to a nearby port and
// then flying from there is cheaper than flying directly, even after adding
// the ferry cost.
func detectFerryPositioning(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.Date == "" {
		return nil
	}

	// Respect PreferDirect preference.
	prefs, _ := preferences.Load()
	if prefs != nil && prefs.PreferDirect {
		return nil
	}

	routes, ok := ferryPositioningRoutes[in.Origin]
	if !ok {
		return nil
	}

	// Ferry legs are EUR-denominated static estimates; each is converted via FX
	// into the traveller's requested currency so the itinerary is labelled
	// honestly in that one currency. A candidate whose ferry estimate cannot be
	// converted is skipped rather than mislabelled.
	target := strings.ToUpper(strings.TrimSpace(in.currency()))
	if target == "" {
		target = "EUR"
	}

	// Baseline: cheapest direct flight from origin, converted into the requested currency.
	directResult, err := flights.SearchFlights(ctx, in.Origin, in.Destination, in.Date, flights.SearchOptions{})
	if err != nil || !directResult.Success || len(directResult.Flights) == 0 {
		return nil
	}
	directPrice, ok := minFlightPriceConverted(ctx, directResult, target)
	if !ok {
		return nil
	}
	currency := target

	type candidate struct {
		route     ferryRoute
		flightEUR float64
		ferryEUR  float64
	}
	ch := make(chan candidate, len(routes))

	for _, r := range routes {
		r := r
		go func() {
			// Prefer a live ferry price (converted into target); fall back to the
			// static EUR estimate converted into target. Suppress the candidate
			// only when NEITHER the live route nor the static estimate can be
			// shown in the requested currency. Cheapest convertible price wins.
			ferryPrice := 0.0
			haveFerry := false
			ferryResult, ferryErr := ground.SearchByName(ctx, r.FerryFrom, r.FerryTo, in.Date, ground.SearchOptions{
				Currency: "EUR",
				Type:     "ferry",
			})
			if ferryErr == nil && ferryResult.Success && len(ferryResult.Routes) > 0 {
				if _, live, lok := selectCheapestGroundConverted(ctx, ferryResult.Routes, target, false); lok && live > 0 {
					ferryPrice = live
					haveFerry = true
				}
			}
			if est, fec := destinations.ConvertCurrency(ctx, r.FerryEUR, "EUR", target); fec == target {
				if !haveFerry || est < ferryPrice {
					ferryPrice = est
					haveFerry = true
				}
			}
			if !haveFerry {
				ch <- candidate{}
				return
			}

			// Flight from the ferry destination airport, converted into the requested currency.
			flightResult, flightErr := flights.SearchFlights(ctx, r.AirportTo, in.Destination, in.Date, flights.SearchOptions{})
			if flightErr != nil || !flightResult.Success || len(flightResult.Flights) == 0 {
				ch <- candidate{}
				return
			}
			flightPrice, flok := minFlightPriceConverted(ctx, flightResult, target)
			if !flok {
				ch <- candidate{}
				return
			}
			ch <- candidate{route: r, flightEUR: flightPrice, ferryEUR: ferryPrice}
		}()
	}

	var hacks []Hack
	for range routes {
		c := <-ch
		if c.flightEUR == 0 {
			continue
		}
		total := c.flightEUR + c.ferryEUR
		savings := directPrice - total
		if savings < 10 {
			continue
		}

		overnightNote := ""
		if c.route.Overnight {
			overnightNote = " (overnight ferry — no hotel needed)"
		}

		hacks = append(hacks, Hack{
			Type:     "ferry_positioning",
			Title:    fmt.Sprintf("Ferry to %s then fly to %s", c.route.FerryTo, in.Destination),
			Currency: currency,
			Savings:  roundSavings(savings),
			Description: fmt.Sprintf(
				"Ferry %s→%s (%.0f %s%s) + flight %s→%s (%.0f %s) = %.0f %s total vs %.0f %s direct flight. Saves %s %.0f.",
				c.route.FerryFrom, c.route.FerryTo, c.ferryEUR, currency, overnightNote,
				c.route.AirportTo, in.Destination, c.flightEUR, currency,
				total, currency, directPrice, currency, currency, savings,
			),
			Risks: []string{
				"Ferry schedule must align with your flight — check departure times carefully",
				"Overnight ferries add travel time; factor in cabin costs if sleeping onboard",
				"Ferry may be delayed; book a flight with a buffer of at least 3 hours after ferry arrival",
				"Two separate tickets — no through-check protection",
			},
			Steps: []string{
				fmt.Sprintf("Book ferry %s→%s on %s (%s: %.0f %s)", c.route.FerryFrom, c.route.FerryTo, in.Date, c.route.Notes, c.ferryEUR, currency),
				fmt.Sprintf("Transfer from %s port to %s airport (see notes: %s)", c.route.FerryTo, c.route.AirportTo, c.route.Notes),
				fmt.Sprintf("Book flight %s→%s (%s %.0f)", c.route.AirportTo, in.Destination, currency, c.flightEUR),
				"Allow at least 3 hours between ferry arrival and flight departure",
			},
			Citations: []string{
				googleFlightsURL(in.Destination, c.route.AirportTo, in.Date),
			},
		})
	}

	return hacks
}
