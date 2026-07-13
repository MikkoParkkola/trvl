package hacks

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/MikkoParkkola/trvl/internal/batchexec"
	"github.com/MikkoParkkola/trvl/internal/destinations"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/models"
)

// FlightComboInput configures the flight combination optimizer.
type FlightComboInput struct {
	Origin      string    // IATA code
	Destination string    // IATA code
	DepartDate  string    // YYYY-MM-DD (first/only trip)
	ReturnDate  string    // YYYY-MM-DD (first/only trip)
	Trips       []TripLeg // multiple trips (overrides DepartDate/ReturnDate if non-empty)
	Currency    string
}

// TripLeg represents one trip's dates.
type TripLeg struct {
	DepartDate string `json:"depart_date"` // YYYY-MM-DD
	ReturnDate string `json:"return_date"` // YYYY-MM-DD
}

// FlightComboResult is the optimizer output.
type FlightComboResult struct {
	Strategy     string         `json:"strategy"`      // "round_trip", "split_airlines", "nested_returns"
	Tickets      []TicketOption `json:"tickets"`       // tickets to purchase
	TotalCost    float64        `json:"total_cost"`    // optimized total
	BaselineCost float64        `json:"baseline_cost"` // naive approach cost
	Savings      float64        `json:"savings"`       // baseline - total
	Currency     string         `json:"currency"`      // currency code
}

// TicketOption describes one ticket to purchase.
type TicketOption struct {
	Type     string  `json:"type"`              // "round_trip", "one_way"
	Outbound string  `json:"outbound"`          // "HEL->BCN May 1"
	Return   string  `json:"return,omitempty"`  // "BCN->HEL May 8" (for RT)
	Airline  string  `json:"airline,omitempty"` // airline name if known
	Price    float64 `json:"price"`             // price
	Currency string  `json:"currency"`          // currency code
}

// maxComboTrips caps the number of trips to avoid combinatorial explosion.
const maxComboTrips = 4

// comboMinSavingsRatio is the minimum relative saving (5%) to flag a hack.
const comboMinSavingsRatio = 0.05

// DetectFlightCombo finds the cheapest way to purchase flights for one or more
// trips between the same origin and destination. It returns Hack results that
// can be displayed alongside other travel hacks.
func DetectFlightCombo(ctx context.Context, in FlightComboInput) []Hack {
	if in.Origin == "" || in.Destination == "" {
		return nil
	}

	trips := in.Trips
	if len(trips) == 0 {
		if in.DepartDate == "" || in.ReturnDate == "" {
			return nil
		}
		trips = []TripLeg{{DepartDate: in.DepartDate, ReturnDate: in.ReturnDate}}
	}
	if len(trips) == 0 {
		return nil
	}

	currency := strings.ToUpper(strings.TrimSpace(in.Currency))
	if currency == "" {
		currency = "EUR"
	}

	if len(trips) == 1 {
		return detectSingleTripCombo(ctx, in.Origin, in.Destination, trips[0], currency)
	}
	return detectMultiTripCombo(ctx, in.Origin, in.Destination, trips, currency)
}

// cheapestFlightInfo extracts the cheapest price, currency, and airline from a
// search result. Returns zero price on any error or empty result. Also returns
// the best FlightResult (or nil) so detectors that perform concrete searches
// (rail+fly) can capture the real itinerary for candidate injection without
// re-searching or fabricating.
func cheapestFlightInfo(result *models.FlightSearchResult, err error) (price float64, cur string, airline string, flight *models.FlightResult) {
	if err != nil || result == nil || !result.Success || len(result.Flights) == 0 {
		return 0, "", "", nil
	}
	best := result.Flights[0]
	for _, f := range result.Flights[1:] {
		if f.Price > 0 && (best.Price <= 0 || f.Price < best.Price) {
			best = f
		}
	}
	if best.Price <= 0 {
		return 0, "", "", nil
	}
	air := ""
	if len(best.Legs) > 0 {
		air = best.Legs[0].Airline
		if air == "" {
			air = best.Legs[0].AirlineCode
		}
	}
	return best.Price, best.Currency, air, &best
}

