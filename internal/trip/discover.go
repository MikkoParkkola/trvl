// Package trip — inverted search: give me a budget and a time range,
// I return the best quality-per-euro trips that fit my preferences.
//
// This is the trip you'd have if every other tool understood that travelers
// don't start with a destination: they start with a budget, a calendar gap,
// and a vague idea of what they want out of it.
package trip

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/explore"
	"github.com/MikkoParkkola/trvl/internal/flights"
	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/match"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
	"github.com/MikkoParkkola/trvl/internal/scoring"
)

// DiscoverOptions configures an inverted-search discovery.
type DiscoverOptions struct {
	Origin    string  // IATA (default: first home_airport from prefs)
	From      string  // earliest depart date YYYY-MM-DD (required)
	Until     string  // latest return date YYYY-MM-DD (required)
	Budget    float64 // max total EUR (required)
	MinNights int     // default 2
	MaxNights int     // default 4
	Top       int     // results to return (default 5)
	FlexDays  int     // how many start-of-trip candidates to enumerate (default: all Fridays in window)
}

// DiscoverResult is a single ranked trip option.
type DiscoverResult struct {
	Destination        string             `json:"destination"`
	AirportCode        string             `json:"airport_code"`
	DepartDate         string             `json:"depart_date"`
	ReturnDate         string             `json:"return_date"`
	Nights             int                `json:"nights"`
	FlightPrice        float64            `json:"flight_price"`
	HotelPrice         float64            `json:"hotel_price"`
	HotelName          string             `json:"hotel_name"`
	HotelRating        float64            `json:"hotel_rating"`
	Total              float64            `json:"total"`
	Currency           string             `json:"currency"`
	ProfileMatch       int                `json:"profile_match"`                  // 0–100; replaces ValueScore
	MatchBreakdown     map[string]float64 `json:"match_breakdown,omitempty"`      // per-factor scores in [0,1]
	RequestMatch       int                `json:"request_match,omitempty"`        // 0–100; literal-request match
	RequestMatchReason string             `json:"request_match_reason,omitempty"` // dominant penalty axis
	BudgetSlack        float64            `json:"budget_slack"`                   // currency units remaining
	Reasoning          string             `json:"reasoning,omitempty"`
}

// DiscoverOutput is the top-level response.
type DiscoverOutput struct {
	Success bool             `json:"success"`
	Origin  string           `json:"origin"`
	From    string           `json:"from"`
	Until   string           `json:"until"`
	Budget  float64          `json:"budget"`
	Count   int              `json:"count"`
	Trips   []DiscoverResult `json:"trips"`
	Error   string           `json:"error,omitempty"`
}

func (o *DiscoverOptions) applyDefaults() {
	if o.MinNights <= 0 {
		o.MinNights = 2
	}
	if o.MaxNights <= 0 {
		o.MaxNights = 4
	}
	if o.MaxNights < o.MinNights {
		o.MaxNights = o.MinNights
	}
	if o.Top <= 0 {
		o.Top = 5
	}
}

