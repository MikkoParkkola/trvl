package hacks

import (
	"context"
	"fmt"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// backToBackSearchFunc is the function used to search flights. Package-level
// variable allows test injection without modifying the detectFn signature.
var backToBackSearchFunc = flights.SearchFlightsWithClient

// backToBackOverlapDays is the offset for the dummy return leg of each
// overlapping round-trip. 14 days is long enough to trigger the cheaper
// long-stay round-trip pricing that makes back-to-back work.
const backToBackOverlapDays = 14

// detectBackToBack suggests the back-to-back ticketing strategy for frequent
// travellers on the same route. Two overlapping round-trips are typically
// 20-40% cheaper than individual one-ways because airlines discount return
// fares.
//
// When possible it performs 4 parallel flight searches to produce concrete
// savings. If any search fails it falls back to the advisory message.
func detectBackToBack(ctx context.Context, in DetectorInput) []Hack {
	// Only fire when there's a return date (user is booking round-trip).
	if !in.valid() || in.ReturnDate == "" {
		return nil
	}
	if in.Date == "" {
		return nil
	}

	// Calculate trip duration — only suggest for short trips (1-14 days)
	// where business-style back-to-back is common.
	depart, err1 := parseDate(in.Date)
	ret, err2 := parseDate(in.ReturnDate)
	if err1 != nil || err2 != nil {
		return nil
	}
	nights := int(ret.Sub(depart).Hours() / 24)
	if nights < 1 || nights > 14 {
		return nil
	}

	// Try live price comparison. Fall back to advisory on failure.
	if hack, ok := backToBackLivePrices(ctx, in); ok {
		return hack
	}

	return backToBackAdvisory(in)
}

// backToBackLivePrices performs 4 parallel searches and returns a priced hack
// when 2x overlapping round-trips are cheaper than 2x one-ways. Returns
// (nil, false) when any search fails or when there are no savings.
func backToBackLivePrices(ctx context.Context, in DetectorInput) ([]Hack, bool) {
	origin := in.Origin
	dest := in.Destination
	departDate := in.Date
	returnDate := in.ReturnDate
	currency := in.currency()

	// Dummy return dates for the overlapping round-trips.
	rtFromOriginDummyReturn := addDays(departDate, backToBackOverlapDays)
	rtFromDestDummyReturn := addDays(returnDate, backToBackOverlapDays)
	if rtFromOriginDummyReturn == "" || rtFromDestDummyReturn == "" {
		return nil, false
	}

	client := batchexec.NewClient()

	var (
		owOutResult, owRetResult     *models.FlightSearchResult
		rtOriginResult, rtDestResult *models.FlightSearchResult
		owOutErr, owRetErr           error
		rtOriginErr, rtDestErr       error
		wg                           sync.WaitGroup
	)

	wg.Add(4)

	// 1. One-way: origin -> dest on depart_date
	go func() {
		defer wg.Done()
		owOutResult, owOutErr = backToBackSearchFunc(ctx, client, origin, dest, departDate, flights.SearchOptions{
			SortBy: models.SortCheapest,
		})
	}()

	// 2. One-way: dest -> origin on return_date
	go func() {
		defer wg.Done()
		owRetResult, owRetErr = backToBackSearchFunc(ctx, client, dest, origin, returnDate, flights.SearchOptions{
			SortBy: models.SortCheapest,
		})
	}()

	// 3. Overlapping RT from origin: origin->dest depart + dest->origin (depart+14d)
	go func() {
		defer wg.Done()
		rtOriginResult, rtOriginErr = backToBackSearchFunc(ctx, client, origin, dest, departDate, flights.SearchOptions{
			ReturnDate: rtFromOriginDummyReturn,
			SortBy:     models.SortCheapest,
		})
	}()

	// 4. Overlapping RT from dest: dest->origin return + origin->dest (return+14d)
	go func() {
		defer wg.Done()
		rtDestResult, rtDestErr = backToBackSearchFunc(ctx, client, dest, origin, returnDate, flights.SearchOptions{
			ReturnDate: rtFromDestDummyReturn,
			SortBy:     models.SortCheapest,
		})
	}()

	wg.Wait()

	// All 4 must succeed for a valid comparison.
	owOutPrice, owOutCur, _ := cheapestFlightInfo(owOutResult, owOutErr)
	owRetPrice, _, _ := cheapestFlightInfo(owRetResult, owRetErr)
	rtOriginPrice, rtCur, _ := cheapestFlightInfo(rtOriginResult, rtOriginErr)
	rtDestPrice, _, _ := cheapestFlightInfo(rtDestResult, rtDestErr)

	if owOutPrice <= 0 || owRetPrice <= 0 || rtOriginPrice <= 0 || rtDestPrice <= 0 {
		return nil, false
	}

	// Pick currency from whichever result provided one.
	if rtCur != "" {
		currency = rtCur
	} else if owOutCur != "" {
		currency = owOutCur
	}

	oneWayTotal := owOutPrice + owRetPrice
	rtTotal := rtOriginPrice + rtDestPrice
	savings := oneWayTotal - rtTotal

	if savings <= 0 || savings/oneWayTotal < comboMinSavingsRatio {
		return nil, true // searches succeeded but no savings — suppress advisory too
	}

	hack := Hack{
		Type:  "back_to_back",
		Title: "Back-to-back round-trips save vs one-ways",
		Description: fmt.Sprintf(
			"Two overlapping round-trips (%s %.0f + %.0f = %.0f) beat two one-ways "+
				"(%s %.0f + %.0f = %.0f). Saves %s %.0f (%.0f%%).",
			currency, rtOriginPrice, rtDestPrice, rtTotal,
			currency, owOutPrice, owRetPrice, oneWayTotal,
			currency, savings, savings/oneWayTotal*100,
		),
		Savings:  roundSavings(savings),
		Currency: currency,
		Steps: []string{
			fmt.Sprintf("Book round-trip A: %s->%s %s, return %s (%s %.0f) — use outbound only",
				origin, dest, departDate, rtFromOriginDummyReturn, currency, rtOriginPrice),
			fmt.Sprintf("Book round-trip B: %s->%s %s, return %s (%s %.0f) — use outbound only",
				dest, origin, returnDate, rtFromDestDummyReturn, currency, rtDestPrice),
			"Discard both return legs — the round-trip discount more than compensates",
		},
		Risks: []string{
			"Unused return legs waste carbon (ethical consideration)",
			"Airlines may flag accounts with systematic no-shows on returns",
			"Book on different booking references to avoid pattern detection",
		},
		Citations: []string{
			googleFlightsURL(dest, origin, departDate),
			googleFlightsURL(origin, dest, returnDate),
		},
	}

	return []Hack{hack}, true
}

// backToBackAdvisory returns the original advisory-only hack with no prices.
func backToBackAdvisory(in DetectorInput) []Hack {
	return []Hack{{
		Type:  "back_to_back",
		Title: "Frequent route? Back-to-back round-trips beat one-ways",
		Description: fmt.Sprintf(
			"If you travel %s↔%s regularly, buying two overlapping round-trips "+
				"(outbound trip 1 + outbound trip 2, discard both returns) is typically "+
				"20-40%% cheaper than individual one-ways because airlines discount returns.",
			in.Origin, in.Destination),
		Savings:  0, // advisory — no concrete savings estimate
		Currency: in.currency(),
		Steps: []string{
			fmt.Sprintf("For your next 2 trips %s→%s:", in.Origin, in.Destination),
			"Ticket A: round-trip starting from " + in.Origin + " (use outbound only)",
			"Ticket B: round-trip starting from " + in.Destination + " (use outbound only)",
			"Each ticket's return leg is unused — but the round-trip price is cheaper than one-way",
			"Also exploits Saturday-night-stay discounting if trips span weekends",
		},
		Risks: []string{
			"Unused return legs waste carbon (ethical consideration)",
			"Airlines may flag accounts with systematic no-shows on returns",
			"Book on different booking references to avoid pattern detection",
		},
	}}
}