// cheapestFlightPriceInCurrency picks the cheapest flight via cheapestFlightInfo
// and converts its price into target. Returns (0, false) when there is no
// priced flight or its price cannot be converted into target — callers must
// suppress rather than show an unconverted or mislabelled figure. Shared by
// the currency-honesty fix across flight_combo.go and back_to_back.go.
func cheapestFlightPriceInCurrency(ctx context.Context, result *models.FlightSearchResult, err error, target string) (float64, bool) {
	price, cur, _, _ := cheapestFlightInfo(result, err)
	if price <= 0 {
		return 0, false
	}
	conv, gotCur := destinations.ConvertCurrency(ctx, price, cur, target)
	if gotCur != target {
		return 0, false
	}
	return conv, true
}

// detectSingleTripCombo compares a round-trip price against the sum of two
// one-way tickets (potentially on different airlines). currency is the
// requested display (target) currency; every price is converted into it and
// suppressed rather than shown unconverted when conversion fails.
func detectSingleTripCombo(ctx context.Context, origin, dest string, trip TripLeg, currency string) []Hack {
	client := batchexec.NewClient()

	var (
		rtResult, owOutResult, owRetResult *models.FlightSearchResult
		rtErr, owOutErr, owRetErr          error
		wg                                 sync.WaitGroup
	)

	// Search all 3 in parallel.
	wg.Add(3)
	go func() {
		defer wg.Done()
		rtResult, rtErr = flights.SearchFlightsWithClient(ctx, client, origin, dest, trip.DepartDate, flights.SearchOptions{
			ReturnDate: trip.ReturnDate,
			SortBy:     models.SortCheapest,
		})
	}()
	go func() {
		defer wg.Done()
		owOutResult, owOutErr = flights.SearchFlightsWithClient(ctx, client, origin, dest, trip.DepartDate, flights.SearchOptions{
			SortBy: models.SortCheapest,
		})
	}()
	go func() {
		defer wg.Done()
		owRetResult, owRetErr = flights.SearchFlightsWithClient(ctx, client, dest, origin, trip.ReturnDate, flights.SearchOptions{
			SortBy: models.SortCheapest,
		})
	}()
	wg.Wait()

	_, _, owOutAirline, _ := cheapestFlightInfo(owOutResult, owOutErr)
	_, _, owRetAirline, _ := cheapestFlightInfo(owRetResult, owRetErr)

	rtPrice, validRT := cheapestFlightPriceInCurrency(ctx, rtResult, rtErr, currency)
	owOutPrice, okOut := cheapestFlightPriceInCurrency(ctx, owOutResult, owOutErr, currency)
	owRetPrice, okRet := cheapestFlightPriceInCurrency(ctx, owRetResult, owRetErr, currency)
	validSplit := okOut && okRet

	// Need at least one valid strategy.
	if !validRT && !validSplit {
		return nil
	}

	splitTotal := owOutPrice + owRetPrice

	// Determine the better option and compute savings.
	if validRT && validSplit {
		savings := rtPrice - splitTotal
		if savings > 0 && savings/rtPrice >= comboMinSavingsRatio {
			// Split is cheaper.
			return []Hack{buildSplitHack(origin, dest, trip, currency, rtPrice, owOutPrice, owRetPrice, owOutAirline, owRetAirline)}
		}
		// RT is cheaper or equivalent — no hack to report.
		return nil
	}

	// If only split prices available, nothing to compare.
	return nil
}