// Discover finds the best-quality trips that fit within a budget and a
// flexible date window, applying user preferences.
//
// The strategy is "explore-then-verify": query Google's explore API for each
// candidate weekend to get cheapest destinations, then search real hotel
// prices for top candidates with preferences applied. Results are ranked by
// ProfileMatch score (0-100) — how well each trip matches the user's full
// travel profile.
func Discover(ctx context.Context, opts DiscoverOptions) (*DiscoverOutput, error) {
	opts.applyDefaults()

	if opts.Budget <= 0 {
		return nil, fmt.Errorf("budget must be > 0")
	}
	if opts.From == "" || opts.Until == "" {
		return nil, fmt.Errorf("from and until dates are required")
	}
	if opts.Origin == "" {
		return nil, fmt.Errorf("origin is required (or set home_airports in preferences)")
	}

	fromDate, err := models.ParseDate(opts.From)
	if err != nil {
		return nil, fmt.Errorf("invalid from date: %w", err)
	}
	untilDate, err := models.ParseDate(opts.Until)
	if err != nil {
		return nil, fmt.Errorf("invalid until date: %w", err)
	}
	if untilDate.Before(fromDate) {
		return nil, fmt.Errorf("until must be after from")
	}

	// Enumerate candidate trip windows: every Friday in [from, until]
	// with nights = MinNights..MaxNights.
	var windows []candidateWindow
	for d := fromDate; !d.After(untilDate); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Friday {
			continue
		}
		for nights := opts.MinNights; nights <= opts.MaxNights; nights++ {
			end := d.AddDate(0, 0, nights)
			if end.After(untilDate) {
				continue
			}
			windows = append(windows, candidateWindow{start: d, end: end, nights: nights})
		}
	}
	if len(windows) == 0 {
		return &DiscoverOutput{Success: true, Origin: opts.Origin, From: opts.From, Until: opts.Until, Budget: opts.Budget}, nil
	}

	prefs, _ := preferences.Load()
	client := flights.DefaultClient()

	// Phase 1: for each candidate window, run an explore query to find
	// cheap destinations. Bounded concurrency (3 parallel explore calls).
	type exploreFinding struct {
		window candidateWindow
		dests  []models.ExploreDestination
	}
	var exploreMu sync.Mutex
	var findings []exploreFinding

	exploreSem := make(chan struct{}, 3)
	var exploreWg sync.WaitGroup

	for _, w := range windows {
		w := w
		exploreWg.Add(1)
		go func() {
			defer exploreWg.Done()
			exploreSem <- struct{}{}
			defer func() { <-exploreSem }()

			res, err := explore.SearchExplore(ctx, client, opts.Origin, explore.ExploreOptions{
				DepartureDate: w.start.Format("2006-01-02"),
				ReturnDate:    w.end.Format("2006-01-02"),
				Adults:        1,
			})
			if err != nil || res == nil || len(res.Destinations) == 0 {
				return
			}

			// Sort by flight price, keep top 5 per window to bound hotel searches.
			dests := res.Destinations
			sort.Slice(dests, func(i, j int) bool { return dests[i].Price < dests[j].Price })

			// Drop destinations whose flight price exceeds the user's budget.
			if prefs != nil && prefs.BudgetFlightMax > 0 {
				filtered := dests[:0]
				for _, d := range dests {
					if d.Price <= prefs.BudgetFlightMax {
						filtered = append(filtered, d)
					}
				}
				dests = filtered
			}

			if len(dests) > 5 {
				dests = dests[:5]
			}

			exploreMu.Lock()
			findings = append(findings, exploreFinding{window: w, dests: dests})
			exploreMu.Unlock()
		}()
	}
	exploreWg.Wait()

	if len(findings) == 0 {
		return &DiscoverOutput{Success: true, Origin: opts.Origin, From: opts.From, Until: opts.Until, Budget: opts.Budget}, nil
	}

	// Detect currency once (explore API doesn't label prices).
	currency := "EUR"
	for _, f := range findings {
		if len(f.dests) > 0 {
			if detected := flights.DetectSourceCurrency(ctx, opts.Origin, f.dests[0].AirportCode); detected != "" {
				currency = detected
			}
			break
		}
	}

	// Phase 2: for each (window, destination) candidate, search real hotels
	// with user preferences applied. Bounded concurrency (5 parallel).
	// Dedupe by airport code + nights — multiple explore responses may return
	// the same destination on different dates, but we only need to cost the
	// cheapest window per destination.
	bestPerKey := make(map[discoverTrialKey]*discoverTrial)
	for i := range findings {
		f := &findings[i]
		for _, d := range f.dests {
			k := discoverTrialKey{airport: d.AirportCode, nights: f.window.nights}
			if existing, ok := bestPerKey[k]; !ok || d.Price < existing.dest.Price {
				bestPerKey[k] = &discoverTrial{window: f.window, dest: d}
			}
		}
	}

	var trials []discoverTrial
	for _, t := range bestPerKey {
		trials = append(trials, *t)
	}

	hotelMu := sync.Mutex{}
	hotelResults := make(map[discoverTrialKey]*discoverHotelInfo)

	hotelSem := make(chan struct{}, 3) // reduced from 5: each hotel search fetches ~1MB HTML
	var hotelWg sync.WaitGroup

	for _, t := range trials {
		t := t
		hotelWg.Add(1)
		go func() {
			defer hotelWg.Done()
			hotelSem <- struct{}{}
			defer func() { <-hotelSem }()

			cityName := t.dest.CityName
			if cityName == "" {
				cityName = models.LookupAirportName(t.dest.AirportCode)
			}

			hotelOpts := hotels.HotelSearchOptions{
				CheckIn:  t.window.start.Format("2006-01-02"),
				CheckOut: t.window.end.Format("2006-01-02"),
				Guests:   1,
				Sort:     "cheapest",
				MaxPages: 1, // single page: compound command only needs cheapest
			}
			if prefs != nil {
				if prefs.MinHotelStars > 0 {
					hotelOpts.Stars = prefs.MinHotelStars
				}
				if prefs.MinHotelRating > 0 {
					hotelOpts.MinRating = prefs.MinHotelRating
				}
				if prefs.BudgetPerNightMax > 0 {
					hotelOpts.MaxPrice = prefs.BudgetPerNightMax
				}
			}

			hr, err := hotels.SearchHotels(ctx, cityName, hotelOpts)
			if err != nil || hr == nil || !hr.Success || len(hr.Hotels) == 0 {
				return
			}

			filtered := hr.Hotels
			if prefs != nil {
				filtered = preferences.FilterHotels(filtered, cityName, prefs)
			}
			if len(filtered) == 0 {
				return
			}

			// Pick cheapest with rating >= preference minimum (already filtered).
			cheapest := filtered[0]
			for _, h := range filtered[1:] {
				if h.Price > 0 && h.Price < cheapest.Price {
					cheapest = h
				}
			}
			if cheapest.Price <= 0 {
				return
			}

			hotelMu.Lock()
			hotelResults[discoverTrialKey{airport: t.dest.AirportCode, nights: t.window.nights}] = &discoverHotelInfo{
				price:  cheapest.Price,
				total:  cheapest.Price * float64(t.window.nights),
				name:   cheapest.Name,
				rating: cheapest.Rating,
			}
			hotelMu.Unlock()
		}()
	}
	hotelWg.Wait()

	results := rankDiscoverTrials(trials, hotelResults, opts.Budget, currency, opts.Top,
		buildDiscoverMatchRequest(opts, fromDate, untilDate, currency))

	return &DiscoverOutput{
		Success: true,
		Origin:  opts.Origin,
		From:    opts.From,
		Until:   opts.Until,
		Budget:  opts.Budget,
		Count:   len(results),
		Trips:   results,
	}, nil
}

