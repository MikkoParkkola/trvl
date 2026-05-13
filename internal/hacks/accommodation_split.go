package hacks

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/MikkoParkkola/trvl/internal/hotels"
	"github.com/MikkoParkkola/trvl/internal/models"
	"github.com/MikkoParkkola/trvl/internal/preferences"
)

// AccommodationSplitInput is the input for the accommodation split detector.
type AccommodationSplitInput struct {
	City      string // "Prague", "Amsterdam"
	CheckIn   string // YYYY-MM-DD
	CheckOut  string // YYYY-MM-DD
	Currency  string // "EUR"
	MaxSplits int    // max properties to split across (default 3, min 2)
	Guests    int    // number of guests (default 2)
}

// movingCostEUR is the fixed friction cost (taxi/transit) per hotel move.
const movingCostEUR = 15.0

// minSavingsEUR is the minimum net saving required to flag a split.
const minSavingsEUR = 50.0

// minSavingsRatio is the minimum proportional saving (15%) required.
const minSavingsRatio = 0.85

// minSegmentNights is the minimum nights per segment (1-night stays add too
// much friction).
const minSegmentNights = 2

// DetectAccommodationSplit finds opportunities to save money by splitting a
// long hotel stay across multiple properties.
//
// It searches the baseline single-property cost, then evaluates 2-way and
// (optionally) 3-way splits, returning the best split that beats the baseline
// by at least 15% and saves at least EUR 50 after accounting for moving costs.
func DetectAccommodationSplit(ctx context.Context, in AccommodationSplitInput) []Hack {
	// Normalise inputs.
	if in.City == "" || in.CheckIn == "" || in.CheckOut == "" {
		return nil
	}
	if in.MaxSplits < 2 {
		in.MaxSplits = 3
	}
	if in.Guests <= 0 {
		in.Guests = 2
	}
	currency := in.Currency
	if currency == "" {
		currency = "EUR"
	}

	checkIn, err := parseDate(in.CheckIn)
	if err != nil {
		return nil
	}
	checkOut, err := parseDate(in.CheckOut)
	if err != nil {
		return nil
	}
	totalNights := int(checkOut.Sub(checkIn).Hours() / 24)
	if totalNights < minSegmentNights*2 {
		// Not enough nights for any valid split.
		return nil
	}

	// Load user preferences for hotel filtering.
	prefs, _ := preferences.Load()

	// 1. Baseline: single stay for the full duration.
	baseline := searchBestHotel(ctx, in.City, in.CheckIn, in.CheckOut, in.Guests, currency, prefs)
	if baseline == nil || baseline.Price <= 0 {
		return nil
	}
	baselineTotal := baseline.Price * float64(totalNights)

	// 2. Find the best split (2-way first, 3-way if MaxSplits >= 3).
	best := findBestSplit(ctx, in, checkIn, totalNights, currency, baselineTotal, prefs)
	if best == nil {
		return nil
	}
	return []Hack{*best}
}

// splitSegment describes one segment of a split stay.
type splitSegment struct {
	Hotel     *models.HotelResult
	CheckIn   string
	CheckOut  string
	Nights    int
	TotalCost float64
}