// detectMultiTripCombo finds the optimal ticket assignment for multiple trips.
// currency is the requested display (target) currency; every price is
// converted into it and a trip whose price cannot be converted suppresses
// the whole comparison (baseline is required for every trip).
func detectMultiTripCombo(ctx context.Context, origin, dest string, trips []TripLeg, currency string) []Hack {
	if len(trips) > maxComboTrips {
		trips = trips[:maxComboTrips]
	}

	client := batchexec.NewClient()
	n := len(trips)

	// Step 1: get baseline (N separate round-trips).
	baselinePrices := make([]float64, n)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3) // limit concurrency

	for i, t := range trips {
		i, t := i, t
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			result, err := flights.SearchFlightsWithClient(ctx, client, origin, dest, t.DepartDate, flights.SearchOptions{
				ReturnDate: t.ReturnDate,
				SortBy:     models.SortCheapest,
			})
			p, ok := cheapestFlightPriceInCurrency(ctx, result, err, currency)
			if !ok {
				p = 0
			}
			baselinePrices[i] = p
		}()
	}
	wg.Wait()

	baseline := 0.0
	for _, p := range baselinePrices {
		if p <= 0 {
			return nil // can't compute baseline
		}
		baseline += p
	}

	// Step 2: try all permutations of pairing outbound[i] with return[perm[i]].
	perms := permutations(n)

	// Identity permutation = baseline; skip it.
	identity := make([]int, n)
	for i := range identity {
		identity[i] = i
	}

	type permResult struct {
		perm []int
		cost float64
	}

	// Search nested RT prices in parallel (only non-identity permutations).
	var mu sync.Mutex
	var bestPerm permResult
	bestPerm.cost = baseline

	var wg2 sync.WaitGroup
	for _, perm := range perms {
		if isIdentityPerm(perm) {
			continue
		}
		perm := perm
		wg2.Add(1)
		go func() {
			defer wg2.Done()

			totalCost := 0.0
			for i := 0; i < n; i++ {
				sem <- struct{}{}
				result, err := flights.SearchFlightsWithClient(ctx, client, origin, dest, trips[i].DepartDate, flights.SearchOptions{
					ReturnDate: trips[perm[i]].ReturnDate,
					SortBy:     models.SortCheapest,
				})
				<-sem

				p, ok := cheapestFlightPriceInCurrency(ctx, result, err, currency)
				if !ok {
					return // this permutation is invalid
				}
				totalCost += p
			}

			mu.Lock()
			if totalCost < bestPerm.cost {
				bestPerm = permResult{perm: perm, cost: totalCost}
			}
			mu.Unlock()
		}()
	}
	wg2.Wait()

	if bestPerm.perm == nil {
		return nil // no permutation beat baseline
	}

	savings := baseline - bestPerm.cost
	if savings <= 0 || savings/baseline < comboMinSavingsRatio {
		return nil
	}

	return []Hack{buildNestedHack(origin, dest, trips, bestPerm.perm, currency, baseline, bestPerm.cost, savings)}
}

// buildSplitHack creates a Hack for split-airline single trip.
func buildSplitHack(origin, dest string, trip TripLeg, currency string, rtPrice, owOutPrice, owRetPrice float64, owOutAirline, owRetAirline string) Hack {
	savings := rtPrice - (owOutPrice + owRetPrice)

	outDesc := fmt.Sprintf("%s->%s %s", origin, dest, trip.DepartDate)
	retDesc := fmt.Sprintf("%s->%s %s", dest, origin, trip.ReturnDate)

	if owOutAirline != "" {
		outDesc += " (" + owOutAirline + ")"
	}
	if owRetAirline != "" {
		retDesc += " (" + owRetAirline + ")"
	}

	return Hack{
		Type:     "flight_combo",
		Title:    "Flight combination optimizer: split airlines",
		Currency: currency,
		Savings:  roundSavings(savings),
		Description: fmt.Sprintf(
			"Two one-way tickets (%s %.0f out + %.0f return = %.0f) beat round-trip %.0f. Saves %s %.0f (%.0f%%).",
			currency, owOutPrice, owRetPrice, owOutPrice+owRetPrice, rtPrice,
			currency, savings, savings/rtPrice*100,
		),
		Risks: []string{
			"Separate tickets = separate contracts; no rebooking if outbound delays",
			"Check baggage policies differ between airlines",
			"Book both tickets simultaneously to lock in prices",
		},
		Steps: []string{
			fmt.Sprintf("Book one-way %s (%s %.0f)", outDesc, currency, owOutPrice),
			fmt.Sprintf("Book one-way %s (%s %.0f)", retDesc, currency, owRetPrice),
		},
		Citations: []string{
			googleFlightsURL(dest, origin, trip.DepartDate),
			googleFlightsURL(origin, dest, trip.ReturnDate),
		},
	}
}