// candidateWindow is a departure/return date pair with a night count.
type candidateWindow struct {
	start  time.Time
	end    time.Time
	nights int
}

// discoverTrialKey identifies a unique (airport, nights) combination.
type discoverTrialKey struct {
	airport string
	nights  int
}

// discoverTrial pairs an explore destination with a candidate window.
type discoverTrial struct {
	window candidateWindow
	dest   models.ExploreDestination
}

// discoverHotelInfo holds the cheapest hotel for a trial key.
type discoverHotelInfo struct {
	price  float64
	total  float64
	name   string
	rating float64
}

// buildDiscoverMatchRequest converts DiscoverOptions and a date window into a
// match.Request so that each candidate trip can be scored for literal-request
// fit via match.Compute.
func buildDiscoverMatchRequest(opts DiscoverOptions, from, until time.Time, currency string) match.Request {
	nights := (opts.MinNights + opts.MaxNights) / 2
	if nights < 1 {
		nights = 1
	}
	halfWindow := int(until.Sub(from).Hours()/24) / 2
	if halfWindow < 0 {
		halfWindow = 0
	}
	mid := from.AddDate(0, 0, halfWindow)
	return match.Request{
		OriginIATA:       opts.Origin,
		DepartDateCenter: mid.Format("2006-01-02"),
		FlexDays:         halfWindow,
		DateWindowDays:   halfWindow * 2,
		Nights:           nights,
		MaxNightsDrift:   (opts.MaxNights - opts.MinNights) / 2,
		Currency:         currency,
	}
}

