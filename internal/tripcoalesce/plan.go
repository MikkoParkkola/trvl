package tripcoalesce

import (
	"context"
	"fmt"
	"time"

	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/ground"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"golang.org/x/sync/errgroup"
)

// domainResult is the self-contained outcome of one domain goroutine. Each
// goroutine writes to its own slot (no shared mutable state) so the fan-out is
// race-free without locks, mirroring internal/flights/concurrency.go.
type domainResult struct {
	status DomainStatus
	flight *models.FlightSearchResult
	hotel  *models.HotelSearchResult
	ground *models.GroundSearchResult
}

// Plan issues the flight, hotel, and ground searches concurrently through a
// bounded errgroup, isolates per-domain failures and timeouts, and assembles
// one combined TripPlan. A single domain failing yields a partial plan with the
// other domains intact — it never aborts the peers (the group func always
// returns nil, so errgroup is used purely for bounded concurrency).
func (c *Coalescer) Plan(ctx context.Context, p Params) *TripPlan {
	plan := &TripPlan{
		Origin:      p.Origin,
		Destination: p.Destination,
		DepartDate:  p.DepartDate,
		ReturnDate:  p.ReturnDate,
		Currency:    p.currency(),
	}

	clock := c.now
	if clock == nil {
		clock = time.Now
	}
	timeout := c.PerDomainTimeout
	if timeout <= 0 {
		timeout = defaultPerDomainTimeout
	}
	limit := c.Concurrency
	if limit <= 0 {
		limit = defaultDomainConcurrency
	}

	// Three independent domain tasks. Each returns a self-contained result and
	// never mutates caller state, so they are safe to run concurrently.
	tasks := []struct {
		domain string
		run    func(ctx context.Context) domainResult
	}{
		{"flights", func(ctx context.Context) domainResult { return c.runFlights(ctx, p) }},
		{"hotels", func(ctx context.Context) domainResult { return c.runHotels(ctx, p) }},
		{"ground", func(ctx context.Context) domainResult { return c.runGround(ctx, p) }},
	}

	results := make([]domainResult, len(tasks))

	g := new(errgroup.Group)
	g.SetLimit(limit)
	for i, task := range tasks {
		i, task := i, task
		g.Go(func() error {
			start := clock()
			tctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			done := make(chan domainResult, 1)
			go func() { done <- task.run(tctx) }()

			select {
			case res := <-done:
				res.status.Domain = task.domain
				res.status.ElapsedMs = clock().Sub(start).Milliseconds()
				results[i] = res
			case <-tctx.Done():
				// The domain exceeded its slice of the budget. Record a typed
				// timeout outcome rather than a fabricated empty; the orphaned
				// goroutine drains into the buffered channel and exits.
				results[i] = domainResult{status: DomainStatus{
					Domain:    task.domain,
					OK:        false,
					Error:     tctx.Err().Error(),
					ElapsedMs: clock().Sub(start).Milliseconds(),
				}}
			}
			// Always nil: one domain's failure must not cancel its peers.
			return nil
		})
	}
	_ = g.Wait()

	assemble(plan, results, p.Nights)
	return plan
}

func (c *Coalescer) runFlights(ctx context.Context, p Params) domainResult {
	res, err := c.FlightSearch(ctx, p.Origin, p.Destination, p.DepartDate, flights.SearchOptions{
		ReturnDate: p.ReturnDate,
		Adults:     p.travelers(),
		Currency:   p.currency(),
	})
	st := DomainStatus{Domain: "flights"}
	if err != nil {
		st.Error = err.Error()
		return domainResult{status: st, flight: res}
	}
	if res != nil {
		st.OK = true
		st.Count = len(res.Flights)
		if res.Error != "" {
			st.Error = res.Error
		}
	}
	return domainResult{status: st, flight: res}
}