// findBestSplit tries all 2-way and (if allowed) 3-way splits, returning the
// best hack, or nil if no split beats the baseline.
func findBestSplit(
	ctx context.Context,
	in AccommodationSplitInput,
	checkIn time.Time,
	totalNights int,
	currency string,
	baselineTotal float64,
	prefs *preferences.Preferences,
) *Hack {
	type candidate struct {
		segments  []splitSegment
		totalCost float64
		moves     int
	}

	var mu sync.Mutex
	var best *candidate

	type job struct {
		splitPoints []int // day indices where we switch hotels
	}

	var jobs []job

	// 2-way splits: one split point N where both segments >= minSegmentNights.
	for n := minSegmentNights; n <= totalNights-minSegmentNights; n++ {
		jobs = append(jobs, job{splitPoints: []int{n}})
	}

	// 3-way splits: two split points (n1, n2) where each segment >= minSegmentNights.
	if in.MaxSplits >= 3 {
		for n1 := minSegmentNights; n1 <= totalNights-2*minSegmentNights; n1++ {
			for n2 := n1 + minSegmentNights; n2 <= totalNights-minSegmentNights; n2++ {
				jobs = append(jobs, job{splitPoints: []int{n1, n2}})
			}
		}
	}

	// Cap concurrency to avoid overwhelming the search API.
	const maxConcurrent = 6
	sem := make(chan struct{}, maxConcurrent)
	var wg sync.WaitGroup

	for _, j := range jobs {
		j := j
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			segments, ok := evaluateSplit(ctx, in.City, in.CheckIn, checkIn, j.splitPoints, totalNights, in.Guests, currency, prefs)
			if !ok {
				return
			}

			moves := len(j.splitPoints)
			movingCost := float64(moves) * movingCostEUR
			totalCost := 0.0
			for _, s := range segments {
				totalCost += s.TotalCost
			}
			totalCost += movingCost

			mu.Lock()
			defer mu.Unlock()
			if best == nil || totalCost < best.totalCost {
				best = &candidate{segments: segments, totalCost: totalCost, moves: moves}
			}
		}()
	}

	wg.Wait()

	if best == nil {
		return nil
	}

	// Net savings after moving costs are already baked into best.totalCost.
	netSavings := baselineTotal - best.totalCost
	if netSavings < minSavingsEUR {
		return nil
	}
	if best.totalCost > baselineTotal*minSavingsRatio {
		return nil
	}

	return buildAccommodationHack(in.City, best.segments, best.moves, netSavings, baselineTotal, currency)
}

