package hacks

import (
	"context"
	"fmt"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// multiModalHub describes a nearby city reachable by ferry, bus, or train from
// which cheaper flights to many European destinations are available.
type multiModalHub struct {
	// HubCode is the IATA airport code of the hub to fly from.
	HubCode string
	// HubCity is the city name used for ground search.
	HubCity string
	// OriginCity is the city name of the origin airport used for ground search.
	OriginCity string
	// GroundType filters the ground search ("bus", "train", "ferry", or "" for all).
	GroundType string
	// StaticGroundEUR is a conservative static fallback ground cost (EUR) used
	// when the live ground search returns no results.
	StaticGroundEUR float64
	// Notes is shown to the user in the Steps section.
	Notes string
}

// multiModalHubs maps an origin IATA code to viable cross-modal positioning hubs.
var multiModalHubs = map[string][]multiModalHub{
	"HEL": {
		{
			HubCode: "TLL", HubCity: "Tallinn", OriginCity: "Helsinki",
			GroundType: "ferry", StaticGroundEUR: 19,
			Notes: "Eckerö Line or Tallink ferry HEL→TLL (~2.5h); airport 10 min from port",
		},
		{
			HubCode: "RIX", HubCity: "Riga", OriginCity: "Helsinki",
			GroundType: "ferry", StaticGroundEUR: 45,
			Notes: "Tallink ferry HEL→RIX (~27h overnight) or bus via TLL",
		},
		{
			HubCode: "ARN", HubCity: "Stockholm", OriginCity: "Helsinki",
			GroundType: "ferry", StaticGroundEUR: 35,
			Notes: "Viking Line / Tallink overnight ferry HEL→ARN (~16h)",
		},
	},
	"AMS": {
		{
			HubCode: "BRU", HubCity: "Brussels", OriginCity: "Amsterdam",
			GroundType: "train", StaticGroundEUR: 25,
			Notes: "Thalys/Eurostar AMS→BRU (~1h45 by train)",
		},
		{
			HubCode: "EIN", HubCity: "Eindhoven", OriginCity: "Amsterdam",
			GroundType: "train", StaticGroundEUR: 20,
			Notes: "Train AMS Centraal→Eindhoven (~1h15)",
		},
		{
			HubCode: "DUS", HubCity: "Dusseldorf", OriginCity: "Amsterdam",
			GroundType: "train", StaticGroundEUR: 20,
			Notes: "Train AMS→DUS (~2h)",
		},
	},
	"ARN": {
		{
			HubCode: "TLL", HubCity: "Tallinn", OriginCity: "Stockholm",
			GroundType: "ferry", StaticGroundEUR: 30,
			Notes: "Tallink overnight ferry ARN→TLL (~18h)",
		},
		{
			HubCode: "CPH", HubCity: "Copenhagen", OriginCity: "Stockholm",
			GroundType: "train", StaticGroundEUR: 30,
			Notes: "Train ARN→CPH (~5h via Øresund bridge)",
		},
	},
	"OSL": {
		{
			HubCode: "CPH", HubCity: "Copenhagen", OriginCity: "Oslo",
			GroundType: "bus", StaticGroundEUR: 20,
			Notes: "FlixBus OSL→CPH (~8h); CPH has more LCC routes",
		},
	},
	"CPH": {
		{
			HubCode: "ARN", HubCity: "Stockholm", OriginCity: "Copenhagen",
			GroundType: "train", StaticGroundEUR: 30,
			Notes: "Train CPH→ARN (~5h via Øresund bridge)",
		},
		{
			HubCode: "OSL", HubCity: "Oslo", OriginCity: "Copenhagen",
			GroundType: "bus", StaticGroundEUR: 20,
			Notes: "FlixBus CPH→OSL (~8h)",
		},
	},
}

// minSavingsFraction is the minimum relative saving required (20 %) before the
// multi-modal positioning hack is surfaced.
const minSavingsFraction = 0.20

// detectMultiModalPositioning checks whether taking ground transport to a
// nearby hub airport and flying from there is cheaper than flying directly,
// by more than 20 %.
func detectMultiModalPositioning(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.Date == "" {
		return nil
	}

	prefs, _ := preferences.Load()
	if prefs != nil && prefs.PreferDirect {
		return nil
	}

	hubs, ok := multiModalHubs[in.Origin]
	if !ok {
		return nil
	}

	// Baseline: cheapest direct flight from origin.
	directResult, err := flights.SearchFlights(ctx, in.Origin, in.Destination, in.Date, flights.SearchOptions{})
	if err != nil || !directResult.Success || len(directResult.Flights) == 0 {
		return nil
	}
	directPrice := minFlightPrice(directResult)
	if directPrice <= 0 {
		return nil
	}
	currency := flightCurrency(directResult, in.currency())

	type candidate struct {
		hub       multiModalHub
		groundEUR float64
		flightEUR float64
	}

	ch := make(chan candidate, len(hubs))
	var wg sync.WaitGroup

	for _, h := range hubs {
		h := h
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Live ground price (fallback to static estimate).
			groundEUR := h.StaticGroundEUR
			gr, gerr := ground.SearchByName(ctx, h.OriginCity, h.HubCity, in.Date, ground.SearchOptions{
				Currency: "EUR",
				Type:     h.GroundType,
			})
			if gerr == nil && gr.Success {
				for _, r := range gr.Routes {
					if r.Price > 0 && r.Price < groundEUR {
						groundEUR = r.Price
					}
				}
			}

			// Flight from hub to destination.
			fr, ferr := flights.SearchFlights(ctx, h.HubCode, in.Destination, in.Date, flights.SearchOptions{})
			if ferr != nil || !fr.Success || len(fr.Flights) == 0 {
				ch <- candidate{}
				return
			}
			flightPrice := minFlightPrice(fr)
			if flightPrice <= 0 {
				ch <- candidate{}
				return
			}
			ch <- candidate{hub: h, groundEUR: groundEUR, flightEUR: flightPrice}
		}()
	}

	wg.Wait()
	close(ch)

	var hacks []Hack
	for c := range ch {
		if c.flightEUR == 0 {
			continue
		}
		total := c.groundEUR + c.flightEUR
		savings := directPrice - total
		// Require both an absolute saving of EUR 10 and a relative saving of 20 %.
		if savings < 10 || savings/directPrice < minSavingsFraction {
			continue
		}

		hacks = append(hacks, Hack{
			Type:     "multimodal_positioning",
			Title:    fmt.Sprintf("Ground to %s, then fly to %s cheaper", c.hub.HubCity, in.Destination),
			Currency: currency,
			Savings:  roundSavings(savings),
			Description: fmt.Sprintf(
				"%s to %s (%.0f EUR) + flight %s→%s (%.0f EUR) = %.0f EUR total, vs direct flight %.0f EUR. Saves %s %.0f (%.0f%%).",
				c.hub.OriginCity, c.hub.HubCity, c.groundEUR,
				c.hub.HubCode, in.Destination, c.flightEUR,
				total, directPrice,
				currency, savings, 100*savings/directPrice,
			),
			Risks: []string{
				"Two separate tickets — no through-check protection",
				"Ground leg must arrive before flight check-in closes; allow at least 2h buffer",
				"Ground transport delays (traffic, weather) may cause you to miss the flight",
				"Overnight ground legs add travel time; factor in comfort",
			},
			Steps: []string{
				fmt.Sprintf("%s (%s %.0f)", c.hub.Notes, currency, c.groundEUR),
				fmt.Sprintf("Transfer from %s to %s airport", c.hub.HubCity, c.hub.HubCode),
				fmt.Sprintf("Book flight %s→%s on %s (%s %.0f)", c.hub.HubCode, in.Destination, in.Date, currency, c.flightEUR),
				"Allow at least 2 hours between ground arrival and flight departure",
			},
			Citations: []string{
				googleFlightsURL(in.Destination, c.hub.HubCode, in.Date),
			},
		})
	}

	return hacks
}