// rankDiscoverTrials scores and ranks discover candidates.
//
// Each candidate receives a RequestMatch score (0–100) measuring how closely
// the literal request was satisfied, and a ProfileMatch score (0–100) from the
// user's preference profile. Results are sorted by RequestMatch descending.
func rankDiscoverTrials(trials []discoverTrial, hotelResults map[discoverTrialKey]*discoverHotelInfo, budget float64, currency string, top int, matchReq match.Request) []DiscoverResult {
	var results []DiscoverResult
	for _, t := range trials {
		k := discoverTrialKey{airport: t.dest.AirportCode, nights: t.window.nights}
		h, ok := hotelResults[k]
		if !ok {
			continue
		}

		total := t.dest.Price + h.total
		if total > budget {
			continue
		}

		slack := budget - total

		cityName := t.dest.CityName
		if cityName == "" {
			cityName = models.LookupAirportName(t.dest.AirportCode)
		}

		input := scoring.DiscoverInput{
			AirportCode: t.dest.AirportCode,
			CityName:    cityName,
			FlightPrice: t.dest.Price,
			HotelPrice:  h.total,
			Total:       total,
			Budget:      budget,
			HotelRating: h.rating,
			HotelName:   h.name,
		}

		matchScore, breakdown := scoring.ComputeProfileMatch(nil, input)
		reasoning := buildDiscoverReasoning(h.rating, slack, currency)

		offered := match.Offered{
			OriginIATA: matchReq.OriginIATA,
			DestIATA:   t.dest.AirportCode,
			DepartDate: t.window.start.Format("2006-01-02"),
			ReturnDate: t.window.end.Format("2006-01-02"),
			Currency:   currency,
		}
		reqScore := match.Compute(matchReq, offered)

		results = append(results, DiscoverResult{
			Destination:        cityName,
			AirportCode:        t.dest.AirportCode,
			DepartDate:         t.window.start.Format("2006-01-02"),
			ReturnDate:         t.window.end.Format("2006-01-02"),
			Nights:             t.window.nights,
			FlightPrice:        t.dest.Price,
			HotelPrice:         h.total,
			HotelName:          h.name,
			HotelRating:        h.rating,
			Total:              total,
			Currency:           currency,
			ProfileMatch:       matchScore,
			MatchBreakdown:     breakdown,
			RequestMatch:       reqScore.Total,
			RequestMatchReason: matchReasonForDisplay(reqScore.Reason),
			BudgetSlack:        slack,
			Reasoning:          reasoning,
		})
	}

	// Rank by request match descending; break ties with profile match.
	sort.Slice(results, func(i, j int) bool {
		if results[i].RequestMatch != results[j].RequestMatch {
			return results[i].RequestMatch > results[j].RequestMatch
		}
		return results[i].ProfileMatch > results[j].ProfileMatch
	})
	if len(results) > top {
		results = results[:top]
	}

	return results
}

func buildDiscoverReasoning(rating, slack float64, currency string) string {
	var parts []string
	if rating > 0 {
		parts = append(parts, fmt.Sprintf("%.1f★ hotel", rating))
	}
	if slack > 0 {
		parts = append(parts, fmt.Sprintf("%s %.0f under budget", currency, slack))
	}
	return strings.Join(parts, ", ")
}

// matchReasonForDisplay converts the match package's internal "exact_match"
// sentinel to an empty string — UIs and tests should show no reason when the
// match is perfect.
func matchReasonForDisplay(reason string) string {
	if reason == "exact_match" {
		return ""
	}
	return reason
}
