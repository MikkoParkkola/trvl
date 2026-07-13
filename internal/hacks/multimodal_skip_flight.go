package hacks

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
)

// detectMultiModalSkipFlight checks whether skipping the flight entirely and
// taking an overnight ground option (bus or train) is cheaper, factoring in the
// saved hotel night.
//
// Threshold: net saving must exceed 50 (in the requested currency) before this
// hack is surfaced.
func detectMultiModalSkipFlight(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.Date == "" {
		return nil
	}

	// The flight price, every ground route's price, and the flat EUR hotel
	// estimate are each converted into the requested target currency before
	// being combined; anything that cannot be converted drops the whole
	// candidate rather than mixing currencies in one saving figure.
	target := strings.ToUpper(strings.TrimSpace(in.currency()))
	if target == "" {
		target = "EUR"
	}

	// Baseline: cheapest direct flight.
	flightResult, err := flights.SearchFlights(ctx, in.Origin, in.Destination, in.Date, flights.SearchOptions{SearchOverride: in.SearchOverride})
	if err != nil || !flightResult.Success || len(flightResult.Flights) == 0 {
		return nil
	}
	flightPrice, ok := cheapestFlightPriceInTarget(ctx, flightResult, target)
	if !ok {
		return nil
	}
	currency := target

	// Ground search — any mode.
	originCity := cityFromCode(in.Origin)
	destCity := cityFromCode(in.Destination)
	groundResult, err := ground.SearchByName(ctx, originCity, destCity, in.Date, ground.SearchOptions{
		Currency:       target,
		SearchOverride: in.GroundSearchOverride,
	})
	if err != nil || !groundResult.Success || len(groundResult.Routes) == 0 {
		return nil
	}

	// Find the cheapest overnight route whose price converts into target.
	var bestPrice float64
	var bestRoute *groundRoute
	for i := range groundResult.Routes {
		r := &groundResult.Routes[i]
		if r.Price <= 0 {
			continue
		}
		if !isOvernightRoute(r.Departure.Time, r.Arrival.Time) {
			continue
		}
		conv, ok := convertCurrency(ctx, r.Price, r.Currency, target)
		if !ok {
			continue
		}
		if bestRoute == nil || conv < bestPrice {
			bestPrice = conv
			bestRoute = &groundRoute{
				provider:   r.Provider,
				routeType:  r.Type,
				price:      conv,
				currency:   target,
				depCity:    r.Departure.City,
				arrCity:    r.Arrival.City,
				depTime:    r.Departure.Time,
				arrTime:    r.Arrival.Time,
				bookingURL: r.BookingURL,
			}
		}
	}

	if bestRoute == nil {
		return nil
	}

	// The saved hotel night is a flat EUR estimate; convert it into target
	// before adding it to a saving figure already expressed in target.
	hotelBonus, ok := convertCurrency(ctx, averageHotelCost, "EUR", target)
	if !ok {
		return nil
	}

	// Total saving: flight price − ground price + saved hotel night, all in target.
	savings := (flightPrice - bestPrice) + hotelBonus
	if savings < 50 {
		return nil
	}

	depTime := trimToHHMM(bestRoute.depTime)
	arrTime := trimToHHMM(bestRoute.arrTime)

	return []Hack{{
		Type:     "multimodal_skip_flight",
		Title:    fmt.Sprintf("Skip the flight — overnight %s saves %s %.0f", bestRoute.routeType, currency, savings),
		Currency: currency,
		Savings:  roundSavings(savings),
		Description: fmt.Sprintf(
			"Overnight %s %s→%s departs %s arrives %s (%.0f %s) vs flight %.0f %s. "+
				"Ground saves %.0f on transport + ~%.0f saved hotel night = %.0f total saving.",
			bestRoute.routeType, bestRoute.depCity, bestRoute.arrCity,
			depTime, arrTime,
			bestRoute.price, currency,
			flightPrice, currency,
			flightPrice-bestRoute.price, hotelBonus, savings,
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
