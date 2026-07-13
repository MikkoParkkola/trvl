package hacks

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/destinations"
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

	// Ground legs here are EUR-denominated static estimates; each is converted
	// via FX into the traveller's requested currency so the itinerary is
	// labelled honestly in that one currency. A candidate whose ground estimate
	// cannot be converted is skipped rather than mislabelled.
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

			// Prefer a live ground price (converted into target); fall back to the
			// static EUR estimate converted into target. Suppress the candidate
			// only when NEITHER the live route nor the static estimate can be
			// shown in the requested currency. Cheapest convertible price wins.
			groundConv := 0.0
			haveGround := false
			gr, gerr := ground.SearchByName(ctx, h.OriginCity, h.HubCity, in.Date, ground.SearchOptions{
				Currency: "EUR",
				Type:     h.GroundType,
			})
			if gerr == nil && gr.Success {
				if _, live, lok := selectCheapestGroundConverted(ctx, gr.Routes, target, false); lok && live > 0 {
					groundConv = live
					haveGround = true
				}
			}
			if est, gec := destinations.ConvertCurrency(ctx, h.StaticGroundEUR, "EUR", target); gec == target {
				if !haveGround || est < groundConv {
					groundConv = est
					haveGround = true
				}
			}
			if !haveGround {
				ch <- candidate{}
				return
			}

			// Flight from hub to destination, converted into the requested currency.
			fr, ferr := flights.SearchFlights(ctx, h.HubCode, in.Destination, in.Date, flights.SearchOptions{})
			if ferr != nil || !fr.Success || len(fr.Flights) == 0 {
				ch <- candidate{}
				return
			}
			flightPrice, fok := minFlightPriceConverted(ctx, fr, target)
			if !fok {
				ch <- candidate{}
				return
			}
			ch <- candidate{hub: h, groundEUR: groundConv, flightEUR: flightPrice}
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
				"%s to %s (%.0f %s) + flight %s→%s (%.0f %s) = %.0f %s total, vs direct flight %.0f %s. Saves %s %.0f (%.0f%%).",
				c.hub.OriginCity, c.hub.HubCity, c.groundEUR, currency,
				c.hub.HubCode, in.Destination, c.flightEUR, currency,
				total, currency, directPrice, currency,
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
