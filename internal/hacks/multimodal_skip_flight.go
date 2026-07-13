package hacks

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
)

// detectMultiModalSkipFlight checks whether skipping the flight entirely and
// taking an overnight ground option (bus or train) is cheaper, factoring in the
// saved hotel night.
//
// Threshold: net saving must exceed EUR 50 before this hack is surfaced.
func detectMultiModalSkipFlight(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.Date == "" {
		return nil
	}

	// Baseline: cheapest direct flight, converted into the traveller's
	// requested currency so every leg and total below is labelled honestly in
	// that one currency via FX conversion (not currency-suppression).
	target := strings.ToUpper(strings.TrimSpace(in.currency()))
	if target == "" {
		target = "EUR"
	}
	flightResult, err := flights.SearchFlights(ctx, in.Origin, in.Destination, in.Date, flights.SearchOptions{})
	if err != nil || !flightResult.Success || len(flightResult.Flights) == 0 {
		return nil
	}
	flightPrice, ok := minFlightPriceConverted(ctx, flightResult, target)
	if !ok {
		return nil // no flight convertible into the requested currency
	}

	// Ground search — any mode. The request stays EUR; each route's own price
	// is converted into target below.
	originCity := cityFromCode(in.Origin)
	destCity := cityFromCode(in.Destination)
	groundResult, err := ground.SearchByName(ctx, originCity, destCity, in.Date, ground.SearchOptions{
		Currency: "EUR",
	})
	if err != nil || !groundResult.Success || len(groundResult.Routes) == 0 {
		return nil
	}

	// Cheapest overnight route, converted into the requested currency.
	bestRoute, bestPrice, gok := selectCheapestGroundConverted(ctx, groundResult.Routes, target, true)
	if !gok {
		return nil
	}

	// Total saving: flight − ground + saved hotel night. The hotel saving is a
	// EUR estimate, converted into target; folded in only if convertible.
	hotelBonus := 0.0
	if hb, hbcur := destinations.ConvertCurrency(ctx, averageHotelCost, "EUR", target); hbcur == target {
		hotelBonus = hb
	}
	savings := (flightPrice - bestPrice) + hotelBonus
	if savings < 50 {
		return nil
	}

	currency := target
	depTime := trimToHHMM(bestRoute.depTime)
	arrTime := trimToHHMM(bestRoute.arrTime)

	hotelNote := ""
	if hotelBonus > 0 {
		hotelNote = fmt.Sprintf(" + ~%.0f saved hotel night", hotelBonus)
	}

	return []Hack{{
		Type:     "multimodal_skip_flight",
		Title:    fmt.Sprintf("Skip the flight — overnight %s saves %s %.0f", bestRoute.routeType, currency, savings),
		Currency: currency,
		Savings:  roundSavings(savings),
		Description: fmt.Sprintf(
			"Overnight %s %s→%s departs %s arrives %s (%.0f %s) vs flight %.0f %s. "+
				"Ground saves %.0f on transport%s = %.0f total saving.",
			bestRoute.routeType, bestRoute.depCity, bestRoute.arrCity,
			depTime, arrTime,
			bestRoute.price, currency,
			flightPrice, currency,
			flightPrice-bestRoute.price, hotelNote, savings,
		),
		Risks: []string{
			"Overnight ground transport is slower; factor in comfort and rest quality",
			"Ground routes may not run daily — check the schedule for your exact date",
			"No through-check protection: if the ground leg is delayed you bear the cost",
			"Carry-on only recommended to avoid lost luggage risk",
		},
		Steps: []string{
			fmt.Sprintf("Book overnight %s %s→%s departing %s on %s (%.0f %s)",
				bestRoute.routeType, bestRoute.depCity, bestRoute.arrCity,
				depTime, in.Date, bestRoute.price, currency),
			"Skip booking a hotel for that night — arrive early morning rested",
			fmt.Sprintf("Check booking at: %s", bestRoute.bookingURL),
		},
		Citations: []string{bestRoute.bookingURL},
	}}
}

// groundRoute is a minimal internal struct to hold the fields we need from
// models.GroundRoute without taking a pointer into the slice (which can move).
type groundRoute struct {
	provider   string
	routeType  string
	price      float64
	currency   string
	depCity    string
	arrCity    string
	depTime    string
	arrTime    string
	bookingURL string
}

// trimToHHMM trims an ISO 8601 datetime to HH:MM. Returns the original string
// unchanged if it is already short.
func trimToHHMM(s string) string {
	if len(s) >= 16 {
		return s[11:16]
	}
	return s
}