// buildNestedHack creates a Hack for nested-return multi-trip optimization.
func buildNestedHack(origin, dest string, trips []TripLeg, perm []int, currency string, baseline, totalCost, savings float64) Hack {
	n := len(trips)

	steps := make([]string, 0, n+1)
	steps = append(steps, fmt.Sprintf("Instead of %d separate round-trips (%s %.0f), book %d nested round-trips (%s %.0f):",
		n, currency, baseline, n, currency, totalCost))

	for i := 0; i < n; i++ {
		steps = append(steps, fmt.Sprintf(
			"Ticket %d (RT): %s->%s %s, %s->%s %s",
			i+1,
			origin, dest, trips[i].DepartDate,
			dest, origin, trips[perm[i]].ReturnDate,
		))
	}

	desc := fmt.Sprintf(
		"Nested round-trip tickets save %s %.0f (%.0f%%) vs %d separate round-trips. "+
			"Each ticket uses an outbound from one trip and a return from another, "+
			"exploiting cheaper long-stay round-trip pricing.",
		currency, savings, savings/baseline*100, n,
	)

	citations := make([]string, 0, n)
	for i := 0; i < n; i++ {
		citations = append(citations, fmt.Sprintf(
			"https://www.google.com/travel/flights?q=Flights+to+%s+from+%s+on+%s+return+%s",
			dest, origin, trips[i].DepartDate, trips[perm[i]].ReturnDate,
		))
	}

	return Hack{
		Type:        "flight_combo",
		Title:       "Flight combination optimizer: nested returns",
		Currency:    currency,
		Savings:     roundSavings(savings),
		Description: desc,
		Risks: []string{
			"Must use all tickets; missing one leg may void the return",
			"Airlines may flag unusual booking patterns",
			"If any trip dates change, the nested pairing may no longer work",
			"Some airlines void tickets when legs are skipped",
		},
		Steps:     steps,
		Citations: citations,
	}
}

// permutations generates all permutations of [0..n-1] using Heap's algorithm.
// Returns nil for n <= 0. Caps at n=4 (24 permutations).
func permutations(n int) [][]int {
	if n <= 0 || n > maxComboTrips {
		return nil
	}

	var result [][]int
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}

	// Heap's algorithm (iterative).
	c := make([]int, n)
	snapshot := make([]int, n)
	copy(snapshot, perm)
	result = append(result, snapshot)

	i := 0
	for i < n {
		if c[i] < i {
			if i%2 == 0 {
				perm[0], perm[i] = perm[i], perm[0]
			} else {
				perm[c[i]], perm[i] = perm[i], perm[c[i]]
			}
			snapshot := make([]int, n)
			copy(snapshot, perm)
			result = append(result, snapshot)
			c[i]++
			i = 0
		} else {
			c[i] = 0
			i++
		}
	}

	return result
}

// isIdentityPerm returns true if perm == [0, 1, 2, ...].
func isIdentityPerm(perm []int) bool {
	for i, v := range perm {
		if v != i {
			return false
		}
	}
	return true
}

// sortTrips sorts trips by departure date for consistent ordering.
// This is exported for use in MCP handlers.
func sortTrips(trips []TripLeg) {
	sort.Slice(trips, func(i, j int) bool {
		return trips[i].DepartDate < trips[j].DepartDate
	})
}