// evaluateSplit searches for the cheapest hotel for each segment defined by
// splitPoints, returning segments and true on success.
func evaluateSplit(
	ctx context.Context,
	city, baseCheckIn string,
	checkInTime time.Time,
	splitPoints []int,
	totalNights int,
	guests int,
	currency string,
	prefs *preferences.Preferences,
) ([]splitSegment, bool) {
	// Build segment date pairs.
	boundaries := append([]int{0}, splitPoints...)
	boundaries = append(boundaries, totalNights)

	type segDates struct {
		checkIn  string
		checkOut string
		nights   int
	}
	var dates []segDates
	for i := 0; i < len(boundaries)-1; i++ {
		segCheckIn := addDays(baseCheckIn, boundaries[i])
		segCheckOut := addDays(baseCheckIn, boundaries[i+1])
		nights := boundaries[i+1] - boundaries[i]
		if segCheckIn == "" || segCheckOut == "" || nights < minSegmentNights {
			return nil, false
		}
		dates = append(dates, segDates{checkIn: segCheckIn, checkOut: segCheckOut, nights: nights})
	}

	// Search all segments in parallel.
	type result struct {
		idx     int
		segment *splitSegment
	}
	results := make(chan result, len(dates))
	var wg sync.WaitGroup

	for i, d := range dates {
		i, d := i, d
		wg.Add(1)
		go func() {
			defer wg.Done()
			h := searchBestHotel(ctx, city, d.checkIn, d.checkOut, guests, currency, prefs)
			if h == nil || h.Price <= 0 {
				results <- result{idx: i, segment: nil}
				return
			}
			results <- result{idx: i, segment: &splitSegment{
				Hotel:     h,
				CheckIn:   d.checkIn,
				CheckOut:  d.checkOut,
				Nights:    d.nights,
				TotalCost: h.Price * float64(d.nights),
			}}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	segments := make([]splitSegment, len(dates))
	for r := range results {
		if r.segment == nil {
			return nil, false
		}
		segments[r.idx] = *r.segment
	}
	return segments, true
}

// searchBestHotel performs a hotel search for the given city and dates and
// returns the cheapest hotel after applying user preferences.
func searchBestHotel(ctx context.Context, city, checkIn, checkOut string, guests int, currency string, prefs *preferences.Preferences) *models.HotelResult {
	opts := hotels.HotelSearchOptions{
		CheckIn:  checkIn,
		CheckOut: checkOut,
		Guests:   guests,
		Sort:     "cheapest",
		Currency: currency,
		MaxPages: 1, // Only need cheapest hotel, not full results. Reduces 9 HTTP requests to 1.
	}
	if prefs != nil {
		if prefs.MinHotelRating > 0 {
			opts.MinRating = prefs.MinHotelRating
		}
		if prefs.MinHotelStars > 0 {
			opts.Stars = prefs.MinHotelStars
		}
	}

	result, err := hotels.SearchHotels(ctx, city, opts)
	if err != nil || !result.Success || len(result.Hotels) == 0 {
		return nil
	}

	// Apply preference filters (dormitories, ensuite, preferred districts).
	filtered := preferences.FilterHotels(result.Hotels, city, prefs)
	if len(filtered) == 0 {
		return nil
	}

	// Return cheapest with a valid price.
	for _, h := range filtered {
		if h.Price > 0 {
			hCopy := h
			return &hCopy
		}
	}
	return nil
}

// buildAccommodationHack assembles the Hack from evaluated segments.
func buildAccommodationHack(city string, segments []splitSegment, moves int, netSavings, baselineTotal float64, currency string) *Hack {
	n := len(segments)
	propertiesWord := "properties"
	if n == 2 {
		propertiesWord = "hotels"
	}

	title := fmt.Sprintf("Split your stay across %d %s", n, propertiesWord)
	nights := 0
	for _, s := range segments {
		nights += s.Nights
	}
	description := fmt.Sprintf(
		"Splitting your %d-night %s stay across %d hotels saves %s %.0f vs a single booking.",
		nights, city, n, currency, roundSavings(netSavings),
	)

	// Build steps.
	var steps []string
	for _, s := range segments {
		name := s.Hotel.Name
		if name == "" {
			name = "hotel"
		}
		steps = append(steps, fmt.Sprintf(
			"%s to %s (%d nights): %s — %s %.0f/night = %s %.0f",
			formatDate(s.CheckIn), formatDate(s.CheckOut), s.Nights,
			name, currency, s.Hotel.Price, currency, roundSavings(s.TotalCost),
		))
	}

	splitCost := baselineTotal - netSavings
	steps = append(steps, fmt.Sprintf(
		"Total: %s %.0f vs baseline %s %.0f",
		currency, roundSavings(splitCost), currency, roundSavings(baselineTotal),
	))
	if moves > 0 {
		moveCost := float64(moves) * movingCostEUR
		if moves == 1 {
			steps = append(steps, fmt.Sprintf("Move between hotels on %s (~%s %.0f taxi)", formatDate(segments[0].CheckOut), currency, moveCost))
		} else {
			steps = append(steps, fmt.Sprintf("%d hotel changes (~%s %.0f total taxi/transit)", moves, currency, moveCost))
		}
	}

	// Build risks.
	risks := []string{
		"Carrying luggage to next hotel (carry-on only strongly recommended)",
		"Re-doing check-in/check-out costs ~30 minutes per move",
		"Different hotel locations may affect your plans",
	}
	if n >= 3 {
		risks = append(risks, "Three separate reservations to manage — confirm all before travel")
	}

	// Build citations.
	var citations []string
	for _, s := range segments {
		if s.Hotel.BookingURL != "" {
			citations = append(citations, s.Hotel.BookingURL)
		}
	}
	if len(citations) == 0 {
		citations = []string{
			fmt.Sprintf("https://www.google.com/travel/hotels/%s", strings.ReplaceAll(city, " ", "+")),
		}
	}

	return &Hack{
		Type:        "accommodation_split",
		Title:       title,
		Description: description,
		Savings:     roundSavings(netSavings),
		Currency:    currency,
		Steps:       steps,
		Risks:       risks,
		Citations:   citations,
	}
}

// formatDate converts YYYY-MM-DD to "Jan 2" for display.
func formatDate(s string) string {
	t, err := parseDate(s)
	if err != nil {
		return s
	}
	return t.Format("Jan 2")
}
