package hacks

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
)

// returnSplitFlightSearch is the flight search seam (overridable in tests).
// Mirrors splitSearchFunc.
var returnSplitFlightSearch = flights.SearchFlights

// returnSplitGroundSearch is the ground search seam (overridable in tests).
var returnSplitGroundSearch = ground.SearchByName

// detectMultiModalReturnSplit checks whether an open-jaw with a mode swap
// (fly out, return by ground or vice versa) is cheaper than a round-trip.
//
// Saving threshold: EUR 50, consistent with other multimodal detectors.
//
// Cross-currency honesty (mirrors detectSplit): a saving is emitted only when
// the baseline currency is known and the round-trip flight, the one-way flight
// used in that direction, and the ground leg all share it. Otherwise the
// direction is skipped — no conversion, no mislabelled total. The EUR hotel
// bonus is only folded in when the shared currency is itself EUR.
func detectMultiModalReturnSplit(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.Date == "" || in.ReturnDate == "" {
		return nil
	}

	originCity := cityFromCode(in.Origin)
	destCity := cityFromCode(in.Destination)

	// Baseline: cheapest round-trip flight.
	rtResult, err := returnSplitFlightSearch(ctx, in.Origin, in.Destination, in.Date, flights.SearchOptions{
		ReturnDate: in.ReturnDate,
	})
	if err != nil || !rtResult.Success || len(rtResult.Flights) == 0 {
		return nil
	}
	rtPrice, rtCur := minFlightPriceWithCurrency(rtResult)
	if rtPrice <= 0 {
		return nil
	}

	// One-way outbound flight.
	owOutResult, err := returnSplitFlightSearch(ctx, in.Origin, in.Destination, in.Date, flights.SearchOptions{})
	if err != nil || !owOutResult.Success || len(owOutResult.Flights) == 0 {
		return nil
	}
	owOutPrice, owOutCur := minFlightPriceWithCurrency(owOutResult)
	if owOutPrice <= 0 {
		return nil
	}

	// One-way return flight (destination → origin).
	owRetResult, err := returnSplitFlightSearch(ctx, in.Destination, in.Origin, in.ReturnDate, flights.SearchOptions{})
	if err != nil || !owRetResult.Success || len(owRetResult.Flights) == 0 {
		return nil
	}
	owRetPrice, owRetCur := minFlightPriceWithCurrency(owRetResult)
	if owRetPrice <= 0 {
		return nil
	}

	// Currency normalization + baseline (same discipline as detectSplit).
	rtCur = strings.ToUpper(strings.TrimSpace(rtCur))
	owOutCur = strings.ToUpper(strings.TrimSpace(owOutCur))
	owRetCur = strings.ToUpper(strings.TrimSpace(owRetCur))
	baseCur := strings.ToUpper(strings.TrimSpace(in.Currency))

	// Ground return: destination → origin. The request stays EUR; the guard
	// checks the route's own reported Currency, never the requested one.
	groundResult, err := returnSplitGroundSearch(ctx, destCity, originCity, in.ReturnDate, ground.SearchOptions{
		Currency: "EUR",
	})

	var hacks []Hack

	// Direction 1: fly out, return by ground.
	if err == nil && groundResult.Success && len(groundResult.Routes) > 0 {
		var bestGroundPrice float64
		var bestRoute *groundRoute

		for i := range groundResult.Routes {
			r := &groundResult.Routes[i]
			if r.Price <= 0 {
				continue
			}
			if bestRoute == nil || r.Price < bestGroundPrice {
				bestGroundPrice = r.Price
				bestRoute = &groundRoute{
					provider:   r.Provider,
					routeType:  r.Type,
					price:      r.Price,
					currency:   r.Currency,
					depCity:    r.Departure.City,
					arrCity:    r.Arrival.City,
					depTime:    r.Departure.Time,
					arrTime:    r.Arrival.Time,
					bookingURL: r.BookingURL,
				}
			}
		}

		groundCur := ""
		if bestRoute != nil {
			groundCur = strings.ToUpper(strings.TrimSpace(bestRoute.currency))
		}
		if bestRoute != nil && baseCur != "" && rtCur == baseCur && owOutCur == baseCur && groundCur == baseCur {
			currency := baseCur
			overnight := isOvernightRoute(bestRoute.depTime, bestRoute.arrTime)
			hotelBonus := 0.0
			if overnight && baseCur == "EUR" {
				hotelBonus = averageHotelCost
			}

			totalMixed := owOutPrice + bestGroundPrice
			savings := rtPrice - totalMixed + hotelBonus
			if savings >= 50 {
				overnightNote := ""
				if hotelBonus > 0 {
					overnightNote = fmt.Sprintf(" (overnight %s — saves ~%.0f hotel)", bestRoute.routeType, averageHotelCost)
				}

				hacks = append(hacks, Hack{
					Type:     "multimodal_return_split",
					Title:    fmt.Sprintf("Fly out, return by %s — saves %s %.0f", bestRoute.routeType, currency, roundSavings(savings)),
					Currency: currency,
					Savings:  roundSavings(savings),
					Description: fmt.Sprintf(
						"One-way flight %s→%s (%.0f %s) + %s return %s→%s (%.0f %s)%s = %.0f total, vs round-trip %.0f %s. Saves %s %.0f.",
						in.Origin, in.Destination, owOutPrice, currency,
						bestRoute.routeType, bestRoute.depCity, bestRoute.arrCity, bestGroundPrice, currency,
						overnightNote, totalMixed, rtPrice, currency,
						currency, savings,
					),
					Risks: []string{
						"Two separate bookings — no through-check protection",
						"Ground return adds travel time; may be less comfortable than flying",
						"Check ground transport availability on your exact return date",
					},
					Steps: []string{
						fmt.Sprintf("Book one-way flight %s→%s on %s (%s %.0f)", in.Origin, in.Destination, in.Date, currency, owOutPrice),
						fmt.Sprintf("Book %s return %s→%s on %s (%s %.0f)", bestRoute.routeType, bestRoute.depCity, bestRoute.arrCity, in.ReturnDate, currency, bestGroundPrice),
						fmt.Sprintf("Return journey: %s", bestRoute.bookingURL),
					},
					Citations: []string{
						googleFlightsURL(in.Destination, in.Origin, in.Date),
						bestRoute.bookingURL,
					},
				})
			}
		}
	}

	// Direction 2: take ground out, fly back.
	// Only viable when there is a known ground route origin→destination.
	groundOutResult, gerr := returnSplitGroundSearch(ctx, originCity, destCity, in.Date, ground.SearchOptions{
		Currency: "EUR",
	})
	if gerr == nil && groundOutResult.Success && len(groundOutResult.Routes) > 0 {
		var bestGroundPrice float64
		var bestRoute *groundRoute

		for i := range groundOutResult.Routes {
			r := &groundOutResult.Routes[i]
			if r.Price <= 0 {
				continue
			}
			if bestRoute == nil || r.Price < bestGroundPrice {
				bestGroundPrice = r.Price
				bestRoute = &groundRoute{
					provider:   r.Provider,
					routeType:  r.Type,
					price:      r.Price,
					currency:   r.Currency,
					depCity:    r.Departure.City,
					arrCity:    r.Arrival.City,
					depTime:    r.Departure.Time,
					arrTime:    r.Arrival.Time,
					bookingURL: r.BookingURL,
				}
			}
		}

		groundCur := ""
		if bestRoute != nil {
			groundCur = strings.ToUpper(strings.TrimSpace(bestRoute.currency))
		}
		if bestRoute != nil && baseCur != "" && rtCur == baseCur && owRetCur == baseCur && groundCur == baseCur {
			currency := baseCur
			overnight := isOvernightRoute(bestRoute.depTime, bestRoute.arrTime)
			hotelBonus := 0.0
			if overnight && baseCur == "EUR" {
				hotelBonus = averageHotelCost
			}

			totalMixed := bestGroundPrice + owRetPrice
			savings := rtPrice - totalMixed + hotelBonus
			if savings >= 50 {
				overnightNote := ""
				if hotelBonus > 0 {
					overnightNote = fmt.Sprintf(" (overnight %s — saves ~%.0f hotel)", bestRoute.routeType, averageHotelCost)
				}

				hacks = append(hacks, Hack{
					Type:     "multimodal_return_split",
					Title:    fmt.Sprintf("Ground out%s, fly back — saves %s %.0f", overnightNote, currency, roundSavings(savings)),
					Currency: currency,
					Savings:  roundSavings(savings),
					Description: fmt.Sprintf(
						"%s outbound %s→%s (%.0f %s)%s + one-way flight %s→%s (%.0f %s) = %.0f total, vs round-trip %.0f %s. Saves %s %.0f.",
						bestRoute.routeType, bestRoute.depCity, bestRoute.arrCity, bestGroundPrice, currency,
						overnightNote,
						in.Destination, in.Origin, owRetPrice, currency,
						totalMixed, rtPrice, currency,
						currency, savings,
					),
					Risks: []string{
						"Two separate bookings — no through-check protection",
						"Ground outbound adds travel time; plan around departure time",
						"One-way flight fares can be higher than a round-trip component",
					},
					Steps: []string{
						fmt.Sprintf("Book %s outbound %s→%s on %s (%s %.0f)", bestRoute.routeType, bestRoute.depCity, bestRoute.arrCity, in.Date, currency, bestGroundPrice),
						fmt.Sprintf("Book one-way flight %s→%s on %s (%s %.0f)", in.Destination, in.Origin, in.ReturnDate, currency, owRetPrice),
						fmt.Sprintf("Outbound booking: %s", bestRoute.bookingURL),
					},
					Citations: []string{
						bestRoute.bookingURL,
						googleFlightsURL(in.Origin, in.Destination, in.ReturnDate),
					},
				})
			}
		}
	}

	return hacks
}
