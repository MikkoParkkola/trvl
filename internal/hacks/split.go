package hacks

import (
	"context"
	"fmt"
	"strings"

	"github.com/MikkoParkkola/trvl/internal/flights"
)

// splitSearchFunc is the flight search seam (overridable in tests). Mirrors the
// backToBackSearchFunc / railFlyFlightSearcher house pattern.
var splitSearchFunc = flights.SearchFlights

// detectSplit compares a round-trip ticket against the sum of two separate
// one-way tickets (one each direction, potentially different airlines).
//
// Only meaningful when a return date is provided.
func detectSplit(ctx context.Context, in DetectorInput) []Hack {
	if !in.valid() || in.Date == "" || in.ReturnDate == "" {
		return nil
	}

	// Round-trip price + the currency of the exact flight that set the price.
	rtResult, err := splitSearchFunc(ctx, in.Origin, in.Destination, in.Date, flights.SearchOptions{
		ReturnDate: in.ReturnDate,
	})
	if err != nil || rtResult == nil || !rtResult.Success || len(rtResult.Flights) == 0 {
		return nil
	}
	rtPrice, rtCur := minFlightPriceWithCurrency(rtResult)
	if rtPrice <= 0 {
		return nil
	}

	// Cheapest one-way outbound.
	owOutResult, err := splitSearchFunc(ctx, in.Origin, in.Destination, in.Date, flights.SearchOptions{})
	if err != nil || owOutResult == nil || !owOutResult.Success || len(owOutResult.Flights) == 0 {
		return nil
	}
	owOutPrice, owOutCur := minFlightPriceWithCurrency(owOutResult)
	if owOutPrice <= 0 {
		return nil
	}

	// Cheapest one-way return.
	owRetResult, err := splitSearchFunc(ctx, in.Destination, in.Origin, in.ReturnDate, flights.SearchOptions{})
	if err != nil || owRetResult == nil || !owRetResult.Success || len(owRetResult.Flights) == 0 {
		return nil
	}
	owRetPrice, owRetCur := minFlightPriceWithCurrency(owRetResult)
	if owRetPrice <= 0 {
		return nil
	}

	// Emit only when the baseline currency is known and every searched fare is
	// in that same currency. Comparing each leg against a non-empty baseCur
	// covers the empty-currency case too (empty != a known currency), so no
	// separate empty checks are needed. baseCur is also what BestSaving
	// subtracts the saving from, so matching it prevents a cross-currency,
	// mislabeled total downstream. Empty currency is unknown, never EUR.
	rtCur = strings.ToUpper(strings.TrimSpace(rtCur))
	owOutCur = strings.ToUpper(strings.TrimSpace(owOutCur))
	owRetCur = strings.ToUpper(strings.TrimSpace(owRetCur))
	baseCur := strings.ToUpper(strings.TrimSpace(in.Currency))
	if baseCur == "" || rtCur != baseCur || owOutCur != baseCur || owRetCur != baseCur {
		return nil
	}
	currency := baseCur

	splitTotal := owOutPrice + owRetPrice
	savings := rtPrice - splitTotal

	// Only flag when split is materially cheaper (at least EUR 15 saved).
	if savings < 15 {
		return nil
	}

	return []Hack{{
		Type:     "split",
		Title:    "Split ticketing",
		Currency: currency,
		Savings:  roundSavings(savings),
		Description: fmt.Sprintf(
			"Two one-way tickets (%s %.0f out + %.0f return = %.0f total) beat round-trip at %.0f. Saves %s %.0f.",
			currency, owOutPrice, owRetPrice, splitTotal, rtPrice, currency, savings,
		),
		Risks: []string{
			"If outbound flight is delayed, the return ticket is a separate contract — no rebooking obligation",
			"No guaranteed connection between independently-booked tickets",
			"Price may fluctuate; lock in both tickets at the same time",
		},
		Steps: []string{
			fmt.Sprintf("Book cheapest one-way %s→%s on %s (%s %.0f)", in.Origin, in.Destination, in.Date, currency, owOutPrice),
			fmt.Sprintf("Book cheapest one-way %s→%s on %s (%s %.0f)", in.Destination, in.Origin, in.ReturnDate, currency, owRetPrice),
			"Different airlines are fine — these are independent bookings",
		},
		Citations: []string{
			googleFlightsURL(in.Destination, in.Origin, in.Date),
			googleFlightsURL(in.Origin, in.Destination, in.ReturnDate),
		},
	}}
}