func (c *Coalescer) runHotels(ctx context.Context, p Params) domainResult {
	checkIn := p.CheckIn
	if checkIn == "" {
		checkIn = p.DepartDate
	}
	checkOut := p.CheckOut
	if checkOut == "" {
		checkOut = p.ReturnDate
	}
	res, err := c.HotelSearch(ctx, p.hotelLocation(), hotels.HotelSearchOptions{
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Guests:   p.travelers(),
		Currency: p.currency(),
	})
	st := DomainStatus{Domain: "hotels"}
	if err != nil {
		st.Error = err.Error()
		return domainResult{status: st, hotel: res}
	}
	if res != nil {
		st.OK = true
		st.Count = len(res.Hotels)
		if res.Error != "" {
			st.Error = res.Error
		}
	}
	return domainResult{status: st, hotel: res}
}

func (c *Coalescer) runGround(ctx context.Context, p Params) domainResult {
	res, err := c.GroundSearch(ctx, p.groundFrom(), p.groundTo(), p.DepartDate, ground.SearchOptions{
		Currency:              p.currency(),
		AllowBrowserFallbacks: p.AllowBrowserFallbacks,
	})
	st := DomainStatus{Domain: "ground"}
	if err != nil {
		st.Error = err.Error()
		return domainResult{status: st, ground: res}
	}
	if res != nil {
		st.OK = true
		st.Count = len(res.Routes)
		if res.Error != "" {
			st.Error = res.Error
		}
	}
	return domainResult{status: st, ground: res}
}

// assemble folds the per-domain results into the plan: attaches raw results,
// picks the cheapest priced option per domain, and sums them into a floor
// total-cost estimate with an explicit breakdown.
func assemble(plan *TripPlan, results []domainResult, nights int) {
	for _, r := range results {
		plan.Statuses = append(plan.Statuses, r.status)
		switch r.status.Domain {
		case "flights":
			plan.Flights = r.flight
		case "hotels":
			plan.Hotels = r.hotel
		case "ground":
			plan.Ground = r.ground
		}
		if r.status.Error != "" {
			plan.Notes = append(plan.Notes, fmt.Sprintf("%s: %s", r.status.Domain, r.status.Error))
		}
	}

	if plan.Flights != nil {
		if f := cheapestFlight(plan.Flights.Flights); f != nil {
			plan.CheapestFlight = f
			plan.addCost("flights", "cheapest flight", f.Price, f.Currency)
		}
	}
	if plan.Hotels != nil {
		if h := cheapestHotel(plan.Hotels.Hotels); h != nil {
			plan.CheapestHotel = h
			if nights > 0 {
				plan.addCost("hotels", fmt.Sprintf("cheapest stay (%d nights)", nights), h.Price*float64(nights), h.Currency)
			} else {
				plan.addCost("hotels", "cheapest stay (per night)", h.Price, h.Currency)
			}
		}
	}
	if plan.Ground != nil {
		if r := cheapestGround(plan.Ground.Routes); r != nil {
			plan.CheapestGround = r
			plan.addCost("ground", "cheapest ground route", r.Price, r.Currency)
		}
	}
}

func (plan *TripPlan) addCost(domain, label string, amount float64, currency string) {
	if amount <= 0 {
		return
	}
	cur := currency
	if cur == "" {
		cur = plan.Currency
	}
	plan.CostBreakdown = append(plan.CostBreakdown, CostComponent{
		Domain:   domain,
		Label:    label,
		Amount:   amount,
		Currency: cur,
	})
	// Only same-currency components are summed into the headline floor; mixed
	// currencies stay itemised so we never fabricate a cross-currency total.
	if cur == plan.Currency {
		plan.TotalCostEstimate += amount
	}
}

func cheapestFlight(fs []models.FlightResult) *models.FlightResult {
	var best *models.FlightResult
	for i := range fs {
		if fs[i].Price <= 0 {
			continue
		}
		if best == nil || fs[i].Price < best.Price {
			best = &fs[i]
		}
	}
	return best
}

func cheapestHotel(hs []models.HotelResult) *models.HotelResult {
	var best *models.HotelResult
	for i := range hs {
		if hs[i].Price <= 0 {
			continue
		}
		if best == nil || hs[i].Price < best.Price {
			best = &hs[i]
		}
	}
	return best
}

func cheapestGround(rs []models.GroundRoute) *models.GroundRoute {
	var best *models.GroundRoute
	for i := range rs {
		if rs[i].Price <= 0 {
			continue
		}
		if best == nil || rs[i].Price < best.Price {
			best = &rs[i]
		}
	}
	return best
}
